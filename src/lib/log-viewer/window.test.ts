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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { FakeClient, type FakeStream } from './fake-client';
import type { Anchor, FetchQuery, FollowFrame, LogLine, Page } from './types';
import { CORE_INITIAL_STATE, type Edge, type Effect, LogWindow, type LogWindowOptions } from './window';

const HEAD: Anchor = { kind: 'Head', at: null };
const TAIL: Anchor = { kind: 'Tail', at: null };
const AT: Anchor = { kind: 'At', at: '2026-10-15T00:00:30Z' };

// Line `i` is stamped `i` seconds past midnight unless told otherwise.
function line(i: number, second = i): LogLine {
  return {
    timestamp: `2026-10-15T00:${String(Math.floor(second / 60)).padStart(2, '0')}:${String(second % 60).padStart(2, '0')}Z`,
    cursor: `c${i}`,
    message: `line ${i}`,
    stream: { namespace: 'ns', pod: 'p', container: 'app', previous: false },
    place: { node: '', region: '', zone: '', os: '', arch: '' },
  };
}

const range = (from: number, to: number) => Array.from({ length: to - from }, (_, k) => line(from + k));

function page(from: number, to: number, over: Partial<Page> = {}): Page {
  return {
    lines: range(from, to),
    before: { cursor: `before-${from}`, more: true },
    after: { cursor: `after-${to}`, more: true },
    anchor: null,
    skipped: [],
    ...over,
  };
}

const END = { cursor: 'live-end', more: false };
const START = { cursor: null, more: false };

function make(options: Partial<LogWindowOptions> = {}): LogWindow {
  return new LogWindow({ pinToEnd: true, firstPageSize: 50, pageSize: 20, ...options });
}

function landed(anchor: Anchor, p: Page, options: Partial<LogWindowOptions> = {}): LogWindow {
  const w = make(options);
  w.landed(p, anchor);
  return w;
}

const keys = (w: LogWindow) => {
  const { length } = w.getSnapshot();
  return Array.from({ length }, (_, i) => w.keyAt(i));
};
const cursors = (w: LogWindow) => {
  const { length } = w.getSnapshot();
  return Array.from({ length }, (_, i) => w.lineAt(i).cursor);
};

const lineFrame = (i: number): FollowFrame => ({ kind: 'Line', line: line(i) });

describe('before any event', () => {
  it('answers the initial snapshot', () => {
    expect(make().getSnapshot()).toBe(CORE_INITIAL_STATE);
    expect(CORE_INITIAL_STATE).toEqual({
      loading: true,
      failed: false,
      pinnedToEnd: false,
      following: false,
      ended: null,
      atEnd: false,
      range: null,
      visible: 0,
      length: 0,
      before: { more: false, empty: false },
      after: { more: false, empty: false },
      skipped: [],
    });
  });
});

describe('landing', () => {
  it('asks for the first page each anchor reads, changing nothing', () => {
    const w = make({ firstPageSize: 30 });
    expect(w.landing(HEAD)).toEqual({ mode: 'Head', cursor: null, at: null, limit: 30 });
    expect(w.landing(TAIL)).toEqual({ mode: 'Tail', cursor: null, at: null, limit: 30 });
    expect(w.landing(AT)).toEqual({ mode: 'Around', cursor: null, at: AT.at, limit: 30 });
    expect(w.getSnapshot()).toBe(CORE_INITIAL_STATE);
  });

  it('puts key 0 at the first line for Head', () => {
    const w = make();
    expect(w.landed(page(0, 3, { before: START }), HEAD)).toEqual({ kind: 'none' });
    expect(keys(w)).toEqual([0, 1, 2]);
  });

  it('puts key 0 at the last line for Tail, and goes to the end', () => {
    const w = make();
    expect(w.landed(page(0, 3, { after: END }), TAIL)).toEqual({ kind: 'toEnd' });
    expect(keys(w)).toEqual([-2, -1, 0]);
  });

  it('puts key 0 at the page’s anchor for At, and goes to it', () => {
    const w = make();
    expect(w.landed(page(0, 5, { anchor: 2 }), AT)).toEqual({ kind: 'toKey', key: 0 });
    expect(keys(w)).toEqual([-2, -1, 0, 1, 2]);
  });

  it('puts key 0 at the last line for At with no line after the moment', () => {
    const w = make();
    w.landed(page(0, 5, { anchor: 5 }), AT);
    expect(keys(w)).toEqual([-4, -3, -2, -1, 0]);
  });

  it('lands an At with no line at all and goes nowhere', () => {
    const w = make();
    expect(w.landed(page(0, 0, { anchor: 0 }), AT)).toEqual({ kind: 'none' });
    expect(w.getSnapshot().length).toBe(0);
  });

  it('reads both edges off the page, and marks each empty that held no line and said more', () => {
    const w = landed(HEAD, page(0, 3, { before: START }), { pinToEnd: false });
    expect(w.edges()).toEqual({ before: { more: false, empty: false }, after: { more: true, empty: false } });

    const spent = landed(AT, page(0, 0, { anchor: 0 }), { pinToEnd: false });
    expect(spent.edges()).toEqual({ before: { more: true, empty: true }, after: { more: true, empty: true } });
    expect(spent.getSnapshot().before).toEqual({ more: true, empty: true });
    expect(spent.getSnapshot().after).toEqual({ more: true, empty: true });
  });

  it('publishes what it landed', () => {
    const w = landed(HEAD, page(0, 3, { skipped: ['ns/q/app: gone'] }), { pinToEnd: false });
    expect(w.getSnapshot()).toMatchObject({ loading: true, failed: false, length: 3, skipped: ['ns/q/app: gone'] });
    w.loadingEnded();
    expect(w.getSnapshot().loading).toBe(false);
  });
});

describe('the pin on landing', () => {
  it('follows a Tail landing with pinToEnd', () => {
    const w = landed(TAIL, page(0, 3, { after: END }));
    expect(w.getSnapshot()).toMatchObject({ pinnedToEnd: true, following: true });
    expect(w.following()).toBe(true);
    expect(w.followFrom()).toBe('live-end');
  });

  it('pins no Tail landing without pinToEnd', () => {
    const w = landed(TAIL, page(0, 3, { after: END }), { pinToEnd: false });
    expect(w.getSnapshot()).toMatchObject({ pinnedToEnd: false, following: false });
  });

  it('pins no Head or At landing, even when its page reached the end', () => {
    expect(landed(HEAD, page(0, 3, { after: END })).getSnapshot().pinnedToEnd).toBe(false);
    expect(landed(AT, page(0, 3, { after: END, anchor: 1 })).getSnapshot().pinnedToEnd).toBe(false);
  });
});

describe('landingFailed', () => {
  it('holds nothing, pages nothing and unpins', () => {
    const w = make();
    w.landingFailed();
    expect(w.getSnapshot()).toMatchObject({ failed: true, pinnedToEnd: false, length: 0 });
    expect(w.startPage('before')).toBeNull();
    expect(w.startPage('after')).toBeNull();
    expect(w.query('after')).toBeNull();
  });
});

describe('tickets', () => {
  it('answers no ticket before landing, while one is out that way, or with no more that way', () => {
    const w = make({ pinToEnd: false });
    expect(w.startPage('after')).toBeNull();

    w.landed(page(10, 20, { before: START }), HEAD);
    expect(w.startPage('before')).toBeNull();
    const ticket = w.startPage('after');
    expect(ticket).not.toBeNull();
    expect(w.startPage('after')).toBeNull();
    expect(w.query('after')).toEqual({ mode: 'Head', cursor: 'after-20', at: null, limit: 20 });
  });

  it('asks Tail from the before edge’s cursor', () => {
    const w = landed(TAIL, page(10, 20, { after: END }), { pinToEnd: false });
    expect(w.query('before')).toEqual({ mode: 'Tail', cursor: 'before-10', at: null, limit: 20 });
  });

  it('changes nothing for a stale ticket, paged or failed', () => {
    const w = landed(HEAD, page(0, 10), { pinToEnd: false });
    const ticket = w.startPage('after') as number;
    const listener = vi.fn();
    w.subscribe(listener);

    expect(w.paged(ticket + 100, page(10, 20))).toEqual({ kind: 'none' });
    w.pageFailed(ticket + 100);
    expect(w.getSnapshot().length).toBe(10);
    expect(w.startPage('after')).toBeNull();

    w.pageFailed(ticket);
    expect(w.startPage('after')).not.toBeNull();
    expect(listener).not.toHaveBeenCalled();
  });

  it('drops a page after asked before the follow opened', () => {
    const w = landed(HEAD, page(0, 10), { pinToEnd: false });
    const ticket = w.startPage('after') as number;
    w.followOpened();
    expect(w.paged(ticket, page(10, 20))).toEqual({ kind: 'none' });
    expect(w.getSnapshot().length).toBe(10);
  });

  it('names its edge: a ticket before is not one after', () => {
    const w = landed(AT, page(10, 20, { anchor: 5 }), { pinToEnd: false });
    const before = w.startPage('before') as number;
    const after = w.startPage('after') as number;
    expect(before).not.toBe(after);
    w.paged(before, page(5, 10));
    expect(cursors(w)[0]).toBe('c5');
    w.paged(after, page(20, 25));
    expect(cursors(w).at(-1)).toBe('c24');
  });
});

describe('paged', () => {
  it('prepends less the held before, holds, and reads the before edge alone', () => {
    const w = landed(TAIL, page(10, 20, { after: END }), { pinToEnd: false });
    const ticket = w.startPage('before') as number;
    const effect = w.paged(ticket, page(5, 12, { before: START, after: { cursor: 'ignored', more: false } }));
    expect(effect).toEqual({ kind: 'hold' });
    expect(cursors(w)).toEqual(range(5, 20).map((l) => l.cursor));
    expect(w.keyAt(w.getSnapshot().length - 1)).toBe(0);
    expect(w.edges().before).toEqual({ more: false, empty: false });
    expect(w.query('after')).toEqual({ mode: 'Head', cursor: 'live-end', at: null, limit: 20 });
  });

  it('sets empty before when a page held no line and said more', () => {
    const w = landed(TAIL, page(10, 20, { after: END }), { pinToEnd: false });
    w.paged(w.startPage('before') as number, page(0, 0, { before: { cursor: 'older', more: true } }));
    expect(w.edges().before).toEqual({ more: true, empty: true });
    expect(w.getSnapshot().before).toEqual({ more: true, empty: true });
    expect(w.query('before')?.cursor).toBe('older');
  });

  it('appends less the held after, and moves resume', () => {
    const w = landed(HEAD, page(0, 10), { pinToEnd: false });
    const effect = w.paged(w.startPage('after') as number, page(8, 15, { before: { cursor: 'ignored', more: false } }));
    expect(effect).toEqual({ kind: 'none' });
    expect(cursors(w)).toEqual(range(0, 15).map((l) => l.cursor));
    expect(w.query('after')?.cursor).toBe('after-15');
    expect(w.edges().before).toEqual({ more: true, empty: false });
  });

  it('sets empty after when a page held no line and said more', () => {
    const w = landed(HEAD, page(0, 10), { pinToEnd: false });
    w.paged(w.startPage('after') as number, page(0, 0, { after: { cursor: 'on', more: true } }));
    expect(w.getSnapshot().after).toEqual({ more: true, empty: true });
  });
});

describe('readOn', () => {
  it('reads on after a page of nothing but repeats that says more, and not empty', () => {
    const w = landed(HEAD, page(0, 10), { pinToEnd: false });
    w.paged(w.startPage('after') as number, page(5, 10));
    expect(w.readOn('after')).toBe(true);
    expect(w.edges().after.empty).toBe(false);

    w.paged(w.startPage('after') as number, page(10, 12));
    expect(w.readOn('after')).toBe(false);
  });

  it('does not read on after a page of repeats that says no more', () => {
    const w = landed(TAIL, page(10, 20, { after: END }), { pinToEnd: false });
    w.paged(w.startPage('before') as number, page(15, 18, { before: START }));
    expect(w.readOn('before')).toBe(false);
  });

  it('stops reading on when the page fails', () => {
    const w = landed(TAIL, page(10, 20, { after: END }), { pinToEnd: false });
    w.paged(w.startPage('before') as number, page(15, 18));
    expect(w.readOn('before')).toBe(true);
    w.pageFailed(w.startPage('before') as number);
    expect(w.readOn('before')).toBe(false);
  });
});

describe('the after edge', () => {
  it('has no more while following or paused, and more otherwise, whatever the page said', () => {
    const pinned = landed(TAIL, page(0, 10, { after: END }));
    expect(pinned.edges().after).toEqual({ more: false, empty: false });
    expect(pinned.query('after')).toBeNull();

    const behind = landed(TAIL, page(0, 10, { after: END }), { pinToEnd: false });
    expect(behind.edges().after).toEqual({ more: true, empty: false });
    expect(behind.getSnapshot().after).toEqual({ more: true, empty: false });

    behind.setPaused(true);
    expect(behind.edges().after).toEqual({ more: false, empty: false });
    expect(behind.startPage('after')).toBeNull();
  });
});

describe('reaching the bottom', () => {
  it('answers false and wants nothing while following or paused', () => {
    expect(landed(TAIL, page(0, 10, { after: END })).reachedBottom()).toBe(false);
    const paused = landed(HEAD, page(0, 10), { pinToEnd: false });
    paused.setPaused(true);
    expect(paused.reachedBottom()).toBe(false);
  });

  it('pins when the page after finds no more, whether or not it added lines', () => {
    const w = landed(HEAD, page(0, 10), { pinToEnd: false });
    expect(w.reachedBottom()).toBe(true);
    expect(w.paged(w.startPage('after') as number, page(10, 12, { after: END }))).toEqual({ kind: 'toEnd' });
    expect(w.getSnapshot()).toMatchObject({ pinnedToEnd: true, following: true });

    const none = landed(HEAD, page(0, 10, { after: END }), { pinToEnd: false });
    none.reachedBottom();
    expect(none.paged(none.startPage('after') as number, page(8, 10, { after: END }))).toEqual({ kind: 'toEnd' });
    expect(none.getSnapshot().pinnedToEnd).toBe(true);
  });

  it('gives up the wish on a page that adds lines and finds more', () => {
    const w = landed(HEAD, page(0, 10), { pinToEnd: false });
    w.reachedBottom();
    expect(w.paged(w.startPage('after') as number, page(10, 30))).toEqual({ kind: 'none' });
    w.paged(w.startPage('after') as number, page(30, 32, { after: END }));
    expect(w.getSnapshot().pinnedToEnd).toBe(false);
  });

  it('keeps the wish through a page of repeats that finds more', () => {
    const w = landed(HEAD, page(0, 10), { pinToEnd: false });
    w.reachedBottom();
    w.paged(w.startPage('after') as number, page(5, 10));
    expect(w.readOn('after')).toBe(true);
    expect(w.paged(w.startPage('after') as number, page(10, 11, { after: END }))).toEqual({ kind: 'toEnd' });
    expect(w.getSnapshot().pinnedToEnd).toBe(true);
  });

  it('pins nothing without the wish', () => {
    const w = landed(HEAD, page(0, 10), { pinToEnd: false });
    w.paged(w.startPage('after') as number, page(10, 12, { after: END }));
    expect(w.getSnapshot().pinnedToEnd).toBe(false);
  });

  it('unpins and drops the wish on a scroll up', () => {
    const w = landed(TAIL, page(0, 10, { after: END }));
    w.scrolledUp();
    expect(w.getSnapshot()).toMatchObject({ pinnedToEnd: false, following: false });

    w.reachedBottom();
    w.scrolledUp();
    w.paged(w.startPage('after') as number, page(10, 12, { after: END }));
    expect(w.getSnapshot().pinnedToEnd).toBe(false);
  });
});

describe('skipped', () => {
  it('gathers each page’s streams once, first seen first, and a landing starts over', () => {
    const w = landed(HEAD, page(0, 10, { skipped: ['a'] }), { pinToEnd: false });
    w.paged(w.startPage('after') as number, page(10, 12, { skipped: ['b', 'a'] }));
    w.paged(w.startPage('before') as number, page(0, 0, { skipped: ['b', 'c', 'c'] }));
    expect(w.getSnapshot().skipped).toEqual(['a', 'b', 'c']);

    const before = w.getSnapshot().skipped;
    w.paged(w.startPage('after') as number, page(12, 14, { skipped: ['c'] }));
    expect(w.getSnapshot().skipped).toBe(before);

    w.landed(page(0, 1), HEAD);
    expect(w.getSnapshot().skipped).toEqual([]);
  });
});

describe('followed', () => {
  it('appends less the held and goes to the end', () => {
    const w = landed(TAIL, page(0, 10, { after: END }));
    expect(w.followed([lineFrame(8), lineFrame(9), lineFrame(10), lineFrame(11)])).toEqual({ kind: 'toEnd' });
    expect(cursors(w).slice(-3)).toEqual(['c9', 'c10', 'c11']);
    expect(w.getSnapshot().length).toBe(12);
  });

  it('moves resume on a checkpoint and not on a line', () => {
    const w = landed(TAIL, page(0, 10, { after: END }));
    w.followed([lineFrame(10)]);
    expect(w.followFrom()).toBe('live-end');
    w.followed([lineFrame(11), { kind: 'Checkpoint', cursor: 'cp-1' }, lineFrame(12)]);
    expect(w.followFrom()).toBe('cp-1');
  });

  it('tells no one about checkpoints alone, or lines it holds', () => {
    const w = landed(TAIL, page(0, 10, { after: END }));
    const listener = vi.fn();
    w.subscribe(listener);
    w.followed([{ kind: 'Checkpoint', cursor: 'cp' }, lineFrame(9)]);
    expect(listener).not.toHaveBeenCalled();
    w.followed([lineFrame(10)]);
    expect(listener).toHaveBeenCalledOnce();
  });

  it('changes nothing while not following: unpinned, paused, ended or not landed', () => {
    const unpinned = landed(TAIL, page(0, 10, { after: END }), { pinToEnd: false });
    const paused = landed(TAIL, page(0, 10, { after: END }));
    paused.setPaused(true);
    const ended = landed(TAIL, page(0, 10, { after: END }));
    ended.followed([{ kind: 'Ended', reason: 'gone' }]);
    const notLanded = make();

    [unpinned, paused, ended, notLanded].forEach((w) => {
      const before = w.getSnapshot();
      expect(w.followed([lineFrame(10), { kind: 'Checkpoint', cursor: 'cp' }])).toEqual({ kind: 'none' });
      expect(w.getSnapshot()).toBe(before);
    });
    expect(unpinned.query('after')?.cursor).toBe('live-end');
  });
});

describe('an Ended frame', () => {
  it('closes the follow, ignores what comes after it, and opens the after edge', () => {
    const w = landed(TAIL, page(0, 10, { after: END }));
    w.followed([lineFrame(10), { kind: 'Ended', reason: 'the pod went away' }, lineFrame(11)]);
    expect(w.getSnapshot()).toMatchObject({
      ended: 'the pod went away',
      following: false,
      pinnedToEnd: true,
      length: 11,
    });
    expect(w.edges().after.more).toBe(true);

    w.landed(page(0, 3, { after: END }), TAIL);
    expect(w.getSnapshot()).toMatchObject({ ended: null, following: true });
  });
});

describe('setPaused', () => {
  it('closes the follow, asks for nothing after and drops the wish to pin', () => {
    const w = landed(TAIL, page(0, 10, { after: END }));
    w.setPaused(true);
    expect(w.getSnapshot()).toMatchObject({ following: false, pinnedToEnd: true });
    expect(w.query('after')).toBeNull();

    w.setPaused(false);
    expect(w.getSnapshot().following).toBe(true);
  });

  it('drops a page after asked before it', () => {
    const w = landed(HEAD, page(0, 10), { pinToEnd: false });
    w.reachedBottom();
    const ticket = w.startPage('after') as number;
    w.setPaused(true);
    expect(w.paged(ticket, page(10, 12, { after: END }))).toEqual({ kind: 'none' });
    expect(w.getSnapshot()).toMatchObject({ length: 10, pinnedToEnd: false });

    w.setPaused(false);
    w.paged(w.startPage('after') as number, page(10, 12, { after: END }));
    expect(w.getSnapshot().pinnedToEnd).toBe(false);
  });
});

describe('followFailed', () => {
  it('unpins, so the next scroll to the bottom pages, pins and follows', () => {
    const w = landed(TAIL, page(0, 10, { after: END }));
    w.followFailed();
    expect(w.getSnapshot()).toMatchObject({ pinnedToEnd: false, following: false });

    expect(w.reachedBottom()).toBe(true);
    expect(w.paged(w.startPage('after') as number, page(10, 11, { after: END }))).toEqual({ kind: 'toEnd' });
    expect(w.getSnapshot()).toMatchObject({ pinnedToEnd: true, following: true });
  });
});

describe('publishing', () => {
  it('answers the same snapshot until a field changes', () => {
    const w = landed(HEAD, page(0, 10), { pinToEnd: false });
    const first = w.getSnapshot();
    w.setPaused(false);
    w.scrolledUp();
    w.paged(w.startPage('after') as number, page(5, 10));
    expect(w.getSnapshot()).toBe(first);
    expect(w.getSnapshot().after).toBe(first.after);
  });

  it('moves length and tells its listeners on every page and batch that adds a line', () => {
    const w = landed(TAIL, page(10, 20, { after: END }));
    const listener = vi.fn();
    w.subscribe(listener);
    w.paged(w.startPage('before') as number, page(5, 10));
    expect(w.getSnapshot().length).toBe(15);
    w.followed([lineFrame(20)]);
    expect(w.getSnapshot().length).toBe(16);
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it('stops telling a listener that unsubscribed', () => {
    const w = landed(TAIL, page(0, 10, { after: END }));
    const listener = vi.fn();
    w.subscribe(listener)();
    w.followed([lineFrame(10)]);
    expect(listener).not.toHaveBeenCalled();
  });
});

describe('setVisibleRange', () => {
  it('publishes how many lines are on screen and whether the newest is', () => {
    const w = landed(TAIL, page(0, 10, { after: END }));
    w.setVisibleRange(5, 9);
    expect(w.getSnapshot()).toMatchObject({ visible: 5, atEnd: true });
    w.setVisibleRange(3, 7);
    expect(w.getSnapshot()).toMatchObject({ visible: 5, atEnd: false });
  });

  it('is never at the end while not following', () => {
    const w = landed(TAIL, page(0, 10, { after: END }), { pinToEnd: false });
    w.setVisibleRange(5, 9);
    expect(w.getSnapshot().atEnd).toBe(false);
  });

  it('spans the earliest and latest visible timestamps when a follow’s lines arrived out of order', () => {
    const w = landed(TAIL, page(0, 10, { after: END }));
    w.followed([
      { kind: 'Line', line: line(10, 50) },
      { kind: 'Line', line: line(11, 12) },
      { kind: 'Line', line: line(12, 40) },
    ]);
    w.setVisibleRange(9, 12);
    expect(w.getSnapshot().range).toEqual({ from: '2026-10-15T00:00:09Z', to: '2026-10-15T00:00:50Z' });
  });

  it('notifies when atEnd changes though the range moved its end alone', () => {
    const w = make();
    w.landed({ ...page(0, 0), lines: [line(0, 1), line(1, 1), line(2, 1)], after: END }, TAIL);
    w.setVisibleRange(0, 1);
    const listener = vi.fn();
    w.subscribe(listener);
    w.setVisibleRange(1, 2);
    expect(w.getSnapshot().atEnd).toBe(true);
    expect(listener).toHaveBeenCalledOnce();
    w.setVisibleRange(1, 2);
    expect(listener).toHaveBeenCalledOnce();
  });

  it('publishes nothing on screen before any range, or past what it holds', () => {
    const w = landed(HEAD, page(0, 3), { pinToEnd: false });
    expect(w.getSnapshot()).toMatchObject({ visible: 0, range: null });
    w.setVisibleRange(5, 9);
    expect(w.getSnapshot()).toMatchObject({ visible: 0, range: null });
  });
});

// The walk: a seeded random walk drives a window over the fake's log, modelling the adapter
// that step 2 builds around it, and checks the window's rules after every step.

// Runs `count` async steps one after another.
function inTurn(count: number, run: (n: number) => Promise<void>): Promise<void> {
  return Array.from({ length: count }, (_, n) => n).reduce((prev, n) => prev.then(() => run(n)), Promise.resolve());
}

// A linear congruential generator, so a failing seed replays exactly.
function lcg(seed: number) {
  // The product stays below 2^53, so plain arithmetic is exact.
  let state = seed % 2 ** 32;
  const next = () => {
    state = (state * 1664525 + 1013904223) % 2 ** 32;
    return state / 2 ** 32;
  };
  return {
    chance: (p: number) => next() < p,
    int: (n: number) => Math.floor(next() * n),
  };
}

const VIEW = 5;
const PAGE = 8;
const FIRST_PAGE = 12;

const pod = (name: string, weight: number): FakeStream => ({
  stream: { namespace: 'walk', pod: name, container: 'app', previous: false },
  place: { node: `node-${name}`, region: '', zone: '', os: 'linux', arch: 'amd64' },
  weight,
});

type Pending = {
  window: number;
  /** The landing, or the edge a page went toward with its ticket. */
  for: { kind: 'landing'; anchor: Anchor } | { kind: 'page'; edge: Edge; ticket: number };
  /** Spans this fetch's read passed over, counted once its answer is taken. */
  spans: { from: number; to: number }[];
  answer: { page: Page } | { error: unknown } | null;
};

async function walk(seed: number) {
  const rand = lcg(seed);
  const repeat = rand.chance(0.3) ? PAGE + 1 + rand.int(10) : rand.int(PAGE);
  const fake = new FakeClient({
    lines: 120 + rand.int(200),
    streams: [pod('a', 10), pod('b', 10), pod('c', 1)],
    repeat,
  });

  // Each line's index in the log, by its cursor.
  const indexOf = new Map<string, number>();
  const index = (cursor: string) => {
    for (let i = indexOf.size; i < fake.lineCount; i += 1) indexOf.set(fake.lineAt(i).cursor, i);
    return indexOf.get(cursor) as number;
  };

  let windowId = 0;
  let w = make();
  let landedOk = false;
  let paused = false;
  let top = 0;
  let lastRange = '';
  let outstanding: Record<Edge, number | null> = { before: null, after: null };
  let passed: { from: number; to: number }[] = [];
  let byFollow: Set<string> = new Set();
  let bottomWish = false;
  let sawEnded = false;
  let pinned = false;
  const pending: Pending[] = [];
  // Answers the walk holds until a later step lets them through, in any order.
  const heldBack = new Set<Pending>();
  let cancel: (() => void) | null = null;
  let frames: FollowFrame[] = [];
  // What the step did, for the check that nothing else moves the view.
  let moved = false;

  const length = () => w.getSnapshot().length;
  const maxTop = () => Math.max(length() - VIEW, 0);
  const topKey = () => (length() > 0 ? w.keyAt(Math.min(top, length() - 1)) : null);
  const context = `seed ${seed}`;

  // Pinned turns on only where `allowed` says it may.
  function checkPin(allowed: boolean) {
    const now = w.getSnapshot().pinnedToEnd;
    if (now && !pinned) expect(allowed, `${context}: pinned without cause`).toBe(true);
    pinned = now;
  }

  function apply(effect: Effect, heldKey: number | null) {
    if (effect.kind === 'hold' && heldKey !== null) top = w.indexOfKey(heldKey);
    if (effect.kind === 'toEnd') top = maxTop();
    if (effect.kind === 'toKey') top = w.indexOfKey(effect.key);
    top = Math.min(Math.max(top, 0), maxTop());
  }

  function fetch(query: FetchQuery, of: Pending['for']) {
    const decide = rand.int(10);
    if (decide === 0) fake.failNext('fetch');
    if (decide === 1) fake.emptyNext();
    const before = fake.passedOver().length;
    const entry: Pending = { window: windowId, for: of, spans: [], answer: null };
    if (rand.chance(0.3)) heldBack.add(entry);
    fake.fetch(query).then(
      (p) => {
        entry.answer = { page: p };
      },
      (error: unknown) => {
        entry.answer = { error };
      },
    );
    entry.spans = fake.passedOver().slice(before);
    pending.push(entry);
  }

  function pageToward(edge: Edge) {
    const ticket = w.startPage(edge);
    if (ticket === null) return;
    outstanding[edge] = ticket;
    fetch(w.query(edge) as FetchQuery, { kind: 'page', edge, ticket });
  }

  function closeFollow() {
    cancel?.();
    cancel = null;
  }

  function land(next: Anchor, pinToEnd: boolean) {
    closeFollow();
    windowId += 1;
    w = make({ pinToEnd, firstPageSize: FIRST_PAGE, pageSize: PAGE });
    landedOk = false;
    paused = false;
    top = 0;
    lastRange = '';
    outstanding = { before: null, after: null };
    passed = [];
    byFollow = new Set();
    bottomWish = false;
    sawEnded = false;
    pinned = false;
    fetch(w.landing(next), { kind: 'landing', anchor: next });
  }

  function deliver(entry: Pending) {
    pending.splice(pending.indexOf(entry), 1);
    if (entry.window !== windowId || entry.answer === null) return;
    const { answer } = entry;
    if (entry.for.kind === 'landing') {
      if ('page' in answer) {
        apply(w.landed(answer.page, entry.for.anchor), null);
        landedOk = true;
        passed.push(...entry.spans);
        moved = true;
        checkPin(entry.for.anchor.kind === 'Tail' && w.getSnapshot().pinnedToEnd);
      } else {
        w.landingFailed();
        checkPin(false);
      }
      w.loadingEnded();
      return;
    }
    const { edge, ticket } = entry.for;
    const accepted = outstanding[edge] === ticket;
    if (accepted) outstanding[edge] = null;
    if ('error' in answer) {
      w.pageFailed(ticket);
      // A failed fetch leaves its edge to be asked again.
      if (accepted && w.query(edge) !== null) pageToward(edge);
      return;
    }
    const lengthBefore = length();
    const heldIndexes = Array.from({ length: lengthBefore }, (_, i) => index(w.lineAt(i).cursor));
    const heldKey = topKey();
    apply(w.paged(ticket, answer.page), heldKey);
    // A page extends the stretch on its own side: its new lines are past every line held.
    const added = length() - lengthBefore;
    const fresh = Array.from({ length: added }, (_, k) =>
      index(w.lineAt(edge === 'before' ? k : lengthBefore + k).cursor),
    );
    if (heldIndexes.length > 0 && fresh.length > 0) {
      if (edge === 'before')
        expect(Math.max(...fresh), `${context}: a page before inside the stretch`).toBeLessThan(
          Math.min(...heldIndexes),
        );
      else
        expect(Math.min(...fresh), `${context}: a page after inside the stretch`).toBeGreaterThan(
          Math.max(...heldIndexes),
        );
    }
    if (!accepted) {
      expect(length(), `${context}: a stale page added lines`).toBe(lengthBefore);
      return;
    }
    if (paused && edge === 'after') expect(length(), `${context}: a page added lines while paused`).toBe(lengthBefore);
    passed.push(...entry.spans);
    moved = true;
    checkPin(edge === 'after' && !answer.page.after.more && bottomWish);
    if (w.readOn(edge)) pageToward(edge);
  }

  function flush() {
    if (frames.length === 0) return;
    const batch = frames;
    frames = [];
    const wasFollowing = w.following();
    const lengthBefore = length();
    const effect = w.followed(batch);
    for (let i = lengthBefore; i < length(); i += 1) byFollow.add(w.lineAt(i).cursor);
    if (length() > lengthBefore) expect(wasFollowing, `${context}: a closed follow added lines`).toBe(true);
    if (effect.kind !== 'none') moved = true;
    apply(effect, null);
    if (w.getSnapshot().ended !== null) sawEnded = true;
    checkPin(false);
  }

  function syncFollow() {
    if (w.following() && cancel === null) {
      w.followOpened();
      outstanding.after = null;
      try {
        cancel = fake.follow(w.followFrom(), (frame) => frames.push(frame));
      } catch {
        w.followFailed();
        expect(w.getSnapshot().pinnedToEnd, `${context}: a refused follow left it pinned`).toBe(false);
        checkPin(false);
      }
    } else if (!w.following() && cancel !== null) {
      // The unsubscribe lags the close: frames already queued still reach `followed`.
      closeFollow();
    }
  }

  function syncRange() {
    if (length() === 0) return;
    const start = top;
    const end = Math.min(top + VIEW - 1, length() - 1);
    w.setVisibleRange(start, end);
    const key = `${w.keyAt(start)}:${w.keyAt(end)}`;
    if (key === lastRange) return;
    lastRange = key;
    if (start <= 2) pageToward('before');
    if (end >= length() - 3) pageToward('after');
  }

  // The adapter's reactions, until nothing more happens.
  async function settle(round = 0): Promise<void> {
    syncFollow();
    await vi.advanceTimersByTimeAsync(0);
    const ready = pending.filter((entry) => entry.answer !== null && !heldBack.has(entry));
    ready.forEach(deliver);
    const hadFrames = frames.length > 0;
    flush();
    syncFollow();
    syncRange();
    const quiet = ready.length === 0 && !hadFrames && !pending.some((e) => e.answer === null);
    if (!quiet && round < 40) await settle(round + 1);
  }

  function scrollTo(row: number) {
    const target = Math.min(Math.max(row, 0), maxTop());
    if (target < top) {
      w.scrolledUp();
      bottomWish = false;
      top = target;
      // A scroll up that ended off the bottom leaves it unpinned.
      expect(w.getSnapshot().pinnedToEnd, `${context}: pinned after a scroll up`).toBe(false);
      pinned = false;
    } else {
      top = target;
    }
    moved = true;
    if (top === maxTop() && length() > 0 && w.reachedBottom()) {
      bottomWish = true;
      pageToward('after');
    }
  }

  function check() {
    const snap = w.getSnapshot();
    const held = Array.from({ length: snap.length }, (_, i) => w.lineAt(i).cursor);
    expect(new Set(held).size, `${context}: a line held twice`).toBe(held.length);

    // Held lines and passed-over spans are one stretch of the log.
    const covered = new Set(held.map(index));
    passed.forEach(({ from, to }) => {
      for (let i = from; i < to; i += 1) covered.add(i);
    });
    if (covered.size > 0) {
      const all = [...covered];
      const low = Math.min(...all);
      const high = Math.max(...all);
      expect(high - low + 1, `${context}: the lines held are not one stretch`).toBe(covered.size);
    }

    // What pages brought is in log order; a follow's lines keep their arrival order.
    const paged = held.filter((c) => !byFollow.has(c)).map(index);
    paged.forEach((value, i) => {
      if (i > 0) expect(value, `${context}: paged lines out of log order`).toBeGreaterThan(paged[i - 1]);
    });

    expect(snap.following, `${context}: following disagrees`).toBe(
      landedOk && snap.pinnedToEnd && !paused && snap.ended === null,
    );
    if (sawEnded) expect(snap.following, `${context}: an ended follow reopened`).toBe(false);
    if (snap.following && cancel !== null) {
      const last = fake.lineCount - 1;
      const holdsLast =
        held.includes(fake.lineAt(last).cursor) || passed.some(({ from, to }) => from <= last && last < to);
      expect(holdsLast, `${context}: following without the newest line`).toBe(true);
    }
  }

  const randomAnchor = (): Anchor => {
    const kind = (['Head', 'Tail', 'At'] as const)[rand.int(3)];
    return kind === 'At' ? { kind, at: fake.lineAt(rand.int(fake.lineCount)).timestamp } : { kind, at: null };
  };

  const first = randomAnchor();
  land(first, rand.chance(0.5));
  await settle();
  check();

  async function step(n: number) {
    moved = false;
    const keyBefore = topKey();
    const windowBefore = windowId;
    const actions = 1 + rand.int(3);
    for (let a = 0; a < actions; a += 1) {
      switch (rand.int(13)) {
        case 0:
          scrollTo(rand.int(maxTop() + 1));
          break;
        case 1:
          scrollTo(maxTop());
          break;
        case 2:
          scrollTo(top - 1);
          if (top === 0) {
            // A wheel up at the top still moves up as far as the user means.
            w.scrolledUp();
            bottomWish = false;
            pinned = w.getSnapshot().pinnedToEnd;
          }
          break;
        case 3:
          paused = !paused;
          w.setPaused(paused);
          if (paused) {
            outstanding.after = null;
            bottomWish = false;
          }
          checkPin(false);
          break;
        case 4:
          fake.append(1 + rand.int(15));
          break;
        case 5: {
          const next = randomAnchor();
          land(next, next.kind === 'Tail');
          break;
        }
        case 6: {
          const edge: Edge = rand.chance(0.5) ? 'before' : 'after';
          if (w.edges()[edge].empty) pageToward(edge);
          break;
        }
        case 7: {
          const answered = pending.filter((entry) => entry.answer !== null);
          if (answered.length > 0) deliver(answered[rand.int(answered.length)]);
          break;
        }
        case 8:
          fake.append(1);
          break;
        case 9:
          fake.endFollows('ended by the walk');
          break;
        case 10:
          if (rand.chance(0.25)) fake.failNext('follow');
          break;
        case 11:
          vi.advanceTimersByTime(rand.int(1000));
          break;
        default:
          heldBack.clear();
      }
    }
    await settle();
    check();
    if (!moved && windowId === windowBefore && keyBefore !== null) {
      expect(topKey(), `${context}: the view moved on its own at step ${n}`).toBe(keyBefore);
    }
  }

  await inTurn(60, (n) => step(n));
  closeFollow();
}

describe('the walk', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(Date.parse('2026-10-15T12:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('keeps every rule through 100 seeds of 60 steps', async () => {
    await inTurn(100, (n) => walk(n + 1));
  }, 60_000);
});
