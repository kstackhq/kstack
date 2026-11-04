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

import { act, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { DEFAULT_DASHBOARD_RESOURCE } from '@/lib/dashboard-resources';
import type { ServerKind } from '@/lib/dashboard-resources';
import { renderWithRouter } from '@/test-utils';

// Mocks ---------------------------------------------------------------

// Stand in for the layout route, so the real dashboard route mounts without the
// provider stack `__root` builds. The test root comes back with it, to hang the
// tree from.
vi.mock('@/routes/_app', async () => {
  const { createRootRoute, createRoute, Outlet } = await import('@tanstack/react-router');
  const testRoot = createRootRoute();
  return {
    testRoot,
    // Stands in for the real layout route, which owns the `resource` param.
    Route: createRoute({
      getParentRoute: () => testRoot,
      id: 'app',
      validateSearch: (search: Record<string, unknown>): { resource?: string } =>
        typeof search.resource === 'string' && search.resource.length > 0 ? { resource: search.resource } : {},
      component: () => <Outlet />,
    }),
  };
});

// Both panels are covered by their own suites; this one is about which is picked.
vi.mock('@/components/widgets/events-table', () => ({ EventsTable: () => <div data-testid="events-table" /> }));
vi.mock('@/components/widgets/objects-table', () => ({
  ObjectsTable: (props: Record<string, unknown>) => <div data-testid="objects-table">{JSON.stringify(props)}</div>,
}));

// The catalog draws the page again on `catalog.emit()`, as its watch does on each frame.
const { useClusterCachedDataKindsMock, catalog } = vi.hoisted(() => {
  const listeners = new Set<() => void>();
  const state = { version: 0 };
  return {
    useClusterCachedDataKindsMock: vi.fn(),
    catalog: {
      state,
      subscribe: (listener: () => void) => {
        listeners.add(listener);
        return () => listeners.delete(listener);
      },
      emit: () => {
        state.version += 1;
        listeners.forEach((l) => l());
      },
    },
  };
});
vi.mock('@/lib/cluster-cached-data-kinds', async () => {
  const { useSyncExternalStore } = await import('react');
  return {
    useClusterCachedDataKinds: () => {
      useSyncExternalStore(catalog.subscribe, () => catalog.state.version);
      return useClusterCachedDataKindsMock();
    },
  };
});

const { testRoot, Route: appRoute } = (await import('@/routes/_app')) as unknown as {
  testRoot: import('@tanstack/react-router').AnyRoute;
  Route: import('@tanstack/react-router').AnyRoute;
};
const { Route: dashboardRoute } = await import('./dashboard');

// Helpers -------------------------------------------------------------

const KIND: ServerKind = {
  apiVersion: 'apps/v1',
  kind: 'Deployment',
  resource: 'deployments',
  scope: 'Namespaced',
  isCRD: false,
  count: 3,
  printerColumns: [],
};

function render(path: string, kinds: ServerKind[] = []) {
  useClusterCachedDataKindsMock.mockReturnValue({ kinds, active: true, cacheID: 'cache-1', phase: 'live' });
  return renderWithRouter(testRoot.addChildren([appRoute.addChildren([dashboardRoute])]), path);
}

// The catalog's next state, drawn as its watch would draw it.
function emit(value: { kinds: ServerKind[]; active: boolean; cacheID?: string; phase: string }) {
  useClusterCachedDataKindsMock.mockReturnValue(value);
  act(() => catalog.emit());
}

// Tests ---------------------------------------------------------------

describe('dashboard route', () => {
  it('shows the events timeline for the events view', async () => {
    await render('/dashboard?resource=events');
    expect(screen.getByTestId('events-table')).toBeInTheDocument();
    expect(screen.queryByTestId('objects-table')).not.toBeInTheDocument();
  });

  it('shows the generic table for a discovered kind, told what it is listing', async () => {
    await render('/dashboard?resource=deployments', [KIND]);
    expect(screen.getByTestId('objects-table')).toHaveTextContent('"apiVersion":"apps/v1"');
    expect(screen.getByTestId('objects-table')).toHaveTextContent('"resource":"deployments"');
    // `scope` is the table's namespaced flag.
    expect(screen.getByTestId('objects-table')).toHaveTextContent('"namespaced":true');
  });

  it('waits for the catalog before calling a deep-linked kind unknown', async () => {
    useClusterCachedDataKindsMock.mockReturnValue({ kinds: [], active: true, cacheID: 'cache-1', phase: 'connecting' });
    await renderWithRouter(testRoot.addChildren([appRoute.addChildren([dashboardRoute])]), '/dashboard?resource=pods');
    expect(screen.getByText(/loading resources/i)).toBeInTheDocument();
    expect(screen.queryByText(/coming soon/i)).not.toBeInTheDocument();
  });

  it('shows a group row its placeholder at once, with no catalog to wait for', async () => {
    useClusterCachedDataKindsMock.mockReturnValue({ kinds: [], active: true, cacheID: 'cache-1', phase: 'connecting' });
    await renderWithRouter(
      testRoot.addChildren([appRoute.addChildren([dashboardRoute])]),
      '/dashboard?resource=workloads',
    );
    expect(screen.getByText(/coming soon/i)).toBeInTheDocument();
    expect(screen.queryByText(/loading resources/i)).not.toBeInTheDocument();
  });

  it('falls back to a placeholder for a group row', async () => {
    await render('/dashboard?resource=workloads');
    expect(screen.queryByTestId('objects-table')).not.toBeInTheDocument();
    expect(screen.getByText(/coming soon/i)).toBeInTheDocument();
  });

  it('says a kind the synced catalog lacks is not served by this cluster', async () => {
    await render('/dashboard?resource=ingresses', [KIND]);
    expect(screen.getByText(/this cluster does not serve ingresses/i)).toBeInTheDocument();
    expect(screen.queryByText(/coming soon/i)).not.toBeInTheDocument();
  });

  it('says a kind is not synced while the cluster has no active cache', async () => {
    useClusterCachedDataKindsMock.mockReturnValue({
      kinds: [],
      active: false,
      cacheID: undefined,
      phase: 'connecting',
    });
    await renderWithRouter(testRoot.addChildren([appRoute.addChildren([dashboardRoute])]), '/dashboard?resource=pods');
    expect(screen.getByText(/no synced cache for this cluster yet/i)).toBeInTheDocument();
    expect(screen.queryByText(/coming soon/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/loading resources/i)).not.toBeInTheDocument();
  });

  it('keeps a group row’s placeholder while the cluster has no active cache', async () => {
    useClusterCachedDataKindsMock.mockReturnValue({
      kinds: [],
      active: false,
      cacheID: undefined,
      phase: 'connecting',
    });
    await renderWithRouter(
      testRoot.addChildren([appRoute.addChildren([dashboardRoute])]),
      '/dashboard?resource=workloads',
    );
    expect(screen.getByText(/coming soon/i)).toBeInTheDocument();
  });

  it('keeps the table it shows as the catalog’s counts churn', async () => {
    await render('/dashboard?resource=deployments', [KIND]);
    emit({ kinds: [{ ...KIND, count: 4 }], active: true, cacheID: 'cache-1', phase: 'live' });
    expect(screen.getByTestId('objects-table')).toHaveTextContent('"resource":"deployments"');
  });

  it('keeps the table mounted while a reconnect replays the catalog', async () => {
    await render('/dashboard?resource=deployments', [KIND]);
    const table = screen.getByTestId('objects-table');

    // The new connection's snapshot starts on a clean slate.
    emit({ kinds: [], active: true, cacheID: 'cache-1', phase: 'connecting' });
    expect(screen.getByTestId('objects-table')).toBe(table);
    expect(screen.queryByText(/loading resources/i)).not.toBeInTheDocument();

    emit({ kinds: [KIND], active: true, cacheID: 'cache-1', phase: 'live' });
    expect(screen.getByTestId('objects-table')).toBe(table);
  });

  it('lets the held kind go when the replayed catalog no longer serves it', async () => {
    await render('/dashboard?resource=deployments', [KIND]);
    emit({ kinds: [], active: true, cacheID: 'cache-1', phase: 'connecting' });
    emit({ kinds: [], active: true, cacheID: 'cache-1', phase: 'live' });
    expect(screen.queryByTestId('objects-table')).not.toBeInTheDocument();
    expect(screen.getByText(/this cluster does not serve deployments/i)).toBeInTheDocument();
  });

  it('holds no kind across a swap to another cache', async () => {
    await render('/dashboard?resource=deployments', [KIND]);
    emit({ kinds: [], active: true, cacheID: 'cache-2', phase: 'connecting' });
    expect(screen.queryByTestId('objects-table')).not.toBeInTheDocument();
    expect(screen.getByText(/loading resources/i)).toBeInTheDocument();
  });

  it('names the view it is showing', async () => {
    await render('/dashboard?resource=deployments', [KIND]);
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Deployments');
  });

  it('lands on the default view when the link carries no resource', async () => {
    const explicit = await render(`/dashboard?resource=${DEFAULT_DASHBOARD_RESOURCE}`);
    const expected = screen.getByRole('heading', { level: 1 }).textContent ?? '';
    explicit.unmount();

    // A bare `/dashboard` resolves to the default without the link rewriting itself.
    await render('/dashboard');
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(expected);
  });

  it('reads an empty resource param as absent, not as a view named nothing', async () => {
    const explicit = await render(`/dashboard?resource=${DEFAULT_DASHBOARD_RESOURCE}`);
    const expected = screen.getByRole('heading', { level: 1 }).textContent ?? '';
    explicit.unmount();

    await render('/dashboard?resource=');
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(expected);
  });
});
