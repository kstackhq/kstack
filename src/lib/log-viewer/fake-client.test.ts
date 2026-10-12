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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { FakeClient } from './fake-client';
import type { FetchResult } from './types';

const LINES = 100;

let client: FakeClient;

beforeEach(() => {
  vi.useFakeTimers();
  client = new FakeClient({ lines: LINES, delayMs: 10 });
});

afterEach(() => {
  vi.useRealTimers();
});

/** Runs the fake's delay out, then reads the page as its messages and where the next starts. */
async function read(pending: Promise<FetchResult>) {
  await vi.runAllTimersAsync();
  const { lines, nextCursor, skipped } = await pending;
  expect(skipped).toEqual([]);
  return { messages: lines.map((line) => line.message), next: nextCursor };
}

const cursor = (index: number) => client.lineAt(index).cursor;

describe('FakeClient', () => {
  it('counts its lines from the end too', () => {
    expect(client.lineCount).toBe(LINES);
    expect(client.lineAt(-1)).toEqual(client.lineAt(LINES - 1));
    expect(() => client.lineAt(LINES)).toThrow(RangeError);
    expect(() => client.lineAt(-LINES - 1)).toThrow(RangeError);
  });

  it('names each line’s pod by its index, appended lines included', () => {
    const pods = new FakeClient({ lines: 2, podOf: (index) => `pod-${index % 2}` });
    pods.append();
    expect([0, 1, 2].map((index) => pods.lineAt(index).stream.pod)).toEqual(['pod-0', 'pod-1', 'pod-0']);
  });

  it('starts empty with no lines, and grows from there', async () => {
    const empty = new FakeClient({ lines: 0 });
    expect(empty.lineCount).toBe(0);
    expect(await read(empty.fetchSince({ limit: 10 }))).toEqual({ messages: [], next: null });
    empty.append();
    expect(empty.lineAt(0).message).toBe('line 0');
  });

  it('names a line by a cursor that is no timestamp, reads a bare time as a moment, and refuses the rest', async () => {
    expect(cursor(3)).toBe('#3');
    expect(await read(client.fetchSince({ cursor: client.lineAt(3).timestamp, limit: 1 }))).toMatchObject({
      messages: ['line 3'],
    });
    expect(() => client.fetchSince({ cursor: '2026-10-08T10:00:00Z\tns\tpod\tapp\tfalse\t0', limit: 1 })).toThrow(
      RangeError,
    );
  });

  it('answers after its delay', async () => {
    const answered = vi.fn();
    client.fetchSince({ limit: 1 }).then(answered);
    await vi.advanceTimersByTimeAsync(9);
    expect(answered).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(answered).toHaveBeenCalledOnce();
  });

  describe('fetchSince', () => {
    it('reads from the start, or from the cursor’s own line', async () => {
      expect(await read(client.fetchSince({ limit: 3 }))).toEqual({
        messages: ['line 0', 'line 1', 'line 2'],
        next: cursor(2),
      });
      expect(await read(client.fetchSince({ cursor: cursor(3), limit: 2 }))).toEqual({
        messages: ['line 3', 'line 4'],
        next: cursor(4),
      });
    });

    it('stops at the end, with no next cursor', async () => {
      expect(await read(client.fetchSince({ cursor: cursor(-2), limit: 5 }))).toEqual({
        messages: ['line 98', 'line 99'],
        next: null,
      });
      expect(await read(client.fetchSince({ limit: LINES + 1 }))).toMatchObject({ next: null });
      expect(await read(client.fetchSince({ cursor: new Date(Date.now() + 1).toISOString(), limit: 5 }))).toEqual({
        messages: [],
        next: null,
      });
    });

    it('answers no line for a limit of 0, and a cursor just before where it started', async () => {
      expect(await read(client.fetchSince({ limit: 0 }))).toEqual({
        messages: [],
        next: '#-1',
      });
    });
  });

  describe('fetchUntil', () => {
    it('reads up to the end, or up to the cursor’s own line', async () => {
      expect(await read(client.fetchUntil({ limit: 3 }))).toEqual({
        messages: ['line 97', 'line 98', 'line 99'],
        next: cursor(97),
      });
      expect(await read(client.fetchUntil({ cursor: cursor(9), limit: 2 }))).toEqual({
        messages: ['line 8', 'line 9'],
        next: cursor(8),
      });
    });

    it('stops at the start, with no next cursor', async () => {
      expect(await read(client.fetchUntil({ cursor: cursor(2), limit: 5 }))).toEqual({
        messages: ['line 0', 'line 1', 'line 2'],
        next: null,
      });
      expect(
        await read(
          client.fetchUntil({ cursor: new Date(Date.parse(client.lineAt(0).timestamp) - 1).toISOString(), limit: 5 }),
        ),
      ).toEqual({ messages: [], next: null });
    });

    it('reads the whole log the same way round as fetchSince', async () => {
      const since = await read(client.fetchSince({ limit: Infinity }));
      const until = await read(client.fetchUntil({ limit: Infinity }));
      expect(since.messages).toHaveLength(LINES);
      expect(until).toEqual(since);
    });
  });

  describe('fetchAfter and fetchBefore', () => {
    it('leave the cursor’s line out', async () => {
      expect(await read(client.fetchAfter({ cursor: cursor(0), limit: 2 }))).toEqual({
        messages: ['line 1', 'line 2'],
        next: cursor(2),
      });
      expect(await read(client.fetchBefore({ cursor: cursor(10), limit: 2 }))).toEqual({
        messages: ['line 8', 'line 9'],
        next: cursor(8),
      });
    });

    it('read on from a page’s next cursor with no line skipped or repeated, either way', async () => {
      const forward: string[] = [];
      let next: string | null = (await read(client.fetchSince({ limit: 0 }))).next;
      while (next !== null) {
        // eslint-disable-next-line no-await-in-loop -- each page reads from the one before
        const result = await read(client.fetchAfter({ cursor: next, limit: 7 }));
        forward.push(...result.messages);
        next = result.next;
      }
      expect(forward).toEqual(Array.from({ length: LINES }, (_, i) => `line ${i}`));

      const backward: string[] = [];
      next = cursor(-1);
      backward.unshift('line 99');
      while (next !== null) {
        // eslint-disable-next-line no-await-in-loop -- each page reads from the one before
        const result = await read(client.fetchBefore({ cursor: next, limit: 7 }));
        backward.unshift(...result.messages);
        next = result.next;
      }
      expect(backward).toEqual(forward);
    });

    it('answer nothing past either end', async () => {
      expect(await read(client.fetchAfter({ cursor: cursor(-1), limit: 5 }))).toEqual({ messages: [], next: null });
      expect(await read(client.fetchBefore({ cursor: cursor(0), limit: 5 }))).toEqual({ messages: [], next: null });
    });

    it('read from either end with no cursor', async () => {
      expect(await read(client.fetchAfter({ limit: 1 }))).toMatchObject({ messages: ['line 0'] });
      expect(await read(client.fetchBefore({ limit: 1 }))).toMatchObject({ messages: ['line 99'] });
    });
  });

  describe('subscribe', () => {
    it('sends what is appended, as it is appended', async () => {
      const received: string[] = [];
      client.subscribe(null, (line) => received.push(line.message));
      client.setAppendRate(10);
      await vi.advanceTimersByTimeAsync(200);
      expect(received).toEqual(['line 100', 'line 101']);
      expect(client.lineCount).toBe(102);
    });

    it('replays the lines after the cursor first, then what arrived meanwhile', async () => {
      client = new FakeClient({ lines: LINES, delayMs: 150 });
      const received: string[] = [];
      client.subscribe(cursor(-3), (line) => received.push(line.message));
      client.setAppendRate(10);

      // A line appended while the replay is in flight waits its turn.
      await vi.advanceTimersByTimeAsync(100);
      expect(received).toEqual([]);
      await vi.advanceTimersByTimeAsync(50);
      expect(received).toEqual(['line 98', 'line 99', 'line 100']);

      await vi.advanceTimersByTimeAsync(50);
      expect(received).toEqual(['line 98', 'line 99', 'line 100', 'line 101']);
    });

    it('sends what arrived meanwhile when the replay finds nothing after the cursor', async () => {
      const received: string[] = [];
      client.subscribe(cursor(-1), (line) => received.push(line.message));
      client.append();
      await vi.runAllTimersAsync();
      expect(received).toEqual(['line 100']);
    });

    it('sends nothing once cancelled, a replay in flight included', async () => {
      const received: string[] = [];
      const cancel = client.subscribe(cursor(-3), (line) => received.push(line.message));
      client.setAppendRate(10);
      cancel();
      await vi.advanceTimersByTimeAsync(200);
      expect(received).toEqual([]);
    });

    it('grows the log whether or not anyone listens', async () => {
      client.setAppendRate(1000);
      await vi.advanceTimersByTimeAsync(100);
      expect(client.lineCount).toBe(LINES + 100);
      client.setAppendRate(0);
      await vi.advanceTimersByTimeAsync(1000);
      expect(client.lineCount).toBe(LINES + 100);
    });
  });
});
