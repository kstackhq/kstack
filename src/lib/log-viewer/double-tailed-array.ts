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

/**
 * An array that grows at both ends in amortised O(1), indexed from its first
 * element like any array. Each element also has a key that never changes while it
 * is held: keys run `keyOffset`, `keyOffset + 1`, …, and a prepend lowers
 * `keyOffset` by what it adds, so the elements already held keep theirs.
 */
export class DoubleTailedArray<T> {
  /** Prepended elements, last prepended first: the array starts at its end. */
  private before: T[] = [];

  private after: T[];

  private offset: number;

  constructor(items: T[] = [], keyOffset = 0) {
    this.after = [...items];
    this.offset = keyOffset;
  }

  get length(): number {
    return this.before.length + this.after.length;
  }

  keyAt(index: number): number {
    return this.offset + index;
  }

  /** Unchecked: the caller knows whether the key is held. */
  indexOfKey(key: number): number {
    return key - this.offset;
  }

  at(index: number): T {
    if (index < 0 || index >= this.length) throw new RangeError(`index ${index} of ${this.length}`);
    if (index < this.before.length) return this.before[this.before.length - 1 - index];
    return this.after[index - this.before.length];
  }

  first(): T | undefined {
    return this.length ? this.at(0) : undefined;
  }

  last(): T | undefined {
    return this.length ? this.at(this.length - 1) : undefined;
  }

  // Both add with a loop: a spread's arguments are capped, and a follow's batch is not.

  append(values: T[]): void {
    for (let i = 0; i < values.length; i += 1) this.after.push(values[i]);
  }

  /** Adds `values` at the start, in their own order. */
  prepend(values: T[]): void {
    this.offset -= values.length;
    for (let i = values.length - 1; i >= 0; i -= 1) this.before.push(values[i]);
  }
}
