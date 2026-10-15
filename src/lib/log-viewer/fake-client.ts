// Copyright 2026 The Kstack Authors
// Copyright 2024 The Kubetail Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import { compareTimestamps } from './lines';
import type { Client, Cursor, FetchQuery, FollowFrame, LogLine, Page, PageEdge } from './types';

// A `Client` over a log it invents: the one client the viewer runs on until the sidecar's
// wire lands. It models what that wire does to a viewer — edges and repeats, a follow whose
// streams interleave, latency, failures, a stream it could not read — so a viewer that
// holds against it holds against the sidecar.

export type FakeStream = {
  stream: LogLine['stream'];
  place: LogLine['place'];
  /** Turns the stream takes in each round of the rotation. 1 by default. */
  weight?: number;
};

export type FakeClientOptions = {
  /** How many lines the log holds when the fake is made. */
  lines?: number;
  /** How far apart those lines are stamped, the last at the clock's time. 1 by default. */
  spacingMs?: number;
  /** Who writes the log. */
  streams?: FakeStream[];
  /** Lines written per second, on a timer, until `close()`. */
  rate?: number;
  /** How long every fetch, and a follow's replay, takes. */
  delayMs?: number;
  /** How far before the newest line the live end and each checkpoint sit. */
  repeat?: number;
};

class Follower {
  /** Whether the replay went out, after which each append sends its own lines. */
  live = false;

  /** False once cancelled or ended: it sends nothing after. */
  open = true;

  timer: ReturnType<typeof setTimeout> | undefined;

  constructor(
    private readonly onFrame: (frame: FollowFrame) => void,
    /** Where the last checkpoint sat: the next is never earlier. */
    private checkpoint: number,
  ) {}

  send(frame: FollowFrame): void {
    if (this.open) this.onFrame(frame);
  }

  checkpointAt(position: number): void {
    this.checkpoint = Math.max(position, this.checkpoint);
    this.send({ kind: 'Checkpoint', cursor: positionCursor(this.checkpoint) });
  }

  close(): void {
    this.open = false;
    clearTimeout(this.timer);
  }
}

const DEFAULT_STREAM: FakeStream = {
  stream: { namespace: 'default', pod: 'fake-0', container: 'main', previous: false },
  place: { node: 'fake-node', region: '', zone: '', os: 'linux', arch: 'amd64' },
};

// Each stream takes `weight` turns a round, spread evenly through it: turn k of a stream
// sits at (k + 0.5) / weight of the way round.
function rotation(streams: FakeStream[]): number[] {
  const turns: { at: number; stream: number }[] = [];
  streams.forEach((s, stream) => {
    const weight = s.weight ?? 1;
    for (let k = 0; k < weight; k += 1) turns.push({ at: (k + 0.5) / weight, stream });
  });
  turns.sort((a, b) => a.at - b.at || a.stream - b.stream);
  return turns.map((turn) => turn.stream);
}

export class FakeClient implements Client {
  private readonly log: LogLine[] = [];

  private readonly streams: FakeStream[];

  private readonly turns: number[];

  private readonly delayMs: number;

  private readonly repeat: number;

  private lastMs = -Infinity;

  private readonly followers = new Set<Follower>();

  private readonly skipped = new Map<string, string>();

  private readonly passed: { from: number; to: number }[] = [];

  private failFetch = false;

  private failFollow = false;

  private emptyPending = false;

  private timer: ReturnType<typeof setInterval> | undefined;

  constructor(options: FakeClientOptions = {}) {
    this.streams = options.streams?.length ? options.streams : [DEFAULT_STREAM];
    this.turns = rotation(this.streams);
    this.delayMs = options.delayMs ?? 0;
    this.repeat = options.repeat ?? 20;
    const count = options.lines ?? 1000;
    const spacing = options.spacingMs ?? 1;
    const now = Date.now();
    for (let i = 0; i < count; i += 1) this.addLine(now - (count - 1 - i) * spacing);
    const rate = options.rate ?? 0;
    if (rate > 0) this.timer = setInterval(() => this.append(1), 1000 / rate);
  }

  /** Writes `count` lines stamped with the clock's time. */
  append(count: number): void {
    const from = this.log.length;
    for (let i = 0; i < count; i += 1) this.addLine(Date.now());
    this.followers.forEach((follower) => {
      if (follower.live) this.send(follower, from, this.log.length);
    });
  }

  /** Stops the growth `rate` started. */
  close(): void {
    clearInterval(this.timer);
  }

  async fetch(query: FetchQuery): Promise<Page> {
    const refused = this.refuse(query);
    if (refused) throw new Error(refused);
    const failing = this.failFetch;
    this.failFetch = false;
    // Read at the call, as a request is answered from the log as it was when it arrived.
    const page = failing ? null : this.read(query);
    await delay(this.delayMs);
    if (page === null) throw new Error('fake: the fetch failed');
    return page;
  }

  follow(after: Cursor, onFrame: (frame: FollowFrame) => void): () => void {
    if (!this.wrote(after)) throw new Error(`fake: a cursor it did not write: ${after}`);
    if (this.failFollow) {
      this.failFollow = false;
      throw new Error('fake: the follow failed');
    }
    const start = positionAfter(after);
    const follower = new Follower(onFrame, start);
    this.followers.add(follower);
    // What is appended before the replay is held back and sent with it.
    follower.timer = setTimeout(() => {
      follower.live = true;
      this.send(follower, start, this.log.length);
    }, this.delayMs);
    return () => {
      follower.close();
      this.followers.delete(follower);
    };
  }

  /** Makes the next fetch reject, or the next follow throw. */
  failNext(what: 'fetch' | 'follow'): void {
    if (what === 'fetch') this.failFetch = true;
    else this.failFollow = true;
  }

  /** Makes the next fetch answer no line, as a read that spent its budget does. */
  emptyNext(): void {
    this.emptyPending = true;
  }

  /** Keeps a stream's lines out of every page and follow, and names it in each page's `skipped`. */
  skip(stream: LogLine['stream']): void {
    const { namespace, pod, container, previous } = stream;
    const name = `${namespace}/${pod}/${container}${previous ? ' (previous)' : ''}`;
    this.skipped.set(streamKey(stream), `${name}: the fake skipped it`);
  }

  unskip(stream: LogLine['stream']): void {
    this.skipped.delete(streamKey(stream));
  }

  /** Sends `Ended` to every open follow, and nothing after it. */
  endFollows(reason: string): void {
    this.followers.forEach((follower) => {
      follower.send({ kind: 'Ended', reason });
      follower.close();
    });
    this.followers.clear();
  }

  /** Each span an empty read moved its edge across, whose lines no read will serve. */
  passedOver(): { from: number; to: number }[] {
    return [...this.passed];
  }

  get lineCount(): number {
    return this.log.length;
  }

  lineAt(index: number): LogLine {
    return this.log[index];
  }

  // The window must send only what it was given, so the fake refuses anything else.
  private refuse({ mode, cursor, at, limit }: FetchQuery): string | null {
    if (cursor !== null && !this.wrote(cursor)) return `fake: a cursor it did not write: ${cursor}`;
    if (mode === 'Around' && (at === null || cursor !== null)) return 'fake: Around takes `at` and no cursor';
    if (mode !== 'Around' && at !== null) return `fake: ${mode} takes no \`at\``;
    if (limit < 1) return `fake: a limit of ${limit}`;
    return null;
  }

  private wrote(cursor: Cursor): boolean {
    const parsed = parse(cursor);
    if (parsed === null) return false;
    return parsed.kind === 'line' ? parsed.index < this.log.length : parsed.index <= this.log.length;
  }

  private read({ mode, cursor, at, limit }: FetchQuery): Page {
    const empty = this.emptyPending;
    this.emptyPending = false;
    const n = this.log.length;
    // The read starts at `start` and covers lines `low`..`high`: Head reads on from the start,
    // Tail back from it, and Around both ways from the moment.
    let start: number;
    let low: number;
    let high: number;
    if (mode === 'Head') {
      start = cursor === null ? 0 : positionAfter(cursor);
      low = start;
      high = Math.min(start + limit, n);
    } else if (mode === 'Tail') {
      start = cursor === null ? n : positionBefore(cursor);
      low = Math.max(start - limit, 0);
      high = start;
    } else {
      start = this.firstAtOrAfter(at as string);
      low = Math.max(start - limit, 0);
      high = Math.min(start + limit, n);
    }

    // A Head or Around read the limit cut short continues from the cut. Any other after edge
    // is the live end, `repeat` lines before the newest, so a page or a follow from it sends
    // some again — never from before where the read started, or, for an empty read, before
    // what it passed over.
    const cut = mode !== 'Tail' && start + limit < n;
    const afterAt = cut ? start + limit : Math.max(n - this.repeat, empty ? start : low);
    let before: PageEdge;
    if (mode === 'Head') before = { cursor, more: cursor !== null };
    else before = low > 0 ? { cursor: positionCursor(low), more: true } : { cursor: null, more: false };
    let after: PageEdge;
    if (mode === 'Tail' && cursor !== null) after = { cursor, more: true };
    else after = { cursor: positionCursor(afterAt), more: cut };

    if (empty) {
      const to = mode === 'Tail' ? high : afterAt;
      if (to > low) this.passed.push({ from: low, to });
    }
    const kept = (from: number, to: number) =>
      empty ? [] : this.log.slice(from, to).filter((line) => !this.isSkipped(line));
    return {
      lines: kept(low, high),
      before,
      after,
      anchor: mode === 'Around' ? kept(low, start).length : null,
      skipped: [...this.skipped.values()],
    };
  }

  private isSkipped(line: LogLine): boolean {
    return this.skipped.has(streamKey(line.stream));
  }

  // The log is in time order, so a binary search finds it.
  private firstAtOrAfter(at: string): number {
    let low = 0;
    let high = this.log.length;
    while (low < high) {
      const mid = Math.floor((low + high) / 2);
      if (compareTimestamps(this.log[mid].timestamp, at) < 0) low = mid + 1;
      else high = mid;
    }
    return low;
  }

  // Sends lines `from`..`to` one stream's batch after another, as the sidecar's per-stream
  // reads do, then a checkpoint `repeat` lines before the newest.
  private send(follower: Follower, from: number, to: number): void {
    if (from >= to) return;
    const lines = this.log.slice(from, to).filter((line) => !this.isSkipped(line));
    this.streams.forEach(({ stream }) => {
      lines.filter((line) => line.stream === stream).forEach((line) => follower.send({ kind: 'Line', line }));
    });
    follower.checkpointAt(this.log.length - this.repeat);
  }

  private addLine(clock: number): void {
    const ms = Math.max(clock, this.lastMs);
    this.lastMs = ms;
    const index = this.log.length;
    const { stream, place } = this.streams[this.turns[index % this.turns.length]];
    this.log.push({
      timestamp: new Date(ms).toISOString(),
      cursor: lineCursor(index),
      message: `fake line ${index} from ${stream.pod}/${stream.container}`,
      stream,
      place,
    });
  }
}

// A line's cursor names its index; a position's names the gap before index `p`, so `p`
// runs 0..length. Both carry a prefix nothing else writes.
const lineCursor = (index: number): Cursor => `fake:line:${index}`;
const positionCursor = (position: number): Cursor => `fake:position:${position}`;

const CURSOR = /^fake:(line|position):(\d+)$/;

type ParsedCursor = { kind: 'line' | 'position'; index: number };

function parse(cursor: Cursor): ParsedCursor | null {
  const match = CURSOR.exec(cursor);
  return match ? { kind: match[1] as ParsedCursor['kind'], index: Number(match[2]) } : null;
}

// A read on from a cursor starts after the line it names, or at the position; one back from
// it ends before the line, or at the position.
function positionAfter(cursor: Cursor): number {
  const { kind, index } = parse(cursor) as ParsedCursor;
  return kind === 'line' ? index + 1 : index;
}

function positionBefore(cursor: Cursor): number {
  return (parse(cursor) as ParsedCursor).index;
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

const streamKey = ({ namespace, pod, container, previous }: LogLine['stream']) =>
  `${namespace}/${pod}/${container}/${previous}`;
