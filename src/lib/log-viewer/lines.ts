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
// Lines stay in the order the client answered them; a page is sorted by the
// client, a follow arrives in each container's own order.
import { DoubleTailedArray } from './double-tailed-array';
import type { Cursor, LogLine } from './types';

export type Edge = 'before' | 'after';

/**
 * Where the next page toward an edge starts. Lines of other streams can share the
 * edge line's moment, so a page read from it keeps that line (`since`/`until`)
 * and the lines already held come back with it; a page's `nextCursor` is the last
 * position scanned, which the next page leaves out.
 */
export type PageFrom = { cursor: Cursor; keep: boolean };

export class Lines {
  private held = new DoubleTailedArray<LogLine>();

  /**
   * Every held line's cursor. A cursor names one line, so a line whose cursor is
   * held is a repeat: a follow's replay after a reconnect, the overlap a follow or
   * a catch-up page opens with, or the edge line a page from it sends again.
   */
  private cursors = new Set<Cursor>();

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
    this.cursors = new Set(lines.map((line) => line.cursor));
    if (lines.length) this.resume = { before: null, after: null };
    this.onChange(this.length);
  }

  /** Adds a page of history at the start, less the lines held, and answers how many it added. */
  prepend(lines: LogLine[]): number {
    const fresh = this.take(lines);
    if (!fresh.length) return 0;
    this.beforeChange();
    this.held.prepend(fresh);
    this.resume.before = null;
    this.onChange(this.length);
    return fresh.length;
  }

  /** Adds lines at the end, less the lines held, and answers how many it added. */
  append(lines: LogLine[]): number {
    const fresh = this.take(lines);
    if (!fresh.length) return 0;
    this.beforeChange();
    this.held.append(fresh);
    this.resume.after = null;
    this.onChange(this.length);
    return fresh.length;
  }

  /** `lines` less those held, each once, their cursors now held. */
  private take(lines: LogLine[]): LogLine[] {
    const fresh: LogLine[] = [];
    for (let i = 0; i < lines.length; i += 1) {
      if (!this.cursors.has(lines[i].cursor)) {
        this.cursors.add(lines[i].cursor);
        fresh.push(lines[i]);
      }
    }
    return fresh;
  }
}
