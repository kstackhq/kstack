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

// The window's cluster's status, opened from the omnibox's server icon: its
// connection and sync verdicts, each over the Clusters dialog's own detail pane.
import type { ReactElement, RefObject } from 'react';

import { Popover as PopoverPrimitive } from '@base-ui/react/popover';
import { X } from 'lucide-react';

import { Button } from '@kubetail/ui/elements/button';
import { PopoverClose } from '@kubetail/ui/elements/popover';

import { ConnectionDetail, SyncDetail, useCacheContents } from '@/components/widgets/cluster-sync-panel';
import { OmniboxPopover } from '@/components/widgets/omnibox-popover';

import { TONE, connectionStatus, syncStatus } from '@/lib/cluster-health';
import type { Status } from '@/lib/cluster-health';
import type { Cluster } from '@/lib/clusters';

function Verdict({ axis, status }: { axis: string; status: Status }) {
  return (
    <div className="flex gap-2">
      <dt className="w-24 shrink-0 text-muted-foreground">{axis}</dt>
      <dd data-tone={status.tone} className={TONE[status.tone].text}>
        {status.label}
      </dd>
    </div>
  );
}

function ClusterStatus({ cluster }: { cluster: Cluster }) {
  const connection = connectionStatus(cluster, 'active');
  const sync = syncStatus(cluster, 'active');
  const { activeCache } = cluster;
  const health = activeCache?.health;
  const contents = useCacheContents(cluster.id, activeCache?.id ?? '', !activeCache);

  return (
    <div className="flex min-h-0 flex-col gap-3 overflow-y-auto">
      <dl className="space-y-0.5 text-sm">
        <Verdict axis="Connection" status={connection} />
        <Verdict axis="Sync" status={sync} />
      </dl>
      <ConnectionDetail cluster={cluster} connFailed={connection.tone === 'error'} />
      {/* A pending or never-synced cluster has no rollup, so nothing to detail. */}
      {health && <SyncDetail clusterId={cluster.id} health={health} cacheId={health.cacheID} contents={contents} />}
    </div>
  );
}

// `cluster` is the context's record, absent until the sidecar has reached it.
export function ClusterStatusPopover({
  anchorRef,
  context,
  cluster,
  children,
}: {
  anchorRef: RefObject<HTMLElement | null>;
  context: string;
  cluster: Cluster | undefined;
  children: ReactElement;
}) {
  const popup = (
    <PopoverPrimitive.Popup
      data-testid="cluster-status"
      className="flex max-w-[calc(100vw-16px)] flex-col gap-3 rounded-xl border bg-popover p-[18px] text-popover-foreground shadow-lg outline-hidden"
      style={{ width: 'max(var(--anchor-width), 480px)', maxHeight: 'var(--available-height)' }}
    >
      <div className="flex items-center gap-2">
        <div className="flex min-w-0 flex-1 flex-col">
          <p className="text-base font-medium">Cluster status</p>
          <p className="truncate text-xs text-muted-foreground">{context}</p>
        </div>
        <PopoverClose
          render={<Button variant="ghost" size="icon-xs" className="text-muted-foreground" />}
          aria-label="Close"
        >
          <X aria-hidden />
        </PopoverClose>
      </div>
      {cluster ? (
        <ClusterStatus cluster={cluster} />
      ) : (
        <p className="text-sm text-muted-foreground">Kstack has not reached this cluster yet.</p>
      )}
    </PopoverPrimitive.Popup>
  );

  return (
    <OmniboxPopover anchorRef={anchorRef} popup={popup}>
      {children}
    </OmniboxPopover>
  );
}
