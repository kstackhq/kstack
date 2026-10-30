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

import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { clusterRecord } from '@/test-utils';

import type { Cluster } from '@/lib/clusters';
import { ClusterStatusPopover } from './cluster-status-popover';

// The cache's contents gauge, which a test can move after the popover opens.
const { gauge } = vi.hoisted(() => {
  let value: object | null = null;
  const listeners = new Set<() => void>();
  return {
    gauge: {
      get: () => value,
      set: (next: object | null) => {
        value = next;
        listeners.forEach((l) => l());
      },
      subscribe: (l: () => void) => {
        listeners.add(l);
        return () => listeners.delete(l);
      },
    },
  };
});

// The dialog's detail panes are its own to test; here they say which record they got.
vi.mock('@/components/widgets/cluster-sync-panel', async () => {
  const { useSyncExternalStore } = await import('react');
  return {
    ConnectionDetail: ({ cluster }: { cluster: { id: string } }) => <p>connection detail {cluster.id}</p>,
    SyncDetail: ({ cacheId, contents }: { cacheId: string; contents: object | null }) => (
      <p>
        sync detail {cacheId}
        {contents && ' with contents'}
      </p>
    ),
    useCacheContents: () => useSyncExternalStore(gauge.subscribe, gauge.get),
  };
});

async function openStatus(cluster: Cluster | undefined) {
  const user = userEvent.setup();
  render(
    <ClusterStatusPopover anchorRef={{ current: null }} context="prod" cluster={cluster}>
      <button type="button">Open status</button>
    </ClusterStatusPopover>,
  );
  await user.click(screen.getByRole('button', { name: 'Open status' }));
  return screen.findByTestId('cluster-status');
}

describe('ClusterStatusPopover', () => {
  beforeEach(() => gauge.set(null));

  it('names the window’s cluster', async () => {
    const popup = await openStatus(clusterRecord({ context: 'prod' }));
    expect(popup).toHaveTextContent('prod');
  });

  it('gives each axis its verdict over the dialog’s detail', async () => {
    const popup = await openStatus(clusterRecord({ context: 'prod', connected: 'False' }));

    expect(within(popup).getByText('Disconnected')).toHaveAttribute('data-tone', 'error');
    expect(within(popup).getByText('Stalled')).toHaveAttribute('data-tone', 'muted');
    expect(popup).toHaveTextContent('connection detail id-prod');
    expect(popup).toHaveTextContent('sync detail cache-prod');
  });

  it('details no sync before the cache has a verdict', async () => {
    const popup = await openStatus(clusterRecord({ context: 'prod', reason: null }));

    expect(within(popup).getByText('Syncing')).toBeInTheDocument();
    expect(popup).toHaveTextContent('connection detail id-prod');
    expect(popup).not.toHaveTextContent('sync detail');
  });

  it('keeps the verdicts when the omnibox draws it again', async () => {
    const cluster = clusterRecord({ context: 'prod', connected: 'False' });
    const view = render(
      <ClusterStatusPopover anchorRef={{ current: null }} context="prod" cluster={cluster}>
        <button type="button">Open status</button>
      </ClusterStatusPopover>,
    );
    await userEvent.click(screen.getByRole('button', { name: 'Open status' }));
    view.rerender(
      <ClusterStatusPopover anchorRef={{ current: null }} context="prod" cluster={cluster}>
        <button type="button">Open status</button>
      </ClusterStatusPopover>,
    );

    const popup = await screen.findByTestId('cluster-status');
    expect(within(popup).getByText('Disconnected')).toHaveAttribute('data-tone', 'error');
  });

  it('hands the contents gauge to the sync detail as it moves, keeping the verdicts', async () => {
    const popup = await openStatus(clusterRecord({ context: 'prod', connected: 'False' }));
    expect(popup).not.toHaveTextContent('with contents');

    act(() => gauge.set({ exists: true }));
    expect(popup).toHaveTextContent('sync detail cache-prod with contents');
    expect(within(popup).getByText('Disconnected')).toHaveAttribute('data-tone', 'error');
  });

  it('says when the cluster has no record yet', async () => {
    const popup = await openStatus(undefined);
    expect(popup).toHaveTextContent('Kstack has not reached this cluster yet.');
    expect(popup).not.toHaveTextContent('connection detail');
  });
});
