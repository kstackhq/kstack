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
import { describe, expect, it } from 'vitest';

import { compareCursors, cursorBefore, cursorNow } from './cursor';

describe('compareCursors', () => {
  it('orders by time, whatever the number of fractional digits', () => {
    // Go's RFC3339Nano trims trailing zeros, so text order would put .1 after .12.
    expect(compareCursors('2026-10-08T14:02:05.1Z', '2026-10-08T14:02:05.12Z')).toBeLessThan(0);
    expect(compareCursors('2026-10-08T14:02:05Z', '2026-10-08T14:02:05.000000001Z')).toBeLessThan(0);
    expect(compareCursors('2026-10-08T14:02:06Z', '2026-10-08T14:02:05.999999999Z')).toBeGreaterThan(0);
  });

  it('tells nanoseconds apart that a Date cannot', () => {
    expect(compareCursors('2026-10-08T14:02:05.123456789Z', '2026-10-08T14:02:05.123456788Z')).toBeGreaterThan(0);
  });

  it('reads a lower-case T and Z', () => {
    expect(compareCursors('2026-10-08t14:02:05.5z', '2026-10-08T14:02:05.5Z')).toBe(0);
  });

  it('throws on a cursor it cannot read, rather than calling it equal to another', () => {
    expect(() => compareCursors('2026-10-08 14:02:05Z', '2026-10-08T14:02:05Z')).toThrow(RangeError);
    expect(() => compareCursors('2026-10-08T14:02:05Z', 'later')).toThrow(RangeError);
  });

  it('answers 0 for the same moment spelled two ways', () => {
    expect(compareCursors('2026-10-08T14:02:05.5Z', '2026-10-08T14:02:05.500Z')).toBe(0);
    expect(compareCursors('2026-10-08T16:02:05+02:00', '2026-10-08T14:02:05Z')).toBe(0);
  });
});

describe('cursorNow', () => {
  it('is the clock, as a cursor', () => {
    const before = Date.now();
    const now = cursorNow();
    expect(Date.parse(now)).toBeGreaterThanOrEqual(before);
    expect(Date.parse(now)).toBeLessThanOrEqual(Date.now());
    expect(compareCursors(now, cursorBefore(now, 1))).toBeGreaterThan(0);
  });
});

describe('cursorBefore', () => {
  it('steps a cursor back by a number of milliseconds', () => {
    expect(cursorBefore('2026-10-08T14:02:05.123456789Z', 5000)).toBe('2026-10-08T14:02:00.123Z');
    expect(cursorBefore('2026-10-08T16:02:05.5+02:00', 1000)).toBe('2026-10-08T14:02:04.500Z');
  });

  it('throws on a cursor it cannot read', () => {
    expect(() => cursorBefore('soon', 5000)).toThrow(RangeError);
  });
});
