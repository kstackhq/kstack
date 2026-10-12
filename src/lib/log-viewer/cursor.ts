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
// A cursor is an RFC 3339 timestamp with up to nine fractional digits. The sidecar
// trims trailing zeros, so text order is not time order, and a Date keeps only
// milliseconds, so two lines a nanosecond apart would tie. Both are read here.
import type { Cursor } from './types';

const RFC3339 = /^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?(Z|[+-]\d\d:\d\d)$/i;

/**
 * The cursor's whole milliseconds, and the nanoseconds past them. Only the
 * fraction-free form goes to `Date.parse`, the one shape every engine reads alike.
 * Throws on any other text: two cursors that cannot be read cannot be ordered.
 */
function msAndNanos(cursor: Cursor): [number, number] {
  const match = RFC3339.exec(cursor);
  const ms = match ? Date.parse((match[1] + match[3]).toUpperCase()) : NaN;
  if (!match || Number.isNaN(ms)) throw new RangeError(`not an RFC 3339 cursor: ${cursor}`);
  const nanos = Number((match[2] ?? '').padEnd(9, '0'));
  return [ms + Math.floor(nanos / 1e6), nanos % 1e6];
}

/** Negative when `a` is the earlier moment, positive when it is the later, 0 when they are one. */
export function compareCursors(a: Cursor, b: Cursor): number {
  const [aMs, aNanos] = msAndNanos(a);
  const [bMs, bNanos] = msAndNanos(b);
  return aMs - bMs || aNanos - bNanos;
}

/** This moment as a cursor, to the millisecond, on the webview's clock. */
export function cursorNow(): Cursor {
  return new Date().toISOString();
}

/** The cursor `ms` milliseconds before this one, to the millisecond. */
export function cursorBefore(cursor: Cursor, ms: number): Cursor {
  return new Date(msAndNanos(cursor)[0] - ms).toISOString();
}
