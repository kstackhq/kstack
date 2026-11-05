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

// What one cluster's chats remember: its own memories and every cluster's, a delta
// watch folded into an id-keyed map. Stored records with no cache behind them, like
// the chats, so there is no provenance to guard.
import { useMemo } from 'react';

import { graphql } from '@/gql';
import type { MemoriesWatchSubscription } from '@/gql/graphql';
import { fold } from '@/lib/chats';
import type { Folded } from '@/lib/chats';
import { useWatchSubscription, watchPhase } from '@/lib/graphql/use-watch-subscription';
import type { WatchPhase } from '@/lib/graphql/use-watch-subscription';

const MemoriesWatchSubscription = graphql(`
  subscription MemoriesWatch($clusterID: ClusterID!) {
    memoriesWatch(clusterID: $clusterID) {
      type
      memory {
        id
        clusterID
        name
        body
        writtenBy
        updatedAt
      }
    }
  }
`);

export type Memory = NonNullable<MemoriesWatchSubscription['memoriesWatch']['memory']>;

function clusterFirstByName(a: Memory, b: Memory): number {
  return Number(a.clusterID === null) - Number(b.clusterID === null) || a.name.localeCompare(b.name);
}

type MemoriesState = Folded<Memory> & { lastSaved?: Memory };

/**
 * What `clusterID`'s chats see: its own memories, then every cluster's, each by
 * name — the order of the index the model reads. No watch without a cluster.
 *
 * `lastSaved` is the memory the latest save wrote, a new object per save. Every
 * webview watching the cluster receives the same save at once. Only a change after
 * the Bookmark counts, so a snapshot or a reconnect's relist is not a save.
 */
export function useMemories(clusterID: string | undefined): {
  memories: Memory[];
  lastSaved: Memory | undefined;
  phase: WatchPhase;
} {
  // `unshared`: the omnibox's button and the dialog both watch, and the dialog
  // mounts into a stream already running.
  const { data, connected } = useWatchSubscription(
    { query: MemoriesWatchSubscription, variables: { clusterID: clusterID ?? '' }, pause: !clusterID },
    (prev: MemoriesState | undefined, frames) => {
      const changes = frames.map(({ memoriesWatch: { type, memory } }) => ({ type, entity: memory }));
      let synced = prev?.synced ?? false;
      let lastSaved = prev?.lastSaved;
      changes.forEach(({ type, entity }) => {
        if (type === 'Bookmark') synced = true;
        else if (synced && entity && type !== 'Deleted') lastSaved = entity;
      });
      return { ...fold(prev, changes), lastSaved };
    },
    { unshared: true },
  );

  const items = data?.items;
  const memories = useMemo(() => [...(items?.values() ?? [])].toSorted(clusterFirstByName), [items]);

  return { memories, lastSaved: data?.lastSaved, phase: watchPhase(!!data?.synced, connected) };
}
