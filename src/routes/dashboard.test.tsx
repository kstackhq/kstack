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
import { beforeEach, describe, expect, it, vi } from 'vitest';

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
    // Stands in for the real layout route, which owns the `resource` and `namespace` params.
    Route: createRoute({
      getParentRoute: () => testRoot,
      id: 'app',
      validateSearch: (search: Record<string, unknown>) => ({
        ...(typeof search.resource === 'string' && search.resource.length > 0 ? { resource: search.resource } : {}),
        ...(typeof search.namespace === 'string' ? { namespace: search.namespace } : {}),
      }),
      component: () => <Outlet />,
    }),
  };
});

// Both panels are covered by their own suites; this one is about which is picked and
// what each is handed.
vi.mock('@/components/widgets/events-table', () => ({
  EventsTable: (props: Record<string, unknown>) => <div data-testid="events-table">{JSON.stringify(props)}</div>,
}));
vi.mock('@/components/widgets/objects-table', () => ({
  ObjectsTable: (props: Record<string, unknown>) => <div data-testid="objects-table">{JSON.stringify(props)}</div>,
}));

const { useClusterCachedDataObjectsMock, useClusterCachedDataEventsMock } = vi.hoisted(() => ({
  useClusterCachedDataObjectsMock: vi.fn(),
  useClusterCachedDataEventsMock: vi.fn(),
}));
vi.mock('@/lib/cluster-cached-data-objects', () => ({ useClusterCachedDataObjects: useClusterCachedDataObjectsMock }));
vi.mock('@/lib/cluster-cached-data-events', () => ({ useClusterCachedDataEvents: useClusterCachedDataEventsMock }));

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

const pod = (uid: string, namespace: string) => ({ uid, namespace, name: uid });
const event = (uid: string, involvedNamespace: string) => ({ uid, involvedNamespace });

beforeEach(() => {
  useClusterCachedDataObjectsMock.mockReturnValue({ objects: [], active: true, phase: 'live' });
  useClusterCachedDataEventsMock.mockReturnValue({ events: [], active: true, phase: 'live' });
});

// What the mocked component was last handed.
const propsOf = (testId: string) => JSON.parse(screen.getByTestId(testId).textContent ?? '{}');

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

  it('shows Overview its placeholder at once, with no catalog to wait for', async () => {
    useClusterCachedDataKindsMock.mockReturnValue({ kinds: [], active: true, cacheID: 'cache-1', phase: 'connecting' });
    await renderWithRouter(
      testRoot.addChildren([appRoute.addChildren([dashboardRoute])]),
      '/dashboard?resource=overview',
    );
    expect(screen.getByText(/coming soon/i)).toBeInTheDocument();
    expect(screen.queryByText(/loading resources/i)).not.toBeInTheDocument();
  });

  it('reads a group id as a kind this cluster does not serve', async () => {
    await render('/dashboard?resource=workloads');
    expect(screen.queryByTestId('objects-table')).not.toBeInTheDocument();
    expect(screen.getByText(/this cluster does not serve workloads/i)).toBeInTheDocument();
  });

  it('names a discovered kind by the kind the catalog gives it', async () => {
    const hpa = {
      ...KIND,
      apiVersion: 'autoscaling/v2',
      kind: 'HorizontalPodAutoscaler',
      resource: 'horizontalpodautoscalers',
    };
    await render('/dashboard?resource=autoscaling/horizontalpodautoscalers', [hpa]);
    expect(screen.getByRole('heading', { name: /^HorizontalPodAutoscaler\b/ })).toBeInTheDocument();
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

  it('keeps Overview’s placeholder while the cluster has no active cache', async () => {
    useClusterCachedDataKindsMock.mockReturnValue({
      kinds: [],
      active: false,
      cacheID: undefined,
      phase: 'connecting',
    });
    await renderWithRouter(
      testRoot.addChildren([appRoute.addChildren([dashboardRoute])]),
      '/dashboard?resource=overview',
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

  it('heads a kind’s page with its label alone, and no count', async () => {
    useClusterCachedDataObjectsMock.mockReturnValue({
      objects: [pod('a', 'x'), pod('b', 'y')],
      active: true,
      phase: 'live',
    });
    await render('/dashboard?resource=deployments', [KIND]);
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(/^Deployments$/);
    expect(screen.queryByTestId('page-count')).not.toBeInTheDocument();
  });

  it('shows a namespaced kind’s objects in the namespace alone', async () => {
    useClusterCachedDataObjectsMock.mockReturnValue({
      objects: [pod('a', 'checkout'), pod('b', 'default'), pod('c', 'checkout')],
      active: true,
      phase: 'live',
    });
    await render('/dashboard?resource=deployments&namespace=checkout', [KIND]);
    const table = propsOf('objects-table');
    expect(table.objects.map((o: { uid: string }) => o.uid)).toEqual(['a', 'c']);
    expect(table.namespace).toBe('checkout');
  });

  it('shows every object of a cluster-scoped kind under a namespace, and says so', async () => {
    const pv = { ...KIND, apiVersion: 'v1', kind: 'PersistentVolume', resource: 'persistentvolumes', scope: 'Cluster' };
    useClusterCachedDataObjectsMock.mockReturnValue({
      objects: [pod('a', ''), pod('b', '')],
      active: true,
      phase: 'live',
    });
    await render('/dashboard?resource=persistentvolumes&namespace=checkout', [pv]);
    expect(propsOf('objects-table').objects).toHaveLength(2);
    expect(propsOf('objects-table').namespace).toBeUndefined();
    expect(screen.getByText(/Cluster-scoped, so the/)).toHaveTextContent(
      "Cluster-scoped, so the checkout filter isn't applied.",
    );
  });

  it('heads Events with its count, scoped to the namespace', async () => {
    useClusterCachedDataEventsMock.mockReturnValue({
      events: [event('a', 'checkout'), event('b', 'default')],
      active: true,
      phase: 'live',
    });
    await render('/dashboard?resource=events&namespace=checkout');
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Events');
    expect(screen.getByTestId('page-count')).toHaveTextContent('1');
    expect(screen.getByText(/Events in/)).toHaveTextContent('Events in checkout.');
    const table = propsOf('events-table');
    expect(table.events.map((e: { uid: string }) => e.uid)).toEqual(['a']);
    expect(table.namespace).toBe('checkout');
  });

  it('heads Events with every event and no subtitle without a namespace', async () => {
    useClusterCachedDataEventsMock.mockReturnValue({
      events: [event('a', 'x'), event('b', 'y')],
      active: true,
      phase: 'live',
    });
    await render('/dashboard?resource=events');
    expect(screen.getByTestId('page-count')).toHaveTextContent('2');
    expect(screen.queryByText(/Events in/)).not.toBeInTheDocument();
  });

  it('draws no Events count before the events land', async () => {
    useClusterCachedDataEventsMock.mockReturnValue({ events: [], active: true, phase: 'connecting' });
    await render('/dashboard?resource=events');
    expect(screen.queryByTestId('page-count')).not.toBeInTheDocument();
  });
});
