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

import { createStore } from './store';

describe('createStore', () => {
  it('answers the last value written, and tells its listeners once per write until they leave', () => {
    const store = createStore(0);
    const listener = vi.fn();
    const unsubscribe = store.subscribe(listener);

    store.set(1);
    expect(store.get()).toBe(1);
    expect(listener).toHaveBeenCalledOnce();

    unsubscribe();
    store.set(2);
    expect(listener).toHaveBeenCalledOnce();
  });
});
