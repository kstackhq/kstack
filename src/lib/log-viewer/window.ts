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

import { Lines, compareTimestamps } from './lines';
import type { Anchor, Cursor, FetchQuery, FollowFrame, LogLine, Page } from './types';

// A window of a log, with no React in it. It holds the lines, both edges, the pin, the pause
// and where the after edge resumes; it takes every event — a landing, a page, a follow's
// frames, the user's scroll, a pause — and answers what the scroller must do next. The
// adapter performs the answers and never decides anything itself.

export type Edge = 'before' | 'after';

export type EdgeState = {
  /** Whether there are more lines that way. */
  more: boolean;
  /** Whether the last page that way held no line at all but said there are more: a budget spent, read on by hand. */
  empty: boolean;
  /** Whether a page that way is in flight. Only an edge with more has one. */
  loading: boolean;
};

/** What the last page that way said; `loading` comes from the tickets. */
type PageSaid = Omit<EdgeState, 'loading'>;

export type Effect =
  | { kind: 'none' }
  /** Keep the first visible row where it is on screen across this change. */
  | { kind: 'hold' }
  | { kind: 'toEnd' }
  | { kind: 'toKey'; key: number };

export type CoreState = {
  loading: boolean;
  /** The landing failed. The window holds nothing and pages nothing; `jumpTo` lands again. */
  failed: boolean;
  /** Whether the viewport keeps the end in view. The user's hand changes it. */
  pinnedToEnd: boolean;
  /** Whether the follow is open: landed, pinned, not paused, not ended. */
  following: boolean;
  /** Why the follow ended, in the client's words; null while it has not. A landing clears it. */
  ended: string | null;
  /** Whether the newest line is on screen: following, with the last line in the visible range. */
  atEnd: boolean;
  /** The earliest and latest timestamps among the visible lines; null before any is on screen. */
  range: { from: string; to: string } | null;
  /** How many lines are on screen. */
  visible: number;
  /** How many lines the window holds. It moves whenever a page or a follow adds one, so a render reading the snapshot sees every new line. */
  length: number;
  /** The edges, as `edges()` answers them. */
  before: EdgeState;
  after: EdgeState;
  /** Every stream a page of this landing could not read, once each, first seen first. */
  skipped: string[];
};

export type LogWindowOptions = {
  /** Whether a `Tail` landing keeps the end in view. */
  pinToEnd: boolean;
  /** The landing's limit. */
  firstPageSize: number;
  /** Every later page's limit. */
  pageSize: number;
};

const NO_MORE: EdgeState = { more: false, empty: false, loading: false };

export const CORE_INITIAL_STATE: CoreState = {
  loading: true,
  failed: false,
  pinnedToEnd: false,
  following: false,
  ended: null,
  atEnd: false,
  range: null,
  visible: 0,
  length: 0,
  before: NO_MORE,
  after: NO_MORE,
  skipped: [],
};

const NONE: Effect = { kind: 'none' };

// An edge's state off a page's edge that way: empty when the page held no line and said more.
function edgeOf(page: Page, edge: Edge): PageSaid {
  const { more } = page[edge];
  return { more, empty: more && page.lines.length === 0 };
}

export class LogWindow {
  private readonly lines = new Lines();

  private loading = true;

  private isLanded = false;

  private failed = false;

  private pinned = false;

  private paused = false;

  private ended: string | null = null;

  /** The server's word on each edge, off the last page that way. `edges()` derives the after side's `more`, so only its `empty` is read. */
  private edge: Record<Edge, PageSaid> = { before: NO_MORE, after: NO_MORE };

  /** Where the before edge continues. */
  private beforeCursor: Cursor | null = null;

  /** Where the after edge continues: the last page's after cursor, or the last checkpoint. */
  private resume: Cursor | null = null;

  /** The user's last scroll reached the bottom of a window that is behind; the next page after that finds no more pins. */
  private wantsPin = false;

  /** Whether the last page that way held lines, all of them held already, and said more. */
  private repeats: Record<Edge, boolean> = { before: false, after: false };

  /** The page in flight toward each edge, by a number the window hands out. */
  private ticket: Record<Edge, number | null> = { before: null, after: null };

  private nextTicket = 0;

  /** The first and last index on screen, as the adapter last told it. */
  private visibleRange: { start: number; end: number } | null = null;

  private skipped: string[] = [];

  private snapshot: CoreState = CORE_INITIAL_STATE;

  private readonly listeners = new Set<() => void>();

  constructor(private readonly options: LogWindowOptions) {}

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  getSnapshot = (): CoreState => this.snapshot;

  lineAt(index: number): LogLine {
    return this.lines.at(index);
  }

  keyAt(index: number): number {
    return this.lines.keyAt(index);
  }

  indexOfKey(key: number): number {
    return this.lines.indexOfKey(key);
  }

  following(): boolean {
    return this.isLanded && this.pinned && !this.paused && this.ended === null;
  }

  // A window that is not following is behind the live end whatever its last page said, and a
  // paused one asks for nothing, since Pause stops lines arriving. An edge loads only while it
  // has more, so it never says both that nothing is that way and that a page is coming.
  edges(): Record<Edge, EdgeState> {
    const after = this.following() || this.paused ? NO_MORE : { more: this.isLanded, empty: this.edge.after.empty };
    const { before } = this.edge;
    return {
      before: { ...before, loading: before.more && this.ticket.before !== null },
      after: { ...after, loading: after.more && this.ticket.after !== null },
    };
  }

  query(edge: Edge): FetchQuery | null {
    if (!this.edges()[edge].more) return null;
    const { pageSize: limit } = this.options;
    if (edge === 'before') {
      return this.beforeCursor === null ? null : { mode: 'Tail', cursor: this.beforeCursor, at: null, limit };
    }
    return { mode: 'Head', cursor: this.resume, at: null, limit };
  }

  /** Only called while following, when it is never null. */
  followFrom(): Cursor {
    return this.resume as Cursor;
  }

  landing(anchor: Anchor): FetchQuery {
    const limit = this.options.firstPageSize;
    if (anchor.kind === 'At') return { mode: 'Around', cursor: null, at: anchor.at, limit };
    return { mode: anchor.kind, cursor: null, at: null, limit };
  }

  landed(page: Page, anchor: Anchor): Effect {
    const count = page.lines.length;
    let zeroAt = 0;
    if (anchor.kind === 'Tail') zeroAt = count - 1;
    else if (anchor.kind === 'At') zeroAt = page.anchor ?? 0;
    this.lines.reset(page.lines, zeroAt);
    this.edge = { before: edgeOf(page, 'before'), after: edgeOf(page, 'after') };
    this.beforeCursor = page.before.cursor;
    this.resume = page.after.cursor;
    this.skipped = [];
    this.joinSkipped(page.skipped);
    this.repeats = { before: false, after: false };
    this.ticket = { before: null, after: null };
    this.isLanded = true;
    this.ended = null;
    this.wantsPin = false;
    // A Tail landing shows the end, so it follows from there; the others show their anchor.
    this.pinned = this.options.pinToEnd && anchor.kind === 'Tail';
    this.emit();
    if (anchor.kind === 'Tail') return { kind: 'toEnd' };
    if (anchor.kind === 'At' && count > 0) return { kind: 'toKey', key: 0 };
    return NONE;
  }

  landingFailed(): void {
    this.failed = true;
    this.pinned = false;
    this.emit();
  }

  startPage(edge: Edge): number | null {
    if (!this.isLanded || this.ticket[edge] !== null || this.query(edge) === null) return null;
    this.nextTicket += 1;
    this.ticket[edge] = this.nextTicket;
    this.emit();
    return this.nextTicket;
  }

  paged(ticket: number, page: Page): Effect {
    const edge = this.edgeOfTicket(ticket);
    if (edge === null) return NONE;
    this.ticket[edge] = null;
    // Only the edge paged toward reads the page's edges; the other side is ignored.
    this.edge[edge] = edgeOf(page, edge);
    const added = edge === 'before' ? this.lines.prepend(page.lines) : this.lines.append(page.lines);
    this.repeats[edge] = page.lines.length > 0 && added === 0 && page[edge].more;
    this.joinSkipped(page.skipped);
    let effect: Effect = NONE;
    if (edge === 'before') {
      this.beforeCursor = page.before.cursor;
      // Keys are stable across a prepend, so the adapter holds the first visible row by its key.
      effect = { kind: 'hold' };
    } else {
      this.resume = page.after.cursor;
      if (this.wantsPin && !page.after.more) {
        // The newest lines: a follow would have brought them the same way.
        this.pinned = true;
        this.wantsPin = false;
        effect = { kind: 'toEnd' };
      } else if (this.wantsPin && added > 0) {
        // Rows to read before the bottom; reaching it again asks again.
        this.wantsPin = false;
      }
    }
    this.emit();
    return effect;
  }

  /** A failure waits for the user's next scroll. The adapter reports the error. */
  pageFailed(ticket: number): void {
    const edge = this.edgeOfTicket(ticket);
    if (edge === null) return;
    this.ticket[edge] = null;
    this.repeats[edge] = false;
    this.emit();
  }

  /** The adapter pages that way again at once: a page of repeats moved the edge but brought no row to scroll to. */
  readOn(edge: Edge): boolean {
    return this.repeats[edge] && this.edges()[edge].more;
  }

  /** A page after asked before the follow opened is dropped when it lands. */
  followOpened(): void {
    this.ticket.after = null;
  }

  /**
   * A batch the adapter flushes after the follow closed — the user scrolled up or paused before
   * the unsubscribe ran — must neither add a line behind the reader nor pull the scroller to
   * the end; the page after that comes next reads those lines again from `resume`.
   */
  followed(frames: readonly FollowFrame[]): Effect {
    if (!this.following()) return NONE;
    // An `Ended` is the last frame read; what follows it is ignored.
    const ended = frames.findIndex((frame) => frame.kind === 'Ended');
    const taken = ended === -1 ? frames : frames.slice(0, ended + 1);
    const lines: LogLine[] = [];
    taken.forEach((frame) => {
      if (frame.kind === 'Line') lines.push(frame.line);
      else if (frame.kind === 'Checkpoint') this.resume = frame.cursor;
      else this.ended = frame.reason;
    });
    this.lines.append(lines);
    this.emit();
    return { kind: 'toEnd' };
  }

  /** The client refused to open the follow, so the window is behind until a page pins it again. The adapter reports the error. */
  followFailed(): void {
    this.pinned = false;
    this.wantsPin = false;
    this.emit();
  }

  scrolledUp(): void {
    this.wantsPin = false;
    this.pinned = false;
    this.emit();
  }

  /**
   * The user's scroll reached the bottom of a window that is behind. On true the adapter pages
   * after at once, since a scroller already at the bottom moves no range to page from; the page
   * pins when it finds no more.
   */
  reachedBottom(): boolean {
    if (this.following() || this.paused) return false;
    this.wantsPin = true;
    return true;
  }

  setPaused(paused: boolean): void {
    this.paused = paused;
    if (paused) {
      this.ticket.after = null;
      this.wantsPin = false;
    }
    this.emit();
  }

  setVisibleRange(start: number, end: number): void {
    if (this.visibleRange?.start === start && this.visibleRange.end === end) return;
    this.visibleRange = { start, end };
    this.emit();
  }

  /** The adapter calls it a frame after the landing settled, landed or failed. */
  loadingEnded(): void {
    this.loading = false;
    this.emit();
  }

  private edgeOfTicket(ticket: number): Edge | null {
    if (this.ticket.before === ticket) return 'before';
    if (this.ticket.after === ticket) return 'after';
    return null;
  }

  private joinSkipped(streams: readonly string[]): void {
    const joined = [...new Set([...this.skipped, ...streams])];
    if (joined.length > this.skipped.length) this.skipped = joined;
  }

  // A follow's lines keep their arrival order, so the first and last visible need not be the bounds.
  private onScreen(): Pick<CoreState, 'visible' | 'range' | 'atEnd'> {
    const last = this.lines.length - 1;
    if (this.visibleRange === null) return { visible: 0, range: null, atEnd: false };
    const start = Math.max(this.visibleRange.start, 0);
    const end = Math.min(this.visibleRange.end, last);
    if (end < start) return { visible: 0, range: null, atEnd: false };
    let from = this.lines.at(start).timestamp;
    let to = from;
    for (let i = start + 1; i <= end; i += 1) {
      const { timestamp } = this.lines.at(i);
      if (compareTimestamps(timestamp, from) < 0) from = timestamp;
      if (compareTimestamps(timestamp, to) > 0) to = timestamp;
    }
    return { visible: end - start + 1, range: { from, to }, atEnd: this.following() && this.visibleRange.end === last };
  }

  // Publishes a new snapshot when a field changed. An unchanged object keeps its identity, so
  // a render reading one does not loop.
  private emit(): void {
    const prev = this.snapshot;
    const { before, after } = this.edges();
    const { visible, range, atEnd } = this.onScreen();
    const next: CoreState = {
      loading: this.loading,
      failed: this.failed,
      pinnedToEnd: this.pinned,
      following: this.following(),
      ended: this.ended,
      atEnd,
      range: same(prev.range, range) ? prev.range : range,
      visible,
      length: this.lines.length,
      before: same(prev.before, before) ? prev.before : before,
      after: same(prev.after, after) ? prev.after : after,
      skipped: this.skipped,
    };
    if (same(prev, next)) return;
    this.snapshot = next;
    this.listeners.forEach((listener) => listener());
  }
}

// Whether two objects hold the same values field for field.
function same(a: object | null, b: object | null): boolean {
  if (a === null || b === null) return a === b;
  const fields = Object.entries(a);
  return (
    fields.length === Object.keys(b).length &&
    fields.every(([key, value]) => Object.is(value, b[key as keyof typeof b]))
  );
}
