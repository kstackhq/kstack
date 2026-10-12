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
import { describe, expect, it, vi } from 'vitest';

import { Lines, Read } from './lines';
import type { LogLine } from './types';

const T0 = Date.parse('2026-10-08T14:00:00Z');

function line(i: number, pod = 'pod-1'): LogLine {
  const timestamp = new Date(T0 + i * 1000).toISOString();
  return {
    timestamp,
    cursor: timestamp,
    message: `${pod} ${i}`,
    source: {
      namespace: 'ns',
      pod,
      container: 'app',
      previous: false,
      node: '',
      region: '',
      zone: '',
      os: '',
      arch: '',
    },
  };
}

/** Where a page that spent its budget stopped, toward either edge. */
const SPENT = line(1).cursor;
const LATER = line(9).cursor;

function messages(lines: Lines) {
  return Array.from({ length: lines.length }, (_, i) => lines.at(i).message);
}

function keys(lines: Lines) {
  return Array.from({ length: lines.length }, (_, i) => lines.keyAt(i));
}

describe('Lines', () => {
  it('starts empty, and tells its listener the length after each change', () => {
    const onChange = vi.fn();
    const lines = new Lines(onChange);
    expect(lines.length).toBe(0);
    expect(lines.from('before')).toBeUndefined();

    lines.reset([line(1), line(2)], 'first');
    lines.prepend([line(0)]);
    lines.append([line(3)]);
    expect(onChange.mock.calls).toEqual([[2], [3], [4]]);
  });

  it('keys from the first or the last line of a reset, and keeps keys as pages land above', () => {
    const lines = new Lines(() => {});
    lines.reset([line(5), line(6)], 'last');
    expect(keys(lines)).toEqual([-1, 0]);

    lines.prepend([line(3), line(4)]);
    lines.append([line(7)]);
    expect(keys(lines)).toEqual([-3, -2, -1, 0, 1]);
    expect(messages(lines)).toEqual(['pod-1 3', 'pod-1 4', 'pod-1 5', 'pod-1 6', 'pod-1 7']);
    expect(lines.at(lines.indexOfKey(0)).message).toBe('pod-1 6');

    lines.reset([line(9)], 'first');
    expect(keys(lines)).toEqual([0]);
  });

  it('reads the next page from a recorded cursor, until lines are added at that edge', () => {
    const lines = new Lines(() => {});
    lines.resumeFrom('before', SPENT);
    lines.reset([], 'first');
    expect(lines.from('before')).toEqual({ cursor: SPENT, keep: false });
    expect(lines.from('after')).toBeUndefined();

    lines.resumeFrom('after', LATER);
    lines.reset([line(5)], 'first');
    // The edge line's moment is kept: other sources' lines can share it.
    expect(lines.from('before')).toEqual({ cursor: line(5).cursor, keep: true });
    expect(lines.from('after')).toEqual({ cursor: line(5).cursor, keep: true });

    lines.resumeFrom('before', SPENT);
    lines.resumeFrom('after', LATER);
    lines.append([line(5)]);
    expect(lines.from('after')).toEqual({ cursor: LATER, keep: false });
    lines.prepend([line(4)]);
    lines.append([line(6)]);
    expect(lines.from('before')).toEqual({ cursor: line(4).cursor, keep: true });
    expect(lines.from('after')).toEqual({ cursor: line(6).cursor, keep: true });
  });

  it('calls beforeChange just before a change, and never for one that changes nothing', () => {
    const order: string[] = [];
    const lines = new Lines(
      () => order.push('changed'),
      () => order.push('before'),
    );
    lines.reset([line(5)], 'first');
    lines.prepend([]);
    lines.append([line(5)]);
    lines.append([line(6)]);
    expect(order).toEqual(['before', 'changed', 'before', 'changed']);
  });

  it('appends a line at or after the before edge from a source it holds none of, and drops one before it', () => {
    const lines = new Lines(() => {});
    lines.reset([line(106), line(107)], 'last');
    // A follow's overlap re-sends pod-2's 105, which the next page before will read.
    lines.append([line(105, 'pod-2'), line(106, 'pod-2'), line(108)]);
    expect(messages(lines)).toEqual(['pod-1 106', 'pod-1 107', 'pod-2 106', 'pod-1 108']);

    lines.prepend([line(105, 'pod-2')]);
    expect(messages(lines)[0]).toBe('pod-2 105');
  });

  it('takes the before edge from a recorded cursor while it holds nothing', () => {
    const lines = new Lines(() => {});
    lines.resumeFrom('before', line(10).cursor);
    lines.reset([], 'last');
    lines.append([line(9), line(10, 'pod-2')]);
    expect(messages(lines)).toEqual(['pod-2 10']);
  });

  it('keeps a second line one source wrote at the same moment, and drops one it holds', () => {
    const lines = new Lines(() => {});
    lines.reset([line(5)], 'first');
    const twin = { ...line(5), message: 'pod-1 5, again' };
    expect(lines.append([line(5), twin])).toBe(1);
    expect(lines.append([line(5), twin])).toBe(0);
    expect(messages(lines)).toEqual(['pod-1 5', 'pod-1 5, again']);
  });

  it('keeps a line one source wrote twice at the same moment, and drops each once re-read', () => {
    const lines = new Lines(() => {});
    lines.reset([line(4)], 'first');
    expect(lines.append([line(5), line(5)])).toBe(2);
    // A page or a follow re-reads both before reaching a third.
    expect(lines.append([line(4), line(5), line(5)])).toBe(0);
    expect(lines.append([line(5), line(5), line(5)])).toBe(1);
    expect(messages(lines)).toEqual(['pod-1 4', 'pod-1 5', 'pod-1 5', 'pod-1 5']);
  });

  it('counts a follow’s re-read across its batches', () => {
    const lines = new Lines(() => {});
    lines.reset([line(5), line(5)], 'first');
    const follow = new Read();
    expect(lines.append([line(5)], follow)).toBe(0);
    expect(lines.append([line(5)], follow)).toBe(0);
    expect(lines.append([line(5)], follow)).toBe(1);
    // A page re-reads all three; the follow, past them, takes a fourth.
    expect(lines.append([line(5), line(5), line(5)])).toBe(0);
    expect(lines.append([line(5)], follow)).toBe(1);
    expect(lines.length).toBe(4);
  });

  it('counts a follow’s replay from where it opened as a re-read', () => {
    const lines = new Lines(() => {});
    lines.reset([line(4)], 'first');
    const follow = new Read();
    expect(lines.append([line(5), line(5)], follow)).toBe(2);
    // A reconnect replays from the opening cursor, back past what the follow read.
    expect(lines.append([line(4), line(5), line(5), line(5)], follow)).toBe(1);
    expect(messages(lines)).toEqual(['pod-1 4', 'pod-1 5', 'pod-1 5', 'pod-1 5']);
  });

  it('drops from a page before the lines it holds at the first line’s moment, and keeps the others there', () => {
    const lines = new Lines(() => {});
    lines.reset([line(5), line(6)], 'first');
    // Read from the first line, keeping its moment: pod-2 wrote there too.
    expect(lines.prepend([line(4), line(5, 'pod-2'), line(5)])).toBe(2);
    expect(messages(lines)).toEqual(['pod-1 4', 'pod-2 5', 'pod-1 5', 'pod-1 6']);
    expect(lines.prepend([line(3), line(4)])).toBe(1);
  });

  it('answers 0 for a page before of lines it holds, and leaves the cursor to read on from', () => {
    const lines = new Lines(() => {});
    lines.reset([line(5)], 'first');
    lines.resumeFrom('before', SPENT);
    expect(lines.prepend([line(5)])).toBe(0);
    expect(lines.from('before')).toEqual({ cursor: SPENT, keep: false });
  });

  it('prepends a page whole but for what it holds at the first line’s moment', () => {
    const lines = new Lines(() => {});
    lines.reset([line(5)], 'first');
    lines.append([line(6, 'pod-2')]);
    lines.prepend([line(0, 'pod-2'), line(1), line(6, 'pod-2')]);
    expect(messages(lines)).toEqual(['pod-2 0', 'pod-1 1', 'pod-2 6', 'pod-1 5', 'pod-2 6']);
  });
});
