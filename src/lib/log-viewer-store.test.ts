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

import { EMPTY_VIEWER_STATE, createViewerStore } from './log-viewer-store';

describe('createViewerStore', () => {
  it('starts empty, at the end and unpinned, with nothing selected', () => {
    expect(createViewerStore().getState()).toEqual(EMPTY_VIEWER_STATE);
  });

  it('answers the last state written, and tells its listeners once per write', () => {
    const store = createViewerStore();
    const listener = vi.fn();
    const unsubscribe = store.subscribe(listener);

    const next = { range: { from: 'a', to: 'b' }, atEnd: false, pinnedToEnd: false, visible: 3, selection: [] };
    store.set(next);
    expect(store.getState()).toBe(next);
    expect(listener).toHaveBeenCalledOnce();

    unsubscribe();
    store.set(EMPTY_VIEWER_STATE);
    expect(listener).toHaveBeenCalledOnce();
  });
});
