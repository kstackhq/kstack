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
import { DoubleTailedArray } from './double-tailed-array';
import type { Cursor, LogLine } from './types';

/** Orders two RFC 3339 timestamps as instants, whatever offset each is written in. */
export function compareTimestamps(a: string, b: string): number {
  const x = instant(a);
  const y = instant(b);
  if (x.seconds !== y.seconds) return x.seconds < y.seconds ? -1 : 1;
  if (x.fraction === y.fraction) return 0;
  return x.fraction < y.fraction ? -1 : 1;
}

// The seconds, the fraction and the offset (`Z` or `±HH:MM`).
const RFC3339 = /^(.{19})(?:\.(\d+))?(.*)$/;

// `Date.parse` cuts a fraction to milliseconds, so it reads the whole seconds with the offset,
// and the fraction, padded to nine digits, orders as text.
function instant(timestamp: string): { seconds: number; fraction: string } {
  const [, seconds = '', fraction = '', offset = ''] = RFC3339.exec(timestamp) ?? [];
  return { seconds: Date.parse(seconds + offset), fraction: fraction.padEnd(9, '0') };
}

// What the window holds: the lines in the order the client answered them, and every
// held line's cursor. A line whose cursor is held is a repeat — what a page or a reopened
// follow sends again at the edge it went on from — and is dropped.
export class Lines {
  private items = new DoubleTailedArray<LogLine>();

  private held = new Set<Cursor>();

  get length(): number {
    return this.items.length;
  }

  /** Starts over with key 0 at the index `zeroAt`, kept within the lines. */
  reset(lines: readonly LogLine[], zeroAt: number): void {
    const zero = Math.max(Math.min(zeroAt, lines.length - 1), 0);
    this.items = new DoubleTailedArray<LogLine>();
    this.held = new Set<Cursor>();
    // The lines before `zero` go in as a prepend, which keys them below 0.
    this.prepend(lines.slice(0, zero));
    this.append(lines.slice(zero));
  }

  /** Adds the lines not held, each once, after the last. Answers how many it added. */
  append(lines: readonly LogLine[]): number {
    const fresh = this.fresh(lines);
    this.items.append(fresh);
    return fresh.length;
  }

  /** Adds the lines not held, each once and in their own order, before the first. Answers how many it added. */
  prepend(lines: readonly LogLine[]): number {
    const fresh = this.fresh(lines);
    this.items.prepend(fresh);
    return fresh.length;
  }

  at(index: number): LogLine {
    return this.items.at(index);
  }

  keyAt(index: number): number {
    return this.items.keyAt(index);
  }

  indexOfKey(key: number): number {
    return this.items.indexOfKey(key);
  }

  first(): LogLine | undefined {
    return this.items.first();
  }

  last(): LogLine | undefined {
    return this.items.last();
  }

  private fresh(lines: readonly LogLine[]): LogLine[] {
    return lines.filter((line) => {
      if (this.held.has(line.cursor)) return false;
      this.held.add(line.cursor);
      return true;
    });
  }
}
