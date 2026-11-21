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

import { SidebarProvider } from '@kubetail/ui/elements/sidebar';
import { fireEvent, render as rtlRender, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { AppMode } from '@/lib/app-mode';
import {
  MAC_USER_AGENT,
  NON_MAC_USER_AGENT,
  mockMatchMedia,
  mockTauriCore,
  mockTauriWindow,
  restoreUserAgent,
  setUserAgent,
} from '@/test-utils';

// Mocks ---------------------------------------------------------------

// `WindowControls` (rendered off macOS) drives the native window via
// `getCurrentWindow`, so the window API must be mocked before importing.
const { factory } = mockTauriWindow();
vi.mock('@tauri-apps/api/window', () => factory());

// Each control has its own suite; here they only mark their place. The history nav
// needs a router, the title and the chips read providers.
vi.mock('./history-nav', () => ({
  HistoryNav: () => <div data-testid="history-nav" />,
}));
vi.mock('./title', () => ({
  AppBarTitle: ({ mode }: { mode: AppMode }) => <div data-testid="app-bar-title" data-mode={mode} />,
}));
vi.mock('@/components/shell/context-omnibox/omnibox', () => ({
  ContextOmnibox: () => <div data-testid="context-omnibox" />,
}));
vi.mock('@/components/dashboard/dashboard-resource-nav', () => ({
  NamespacePicker: () => <div data-testid="namespace-picker" />,
}));
vi.mock('@/components/shell/right-sidebar', () => ({
  RightSidebarToggle: () => <button type="button" aria-label="Toggle right sidebar" />,
}));

// `AppMenu` (rendered off macOS) drives the host over `invoke`.
const { invokeMock, factory: coreFactory } = mockTauriCore();
vi.mock('@tauri-apps/api/core', () => coreFactory());

const { AppBar } = await import('./bar');

// Helpers -------------------------------------------------------------

// The left toggle reads the provider the layout mounts.
function render({
  mode,
  sidebarWidth = 256,
  open = true,
}: { mode?: AppMode; sidebarWidth?: number; open?: boolean } = {}) {
  return rtlRender(
    <SidebarProvider open={open}>
      <AppBar mode={mode ?? 'chat'} sidebarWidth={sidebarWidth} />
    </SidebarProvider>,
  );
}

const bands = () => Array.from(screen.getByTestId('app-bar').children);
const indexOf = (el: Element) => bands().findIndex((band) => band === el || band.contains(el));

beforeEach(() => {
  invokeMock.mockReset();
  invokeMock.mockResolvedValue(undefined);
  setUserAgent(NON_MAC_USER_AGENT);
});

afterEach(() => {
  restoreUserAgent();
});

// Tests ---------------------------------------------------------------

describe('AppBar', () => {
  it('holds its height as the layout column’s top band, on the chrome’s colour', () => {
    render();
    // 56px, and never shrunk by the row below it. It paints the chrome itself: its
    // compositor layer would otherwise show through on a Linux resize.
    expect(screen.getByTestId('app-bar')).toHaveClass('h-14', 'shrink-0', 'bg-border', 'transform-gpu');
  });

  it('drags the window from anywhere it is not a control, on every platform', () => {
    const { unmount } = render();
    expect(screen.getByTestId('app-bar')).toHaveAttribute('data-tauri-drag-region', 'deep');
    unmount();

    setUserAgent(MAC_USER_AGENT);
    render();
    expect(screen.getByTestId('app-bar')).toHaveAttribute('data-tauri-drag-region', 'deep');
  });

  it('clicks the pressed element when the press drags the window', () => {
    render();
    const spacer = screen.getByTestId('app-bar-spacer');
    const onClick = vi.fn();
    spacer.addEventListener('click', onClick);
    fireEvent.pointerDown(spacer, { button: 0 });
    expect(onClick).toHaveBeenCalledOnce();
  });

  it('reads left to right: gutter, divider, cluster chip, title, spacer, right toggle, caption buttons', () => {
    render();
    const order = [
      screen.getByTestId('app-bar-gutter'),
      screen.getByTestId('app-bar-divider'),
      screen.getByTestId('context-omnibox'),
      screen.getByTestId('app-bar-title'),
      screen.getByTestId('app-bar-spacer'),
      screen.getByRole('button', { name: 'Toggle right sidebar' }),
      screen.getByRole('button', { name: /close/i }),
    ].map(indexOf);
    expect(order).toEqual(order.toSorted((a, b) => a - b));
    expect(new Set(order).size).toBe(order.length);
    expect(screen.getByTestId('app-bar-spacer')).toHaveClass('flex-1');
  });

  it('holds the window’s controls, the history nav and the left toggle in the gutter', () => {
    render();
    const gutter = screen.getByTestId('app-bar-gutter');
    const inside = [
      screen.getByRole('button', { name: /application menu/i }),
      screen.getByTestId('history-nav'),
      screen.getByRole('button', { name: 'Toggle sidebar' }),
    ];
    expect(inside.every((el) => gutter.contains(el))).toBe(true);
    expect(Array.from(gutter.children)).toEqual(inside);
  });

  it('widens the gutter to the page’s left edge while the sidebar is open', () => {
    render({ sidebarWidth: 300 });
    // 38px rail, 8px gap, the sidebar, the main card's 1px border.
    expect(screen.getByTestId('app-bar-gutter').style.width).toBe('347px');
  });

  it('hugs its contents while the sidebar is collapsed', () => {
    render({ open: false });
    expect(screen.getByTestId('app-bar-gutter').style.width).toBe('');
  });

  it('hugs its contents on a narrow window, where the sidebar is a drawer', () => {
    // `matches` means "below the medium breakpoint".
    const setNarrow = mockMatchMedia();
    Object.defineProperty(window, 'innerWidth', { value: 600, configurable: true });
    setNarrow(true);
    render();
    expect(screen.getByTestId('app-bar-gutter').style.width).toBe('');
    Object.defineProperty(window, 'innerWidth', { value: 1024, configurable: true });
    setNarrow(false);
  });

  it('collapses the sidebar from its toggle', () => {
    const onOpenChange = vi.fn();
    rtlRender(
      <SidebarProvider open onOpenChange={onOpenChange}>
        <AppBar mode="chat" sidebarWidth={256} />
      </SidebarProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Toggle sidebar' }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('passes the mode to the title', () => {
    render({ mode: 'dashboard' });
    expect(screen.getByTestId('app-bar-title')).toHaveAttribute('data-mode', 'dashboard');
  });

  it('draws the namespace scope chip on the dashboard alone, after the spacer', () => {
    const { unmount } = render({ mode: 'dashboard' });
    const chip = screen.getByTestId('namespace-picker');
    expect(indexOf(screen.getByTestId('app-bar-spacer'))).toBeLessThan(indexOf(chip));
    expect(indexOf(chip)).toBeLessThan(indexOf(screen.getByRole('button', { name: 'Toggle right sidebar' })));
    unmount();

    render({ mode: 'chat' });
    expect(screen.queryByTestId('namespace-picker')).not.toBeInTheDocument();
  });

  it('keeps notifications and settings off the bar', () => {
    render();
    expect(screen.queryByRole('button', { name: 'Notifications' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Settings' })).not.toBeInTheDocument();
  });

  it('ends 12px from the edge, or from the caption buttons', () => {
    render();
    const toggle = screen.getByRole('button', { name: 'Toggle right sidebar' });
    expect(toggle.parentElement).toHaveClass('pr-3', 'pl-1');
  });

  // The bar is the one place that decides, so `AppMenu`'s `Ctrl/Cmd+N`/`Q`
  // accelerators ride on that decision — they register only where it mounts.
  it('carries the menu accelerators off macOS', () => {
    render();
    fireEvent.keyDown(window, { key: 'n', ctrlKey: true });
    expect(invokeMock).toHaveBeenCalledWith('new_window');
  });

  it('leaves the accelerators to the native menu on macOS', () => {
    setUserAgent(MAC_USER_AGENT);
    render();
    // Would fire twice beside the native menu item if the menu mounted here.
    fireEvent.keyDown(window, { key: 'n', metaKey: true });
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it('reserves the traffic lights and drops the caption chrome on macOS', () => {
    setUserAgent(MAC_USER_AGENT);
    render();
    // The OS draws the lights and owns the menu bar, so the bar only makes room.
    const gutter = screen.getByTestId('app-bar-gutter');
    expect(gutter.firstElementChild).toBe(screen.getByTestId('traffic-light-gutter'));
    expect(screen.queryByRole('button', { name: /application menu/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /minimize/i })).not.toBeInTheDocument();
  });
});
