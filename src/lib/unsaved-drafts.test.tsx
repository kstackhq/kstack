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

import { renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it } from 'vitest';

import { UnsavedDrafts, useHoldsUnsaved } from './unsaved-drafts';

function holding(drafts: Set<string> | null) {
  const wrapper = ({ children }: { children: ReactNode }) => <UnsavedDrafts value={drafts}>{children}</UnsavedDrafts>;
  return renderHook(({ held }) => useHoldsUnsaved(held), { wrapper, initialProps: { held: false } });
}

describe('useHoldsUnsaved', () => {
  it('holds its place in the set while it holds work, and lets go after', () => {
    const drafts = new Set<string>();
    const { rerender, unmount } = holding(drafts);
    expect(drafts.size).toBe(0);

    rerender({ held: true });
    expect(drafts.size).toBe(1);
    rerender({ held: false });
    expect(drafts.size).toBe(0);

    rerender({ held: true });
    unmount();
    expect(drafts.size).toBe(0);
  });

  it('does nothing with no set above it', () => {
    const { rerender } = holding(null);
    expect(() => rerender({ held: true })).not.toThrow();
  });
});
