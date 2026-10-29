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

// A cluster record's health: a connection axis and a sync axis, each a word with a
// tone, and the overall tone, the worse of the two. The Clusters dialog draws the
// axes as columns and the overall tone as its leading circle; the cluster picker
// and the omnibox's dot show the overall tone per kubeconfig context.
import { clusterForContext } from '@/lib/clusters';
import type { Cluster } from '@/lib/clusters';

export type Tone = 'ok' | 'attention' | 'error' | 'muted';
// A record still named by the kubeconfig, or one whose cache outlived its context.
export type Group = 'active' | 'orphaned';

// Per tone: its severity, its dot fill, and a text-tuned shade for a tinted label.
export const TONE: Record<Tone, { severity: number; dot: string; text: string }> = {
  error: { severity: 3, dot: 'bg-red-500', text: 'text-red-600 dark:text-red-400' },
  attention: { severity: 2, dot: 'bg-amber-500', text: 'text-amber-600 dark:text-amber-400' },
  ok: { severity: 1, dot: 'bg-emerald-500', text: 'text-emerald-600 dark:text-emerald-400' },
  muted: { severity: 0, dot: 'bg-muted-foreground/50', text: 'text-muted-foreground' },
};

export type Status = { label: string; tone: Tone };
export type Health = { tone: Tone; summary: string };

export function overallTone(connection: Tone, sync: Tone): Tone {
  return TONE[connection].severity >= TONE[sync].severity ? connection : sync;
}

// Pending = not yet identified (kube-system UID probe hasn't succeeded, e.g. a
// stopped minikube): can't sync or cache yet.
export function isPending(c: Cluster): boolean {
  return !c.status.server.uid;
}

export function findCondition<T extends { type: string }>(conditions: T[], type: string): T | undefined {
  return conditions.find((cond) => cond.type === type);
}

// Derives from the live `Connected` condition, not the sticky `server.uid` (never
// clears, so it would read "Active" after a drop).
export function connectionStatus(c: Cluster, group: Group): Status {
  if (group === 'orphaned') return { label: 'Unavailable', tone: 'muted' };
  const connected = findCondition(c.conditions, 'Connected');
  if (connected?.status === 'True') return { label: 'Active', tone: 'ok' };
  if (connected?.status === 'False') return { label: 'Disconnected', tone: 'error' };
  return { label: 'Connecting', tone: 'attention' };
}

// Gates on the live `Connected` condition first (a dropped connection leaves the
// engine reporting a stale "Watching"), then reads the rollup.
export function syncStatus(c: Cluster, group: Group): Status {
  if (group === 'orphaned') return { label: 'Stopped', tone: 'muted' };
  if (isPending(c)) return { label: 'Not synced', tone: 'muted' };
  if (!c.spec.syncEnabled) return { label: 'Paused', tone: 'muted' };
  const connected = findCondition(c.conditions, 'Connected');
  // Muted, not amber: the fault is on the connection axis, and graying the gated
  // sync value keeps it reading as a downstream symptom.
  if (connected?.status === 'False') return { label: 'Stalled', tone: 'muted' };
  // The sidecar's per-kind rollup, dominated by the worst kind. Neither the cache's
  // coarse Synced condition nor any single kind's would do.
  const health = c.activeCache?.health;
  // No rollup yet: nothing observed, only work in progress.
  if (!health) return { label: 'Syncing', tone: 'ok' };
  switch (health.reason) {
    case 'SyncFailed':
      return { label: 'Error', tone: 'error' };
    // The cache's file will not open, so nothing under it syncs and no kind can say
    // why. It never clears on its own (the Clear button is the fix), which is why it
    // reads as hard as a failing kind rather than as a stall.
    case 'StoreFailed':
      return { label: 'Storage error', tone: 'error' };
    // A watch went quiet past the freshness threshold: amber, not a hard error.
    case 'Stale':
      return { label: 'Stale', tone: 'attention' };
    // Steady state: all kinds caught up and streaming deltas.
    case 'Watching':
      return { label: 'Synced', tone: 'ok' };
    // Waiting on credentials; muted like Stalled, since the sync itself is fine.
    case 'NoConnection':
      return { label: 'Connecting', tone: 'muted' };
    // Every kind paused. Distinct from the spec.syncEnabled gate above, which flips
    // instantly on resume, before the kinds' reconciles land.
    case 'Paused':
      return { label: 'Paused', tone: 'muted' };
    // Catching up, or discovery hasn't landed: not a fault.
    case 'Syncing':
    case 'Unknown':
      return { label: 'Syncing', tone: 'ok' };
    // An unknown reason is DEGRADED, not healthy (the schema and the sidecar's fold
    // both read it that way); green here would paint every future verdict healthy.
    default:
      return { label: 'Degraded', tone: 'attention' };
  }
}

export function overallStatus(connection: Status, sync: Status): Health {
  return { tone: overallTone(connection.tone, sync.tone), summary: `${connection.label} · ${sync.label}` };
}

// A kubeconfig context's health: its record's, or muted while no record matches,
// which is also every context's before the clusters snapshot lands.
export function contextHealth(clusters: Cluster[] | null, context: string): Health {
  const record = clusterForContext(clusters, context);
  if (!record) return { tone: 'muted', summary: 'Not reached' };
  return overallStatus(connectionStatus(record, 'active'), syncStatus(record, 'active'));
}
