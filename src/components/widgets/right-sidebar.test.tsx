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

import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { LogsViewProvider, useLogsView } from '@/lib/logs-view';
import type { LogsView } from '@/lib/logs-view';
import { RightSidebarProvider } from '@/lib/right-sidebar';
import type { AppMode } from '@/lib/app-mode';

// The panel's contents have a suite of their own, and the real widget wants a router,
// a urql client and the Tauri bridge. This file is about which one is mounted.
vi.mock('@/components/widgets/dashboard-chat', () => ({
  DashboardChat: () => <div data-testid="dashboard-chat" />,
}));

const { RightSidebar, RightSidebarToggle } = await import('./right-sidebar');

// Helpers -------------------------------------------------------------

// The toggle and the panel sit in different branches of the layout, so a test
// renders both under the state they share.
const renderPanel = ({ mode = 'chat' }: { mode?: AppMode } = {}) =>
  render(
    <RightSidebarProvider mode={mode}>
      <LogsViewProvider>
        <RightSidebarToggle />
        <RightSidebar />
        <FocusView />
      </LogsViewProvider>
    </RightSidebarProvider>,
  );

const logsView: LogsView = {
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

// Stands in for whatever sets the focused view: the card's Expand, or the newest call.
function FocusView() {
  const { set } = useLogsView();
  return (
    <button type="button" onClick={() => set(logsView)}>
      focus view
    </button>
  );
}

const panel = () => screen.queryByTestId('right-sidebar');
const panelWidth = () => screen.getByTestId('right-sidebar').style.width;
const handle = () => screen.getByRole('separator', { name: 'Resize right sidebar' });

// jsdom doesn't implement pointer capture; the handle calls it, so stub it out.
if (!HTMLElement.prototype.setPointerCapture) {
  HTMLElement.prototype.setPointerCapture = () => {};
}

beforeEach(() => {
  localStorage.clear();
  // The drag measures from the window's right edge, so tests need a known one.
  Object.defineProperty(window, 'innerWidth', { value: 1000, configurable: true });
});
const clickToggle = () => fireEvent.click(screen.getByRole('button', { name: /toggle right sidebar/i }));

// A drag that a test leaves mid-flight keeps its window listeners and the forced
// cursor, which the next test would inherit.
afterEach(() => {
  fireEvent.pointerUp(window);
});

// Tests ---------------------------------------------------------------

describe('RightSidebar', () => {
  it('stays closed until it is asked for', () => {
    renderPanel();
    expect(panel()).not.toBeInTheDocument();
  });

  it('opens and closes from the toggle', () => {
    renderPanel();
    clickToggle();
    expect(panel()).toBeInTheDocument();

    clickToggle();
    expect(panel()).not.toBeInTheDocument();
  });

  it('says whether it is open, for anything reading the button', () => {
    renderPanel();
    const toggle = screen.getByRole('button', { name: /toggle right sidebar/i });
    expect(toggle).toHaveAttribute('aria-pressed', 'false');

    clickToggle();
    expect(toggle).toHaveAttribute('aria-pressed', 'true');
  });

  it('sits in the row as an ordinary flex item, flush to the window', () => {
    renderPanel();
    clickToggle();
    // Not floating: it takes width from the page and the row's height makes it
    // flush with the bar above and the window's bottom edge.
    expect(panel()).toHaveClass('shrink-0');
    expect(panel()).not.toHaveClass('absolute', 'fixed', 'rounded-lg');
  });

  it('resizes by dragging its inner edge, and persists the width', () => {
    renderPanel();
    clickToggle();
    expect(panelWidth()).toBe('400px');

    // The panel ends at the window's right edge, so its width is what is left of
    // the window from the pointer.
    fireEvent.pointerDown(handle(), { pointerId: 1 });
    fireEvent.pointerMove(window, { clientX: 600 });
    expect(panelWidth()).toBe('400px');
    expect(localStorage.getItem('kstack:right-sidebar-width:chat')).toBe('400');

    fireEvent.pointerUp(window);
    // Releasing detaches the listeners: further movement is ignored.
    fireEvent.pointerMove(window, { clientX: 700 });
    expect(panelWidth()).toBe('400px');
  });

  it('clamps the dragged width to the min/max bounds', () => {
    renderPanel();
    clickToggle();

    fireEvent.pointerDown(handle(), { pointerId: 1 });
    fireEvent.pointerMove(window, { clientX: 10 });
    expect(panelWidth()).toBe('640px');
    fireEvent.pointerMove(window, { clientX: 990 });
    expect(panelWidth()).toBe('240px');
  });

  it('restores the last-saved width on mount (new windows inherit it)', () => {
    localStorage.setItem('kstack:right-sidebar-width:chat', '500');
    renderPanel();
    clickToggle();
    expect(panelWidth()).toBe('500px');
  });

  it('keeps its width per mode, since the two hold different things', () => {
    localStorage.setItem('kstack:right-sidebar-width:chat', '300');
    localStorage.setItem('kstack:right-sidebar-width:dashboard', '500');

    const chat = renderPanel();
    clickToggle();
    expect(panelWidth()).toBe('300px');
    chat.unmount();

    renderPanel({ mode: 'dashboard' });
    clickToggle();
    expect(panelWidth()).toBe('500px');
  });

  it('shows the chat’s panel in chat mode', () => {
    renderPanel();
    clickToggle();
    expect(screen.getByRole('complementary', { name: 'Chat details' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Conversation' })).toBeInTheDocument();
  });

  it('draws the focused log view in chat mode, and the placeholder again once it is closed', () => {
    renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'focus view' }));
    // Setting the view opens the panel by itself.
    expect(screen.getByRole('heading', { name: /Logs: Deployment webapp in prod/ })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Conversation' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Close log view' }));
    expect(panel()).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Conversation' })).toBeInTheDocument();
  });

  it('holds a chat in dashboard mode', () => {
    renderPanel({ mode: 'dashboard' });
    clickToggle();
    expect(screen.getByRole('complementary', { name: 'Dashboard chat' })).toBeInTheDocument();
    expect(screen.getByTestId('dashboard-chat')).toBeInTheDocument();
  });

  // The scroller is inside the panel now, and a scroller inside a scroller gives two
  // scrollbars that fight.
  it('does not scroll itself', () => {
    renderPanel({ mode: 'dashboard' });
    clickToggle();
    expect(panel()).not.toHaveClass('overflow-y-auto');
  });
});
