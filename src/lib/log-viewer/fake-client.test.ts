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
import type { FetchQuery, FollowFrame, LogLine, Page } from './types';

const NOW = Date.parse('2026-10-15T12:00:00.000Z');

function stream(pod: string, weight?: number): FakeStream {
  return {
    stream: { namespace: 'default', pod, container: 'app', previous: false },
    place: { node: `node-${pod}`, region: 'r', zone: 'z', os: 'linux', arch: 'arm64' },
    weight,
  };
}

const q = (query: Partial<FetchQuery>): FetchQuery => ({ mode: 'Head', cursor: null, at: null, limit: 10, ...query });

// Every fetch takes `delayMs` on a timer; this runs it out and answers the page.
async function fetchNow(fake: FakeClient, query: FetchQuery): Promise<Page> {
  const pending = fake.fetch(query);
  await vi.runOnlyPendingTimersAsync();
  return pending;
}

const indexes = (fake: FakeClient, lines: LogLine[]) =>
  lines.map((line) => Array.from({ length: fake.lineCount }, (_, i) => fake.lineAt(i).cursor).indexOf(line.cursor));

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
});

afterEach(() => {
  vi.useRealTimers();
});

describe('the log', () => {
  it('writes lines a millisecond apart, the last stamped with the clock', () => {
    const fake = new FakeClient({ lines: 3, streams: [stream('a')] });
    expect(fake.lineCount).toBe(3);
    expect([0, 1, 2].map((i) => fake.lineAt(i).timestamp)).toEqual([
      '2026-10-15T11:59:59.998Z',
      '2026-10-15T11:59:59.999Z',
      '2026-10-15T12:00:00.000Z',
    ]);
  });

  it('spaces its history `spacingMs` apart, the last stamped with the clock', () => {
    const fake = new FakeClient({ lines: 3, streams: [stream('a')], spacingMs: 2000 });
    expect([0, 1, 2].map((i) => fake.lineAt(i).timestamp)).toEqual([
      '2026-10-15T11:59:56.000Z',
      '2026-10-15T11:59:58.000Z',
      '2026-10-15T12:00:00.000Z',
    ]);
  });

  it('answers Around at a moment inside a spaced history with lines on both sides', async () => {
    const fake = new FakeClient({ lines: 5000, streams: [stream('a')], spacingMs: 2000 });
    const page = await fetchNow(fake, q({ mode: 'Around', at: '2026-10-15T11:00:00Z', limit: 5 }));
    expect(page.lines).toHaveLength(10);
    expect(page.anchor).toBe(5);
    expect(page.lines[5].timestamp).toBe('2026-10-15T11:00:00.000Z');
  });

  it('gives every line a cursor of its own', () => {
    const fake = new FakeClient({ lines: 50, streams: [stream('a'), stream('b')] });
    const cursors = new Set(Array.from({ length: 50 }, (_, i) => fake.lineAt(i).cursor));
    expect(cursors.size).toBe(50);
  });

  it('writes the streams in a fixed rotation, each in proportion to its weight', () => {
    const fake = new FakeClient({ lines: 120, streams: [stream('a'), stream('b'), stream('c')] });
    const pods = Array.from({ length: 6 }, (_, i) => fake.lineAt(i).stream.pod);
    expect(pods).toEqual(['a', 'b', 'c', 'a', 'b', 'c']);

    const weighted = new FakeClient({ lines: 120, streams: [stream('a', 10), stream('b', 10), stream('c', 1)] });
    const count = (pod: string) =>
      Array.from({ length: 105 }, (_, i) => weighted.lineAt(i).stream.pod).filter((p) => p === pod).length;
    expect([count('a'), count('b'), count('c')]).toEqual([50, 50, 5]);
    // Spread through the round, not bunched at its start.
    const round = Array.from({ length: 21 }, (_, i) => weighted.lineAt(i).stream.pod).join('');
    expect(round).not.toMatch(/aaaaa/);
  });

  it('carries each stream’s place', () => {
    const fake = new FakeClient({ lines: 2, streams: [stream('a'), stream('b')] });
    expect(fake.lineAt(1).place.node).toBe('node-b');
    expect(fake.lineAt(1).stream).toEqual(stream('b').stream);
  });

  it('writes nothing but ASCII', () => {
    const fake = new FakeClient({ lines: 30, streams: [stream('a'), stream('b')] });
    for (let i = 0; i < fake.lineCount; i += 1) {
      const { timestamp, cursor, message } = fake.lineAt(i);
      expect(`${timestamp}${cursor}${message}`).toMatch(/^[\x20-\x7e]*$/);
    }
  });
});

// Where a page's after edge goes on from, read off the next page `Head` reads from it.
async function firstAfter(fake: FakeClient, cursor: string | null): Promise<number> {
  const next = await fetchNow(fake, q({ mode: 'Head', cursor, limit: 500 }));
  return next.lines.length ? indexes(fake, next.lines)[0] : fake.lineCount;
}

describe('fetch', () => {
  it('answers Head with no cursor from the start, cut by the limit', async () => {
    const fake = new FakeClient({ lines: 100, repeat: 5 });
    const page = await fetchNow(fake, q({ mode: 'Head', limit: 10 }));
    expect(indexes(fake, page.lines)).toEqual([0, 1, 2, 3, 4, 5, 6, 7, 8, 9]);
    expect(page.before).toEqual({ cursor: null, more: false });
    expect(page.after.more).toBe(true);
    expect(await firstAfter(fake, page.after.cursor)).toBe(10);
    expect(page.anchor).toBeNull();
    expect(page.skipped).toEqual([]);
  });

  it('answers Head that reaches the newest line with the live end, `repeat` lines back', async () => {
    const fake = new FakeClient({ lines: 8, repeat: 3 });
    const page = await fetchNow(fake, q({ mode: 'Head', limit: 10 }));
    expect(page.lines).toHaveLength(8);
    expect(page.after.more).toBe(false);
    expect(await firstAfter(fake, page.after.cursor)).toBe(5);
  });

  it('never puts the live end before where the read started', async () => {
    const fake = new FakeClient({ lines: 30, repeat: 25 });
    const head = await fetchNow(fake, q({ mode: 'Head', limit: 10 }));
    const page = await fetchNow(fake, q({ mode: 'Head', cursor: head.after.cursor, limit: 40 }));
    expect(indexes(fake, page.lines)[0]).toBe(10);
    expect(await firstAfter(fake, page.after.cursor)).toBe(10);

    const tail = await fetchNow(fake, q({ mode: 'Tail', limit: 10 }));
    expect(indexes(fake, tail.lines)[0]).toBe(20);
    expect(await firstAfter(fake, tail.after.cursor)).toBe(20);
  });

  it('answers Head with a cursor after it, its before edge the cursor given', async () => {
    const fake = new FakeClient({ lines: 100 });
    const first = await fetchNow(fake, q({ mode: 'Head', limit: 10 }));
    const page = await fetchNow(fake, q({ mode: 'Head', cursor: first.after.cursor, limit: 10 }));
    expect(indexes(fake, page.lines)[0]).toBe(10);
    expect(page.before).toEqual({ cursor: first.after.cursor, more: true });
  });

  it('reads after a line when a line’s cursor is the Head cursor', async () => {
    const fake = new FakeClient({ lines: 100 });
    const page = await fetchNow(fake, q({ mode: 'Head', cursor: fake.lineAt(41).cursor, limit: 3 }));
    expect(indexes(fake, page.lines)).toEqual([42, 43, 44]);
  });

  it('answers Tail with no cursor from the end, cut by the limit', async () => {
    const fake = new FakeClient({ lines: 100, repeat: 4 });
    const page = await fetchNow(fake, q({ mode: 'Tail', limit: 10 }));
    expect(indexes(fake, page.lines)).toEqual([90, 91, 92, 93, 94, 95, 96, 97, 98, 99]);
    expect(page.before.more).toBe(true);
    expect(page.after.more).toBe(false);
    expect(await firstAfter(fake, page.after.cursor)).toBe(96);

    const older = await fetchNow(fake, q({ mode: 'Tail', cursor: page.before.cursor, limit: 10 }));
    expect(indexes(fake, older.lines)).toEqual([80, 81, 82, 83, 84, 85, 86, 87, 88, 89]);
    expect(older.after).toEqual({ cursor: page.before.cursor, more: true });
  });

  it('answers Tail that reaches the oldest line with no before edge', async () => {
    const fake = new FakeClient({ lines: 5 });
    const page = await fetchNow(fake, q({ mode: 'Tail', limit: 10 }));
    expect(page.lines).toHaveLength(5);
    expect(page.before).toEqual({ cursor: null, more: false });

    const middle = await fetchNow(fake, q({ mode: 'Head', limit: 2 }));
    const older = await fetchNow(fake, q({ mode: 'Tail', cursor: middle.after.cursor, limit: 10 }));
    expect(indexes(fake, older.lines)).toEqual([0, 1]);
    expect(older.before).toEqual({ cursor: null, more: false });
  });

  it('answers Around with the Tail half’s before, the Head half’s after and the anchor', async () => {
    const fake = new FakeClient({ lines: 100, repeat: 2 });
    const at = fake.lineAt(50).timestamp;
    const page = await fetchNow(fake, q({ mode: 'Around', at, limit: 5 }));
    expect(indexes(fake, page.lines)).toEqual([45, 46, 47, 48, 49, 50, 51, 52, 53, 54]);
    expect(page.anchor).toBe(5);
    expect(page.before.more).toBe(true);
    expect(page.after.more).toBe(true);
    expect(await firstAfter(fake, page.after.cursor)).toBe(55);
    const older = await fetchNow(fake, q({ mode: 'Tail', cursor: page.before.cursor, limit: 1 }));
    expect(indexes(fake, older.lines)).toEqual([44]);
  });

  it('answers Around at a moment written with an offset', async () => {
    const fake = new FakeClient({ lines: 100 });
    const utc = new Date(fake.lineAt(50).timestamp);
    const at = `${new Date(utc.getTime() + 2 * 3600_000).toISOString().slice(0, -1)}+02:00`;
    const page = await fetchNow(fake, q({ mode: 'Around', at, limit: 1 }));
    expect(indexes(fake, page.lines)).toEqual([49, 50]);
  });

  it('answers Around near either end as the halves’ tables say', async () => {
    const fake = new FakeClient({ lines: 10, repeat: 2 });
    const early = await fetchNow(fake, q({ mode: 'Around', at: fake.lineAt(1).timestamp, limit: 3 }));
    expect(indexes(fake, early.lines)).toEqual([0, 1, 2, 3]);
    expect(early.anchor).toBe(1);
    expect(early.before).toEqual({ cursor: null, more: false });

    const late = await fetchNow(fake, q({ mode: 'Around', at: '2030-01-01T00:00:00Z', limit: 3 }));
    expect(indexes(fake, late.lines)).toEqual([7, 8, 9]);
    expect(late.anchor).toBe(3);
    expect(late.after.more).toBe(false);
    expect(await firstAfter(fake, late.after.cursor)).toBe(8);
  });
});

describe('what the fake refuses', () => {
  it('rejects a fetch with a cursor it did not write', async () => {
    const fake = new FakeClient({ lines: 10 });
    const foreign = ['line-3', 'fake:line:x', 'fake:line:10', 'fake:position:11', ''];
    const answers = await Promise.allSettled(foreign.map((cursor) => fake.fetch(q({ mode: 'Head', cursor }))));
    expect(answers.map((a) => a.status)).toEqual(foreign.map(() => 'rejected'));
    await expect(fake.fetch(q({ mode: 'Tail', cursor: 'nope' }))).rejects.toThrow(Error);
  });

  it('rejects a cursor beside `at`, `at` beside Head or Tail, and Around with none', async () => {
    const fake = new FakeClient({ lines: 10 });
    const at = fake.lineAt(5).timestamp;
    await expect(fake.fetch(q({ mode: 'Around', at, cursor: fake.lineAt(1).cursor }))).rejects.toThrow(Error);
    await expect(fake.fetch(q({ mode: 'Around' }))).rejects.toThrow(Error);
    await expect(fake.fetch(q({ mode: 'Head', at }))).rejects.toThrow(Error);
    await expect(fake.fetch(q({ mode: 'Tail', at }))).rejects.toThrow(Error);
    await expect(fake.fetch(q({ mode: 'Head', limit: 0 }))).rejects.toThrow(Error);
  });

  it('throws from follow with a cursor it did not write', () => {
    const fake = new FakeClient({ lines: 10 });
    expect(() => fake.follow('elsewhere', () => {})).toThrow(Error);
  });
});

describe('time', () => {
  it('answers a fetch once `delayMs` has passed', async () => {
    const fake = new FakeClient({ lines: 10, delayMs: 100 });
    let page: Page | undefined;
    fake.fetch(q({})).then(
      (p) => {
        page = p;
      },
      () => {},
    );
    await vi.advanceTimersByTimeAsync(99);
    expect(page).toBeUndefined();
    await vi.advanceTimersByTimeAsync(1);
    expect(page?.lines).toHaveLength(10);
  });

  it('stamps appended lines with the clock, never earlier than the line before', () => {
    const fake = new FakeClient({ lines: 2 });
    vi.advanceTimersByTime(5);
    fake.append(2);
    expect(fake.lineCount).toBe(4);
    expect(fake.lineAt(3).timestamp).toBe('2026-10-15T12:00:00.005Z');

    vi.setSystemTime(NOW - 1000);
    fake.append(1);
    expect(fake.lineAt(4).timestamp).toBe('2026-10-15T12:00:00.005Z');
  });

  it('grows at `rate` lines a second until closed', () => {
    const fake = new FakeClient({ lines: 0, rate: 10 });
    vi.advanceTimersByTime(1000);
    expect(fake.lineCount).toBe(10);
    fake.close();
    vi.advanceTimersByTime(1000);
    expect(fake.lineCount).toBe(10);
  });
});

function record(fake: FakeClient, after: string) {
  const frames: FollowFrame[] = [];
  const cancel = fake.follow(after, (frame) => frames.push(frame));
  const lines = () =>
    indexes(
      fake,
      frames.flatMap((f) => (f.kind === 'Line' ? [f.line] : [])),
    );
  return { frames, cancel, lines };
}

const position = (index: number) => `fake:position:${index}`;

describe('follow', () => {
  it('replays the lines after `after` once `delayMs` has passed, with what was appended meanwhile', async () => {
    const fake = new FakeClient({ lines: 10, delayMs: 50, repeat: 0 });
    const follow = record(fake, fake.lineAt(6).cursor);
    fake.append(1);
    expect(follow.frames).toEqual([]);
    await vi.advanceTimersByTimeAsync(50);
    expect(follow.lines()).toEqual([7, 8, 9, 10]);
    expect(follow.frames.at(-1)).toEqual({ kind: 'Checkpoint', cursor: position(11) });
  });

  it('sends nothing on a quiet follow', async () => {
    const fake = new FakeClient({ lines: 10 });
    const follow = record(fake, position(10));
    await vi.advanceTimersByTimeAsync(0);
    expect(follow.frames).toEqual([]);
  });

  it('sends each append’s lines inside the append, one stream’s batch after another, then a checkpoint', async () => {
    const fake = new FakeClient({ lines: 0, streams: [stream('a'), stream('b')], repeat: 1 });
    const follow = record(fake, position(0));
    await vi.advanceTimersByTimeAsync(0);

    fake.append(4);
    expect(follow.frames.map((f) => (f.kind === 'Line' ? f.line.stream.pod : f.kind))).toEqual([
      'a',
      'a',
      'b',
      'b',
      'Checkpoint',
    ]);
    expect(follow.lines()).toEqual([0, 2, 1, 3]);
    expect(follow.frames.at(-1)).toEqual({ kind: 'Checkpoint', cursor: position(3) });

    fake.append(1);
    expect(follow.lines()).toEqual([0, 2, 1, 3, 4]);
    expect(follow.frames.at(-1)).toEqual({ kind: 'Checkpoint', cursor: position(4) });
  });

  it('never checkpoints before `after` or before its last checkpoint', async () => {
    const fake = new FakeClient({ lines: 10, repeat: 5 });
    const follow = record(fake, position(9));
    await vi.advanceTimersByTimeAsync(0);
    expect(follow.frames.at(-1)).toEqual({ kind: 'Checkpoint', cursor: position(9) });
    fake.append(1);
    expect(follow.frames.at(-1)).toEqual({ kind: 'Checkpoint', cursor: position(9) });
  });

  it('delivers nothing once cancelled, before its replay or after', async () => {
    const fake = new FakeClient({ lines: 10 });
    const early = record(fake, position(0));
    early.cancel();
    await vi.advanceTimersByTimeAsync(0);
    expect(early.frames).toEqual([]);

    const late = record(fake, position(10));
    await vi.advanceTimersByTimeAsync(0);
    late.cancel();
    fake.append(3);
    expect(late.frames).toEqual([]);
  });

  it('reads on from a checkpoint’s cursor as a Head cursor and as `after`', async () => {
    const fake = new FakeClient({ lines: 10, repeat: 2 });
    const follow = record(fake, position(10));
    await vi.advanceTimersByTimeAsync(0);
    fake.append(5);
    const checkpoint = follow.frames.at(-1) as { kind: 'Checkpoint'; cursor: string };

    const page = await fetchNow(fake, q({ mode: 'Head', cursor: checkpoint.cursor }));
    expect(indexes(fake, page.lines)).toEqual([13, 14]);

    const reopened = record(fake, checkpoint.cursor);
    await vi.advanceTimersByTimeAsync(0);
    expect(reopened.lines()).toEqual([13, 14]);
  });

  it('repeats `repeat` lines from the live end, and no more', async () => {
    const fake = new FakeClient({ lines: 100, repeat: 7 });
    const tail = await fetchNow(fake, q({ mode: 'Tail', limit: 50 }));
    const follow = record(fake, tail.after.cursor as string);
    await vi.advanceTimersByTimeAsync(0);
    expect(follow.lines()).toEqual([93, 94, 95, 96, 97, 98, 99]);
  });
});

describe('the knobs', () => {
  it('fails the next fetch, and only it', async () => {
    const fake = new FakeClient({ lines: 10 });
    fake.failNext('fetch');
    const failed = fake.fetch(q({}));
    const caught = failed.catch((e: unknown) => e);
    await vi.runOnlyPendingTimersAsync();
    expect(await caught).toBeInstanceOf(Error);
    expect((await fetchNow(fake, q({}))).lines).toHaveLength(10);
  });

  it('throws from the next follow, and only it', () => {
    const fake = new FakeClient({ lines: 10 });
    fake.failNext('follow');
    expect(() => fake.follow(position(0), () => {})).toThrow(Error);
    expect(() => fake.follow(position(0), () => {})).not.toThrow();
  });

  it('keeps a skipped stream out of every page and follow, and names it, until unskipped', async () => {
    const a = stream('a');
    const b = stream('b');
    const fake = new FakeClient({ lines: 10, streams: [a, b], repeat: 0 });
    fake.skip(b.stream);

    const page = await fetchNow(fake, q({ mode: 'Head', limit: 10 }));
    expect(page.lines.map((l) => l.stream.pod)).toEqual(['a', 'a', 'a', 'a', 'a']);
    expect(page.skipped).toHaveLength(1);
    expect(page.skipped[0]).toContain('default/b/app');

    const around = await fetchNow(fake, q({ mode: 'Around', at: fake.lineAt(5).timestamp, limit: 4 }));
    expect(indexes(fake, around.lines)).toEqual([2, 4, 6, 8]);
    expect(around.anchor).toBe(2);

    const follow = record(fake, position(10));
    await vi.advanceTimersByTimeAsync(0);
    fake.append(2);
    expect(follow.lines()).toEqual([10]);

    fake.unskip(b.stream);
    expect((await fetchNow(fake, q({ mode: 'Head', limit: 20 }))).skipped).toEqual([]);
    fake.append(2);
    expect(follow.lines()).toEqual([10, 12, 13]);
  });

  it('answers the next fetch with no line, its read-on edges `limit` past where it started', async () => {
    const fake = new FakeClient({ lines: 100, repeat: 2 });

    fake.emptyNext();
    const head = await fetchNow(fake, q({ mode: 'Head', limit: 10 }));
    expect(head.lines).toEqual([]);
    expect(head.before).toEqual({ cursor: null, more: false });
    expect(head.after.more).toBe(true);
    expect(await firstAfter(fake, head.after.cursor)).toBe(10);

    fake.emptyNext();
    const tail = await fetchNow(fake, q({ mode: 'Tail', cursor: position(50), limit: 10 }));
    expect(tail.lines).toEqual([]);
    expect(tail.before.more).toBe(true);
    expect(tail.after).toEqual({ cursor: position(50), more: true });

    fake.emptyNext();
    const around = await fetchNow(fake, q({ mode: 'Around', at: fake.lineAt(70).timestamp, limit: 5 }));
    expect(around.lines).toEqual([]);
    expect(around.anchor).toBe(0);
    expect(await firstAfter(fake, around.after.cursor)).toBe(75);

    fake.emptyNext();
    const end = await fetchNow(fake, q({ mode: 'Head', cursor: position(95), limit: 10 }));
    expect(end.after.more).toBe(false);
    expect(await firstAfter(fake, end.after.cursor)).toBe(98);

    fake.emptyNext();
    const landing = await fetchNow(fake, q({ mode: 'Tail', limit: 10 }));
    expect(landing.after.more).toBe(false);
    expect(await firstAfter(fake, landing.after.cursor)).toBe(100);

    expect(fake.passedOver()).toEqual([
      { from: 0, to: 10 },
      { from: 40, to: 50 },
      { from: 65, to: 75 },
      { from: 95, to: 98 },
      { from: 90, to: 100 },
    ]);
    const older = await fetchNow(fake, q({ mode: 'Tail', cursor: tail.before.cursor, limit: 1 }));
    expect(indexes(fake, older.lines)).toEqual([39]);
  });

  it('ends every open follow, and sends nothing after', async () => {
    const fake = new FakeClient({ lines: 10, delayMs: 10 });
    const live = record(fake, position(10));
    await vi.advanceTimersByTimeAsync(10);
    const waiting = record(fake, position(0));

    fake.endFollows('the pod went away');
    expect(live.frames).toEqual([{ kind: 'Ended', reason: 'the pod went away' }]);
    expect(waiting.frames).toEqual([{ kind: 'Ended', reason: 'the pod went away' }]);

    fake.append(3);
    await vi.advanceTimersByTimeAsync(10);
    expect(live.frames).toHaveLength(1);
    expect(waiting.frames).toHaveLength(1);
  });
});
