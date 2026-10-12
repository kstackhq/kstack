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
// A test fixture, not part of the library: `index.ts` leaves it out, and only tests
// import it. Its delays are timers, so a test drives them with fake timers.
import type { Client, Cursor, FetchOptions, FetchResult, LogLine } from './types';

export type FakeClientOptions = {
  /** How many lines the log starts with, one per millisecond up to now. */
  lines?: number;
  /** Lines appended per second. */
  rate?: number;
  /** How long each fetch takes. */
  delayMs?: number;
  /** The pod that writes line `index`; one pod writes them all by default. */
  podOf?: (index: number) => string;
};

/**
 * A log of `lines` lines a millisecond apart, that can grow at `rate` per second.
 * A line appended later is stamped at the clock, so it is newer than the moment before it.
 */
export class FakeClient implements Client {
  /** The timestamp of line 0, in milliseconds, with no line to read it from. */
  private readonly first: number;

  /** Each line's timestamp, in milliseconds, in order. */
  private stamps: number[];

  private readonly delayMs: number;

  private readonly podOf: (index: number) => string;

  private listeners = new Set<(line: LogLine) => void>();

  private timer: ReturnType<typeof setInterval> | null = null;

  constructor({ lines = 1000, rate = 0, delayMs = 0, podOf = () => 'pod-1' }: FakeClientOptions = {}) {
    this.first = Date.now() - lines;
    this.stamps = Array.from({ length: lines }, (_, i) => this.first + i);
    this.delayMs = delayMs;
    this.podOf = podOf;
    this.setAppendRate(rate);
  }

  get lineCount(): number {
    return this.stamps.length;
  }

  /** The line at `index`, counted from the end when negative. */
  lineAt(index: number): LogLine {
    const i = index < 0 ? this.lineCount + index : index;
    if (i < 0 || i >= this.lineCount) throw new RangeError(`line ${index} of ${this.lineCount}`);
    const timestamp = new Date(this.stamps[i]).toISOString();
    return {
      timestamp,
      cursor: timestamp,
      message: `line ${i}`,
      source: {
        namespace: 'ns',
        pod: this.podOf(i),
        container: 'container-1',
        previous: false,
        node: 'node-1',
        region: 'region-1',
        zone: 'az-1',
        os: 'linux',
        arch: 'amd64',
      },
    };
  }

  fetchSince({ cursor, limit }: FetchOptions): Promise<FetchResult> {
    return this.forward(cursor ? this.firstAtOrAfter(cursor) : 0, limit);
  }

  fetchUntil({ cursor, limit }: FetchOptions): Promise<FetchResult> {
    return this.backward(cursor ? this.firstAfter(cursor) : this.lineCount, limit);
  }

  fetchAfter({ cursor, limit }: FetchOptions): Promise<FetchResult> {
    return this.forward(cursor ? this.firstAfter(cursor) : 0, limit);
  }

  fetchBefore({ cursor, limit }: FetchOptions): Promise<FetchResult> {
    return this.backward(cursor ? this.firstAtOrAfter(cursor) : this.lineCount, limit);
  }

  /**
   * Replays the lines after `after` first, holding back what is appended
   * meanwhile and sending that on once the replay has passed it.
   */
  subscribe(after: Cursor | null, onLine: (line: LogLine) => void): () => void {
    let replaying = after !== null;
    let cancelled = false;
    const heldBack: LogLine[] = [];
    const listener = (line: LogLine) => {
      if (replaying) heldBack.push(line);
      else onLine(line);
    };
    this.listeners.add(listener);

    if (replaying) {
      this.fetchAfter({ cursor: after, limit: Infinity }).then(({ lines }) => {
        if (cancelled) return;
        lines.forEach(onLine);
        const replayedTo = lines.length ? Date.parse(lines[lines.length - 1].cursor) : -Infinity;
        heldBack.filter((line) => Date.parse(line.cursor) > replayedTo).forEach(onLine);
        replaying = false;
      });
    }

    return () => {
      cancelled = true;
      this.listeners.delete(listener);
    };
  }

  setAppendRate(rate: number): void {
    if (this.timer !== null) clearInterval(this.timer);
    this.timer = null;
    if (rate <= 0) return;
    // A tick at least every 100ms, with as many lines as the rate needs.
    const intervalMs = Math.max(100, 1000 / rate);
    const perTick = Math.max(1, Math.round((rate * intervalMs) / 1000));
    this.timer = setInterval(() => this.append(perTick), intervalMs);
  }

  /** Appends `count` lines now, each handed to every subscriber. */
  append(count = 1): void {
    for (let i = 0; i < count; i += 1) {
      this.stamps.push(Math.max(this.timeAt(this.lineCount), Date.now()));
      const line = this.lineAt(-1);
      this.listeners.forEach((listener) => listener(line));
    }
  }

  /** The index of the first line at or after `cursor`; `lineCount` when none is. */
  private firstAtOrAfter(cursor: Cursor): number {
    return this.search((stamp) => stamp >= Date.parse(cursor));
  }

  /** The index of the first line after `cursor`; `lineCount` when none is. */
  private firstAfter(cursor: Cursor): number {
    return this.search((stamp) => stamp > Date.parse(cursor));
  }

  /** The index of the first stamp `past` holds for, the stamps being in order. */
  private search(past: (stamp: number) => boolean): number {
    let low = 0;
    let high = this.lineCount;
    while (low < high) {
      const mid = (low + high) >> 1; // eslint-disable-line no-bitwise -- the midpoint
      if (past(this.stamps[mid])) high = mid;
      else low = mid + 1;
    }
    return low;
  }

  /** The timestamp at `index`, a millisecond per index past either end. */
  private timeAt(index: number): number {
    if (!this.lineCount) return this.first + index;
    if (index < 0) return this.stamps[0] + index;
    if (index >= this.lineCount) return this.stamps[this.lineCount - 1] + (index - this.lineCount + 1);
    return this.stamps[index];
  }

  /** The `limit` lines from `start` on; the next cursor is the last one read. */
  private forward(start: number, limit: number): Promise<FetchResult> {
    const end = Math.min(this.lineCount, start + limit);
    return this.answer(start, end, end < this.lineCount ? end - 1 : null);
  }

  /** The `limit` lines up to `end`; the next cursor is the first one read. */
  private backward(end: number, limit: number): Promise<FetchResult> {
    const start = Math.max(0, end - limit);
    return this.answer(start, end, start > 0 ? start : null);
  }

  private answer(start: number, end: number, next: number | null): Promise<FetchResult> {
    const lines = Array.from({ length: Math.max(0, end - start) }, (_, i) => this.lineAt(start + i));
    const nextCursor = next === null ? null : new Date(this.timeAt(next)).toISOString();
    const result = { lines, nextCursor, skipped: [] };
    return new Promise((resolve) => {
      setTimeout(resolve, this.delayMs, result);
    });
  }
}
