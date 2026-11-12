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
import { PageHeader } from '@/components/widgets/page-frame/page-header';
import { useClusterCachedDataEvents } from '@/lib/cluster-cached-data-events';
import { useClusterCachedDataKinds } from '@/lib/cluster-cached-data-kinds';
import { useClusterCachedDataObjects } from '@/lib/cluster-cached-data-objects';
import {
  dashboardResourceLabel,
  mayNameDiscoveredKind,
  resolveDashboardResource,
  serverKindForResource,
  shortNamesOf,
} from '@/lib/dashboard-resources';
import type { DashboardResource, ServerKind } from '@/lib/dashboard-resources';
import type { WatchPhase } from '@/lib/graphql/use-watch-subscription';
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

// Each resource is a page, a header over a panel: `events` → `EventsPage`; a resolved
// discovered kind → `KindPage`; anything else → `PlaceholderPage`. Label and kind resolve
// against the same live catalog the sidebar uses.
function Dashboard() {
  const { resource } = Route.useSearch();
  const { kinds, active, cacheID, phase } = useClusterCachedDataKinds();
  const resolved = resolveDashboardResource(resource);
  // The catalog re-emits on count churn — memoize so a Modified frame doesn't rescan it
  // every render.
  const label = useMemo(() => dashboardResourceLabel(kinds, resolved), [kinds, resolved]);
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

  let page;
  if (resolved === 'events') {
    page = <EventsPage />;
  } else if (kind) {
    page = <KindPage kind={kind} label={label} />;
  } else {
    page = <PlaceholderPage label={label} resource={resolved} active={active} phase={phase} />;
  }
  return <section className="flex min-h-0 min-w-0 flex-1 flex-col p-6">{page}</section>;
}

// A page counts its rows once its watch's snapshot has landed.
function countOf(rows: unknown[], active: boolean, phase: WatchPhase): number | undefined {
  return active && phase !== 'connecting' ? rows.length : undefined;
}

// A kind's page. It owns the objects watch, so the header counts the rows the table
// shows.
function KindPage({ kind, label }: { kind: ServerKind; label: string }) {
  const { objects, active, phase } = useClusterCachedDataObjects({
    apiVersion: kind.apiVersion,
    resource: kind.resource,
  });
  const meta = [kind.apiVersion, shortNamesOf(kind).join(', ')].filter(Boolean).join(' · ');

  return (
    <>
      <PageHeader title={label} count={countOf(objects, active, phase)} meta={meta} />
      <ObjectsTable
        apiVersion={kind.apiVersion}
        resource={kind.resource}
        kind={kind.kind}
        namespaced={kind.scope === 'Namespaced'}
        printerColumns={kind.printerColumns}
        objects={objects}
        active={active}
        phase={phase}
      />
    </>
  );
}

// The Events page, over the events watch as `KindPage` is over its objects.
function EventsPage() {
  const { events, active, phase } = useClusterCachedDataEvents();

  return (
    <>
      <PageHeader title="Events" count={countOf(events, active, phase)} />
      <EventsTable events={events} active={active} phase={phase} />
    </>
  );
}

type PlaceholderPageProps = { label: string; resource: DashboardResource; active: boolean; phase: WatchPhase };

// A page with nothing to list. Overview reads "coming soon" at once. A resource the
// catalog could name (`mayNameDiscoveredKind`) and does not reads "not synced" with no
// active cache, waits behind a spinner for the Bookmark, and after it is not served by
// this cluster.
function PlaceholderPage({ label, resource, active, phase }: PlaceholderPageProps) {
  const name = label.toLowerCase();
  let note;
  if (!mayNameDiscoveredKind(resource)) {
    note = <Note>The {name} view is coming soon.</Note>;
  } else if (!active) {
    note = <Note>No synced cache for this cluster yet — {name} appear once syncing has started.</Note>;
  } else if (phase === 'connecting') {
    note = (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Spinner size="sm" />
        Loading resources…
      </div>
    );
  } else {
    note = <Note>This cluster does not serve {name}, or your credentials cannot list them.</Note>;
  }
  return (
    <>
      <PageHeader title={label} />
      {note}
    </>
  );
}

function Note({ children }: { children: React.ReactNode }) {
  return <p className="text-sm text-muted-foreground">{children}</p>;
}
