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
import { describe, expect, it } from 'vitest';

import { DoubleTailedArray } from './double-tailed-array';

function items<T>(arr: DoubleTailedArray<T>): T[] {
  return Array.from({ length: arr.length }, (_, i) => arr.at(i));
}

describe('DoubleTailedArray', () => {
  it('starts from its items, or empty', () => {
    expect(items(new DoubleTailedArray())).toEqual([]);
    expect(items(new DoubleTailedArray([1, 2, 3]))).toEqual([1, 2, 3]);
    expect(new DoubleTailedArray([1, 2, 3]).length).toBe(3);
  });

  it('keeps order across appends and prepends, each in the order given', () => {
    const arr = new DoubleTailedArray([3]);
    arr.prepend([1, 2]);
    arr.append([4, 5]);
    arr.prepend([0]);
    expect(items(arr)).toEqual([0, 1, 2, 3, 4, 5]);
    expect(arr.length).toBe(6);
  });

  it('indexes from the first element, whichever end it came in at', () => {
    const arr = new DoubleTailedArray([3]);
    arr.prepend([1, 2]);
    arr.append([4]);
    expect([0, 1, 2, 3].map((i) => arr.at(i))).toEqual([1, 2, 3, 4]);
    expect(arr.first()).toBe(1);
    expect(arr.last()).toBe(4);
  });

  it('throws on an index it does not hold', () => {
    const arr = new DoubleTailedArray([1, 2]);
    expect(() => arr.at(-1)).toThrow(RangeError);
    expect(() => arr.at(2)).toThrow(RangeError);
    expect(() => new DoubleTailedArray().at(0)).toThrow(RangeError);
  });

  it('answers undefined for the first and last of nothing', () => {
    const arr = new DoubleTailedArray();
    expect(arr.first()).toBeUndefined();
    expect(arr.last()).toBeUndefined();
  });

  it('keys every element once, and keeps the key as the array grows at either end', () => {
    const arr = new DoubleTailedArray([10, 20], -1);
    expect(arr.keyAt(0)).toBe(-1);
    expect(arr.keyAt(1)).toBe(0);

    arr.append([30]);
    arr.prepend([1, 2]);
    expect(items(arr).map((_, i) => arr.keyAt(i))).toEqual([-3, -2, -1, 0, 1]);
    expect(arr.at(arr.indexOfKey(0))).toBe(20);
    expect(arr.at(arr.indexOfKey(-3))).toBe(1);
  });
});
