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

// One cluster's live diagnostics, each a stream the Clusters dialog and the status popover
// read: the cache's contents, the next connection check, and the connection's data rate.
import { graphql } from '@/gql';
import { perFrame, seriesReducer, useWatchSubscription } from '@/lib/graphql/use-watch-subscription';
import type { SeriesFrame } from '@/lib/graphql/use-watch-subscription';

// The cache's contents as a live gauge. NOT read off the ClusterCache record: that
// object stops changing once its sync settles, so a field there would freeze at
// subscribe time.
const ClusterCacheStatsSubscription = graphql(`
  subscription ClusterCacheStats($id: ClusterID!, $cacheID: ObjectID!) {
    clusterCacheStatsWatch(id: $id, cacheID: $cacheID) {
      exists
      bytes
      dbBytes
      walBytes
      shmBytes
      objectCount
      kindCount
    }
  }
`);

export type CacheContents = {
  exists: boolean;
  bytes: number;
  dbBytes: number;
  walBytes: number;
  shmBytes: number;
  objectCount: number;
  kindCount: number;
};

// A gauge: each frame replaces the last outright. null until the first frame.
export function useCacheContents(clusterId: string, cacheId: string, pause: boolean): CacheContents | null {
  const { data } = useWatchSubscription<{ clusterCacheStatsWatch: CacheContents }, CacheContents>(
    { query: ClusterCacheStatsSubscription, variables: { id: clusterId, cacheID: cacheId }, pause },
    perFrame((_prev, resp) => resp.clusterCacheStatsWatch),
  );
  return data ?? null;
}

// Next-reconcile time, streamed per-cluster (a scheduling change fires no list
// watch — the only way the countdown stays live for an idle disconnected cluster).
// Subscribed only while a row's diagnostics are open.
const ClusterScheduleSubscription = graphql(`
  subscription ClusterSchedule($id: ClusterID!) {
    clusterScheduleWatch(id: $id) {
      nextRequeueAt
      probing
      failures
    }
  }
`);

// `failures` counts the connection attempts in a row that have failed.
export type NextCheckState = { atMs: number | null; probing: boolean; failures: number };

export function useNextCheck(clusterId: string): NextCheckState {
  // Null `nextRequeueAt` = reconcile in flight (`probing`) or nothing scheduled.
  // Hold the last time only across the in-flight window; with `probing` false a
  // null must clear the countdown, not freeze a stale one.
  const { data } = useWatchSubscription<
    { clusterScheduleWatch: { nextRequeueAt: string | null; probing: boolean; failures: number } },
    { nextRequeueAt: string | null; probing: boolean; failures: number }
  >(
    { query: ClusterScheduleSubscription, variables: { id: clusterId } },
    perFrame((prev, resp) => {
      const { nextRequeueAt, probing, failures } = resp.clusterScheduleWatch;
      return {
        nextRequeueAt: nextRequeueAt ?? (probing ? (prev?.nextRequeueAt ?? null) : null),
        probing,
        failures,
      };
    }),
  );
  return {
    atMs: parseTimeOrNull(data?.nextRequeueAt ?? null),
    probing: data?.probing ?? false,
    failures: data?.failures ?? 0,
  };
}

// The connection's data rate as a series: the sidecar's window first, then each new sample.
// Unshared: a view mounting into a running shared stream would never see the window. Paused
// for a disabled cluster, which the sidecar refuses rather than reads as idle.
const ClusterDataRateSubscription = graphql(`
  subscription ClusterDataRate($id: ClusterID!) {
    clusterDataRateWatch(id: $id) {
      intervalSeconds
      windowSeconds
      samples {
        at
        receivedBytesPerSecond
        sentBytesPerSecond
      }
    }
  }
`);

type DataRateSample = { at: string; receivedBytesPerSecond: number; sentBytesPerSecond: number };
type DataRateSeries = SeriesFrame<DataRateSample>;

// Null until the window arrives.
export function useDataRate(clusterId: string, pause: boolean): DataRateSeries | null {
  const { data } = useWatchSubscription<{ clusterDataRateWatch: DataRateSeries }, DataRateSeries>(
    { query: ClusterDataRateSubscription, variables: { id: clusterId }, pause },
    seriesReducer((resp) => resp.clusterDataRateWatch),
    { unshared: true },
  );
  return data ?? null;
}

// An ISO time in epoch milliseconds; null for a missing or unparseable one.
export function parseTimeOrNull(iso: string | null | undefined): number | null {
  const ms = iso ? Date.parse(iso) : Number.NaN;
  return Number.isNaN(ms) ? null : ms;
}
