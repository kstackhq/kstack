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

import { createRootRoute, createRoute } from '@tanstack/react-router';
import { act, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { mockTauriWindow, renderWithRouter } from '@/test-utils';

// Mocks ---------------------------------------------------------------

// `WindowControls` (in the app bar off macOS) drives the native window via
// `getCurrentWindow`.
const { factory } = mockTauriWindow();
vi.mock('@tauri-apps/api/window', () => factory());

// Replace each chrome widget with an identifiable stub so the test asserts
// *placement* (title bar vs sidebar vs inset) without dragging in their
// providers.
vi.mock('@/components/widgets/app-menu', () => ({
  AppMenu: () => <div data-testid="app-menu" />,
}));
vi.mock('@/components/widgets/mode-nav', () => ({
  ModeNav: () => <div data-testid="mode-nav" />,
}));
// The omnibox in the app bar needs the router search + kubeconfig providers; this
// test is about placement, so stub it.
vi.mock('@/components/widgets/context-omnibox', () => ({
  ContextOmnibox: () => <div data-testid="context-omnibox" />,
}));
// The dialogs host mounts the real overlay panels (which need the GraphQL/clusters
// provider stack); stub it — this test is about chrome placement, not dialogs.
vi.mock('@/components/widgets/app-dialogs', () => ({
  AppDialogs: () => <div data-testid="app-dialogs" />,
}));
// The account control reads the auth provider; this test is about placement.
vi.mock('@/components/widgets/signin-button', () => ({
  SignInButton: () => <div data-testid="signin-button" />,
}));
vi.mock('@/components/widgets/settings-button', () => ({
  SettingsButton: () => <div data-testid="settings-button" />,
}));
vi.mock('@/lib/connection-status', () => ({
  ConnectionStatus: () => <div data-testid="connection-status" />,
}));
// The launcher asks the sidecar and opens a dialog; this test only checks it
// mounts under the dialog host.
vi.mock('@/lib/onboarding', async () => {
  const { useDialog } = await import('@/lib/dialog');
  return {
    OnboardingLaunch: () => {
      useDialog();
      return <div data-testid="onboarding-launch" />;
    },
  };
});
// The real resource nav builds its tree from the active cluster's kinds (urql +
// clusters providers); this test only checks that it mounts on dashboard, so stub
// it with a nav that keeps the accessible "Resources" name the assertions match.
vi.mock('@/components/widgets/chat-nav', () => ({
  ChatNav: () => <nav aria-label="Chats" />,
}));
// The layout mounts the chat outbox, whose send wants a client. Nothing here sends.
vi.mock('urql', () => ({ useMutation: () => [{}, vi.fn()] }));
vi.mock('@/gql', () => ({ graphql: () => ({}) }));
vi.mock('@/components/widgets/dashboard-resource-nav', () => ({
  DashboardResourceNav: () => <nav aria-label="Resources" />,
}));

const { AppLayout } = await import('./app-layout');

// Helpers -------------------------------------------------------------

// AppLayout renders an <Outlet/>, so it can only be exercised inside a router.
// Mount it as the root layout with one child page that drops a marker in the
// inset.
function buildTree() {
  const root = createRootRoute({ component: AppLayout });
  const index = createRoute({
    getParentRoute: () => root,
    path: '/',
    component: () => <div data-testid="page-content" />,
  });
  // Chat and dashboard peers, so the layout's mode detection has real routes to
  // match against (the resource nav mounts only on dashboard).
  const chat = createRoute({
    getParentRoute: () => root,
    path: '/chat',
    component: () => <div data-testid="page-content" />,
  });
  const dashboard = createRoute({
    getParentRoute: () => root,
    path: '/dashboard',
    component: () => <div data-testid="page-content" />,
  });
  return root.addChildren([index, chat, dashboard]);
}

function sidebarOf(container: HTMLElement) {
  const el = container.querySelector('[data-testid="left-sidebar-card"]');
  if (!el) throw new Error('sidebar not found');
  return el;
}

function pageOf(container: HTMLElement) {
  const el = container.querySelector('main');
  if (!el) throw new Error('page frame not found');
  return el;
}

// The row the sidebar and the page share, below the chrome.
function contentRowOf(container: HTMLElement) {
  const el = pageOf(container).parentElement;
  if (!el) throw new Error('content row not found');
  return el;
}

// The window's bands, top to bottom: chrome first, then the content row.
function bandsOf(container: HTMLElement) {
  const column = contentRowOf(container).parentElement;
  if (!column) throw new Error('layout column not found');
  return Array.from(column.children);
}

// Tests ---------------------------------------------------------------

// Both sidebars remember open/collapsed app-wide, so each case starts from a
// pristine store rather than from what the last one left.
beforeEach(() => {
  localStorage.clear();
});

describe('AppLayout', () => {
  it('pins the mode nav at the sidebar’s top, out of the scroll area', async () => {
    const { container } = await renderWithRouter(buildTree(), '/');
    const sidebar = sidebarOf(container);
    const nav = screen.getByTestId('mode-nav');
    expect(sidebar.contains(nav)).toBe(true);
    // It is how you leave whatever the nav below is showing, so it must not
    // scroll away with it.
    expect(sidebar.querySelector('[data-slot="sidebar-content"]')?.contains(nav)).toBe(false);
  });

  it('places the account chrome in the app bar, not the sidebar', async () => {
    const { container } = await renderWithRouter(buildTree(), '/');
    const account = screen.getByTestId('signin-button');
    expect(screen.getByTestId('app-bar').contains(account)).toBe(true);
    expect(sidebarOf(container).contains(account)).toBe(false);
  });

  it('places the app menu in the app bar, not the sidebar', async () => {
    const { container } = await renderWithRouter(buildTree(), '/');
    const menu = screen.getByTestId('app-menu');
    expect(screen.getByTestId('app-bar').contains(menu)).toBe(true);
    expect(sidebarOf(container).contains(menu)).toBe(false);
  });

  // The window is a column of chrome over a row: app bar across the top, then the
  // sidebar beside the page. Nothing is fixed-positioned, so this nesting is what
  // keeps the page out from under the chrome.
  it('stacks the app bar above the row holding the sidebar and the page', async () => {
    const { container } = await renderWithRouter(buildTree(), '/');
    const bands = bandsOf(container);
    const row = contentRowOf(container);

    expect(row.contains(sidebarOf(container))).toBe(true);
    expect(row.contains(screen.getByTestId('app-bar'))).toBe(false);
    expect(bands.indexOf(screen.getByTestId('app-bar'))).toBeLessThan(bands.indexOf(row));
  });

  it('renders the routed page in the page area, not the sidebar', async () => {
    const { container } = await renderWithRouter(buildTree(), '/');
    const page = screen.getByTestId('page-content');
    expect(pageOf(container).contains(page)).toBe(true);
    expect(sidebarOf(container).contains(page)).toBe(false);
  });

  it('mounts the resource nav in the sidebar while in dashboard mode', async () => {
    const { container } = await renderWithRouter(buildTree(), '/dashboard');
    const nav = screen.getByRole('navigation', { name: 'Resources' });
    expect(sidebarOf(container).contains(nav)).toBe(true);
  });

  it('omits the resource nav outside dashboard mode', async () => {
    await renderWithRouter(buildTree(), '/chat');
    expect(screen.queryByRole('navigation', { name: 'Resources' })).not.toBeInTheDocument();
  });

  it('mounts the chat list in the sidebar while in chat mode', async () => {
    const { container } = await renderWithRouter(buildTree(), '/chat');
    expect(sidebarOf(container).contains(screen.getByRole('navigation', { name: 'Chats' }))).toBe(true);
  });

  it('omits the chat list in dashboard mode', async () => {
    await renderWithRouter(buildTree(), '/dashboard');
    expect(screen.queryByRole('navigation', { name: 'Chats' })).not.toBeInTheDocument();
  });

  it('mounts the resource nav when navigating into dashboard on the persistent layout', async () => {
    // The layout stays mounted across chat<->dashboard (only the Outlet swaps),
    // so this exercises a *client navigation* — not a fresh load — which is where
    // a non-reactive mode check would leave the nav stale until a reload.
    const { router } = await renderWithRouter(buildTree(), '/chat');
    expect(screen.queryByRole('navigation', { name: 'Resources' })).not.toBeInTheDocument();

    await act(async () => {
      await router.navigate({ to: '/dashboard' });
    });

    await waitFor(() => expect(screen.getByRole('navigation', { name: 'Resources' })).toBeInTheDocument());

    // ...and unmounts again on the way back to chat.
    await act(async () => {
      await router.navigate({ to: '/chat' });
    });
    await waitFor(() => expect(screen.queryByRole('navigation', { name: 'Resources' })).not.toBeInTheDocument());
  });

  // The pill is the collapsed sidebar's whole affordance, so a collapse is
  // reversible without reaching for `Cmd/Ctrl+B`.
  it('collapses the sidebar to the pill, and reopens it from there', async () => {
    const { container } = await renderWithRouter(buildTree(), '/chat');

    await act(async () => {
      screen.getByRole('button', { name: /toggle sidebar/i }).click();
    });
    const pill = screen.getByTestId('left-sidebar-pill');
    expect(contentRowOf(container).contains(pill)).toBe(true);
    expect(container.querySelector('[data-testid="left-sidebar-card"]')).toBeNull();

    await act(async () => {
      screen.getByRole('button', { name: /toggle sidebar/i }).click();
    });
    expect(sidebarOf(container)).toBeInTheDocument();
    expect(screen.queryByTestId('left-sidebar-pill')).not.toBeInTheDocument();
  });

  // Windows share the collapsed state, so a user who works with the sidebar out of
  // the way does not have to collapse it again in every window they open.
  it('remembers the sidebar collapsed for the next window', async () => {
    const first = await renderWithRouter(buildTree(), '/chat');
    await act(async () => {
      screen.getByRole('button', { name: /toggle sidebar/i }).click();
    });
    first.unmount();

    const { container } = await renderWithRouter(buildTree(), '/chat');
    expect(screen.getByTestId('left-sidebar-pill')).toBeInTheDocument();
    expect(container.querySelector('[data-testid="left-sidebar-card"]')).toBeNull();
  });

  // A virtualized table scrolls inside a definite height; `min-h-*` alone would grow
  // with the rows and scroll the page instead. The column is the definite one, and
  // every flex item down to the page shrinks into it.
  it('bounds the page frame so a route can own its scroll', async () => {
    const { container } = await renderWithRouter(buildTree(), '/');
    const column = contentRowOf(container).parentElement!;
    expect(column).toHaveClass('h-(--app-min-h)', 'min-h-0', 'flex-col');
    expect(contentRowOf(container)).toHaveClass('min-h-0', 'flex-1');
    expect(pageOf(container)).toHaveClass('min-h-0', 'flex-1', 'overflow-hidden');
  });

  it('puts the right sidebar after the page, flush to the window', async () => {
    const { container } = await renderWithRouter(buildTree(), '/');
    // Closed until asked for, and then between the page and the toggle floating
    // over the row's corner — the page keeps the middle, the panel the right wall.
    expect(screen.queryByTestId('right-sidebar')).not.toBeInTheDocument();

    await act(async () => {
      screen.getByRole('button', { name: /toggle right sidebar/i }).click();
    });
    const row = contentRowOf(container);
    const panel = screen.getByTestId('right-sidebar');
    expect(Array.from(row.children).indexOf(panel)).toBe(row.children.length - 2);
    expect(pageOf(container).contains(panel)).toBe(false);
  });

  it('holds the right sidebar’s toggle in the row’s corner, open or closed', async () => {
    const { container } = await renderWithRouter(buildTree(), '/');
    const toggle = screen.getByRole('button', { name: /toggle right sidebar/i });
    const corner = toggle.parentElement!;

    // Pinned to the row, not to the page: the page narrows when the panel opens,
    // and the toggle has to stay where the pointer left it.
    expect(contentRowOf(container).contains(corner)).toBe(true);
    expect(pageOf(container).contains(corner)).toBe(false);
    // 16px in, level with the card's own toggle (`p-2` + the header's `p-2`).
    expect(corner).toHaveClass('absolute', 'top-4', 'right-4');

    await act(async () => {
      toggle.click();
    });
    expect(screen.getByRole('button', { name: /toggle right sidebar/i }).parentElement).toBe(corner);
  });

  it('renders the connection-status banner', async () => {
    await renderWithRouter(buildTree(), '/');
    expect(screen.getByTestId('connection-status')).toBeInTheDocument();
  });

  it('mounts the onboarding launcher under the dialog host', async () => {
    await renderWithRouter(buildTree(), '/');
    expect(screen.getByTestId('onboarding-launch')).toBeInTheDocument();
  });
});
