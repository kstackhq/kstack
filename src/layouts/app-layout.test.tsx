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
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { mockMatchMedia, mockTauriWindow, renderWithRouter } from '@/test-utils';

// Mocks ---------------------------------------------------------------

// `WindowControls` (in the app bar off macOS) drives the native window via
// `getCurrentWindow`.
const { factory } = mockTauriWindow();
vi.mock('@tauri-apps/api/window', () => factory());

// Replace each chrome widget with an identifiable stub so the test asserts
// *placement* (title bar vs sidebar vs inset) without dragging in their
// providers.
vi.mock('@/components/shell/app-bar/menu', () => ({
  AppMenu: () => <div data-testid="app-menu" />,
}));
// The title reads the chats and the kind catalog; its own suite covers it.
vi.mock('@/components/shell/app-bar/title', () => ({
  AppBarTitle: ({ mode }: { mode: string }) => <div data-testid="app-bar-title" data-mode={mode} />,
}));
// The omnibox in the app bar needs the router search + kubeconfig providers; this
// test is about placement, so stub it.
vi.mock('@/components/shell/context-omnibox/omnibox', () => ({
  ContextOmnibox: () => <div data-testid="context-omnibox" />,
}));
// The dialogs host mounts the real overlay panels (which need the GraphQL/clusters
// provider stack); stub it — this test is about chrome placement, not dialogs.
vi.mock('@/components/shell/app-dialogs', () => ({
  AppDialogs: () => <div data-testid="app-dialogs" />,
}));
vi.mock('@/components/shell/mode-rail/settings-button', () => ({
  SettingsButton: () => <div data-testid="settings-button" />,
}));
vi.mock('@/components/shell/connection-status', () => ({
  ConnectionStatus: () => <div data-testid="connection-status" />,
}));
// The log windows' provider talks to the host and reads the window's cluster; its own
// suite covers it. Here it serves an inert value, and says it was mounted.
const { logWindowsMounted } = vi.hoisted(() => ({ logWindowsMounted: { current: false } }));
vi.mock('@/lib/log-windows', async (importOriginal) => {
  const { LogWindowsContext, useLogWindows } = await importOriginal<typeof import('@/lib/log-windows')>();
  const value = { ready: true, windowOf: () => undefined, open: () => {}, focus: () => {}, shownWindowOf: () => null };
  return {
    useLogWindows,
    LogWindowsProvider: ({ children }: { children: import('react').ReactNode }) => {
      logWindowsMounted.current = true;
      return <LogWindowsContext.Provider value={value}>{children}</LogWindowsContext.Provider>;
    },
  };
});
// The real resource nav builds its tree from the active cluster's kinds (urql +
// clusters providers); this test only checks that it mounts on dashboard, so stub
// it with a nav that keeps the accessible "Resources" name the assertions match.
vi.mock('@/components/chat/chat-nav', () => ({
  ChatNav: () => <nav aria-label="Chats" />,
}));
// The layout mounts the chat outbox, whose send wants a client. Nothing here sends.
// The focused object is bound to the active cluster.
vi.mock('@/lib/active-cluster', () => ({ useActiveCluster: () => ({ clusterID: 'c1' }) }));
vi.mock('urql', () => ({ useMutation: () => [{}, vi.fn()] }));
vi.mock('@/gql', () => ({ graphql: () => ({}) }));
// The kind catalog's provider subscribes; record what it was told instead, and mark
// its place in the tree.
const { catalogEnabled } = vi.hoisted(() => ({ catalogEnabled: vi.fn() }));
vi.mock('@/lib/cluster-cached-data-kinds', () => ({
  ClusterCachedDataKindsProvider: ({ enabled, children }: { enabled: boolean; children: React.ReactNode }) => {
    catalogEnabled(enabled);
    return <div data-testid="kind-catalog">{children}</div>;
  },
}));
// Likewise the namespace list's provider.
const { namespacesEnabled } = vi.hoisted(() => ({ namespacesEnabled: vi.fn() }));
vi.mock('@/lib/cluster-namespaces', () => ({
  ClusterNamespacesProvider: ({ enabled, children }: { enabled: boolean; children: React.ReactNode }) => {
    namespacesEnabled(enabled);
    return <div data-testid="namespace-list">{children}</div>;
  },
}));
vi.mock('@/components/dashboard/dashboard-resource-nav', () => ({
  DashboardResourceNav: () => <nav aria-label="Resources" />,
  NamespacePicker: () => <div data-testid="namespace-picker" />,
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

// The card holding the sidebar, the page and the right sidebar.
function mainCardOf(container: HTMLElement) {
  const el = pageOf(container).parentElement;
  if (!el) throw new Error('main card not found');
  return el;
}

// The row below the chrome: the rail beside the main card's frame.
function contentRowOf(container: HTMLElement) {
  const el = mainCardOf(container).parentElement?.parentElement;
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
  it('puts the mode rail at the row’s left, outside the main card', async () => {
    const { container } = await renderWithRouter(buildTree(), '/chat');
    const rail = screen.getByTestId('mode-rail');
    const row = contentRowOf(container);
    expect(row.firstElementChild).toBe(rail);
    expect(mainCardOf(container).contains(rail)).toBe(false);
    expect(rail.contains(screen.getByRole('navigation', { name: 'View' }))).toBe(true);
    expect(rail.contains(screen.getByRole('button', { name: 'Notifications' }))).toBe(true);
    expect(rail.contains(screen.getByTestId('settings-button'))).toBe(true);
  });

  it('keeps the rail while the sidebar is collapsed', async () => {
    await renderWithRouter(buildTree(), '/chat');
    await act(async () => {
      screen.getByRole('button', { name: 'Toggle sidebar' }).click();
    });
    expect(screen.getByRole('navigation', { name: 'View' })).toBeInTheDocument();
  });

  it('holds the sidebar, the page and the right sidebar in one rounded card', async () => {
    const { container } = await renderWithRouter(buildTree(), '/');
    const card = mainCardOf(container);
    expect(card).toHaveClass('rounded-xl', 'border', 'shadow-md', 'overflow-clip', 'min-h-0');
    expect(card.firstElementChild).toBe(sidebarOf(container));
  });

  it('passes the mode to the app bar’s title, and draws the scope chip on the dashboard alone', async () => {
    const { router } = await renderWithRouter(buildTree(), '/chat');
    expect(screen.getByTestId('app-bar-title')).toHaveAttribute('data-mode', 'chat');
    expect(screen.queryByTestId('namespace-picker')).not.toBeInTheDocument();

    await act(async () => {
      await router.navigate({ to: '/dashboard' });
    });
    expect(screen.getByTestId('app-bar-title')).toHaveAttribute('data-mode', 'dashboard');
    expect(screen.getByTestId('app-bar').contains(screen.getByTestId('namespace-picker'))).toBe(true);
  });

  it('widens the bar’s gutter with the sidebar as it is dragged, and hugs once collapsed', async () => {
    HTMLElement.prototype.setPointerCapture ??= () => {};
    await renderWithRouter(buildTree(), '/chat');
    const gutter = () => screen.getByTestId('app-bar-gutter');
    expect(gutter().style.width).toBe('303px');

    fireEvent.pointerDown(screen.getByRole('separator', { name: 'Resize sidebar' }), { pointerId: 1 });
    fireEvent.pointerMove(window, { clientX: 300 });
    fireEvent.pointerUp(window);
    expect(gutter().style.width).toBe('347px');

    await act(async () => {
      screen.getByRole('button', { name: 'Toggle sidebar' }).click();
    });
    expect(gutter().style.width).toBe('');
  });

  it('opens the narrow window’s drawer from the bar’s toggle, over the main card alone', async () => {
    const setNarrow = mockMatchMedia();
    Object.defineProperty(window, 'innerWidth', { value: 600, configurable: true });
    setNarrow(true);
    const { container } = await renderWithRouter(buildTree(), '/chat');
    expect(container.querySelector('[data-testid="left-sidebar-card"]')).toBeNull();

    await act(async () => {
      screen.getByRole('button', { name: 'Toggle sidebar' }).click();
    });
    const card = mainCardOf(container);
    expect(card).toHaveClass('relative');
    expect(card.contains(screen.getByTestId('sidebar-overlay'))).toBe(true);
    expect(card.contains(screen.getByTestId('sidebar-scrim'))).toBe(true);
    Object.defineProperty(window, 'innerWidth', { value: 1024, configurable: true });
    setNarrow(false);
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
  it('stacks the app bar above the row holding the rail and the main card', async () => {
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

  it('holds the kind catalog above the resource nav and the page, which both read it', async () => {
    await renderWithRouter(buildTree(), '/dashboard');
    const catalog = screen.getByTestId('kind-catalog');
    expect(catalog.contains(screen.getByRole('navigation', { name: 'Resources' }))).toBe(true);
    expect(catalog.contains(screen.getByTestId('page-content'))).toBe(true);
  });

  it('runs the kind catalog on the dashboard alone', async () => {
    await renderWithRouter(buildTree(), '/dashboard');
    expect(catalogEnabled).toHaveBeenLastCalledWith(true);
  });

  it('pauses the kind catalog in chat mode', async () => {
    await renderWithRouter(buildTree(), '/chat');
    expect(catalogEnabled).toHaveBeenLastCalledWith(false);
  });

  it('holds the namespace list above the page, and runs it on the dashboard alone', async () => {
    await renderWithRouter(buildTree(), '/dashboard');
    expect(screen.getByTestId('namespace-list').contains(screen.getByTestId('page-content'))).toBe(true);
    expect(namespacesEnabled).toHaveBeenLastCalledWith(true);
  });

  it('pauses the namespace list in chat mode', async () => {
    await renderWithRouter(buildTree(), '/chat');
    expect(namespacesEnabled).toHaveBeenLastCalledWith(false);
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

  // The bar's toggle stays put, so a collapse is reversible without reaching for
  // `Cmd/Ctrl+B`.
  it('collapses the sidebar from the bar’s toggle, and reopens it from there', async () => {
    const { container } = await renderWithRouter(buildTree(), '/chat');
    const toggle = screen.getByRole('button', { name: 'Toggle sidebar' });
    expect(screen.getByTestId('app-bar').contains(toggle)).toBe(true);

    await act(async () => {
      toggle.click();
    });
    expect(container.querySelector('[data-testid="left-sidebar-card"]')).toBeNull();
    // The page is the card's first item then.
    expect(mainCardOf(container).firstElementChild).toBe(pageOf(container));

    await act(async () => {
      screen.getByRole('button', { name: 'Toggle sidebar' }).click();
    });
    expect(sidebarOf(container)).toBeInTheDocument();
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
    expect(container.querySelector('[data-testid="left-sidebar-card"]')).toBeNull();
  });

  // A virtualized table scrolls inside a definite height; `min-h-*` alone would grow
  // with the rows and scroll the page instead. The column is the definite one, and
  // every flex item down to the page shrinks into it.
  it('bounds the page frame so a route can own its scroll', async () => {
    const { container } = await renderWithRouter(buildTree(), '/');
    const column = contentRowOf(container).parentElement!;
    expect(column).toHaveClass('h-(--app-min-h)', 'min-h-0', 'flex-col', 'bg-border');
    expect(contentRowOf(container)).toHaveClass('min-h-0', 'flex-1', 'overflow-clip');
    // The card's frame gives it the 8px of chrome right of and below it.
    expect(mainCardOf(container).parentElement).toHaveClass('min-h-0', 'flex-1', 'px-2', 'pb-2');
    expect(mainCardOf(container)).toHaveClass('min-h-0', 'flex-1');
    expect(pageOf(container)).toHaveClass('min-h-0', 'flex-1', 'overflow-hidden');
  });

  it('puts the right sidebar after the page, as the main card’s last column', async () => {
    const { container } = await renderWithRouter(buildTree(), '/');
    expect(screen.queryByTestId('right-sidebar')).not.toBeInTheDocument();

    await act(async () => {
      screen.getByRole('button', { name: 'Toggle right sidebar' }).click();
    });
    const panel = screen.getByTestId('right-sidebar');
    expect(mainCardOf(container).lastElementChild).toBe(panel);
    expect(pageOf(container).contains(panel)).toBe(false);
  });

  it('holds the right sidebar’s toggle in the app bar', async () => {
    const { container } = await renderWithRouter(buildTree(), '/');
    const toggle = screen.getByRole('button', { name: 'Toggle right sidebar' });
    expect(screen.getByTestId('app-bar').contains(toggle)).toBe(true);
    expect(contentRowOf(container).contains(toggle)).toBe(false);
  });

  it('renders the connection-status banner', async () => {
    await renderWithRouter(buildTree(), '/');
    expect(screen.getByTestId('connection-status')).toBeInTheDocument();
  });

  it('mounts the log windows, which the panel and the cards reach', async () => {
    logWindowsMounted.current = false;
    await renderWithRouter(buildTree(), '/');
    expect(logWindowsMounted.current).toBe(true);
  });
});
