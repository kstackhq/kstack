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

// The active cluster's discovered kind catalog as a live `ServerKind[]`, read by the
// dashboard nav and the object tables (which resolve a curated nav id like "pods"
// back to its full apiVersion/resource/scope here). `clusterCachedDataKindsWatch` is a
// per-cache delta watch; per-kind `count` is live (an object write re-emits as
// `Modified`). Reduced via `useCacheDeltaWatch`, whose cacheID provenance guard keeps
// two caches' kinds from mixing — see docs/adr/2026-08-09-delta-watch-protocol.md
import { createContext, useContext, useMemo } from 'react';

import { graphql } from '@/gql';
import type { ClusterCachedDataKindsWatchSubscription as ClusterCachedDataKindsWatchSubscriptionType } from '@/gql/graphql';
import { useActiveCluster } from '@/lib/active-cluster';
import type { ServerKind } from '@/lib/dashboard-resources';
import { useCacheDeltaWatch, joinProvenance } from '@/lib/graphql/use-cache-delta-watch';
import type { WatchPhase } from '@/lib/graphql/use-watch-subscription';
import { gvrKey } from '@/lib/gvr';

const ClusterCachedDataKindsWatchSubscription = graphql(`
  subscription ClusterCachedDataKindsWatch($id: ClusterID!, $cacheID: ObjectID!) {
    clusterCachedDataKindsWatch(id: $id, cacheID: $cacheID) {
      type
      cacheID
      kind {
        apiVersion
        kind
        resource
        scope
        isCRD
        count
        printerColumns {
          name
          type
          jsonPath
          priority
        }
      }
    }
  }
`);

// The `kind` payload of a change — structurally a `ServerKind`.
type KindRow = NonNullable<ClusterCachedDataKindsWatchSubscriptionType['clusterCachedDataKindsWatch']['kind']>;

type ClusterCachedDataKindsContextValue = { kinds: ServerKind[]; active: boolean; phase: WatchPhase };

const ClusterCachedDataKindsContext = createContext<ClusterCachedDataKindsContextValue | null>(null);

// Holds the window's one catalog subscription, above both consumers, so it outlives the
// nav's card. `enabled` is false off the dashboard, where nothing reads the catalog, and
// the value reports it: a paused watch with no kinds, never one still connecting.
export function ClusterCachedDataKindsProvider({ enabled, children }: { enabled: boolean; children: React.ReactNode }) {
  const { clusterID, cacheID, active } = useActiveCluster();

  // Provenance is just the cacheID — the variables carry no kind — so a cache swap
  // moves the key and re-subscribes.
  const { items, phase } = useCacheDeltaWatch<ClusterCachedDataKindsWatchSubscriptionType, KindRow>(
    {
      query: ClusterCachedDataKindsWatchSubscription,
      variables: { id: clusterID ?? '', cacheID: cacheID ?? '' },
      pause: !enabled || !active,
    },
    {
      select: (d) => {
        const f = d.clusterCachedDataKindsWatch;
        return { type: f.type, entity: f.kind, provenance: joinProvenance(f.cacheID) };
      },
      keyOf: gvrKey,
      currentProvenance: joinProvenance(cacheID ?? ''),
    },
  );

  const value = useMemo<ClusterCachedDataKindsContextValue>(
    () => ({ kinds: enabled ? items : [], active: enabled && active, phase }),
    [items, enabled, active, phase],
  );
  return <ClusterCachedDataKindsContext.Provider value={value}>{children}</ClusterCachedDataKindsContext.Provider>;
}

// The active context's discovered kinds, live. Empty while paused: no active cache,
// or the provider disabled. Insertion order (no sort).
export function useClusterCachedDataKinds(): ClusterCachedDataKindsContextValue {
  const ctx = useContext(ClusterCachedDataKindsContext);
  if (!ctx) throw new Error('useClusterCachedDataKinds must be used inside <ClusterCachedDataKindsProvider>');
  return ctx;
}
