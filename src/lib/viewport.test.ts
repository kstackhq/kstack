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

import { EMPTY_VIEWPORT, createViewportHandle } from './viewport';

describe('createViewportHandle', () => {
  it('starts empty, unpinned and not at the end, with nothing selected', () => {
    expect(createViewportHandle().get()).toEqual(EMPTY_VIEWPORT);
  });

  it('answers the last viewport written, and tells its listeners once per write until they leave', () => {
    const viewport = createViewportHandle();
    const listener = vi.fn();
    const unsubscribe = viewport.subscribe(listener);

    const next = { range: { from: 'a', to: 'b' }, atEnd: false, pinnedToEnd: false, visible: 3, selection: [] };
    viewport.set(next);
    expect(viewport.get()).toBe(next);
    expect(listener).toHaveBeenCalledOnce();

    unsubscribe();
    viewport.set(EMPTY_VIEWPORT);
    expect(listener).toHaveBeenCalledOnce();
  });
});
