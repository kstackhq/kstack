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

import { Lines } from './lines';
import type { LogLine } from './types';

const T0 = Date.parse('2026-10-08T14:00:00Z');

/**
 * The `n`th line pod `pod` wrote at second `i`. The cursor is not a timestamp, as
 * the sidecar's is not: the library reads nothing from it.
 */
function line(i: number, pod = 'pod-1', n = 0): LogLine {
  const timestamp = new Date(T0 + i * 1000).toISOString();
  return {
    timestamp,
    cursor: `${pod}#${i}#${n}`,
    message: `${pod} ${i}`,
    stream: {
      namespace: 'ns',
      pod,
      container: 'app',
      previous: false,
    },
    place: {
      node: '',
      region: '',
      zone: '',
      os: '',
      arch: '',
    },
  };
}

/** Where a page that spent its budget stopped, toward either edge. */
const SPENT = 'stop#1';
const LATER = 'stop#9';

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
    // The edge line is kept: other streams' lines can share its moment.
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
    lines.prepend([line(4)]);
    expect(order).toEqual(['before', 'changed', 'before', 'changed']);
  });

  it('keeps the order it is given, a page before whole and a follow as it arrives', () => {
    const lines = new Lines(() => {});
    lines.reset([line(5)], 'first');
    expect(lines.prepend([line(2, 'pod-2'), line(3), line(4, 'pod-2')])).toBe(3);
    // A container behind another lands its line after the newer one: real, and kept there.
    expect(lines.append([line(7, 'pod-2'), line(6)])).toBe(2);
    expect(messages(lines)).toEqual(['pod-2 2', 'pod-1 3', 'pod-2 4', 'pod-1 5', 'pod-2 7', 'pod-1 6']);
  });

  it('drops a line it holds, by its cursor alone', () => {
    const lines = new Lines(() => {});
    lines.reset([line(5), line(5, 'pod-2')], 'first');
    // A page from the edge line sends it again with its moment's other lines; a follow's
    // replay sends what the follow sent already. Neither adds a row.
    expect(lines.prepend([line(4), line(5), line(5, 'pod-2')])).toBe(1);
    expect(lines.append([line(5), line(5, 'pod-2'), line(6)])).toBe(1);
    expect(messages(lines)).toEqual(['pod-1 4', 'pod-1 5', 'pod-2 5', 'pod-1 6']);
  });

  it('keeps two lines a container wrote at one moment, told apart by their cursors', () => {
    const lines = new Lines(() => {});
    lines.reset([line(5)], 'first');
    expect(lines.append([line(5, 'pod-1', 1), line(5, 'pod-1', 1)])).toBe(1);
    expect(messages(lines)).toEqual(['pod-1 5', 'pod-1 5']);
  });

  it('answers 0 for a page of lines it holds, and leaves the cursor to read on from', () => {
    const lines = new Lines(() => {});
    lines.reset([line(5)], 'first');
    lines.resumeFrom('before', SPENT);
    expect(lines.prepend([line(5)])).toBe(0);
    expect(lines.from('before')).toEqual({ cursor: SPENT, keep: false });
  });

  it('forgets what it held on a reset', () => {
    const lines = new Lines(() => {});
    lines.reset([line(5)], 'first');
    lines.reset([line(6)], 'first');
    expect(lines.append([line(5)])).toBe(1);
  });
});
