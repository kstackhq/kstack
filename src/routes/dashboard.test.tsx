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
  useClusterCachedDataKindsMock.mockReturnValue({ kinds, active: true, phase: 'live' });
  return renderWithRouter(testRoot.addChildren([appRoute.addChildren([dashboardRoute])]), path);
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

  it('falls back to a placeholder for a view with no kind behind it', async () => {
    // A group row, or a deep link the catalog has never heard of.
    await render('/dashboard?resource=workloads');
    expect(screen.queryByTestId('objects-table')).not.toBeInTheDocument();
    expect(screen.getByText(/coming soon/i)).toBeInTheDocument();
  });

  it('keeps the table it shows as the catalog’s counts churn', async () => {
    await render('/dashboard?resource=deployments', [KIND]);
    useClusterCachedDataKindsMock.mockReturnValue({ kinds: [{ ...KIND, count: 4 }], active: true, phase: 'live' });
    act(() => catalog.emit());
    expect(screen.getByTestId('objects-table')).toHaveTextContent('"resource":"deployments"');
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
