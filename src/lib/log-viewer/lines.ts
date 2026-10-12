// Copyright 2026 The Kstack Authors
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
// The lines the core holds: a window onto the log that grows at either edge, where
// every line keeps its key (0 is where the view landed) as pages arrive above it.
import { compareCursors } from './cursor';
import { DoubleTailedArray } from './double-tailed-array';
import type { Cursor, LogLine } from './types';

export type Edge = 'before' | 'after';

/**
 * Where the next page toward an edge starts. A cursor is a timestamp, which lines
 * of other sources can share, so a page read from the edge line keeps that line's
 * moment (`since`/`until`) and the lines already held there are dropped; a page's
 * `nextCursor` is a position scanned, which the next page leaves out.
 */
export type PageFrom = { cursor: Cursor; keep: boolean };

function sourceOf({ source }: LogLine): string {
  return `${source.namespace}/${source.pod}/${source.container}/${source.previous}`;
}

/** A source's latest moment, and how many lines with each message are there. */
type Moment = { cursor: Cursor; counts: Map<string, number> };

/**
 * Counts `line` at its source's moment in `moments`, starting the count over at a
 * later moment. Answers how many lines with its message are there now, or 0 for a
 * line before that moment, which is not counted.
 */
function tally(moments: Map<string, Moment>, line: LogLine): number {
  const source = sourceOf(line);
  let moment = moments.get(source);
  const order = moment ? compareCursors(line.cursor, moment.cursor) : 1;
  if (order < 0) return 0;
  if (!moment || order > 0) {
    moment = { cursor: line.cursor, counts: new Map() };
    moments.set(source, moment);
  }
  const count = (moment.counts.get(line.message) ?? 0) + 1;
  moment.counts.set(line.message, count);
  return count;
}

/**
 * One read of the log: a page, or a follow across its batches and the replays its
 * reconnects send. `append` counts what it has read at each source's newest
 * moment, so it can tell lines it re-reads from lines a container wrote twice.
 */
export class Read {
  readonly seen = new Map<string, Moment>();
}

export class Lines {
  private held = new DoubleTailedArray<LogLine>();

  private newest = new Map<string, Moment>();

  /**
   * Where the next page toward each edge starts, recorded off the last page read
   * that way and forgotten once lines are added at that edge, since the edge line
   * is then the exact answer whatever the cursor meant.
   */
  private resume: Record<Edge, Cursor | null> = { before: null, after: null };

  private readonly onChange: (length: number) => void;

  private readonly beforeChange: () => void;

  /** `beforeChange` is called just before the lines change, and `onChange` is told the new length after. */
  constructor(onChange: (length: number) => void, beforeChange: () => void = () => {}) {
    this.onChange = onChange;
    this.beforeChange = beforeChange;
  }

  get length(): number {
    return this.held.length;
  }

  at(index: number): LogLine {
    return this.held.at(index);
  }

  keyAt(index: number): number {
    return this.held.keyAt(index);
  }

  indexOfKey(key: number): number {
    return this.held.indexOfKey(key);
  }

  /** Where the next page toward `edge` is read from; undefined with nothing to read from. */
  from(edge: Edge): PageFrom | undefined {
    const resume = this.resume[edge];
    if (resume !== null) return { cursor: resume, keep: false };
    const line = edge === 'before' ? this.held.first() : this.held.last();
    return line && { cursor: line.cursor, keep: true };
  }

  /** Records a page's `nextCursor` toward `edge`. Call it before adding the page's lines. */
  resumeFrom(edge: Edge, cursor: Cursor | null): void {
    this.resume[edge] = cursor;
  }

  /** Starts over with `lines`, key 0 at the first or the last of them. */
  reset(lines: LogLine[], zeroAt: 'first' | 'last'): void {
    this.beforeChange();
    this.held = new DoubleTailedArray(lines, zeroAt === 'first' ? 0 : 1 - lines.length);
    this.newest = new Map();
    if (lines.length) this.resume = { before: null, after: null };
    lines.forEach((line) => tally(this.newest, line));
    this.onChange(this.length);
  }

  /**
   * Adds a page of history at the start, and answers how many lines it added. The
   * page is older than every source's newest, so it goes in whole but for the
   * lines it shares with the first ones held, at the first line's moment.
   */
  prepend(lines: LogLine[]): number {
    const fresh = this.withoutHeldAtStart(lines);
    if (!fresh.length) return 0;
    this.beforeChange();
    this.held.prepend(fresh);
    this.resume.before = null;
    fresh.forEach((line) => tally(this.newest, line));
    this.onChange(this.length);
    return fresh.length;
  }

  /**
   * Adds the lines new to their own source (`isNew`), in order, and answers how
   * many. Exact, since each container's lines arrive in order: what a follow's
   * reconnect replays, and what a page overlapping the follow re-reads, is dropped.
   * So is a line before the before edge, from a source the window holds none of:
   * that is history, which the next page before reads. A follow passes one `read`
   * for all its batches; a page needs none.
   */
  append(lines: LogLine[], read = new Read()): number {
    const floor = this.from('before')?.cursor;
    const fresh = lines.filter(
      (line) => (floor === undefined || compareCursors(line.cursor, floor) >= 0) && this.isNew(line, read),
    );
    if (!fresh.length) return 0;
    this.beforeChange();
    this.held.append(fresh);
    this.resume.after = null;
    this.onChange(this.length);
    return fresh.length;
  }

  /**
   * Whether `line` is new to its source, and counts it if so: later than its
   * newest moment, or at that moment past as many lines with its message as are
   * held there. A read that reaches that moment re-reads the lines held there
   * first, so only those beyond them are new.
   */
  private isNew(line: LogLine, read: Read): boolean {
    const source = sourceOf(line);
    const last = read.seen.get(source);
    // A source's lines come in order, so one before the last the read saw is a
    // reconnect replaying the follow from where it opened: the count starts over.
    if (last && compareCursors(line.cursor, last.cursor) < 0) read.seen.delete(source);
    const newest = this.newest.get(source);
    const order = newest ? compareCursors(line.cursor, newest.cursor) : 1;
    if (order < 0) return false;
    const seen = tally(read.seen, line);
    if (order === 0 && seen <= (newest?.counts.get(line.message) ?? 0)) return false;
    tally(this.newest, line);
    return true;
  }

  /** `lines` less those held at the first line's moment, each matched once by source and text. */
  private withoutHeldAtStart(lines: LogLine[]): LogLine[] {
    const first = this.held.first();
    if (!first) return lines;
    const atStart = new Map<string, number>();
    for (let i = 0; i < this.length && compareCursors(this.at(i).cursor, first.cursor) === 0; i += 1) {
      const id = `${sourceOf(this.at(i))}\n${this.at(i).message}`;
      atStart.set(id, (atStart.get(id) ?? 0) + 1);
    }
    return lines.filter((line) => {
      const id = `${sourceOf(line)}\n${line.message}`;
      const held = atStart.get(id) ?? 0;
      if (!held || compareCursors(line.cursor, first.cursor) !== 0) return true;
      atStart.set(id, held - 1);
      return false;
    });
  }
}
