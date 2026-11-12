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

// The live dashboard resource tree over the active cluster's kinds catalog. Until the
// catalog's Bookmark lands, or with no active cache, the tree is the curated kinds without
// counts. See docs/adr/2026-11-12-the-dashboard-nav-groups-kinds-by-scope-and-api-group.md
import { useMemo } from 'react';

import { useClusterCachedDataKinds } from '@/lib/cluster-cached-data-kinds';
import { buildDashboardNav } from '@/lib/dashboard-resources';
import type { DashboardNav } from '@/lib/dashboard-resources';
import type { WatchPhase } from '@/lib/graphql/use-watch-subscription';

// While paused (`active` false), `phase`'s `connected` is meaninglessly false — a
// "reconnecting/loading" affordance must gate on `active`, not `phase` alone, to
// stay silent on the curated-only fallback.
export function useDashboardNav(): { nav: DashboardNav; active: boolean; phase: WatchPhase } {
  const { kinds, active, phase } = useClusterCachedDataKinds();
  const loaded = active && phase !== 'connecting';
  const nav = useMemo(() => buildDashboardNav(loaded ? kinds : undefined), [loaded, kinds]);
  return { nav, active, phase };
}
