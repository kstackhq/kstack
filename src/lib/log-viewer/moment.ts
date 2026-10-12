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
// A moment as a cursor: a bare RFC 3339 time, the one cursor form the contract
// lets the core mint. It is read off the clock, never off a cursor.
import type { Cursor } from './types';

/** A timestamp's milliseconds. The fraction past three digits goes first: `Date.parse` reads no more. */
function millis(timestamp: string): number {
  const ms = Date.parse(timestamp.replace(/(\.\d{3})\d+/, '$1'));
  if (Number.isNaN(ms)) throw new RangeError(`not a timestamp: ${timestamp}`);
  return ms;
}

/** This moment, to the millisecond, on the webview's clock. */
export function momentNow(): Cursor {
  return new Date().toISOString();
}

/** The moment `ms` before `timestamp`, and no earlier than `floor`, to the millisecond. */
export function momentBefore(timestamp: string, ms: number, floor: string): Cursor {
  return new Date(Math.max(millis(timestamp) - ms, millis(floor))).toISOString();
}
