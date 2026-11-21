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

import { createRootRoute, createRoute, Outlet } from '@tanstack/react-router';
import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { buildDashboardNav } from '@/lib/dashboard-resources';
import type { ServerKind } from '@/lib/dashboard-resources';
import { renderWithRouter } from '@/test-utils';

// Mocks ---------------------------------------------------------------

const { useChatsMock } = vi.hoisted(() => ({ useChatsMock: vi.fn() }));
vi.mock('@/lib/chats', () => ({ useChats: useChatsMock }));

// The tree is the curated one until a test hands the nav a catalog.
const { kinds } = vi.hoisted(() => ({ kinds: { current: [] as ServerKind[] } }));
vi.mock('@/lib/cluster-cached-data-kinds', () => ({
  useClusterCachedDataKinds: () => ({ kinds: kinds.current, active: true, phase: 'live' }),
}));
vi.mock('@/lib/dashboard-nav', () => ({
  useDashboardNav: () => ({
    nav: buildDashboardNav(kinds.current.length > 0 ? kinds.current : undefined),
    active: true,
    phase: 'live',
  }),
}));

const { AppBarTitle } = await import('./title');

// Helpers -------------------------------------------------------------

const KIND: ServerKind = {
  apiVersion: 'example.com/v1',
  kind: 'Widget',
  resource: 'widgets',
  scope: 'Namespaced',
  isCRD: true,
  count: 1,
  printerColumns: [],
};

function render(path: string) {
  const mode = path.startsWith('/dashboard') ? 'dashboard' : 'chat';
  const root = createRootRoute({
    component: () => (
      <>
        <div data-testid="bar">
          <AppBarTitle mode={mode} />
        </div>
        <Outlet />
      </>
    ),
  });
  const chat = createRoute({ getParentRoute: () => root, path: '/chat', component: () => null });
  const chatID = createRoute({ getParentRoute: () => root, path: '/chat/$chatId', component: () => null });
  const dashboard = createRoute({ getParentRoute: () => root, path: '/dashboard', component: () => null });
  return renderWithRouter(root.addChildren([chat, chatID, dashboard]), path);
}

const title = () => screen.getByTestId('app-bar-title');

beforeEach(() => {
  kinds.current = [];
  useChatsMock.mockReturnValue({ chats: [{ id: 'c1', title: 'Why is checkout crashing?' }], phase: 'live' });
});

// Tests ---------------------------------------------------------------

describe('AppBarTitle', () => {
  it('names the open chat', async () => {
    await render('/chat/c1');
    expect(title()).toHaveTextContent('Why is checkout crashing?');
  });

  it('spells out invisible and reordering characters in a chat’s title', async () => {
    // The title is model-written text.
    useChatsMock.mockReturnValue({ chats: [{ id: 'c1', title: 'ok‮done' }], phase: 'live' });
    await render('/chat/c1');
    expect(title()).toHaveTextContent('ok\\u{202E}done');
    expect(title().querySelector('mark')).not.toBeNull();
  });

  it('draws nothing, divider included, with no chat open', async () => {
    await render('/chat');
    expect(screen.getByTestId('bar')).toBeEmptyDOMElement();
  });

  it('draws nothing until the open chat is listed', async () => {
    useChatsMock.mockReturnValue({ chats: [], phase: 'connecting' });
    await render('/chat/c1');
    expect(screen.getByTestId('bar')).toBeEmptyDOMElement();
  });

  it('draws a namespaced kind’s section, group and label', async () => {
    await render('/dashboard?resource=pods');
    const crumbs = screen.getByRole('navigation', { name: 'Breadcrumb' });
    expect(crumbs).toHaveTextContent(/^Workloads\s*Pods$/);
    expect(screen.getByTestId('breadcrumb-scope')).toHaveAttribute('data-scope', 'namespace');
    expect(screen.getByText('Pods')).toHaveAttribute('aria-current', 'page');
  });

  it('draws a cluster-scoped kind under the Cluster section', async () => {
    await render('/dashboard?resource=nodes');
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(/^Infrastructure\s*Nodes$/);
    expect(screen.getByTestId('breadcrumb-scope')).toHaveAttribute('data-scope', 'cluster');
  });

  it('places a discovered kind as the nav places it', async () => {
    kinds.current = [KIND];
    await render('/dashboard?resource=example.com/widgets');
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(/^Custom resources\s*Widget$/);
  });

  it.each([
    ['overview', 'Overview'],
    ['events', 'Events'],
  ])('draws the pinned %s row’s label alone', async (resource, label) => {
    await render(`/dashboard?resource=${resource}`);
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(new RegExp(`^${label}$`));
    expect(screen.queryByTestId('breadcrumb-scope')).not.toBeInTheDocument();
  });

  it('lands on Overview with no resource', async () => {
    await render('/dashboard');
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(/^Overview$/);
  });

  it('names a kind the tree does not hold by its id alone', async () => {
    await render('/dashboard?resource=example.com/gadgets');
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(/^example.com\/gadgets$/);
  });

  it('spells out invisible characters in a kind’s label', async () => {
    kinds.current = [{ ...KIND, kind: 'Wid​get', resource: 'wid​gets' }];
    await render('/dashboard?resource=example.com/wid%E2%80%8Bgets');
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' }).querySelector('mark')).not.toBeNull();
  });
});
