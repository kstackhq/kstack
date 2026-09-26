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

import {
  MAC_USER_AGENT,
  NON_MAC_USER_AGENT,
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

// The home button and the history nav both need a router; this suite renders the
// bar outside one, so stub them — their own suites cover the real ones.
vi.mock('@/components/widgets/home-button', () => ({
  HomeButton: () => <div data-testid="home-button" />,
}));
vi.mock('@/components/widgets/history-nav', () => ({
  HistoryNav: () => <div data-testid="history-nav" />,
}));
// The account control reads the auth provider, the notification button opens a
// popover, and the settings button opens the dialog host; their own suites cover
// the real ones.
vi.mock('@/components/widgets/account-avatar', () => ({
  AccountAvatar: () => <div data-testid="account-avatar" />,
}));
vi.mock('@/components/widgets/notification-button', () => ({
  NotificationButton: () => <div data-testid="notification-button" />,
}));
vi.mock('@/components/widgets/settings-button', () => ({
  SettingsButton: () => <div data-testid="settings-button" />,
}));
// The omnibox reads the kubeconfig; its own suite covers it.
vi.mock('@/components/widgets/context-omnibox', () => ({
  ContextOmnibox: () => <div data-testid="context-omnibox" />,
}));

// `AppMenu` (rendered off macOS) drives the host over `invoke`.
const { invokeMock, factory: coreFactory } = mockTauriCore();
vi.mock('@tauri-apps/api/core', () => coreFactory());

const { AppBar } = await import('./app-bar');

// Helpers -------------------------------------------------------------

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
  it('holds its height as the layout column’s top band', () => {
    render(<AppBar />);
    // 56px, and never shrunk by the row below it.
    expect(screen.getByTestId('app-bar')).toHaveClass('h-14', 'shrink-0');
  });

  it('drags the window from anywhere it is not a control, on every platform', () => {
    const { unmount } = render(<AppBar />);
    // `deep`, so the spacing and margins between the controls drag too; Tauri
    // stops at the first clickable element, which is what keeps buttons clicking.
    expect(screen.getByTestId('app-bar')).toHaveAttribute('data-tauri-drag-region', 'deep');
    unmount();

    setUserAgent(MAC_USER_AGENT);
    render(<AppBar />);
    expect(screen.getByTestId('app-bar')).toHaveAttribute('data-tauri-drag-region', 'deep');
  });

  it('centers the omnibox between the two spacers', () => {
    render(<AppBar />);
    const bands = Array.from(screen.getByTestId('app-bar').children);
    const [left, right] = screen.getAllByTestId('app-bar-spacer');
    const picker = screen.getByTestId('context-omnibox').parentElement!;

    expect(bands.indexOf(left)).toBeLessThan(bands.indexOf(picker));
    expect(bands.indexOf(picker)).toBeLessThan(bands.indexOf(right));
    // Both spacers grow, so the omnibox lands in the middle of what's left, and
    // both keep a floor so a narrow window still leaves room either side of it.
    expect(left).toHaveClass('flex-1', 'min-w-4');
    expect(right).toHaveClass('flex-1', 'min-w-4');
  });

  it('carries the app menu and the caption buttons off macOS', () => {
    render(<AppBar />);
    // Frameless there, so the bar is the whole title bar.
    const bar = screen.getByTestId('app-bar');
    expect(bar.contains(screen.getByRole('button', { name: /application menu/i }))).toBe(true);
    expect(bar.contains(screen.getByRole('button', { name: /minimize/i }))).toBe(true);
    expect(bar.contains(screen.getByRole('button', { name: /close/i }))).toBe(true);
    // No native lights to leave room for.
    expect(screen.queryByTestId('traffic-light-gutter')).not.toBeInTheDocument();
  });

  // The bar is the one place that decides, so `AppMenu`'s `Ctrl/Cmd+N`/`Q`
  // accelerators ride on that decision — they register only where it mounts.
  it('carries the menu accelerators off macOS', () => {
    render(<AppBar />);
    fireEvent.keyDown(window, { key: 'n', ctrlKey: true });
    expect(invokeMock).toHaveBeenCalledWith('new_window');
  });

  it('leaves the accelerators to the native menu on macOS', () => {
    setUserAgent(MAC_USER_AGENT);
    render(<AppBar />);
    // Would fire twice beside the native menu item if the menu mounted here.
    fireEvent.keyDown(window, { key: 'n', metaKey: true });
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it('places the controls after the left chrome, ahead of the omnibox', () => {
    render(<AppBar />);
    const bands = Array.from(screen.getByTestId('app-bar').children);
    const controls = screen.getByTestId('app-bar-controls');
    const menu = screen.getByRole('button', { name: /application menu/i });
    expect(bands.indexOf(menu)).toBeLessThan(bands.indexOf(controls));
    expect(bands.indexOf(controls)).toBeLessThan(bands.indexOf(screen.getAllByTestId('app-bar-spacer')[0]));
  });

  it('puts the settings and account controls at the far end, past the omnibox', () => {
    render(<AppBar />);
    const bands = Array.from(screen.getByTestId('app-bar').children);
    const actions = screen.getByTestId('app-bar-actions');
    const rightSpacer = screen.getAllByTestId('app-bar-spacer')[1];
    expect(bands.indexOf(rightSpacer)).toBeLessThan(bands.indexOf(actions));
    // Ahead of the caption buttons, which have to reach the window's corner.
    const captions = screen.getByRole('button', { name: /close/i }).parentElement!;
    expect(bands.indexOf(actions)).toBeLessThan(bands.indexOf(captions));
  });

  it('spaces the right-hand controls 20px from the edge and 8px from each other', () => {
    render(<AppBar />);
    const actions = screen.getByTestId('app-bar-actions');
    expect(actions).toHaveClass('mr-5', 'gap-2');
    // The notification/settings pill first, then the account control nearest the
    // window's edge.
    const pill = screen.getByTestId('settings-button').parentElement!;
    expect(Array.from(actions.children)).toEqual([pill, screen.getByTestId('account-avatar')]);
    expect(Array.from(pill.children)).toContain(screen.getByTestId('notification-button'));
  });

  it('spaces the controls 16px from the chrome and 8px from each other', () => {
    render(<AppBar />);
    const controls = screen.getByTestId('app-bar-controls');
    expect(controls).toHaveClass('ml-4', 'gap-2');
    // Home first, history nav second.
    expect(Array.from(controls.children)).toEqual([
      screen.getByTestId('home-button'),
      screen.getByTestId('history-nav'),
    ]);
  });

  it('reserves the traffic lights and drops the caption chrome on macOS', () => {
    setUserAgent(MAC_USER_AGENT);
    render(<AppBar />);
    // The OS draws the lights and owns the menu bar, so the bar only makes room.
    const bar = screen.getByTestId('app-bar');
    expect(bar.contains(screen.getByTestId('traffic-light-gutter'))).toBe(true);
    expect(screen.queryByRole('button', { name: /application menu/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /minimize/i })).not.toBeInTheDocument();

    // The gutter stands in for the menu, so the home button still follows it.
    const bands = Array.from(bar.children);
    expect(bands.indexOf(screen.getByTestId('traffic-light-gutter'))).toBeLessThan(
      bands.indexOf(screen.getByTestId('app-bar-controls')),
    );
  });
});
