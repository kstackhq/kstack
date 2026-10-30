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

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { clusterRecord } from '@/test-utils';

import type { Cluster } from '@/lib/clusters';
import { ClusterStatusPopover } from './cluster-status-popover';

// The dialog's detail panes are its own to test; here they say which record they got.
vi.mock('@/components/widgets/cluster-sync-panel', () => ({
  ConnectionDetail: ({ cluster }: { cluster: { id: string } }) => <p>connection detail {cluster.id}</p>,
  SyncDetail: ({ cacheId }: { cacheId: string }) => <p>sync detail {cacheId}</p>,
  useCacheContents: () => null,
}));

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

  it('says when the cluster has no record yet', async () => {
    const popup = await openStatus(undefined);
    expect(popup).toHaveTextContent('Kstack has not reached this cluster yet.');
    expect(popup).not.toHaveTextContent('connection detail');
  });
});
