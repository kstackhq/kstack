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
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it } from 'vitest';

import { RightSidebarProvider, useRightSidebar } from '@/lib/right-sidebar';
import { LogsViewProvider, useLogsView } from './logs-view';
import type { LogsView } from './logs-view';

const view: LogsView = {
  chatId: 'chat-1',
  callId: 'call-1',
  action: {
    sources: [{ namespace: 'prod', kind: 'Deployment', name: 'webapp', containers: [], previous: false }],
    filters: [],
    grep: '',
    anchor: { kind: 'Tail', at: null },
    pinToEnd: true,
  },
};

function wrapper({ children }: { children: ReactNode }) {
  return (
    <RightSidebarProvider mode="chat">
      <LogsViewProvider>{children}</LogsViewProvider>
    </RightSidebarProvider>
  );
}

// The focused view beside the sidebar's open state, which it writes.
const renderBoth = () => renderHook(() => ({ logs: useLogsView(), sidebar: useRightSidebar() }), { wrapper });

// The sidebar's open state is persisted app-wide, so each case starts pristine.
beforeEach(() => {
  localStorage.clear();
});

describe('useLogsView', () => {
  it('starts with no view, so a new window opens on none', () => {
    const { result } = renderBoth();
    expect(result.current.logs.view).toBeNull();
    expect(result.current.sidebar.open).toBe(false);
  });

  it('opens the right sidebar when a view is set', () => {
    const { result } = renderBoth();
    act(() => result.current.logs.set(view));
    expect(result.current.logs.view).toBe(view);
    expect(result.current.sidebar.open).toBe(true);
  });

  it('leaves the sidebar open when the view is cleared', () => {
    const { result } = renderBoth();
    act(() => result.current.logs.set(view));
    act(() => result.current.logs.clear());
    expect(result.current.logs.view).toBeNull();
    expect(result.current.sidebar.open).toBe(true);
  });

  it('holds one viewer store for the window, whichever view is set', () => {
    const { result } = renderBoth();
    const { viewer } = result.current.logs;
    act(() => result.current.logs.set(view));
    expect(result.current.logs.viewer).toBe(viewer);
  });

  it('needs its provider', () => {
    expect(() => renderHook(() => useLogsView())).toThrow('within a LogsViewProvider');
  });
});
