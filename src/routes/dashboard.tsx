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

import { Spinner } from '@kubetail/ui/elements/spinner';
import { createRoute } from '@tanstack/react-router';
import { useMemo, useState } from 'react';

import { EventsTable } from '@/components/widgets/events-table';
import { ObjectsTable } from '@/components/widgets/objects-table';
import { useClusterCachedDataKinds } from '@/lib/cluster-cached-data-kinds';
import {
  buildDashboardNav,
  dashboardResourceLabel,
  mayNameDiscoveredKind,
  resolveDashboardResource,
  serverKindForResource,
} from '@/lib/dashboard-resources';
import type { ServerKind } from '@/lib/dashboard-resources';
import { Route as appRoute } from '@/routes/_app';

// The focused kind lives in the `resource` search param, declared on `_app` so it
// survives the switch to chat and back (deep-linkable, each change a history entry —
// see docs/adr/2026-08-09-url-params-as-window-state.md).
export const Route = createRoute({
  getParentRoute: () => appRoute,
  path: '/dashboard',
  component: Dashboard,
});

// The kind a resource last resolved to, under the cache it came from.
type HeldKind = { cacheID: string | undefined; resource: string; kind: ServerKind };

// Panel pick: `events` → bespoke `EventsTable`; a resolved discovered kind → generic
// `ObjectsTable`. A resource the catalog could name (`mayNameDiscoveredKind`) and does
// not reads "not synced" with no active cache, waits behind a spinner for the Bookmark,
// and after it is not served by this cluster. A group row or Overview reads "coming
// soon" at once. Label and kind resolve against the same live catalog the sidebar uses.
function Dashboard() {
  const { resource } = Route.useSearch();
  const { kinds, active, cacheID, phase } = useClusterCachedDataKinds();
  const resolved = resolveDashboardResource(resource);
  // The catalog re-emits on count churn — memoize so a Modified frame doesn't
  // rebuild the tree / rescan the catalog every render.
  const label = useMemo(() => dashboardResourceLabel(buildDashboardNav(kinds), resolved), [kinds, resolved]);
  const serverKind = useMemo(() => serverKindForResource(kinds, resolved), [kinds, resolved]);

  // A new connection replays the catalog onto a clean slate, so it is empty until the
  // next Bookmark. Holding the last resolved kind through that keeps `ObjectsTable`
  // mounted, with its scroll, sort and selection.
  const [held, setHeld] = useState<HeldKind>();
  if (serverKind && (held?.kind !== serverKind || held.cacheID !== cacheID || held.resource !== resolved)) {
    setHeld({ cacheID, resource: resolved, kind: serverKind });
  }
  const holding = held && active && phase === 'connecting' && held.cacheID === cacheID && held.resource === resolved;
  const kind = serverKind ?? (holding ? held.kind : undefined);

  let panel;
  if (resolved === 'events') {
    panel = <EventsTable />;
  } else if (kind) {
    panel = (
      <ObjectsTable
        apiVersion={kind.apiVersion}
        resource={kind.resource}
        kind={kind.kind}
        namespaced={kind.scope === 'Namespaced'}
        printerColumns={kind.printerColumns}
      />
    );
  } else if (!mayNameDiscoveredKind(resolved)) {
    panel = <Note>The {label.toLowerCase()} view is coming soon.</Note>;
  } else if (!active) {
    panel = <Note>No synced cache for this cluster yet — {label.toLowerCase()} appear once syncing has started.</Note>;
  } else if (phase === 'connecting') {
    panel = (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Spinner size="sm" />
        Loading resources…
      </div>
    );
  } else {
    panel = <Note>This cluster does not serve {label.toLowerCase()}, or your credentials cannot list them.</Note>;
  }

  return (
    <section className="flex min-h-0 min-w-0 flex-1 flex-col p-6">
      <h1 className="mb-4 text-lg font-semibold">{label}</h1>
      {panel}
    </section>
  );
}

function Note({ children }: { children: React.ReactNode }) {
  return <p className="text-sm text-muted-foreground">{children}</p>;
}
