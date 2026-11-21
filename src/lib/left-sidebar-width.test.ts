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

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';

import { useLeftSidebarWidth } from './left-sidebar-width';

beforeEach(() => {
  localStorage.clear();
});

describe('useLeftSidebarWidth', () => {
  it('starts at the library’s 16rem', () => {
    const { result } = renderHook(() => useLeftSidebarWidth());
    expect(result.current[0]).toBe(256);
  });

  it('clamps and remembers a width for the next window', () => {
    const { result } = renderHook(() => useLeftSidebarWidth());
    act(() => result.current[1](9999));
    expect(result.current[0]).toBe(480);
    act(() => result.current[1](10));
    expect(result.current[0]).toBe(200);
    expect(localStorage.getItem('kstack:sidebar-width')).toBe('200');

    const next = renderHook(() => useLeftSidebarWidth());
    expect(next.result.current[0]).toBe(200);
  });
});
