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

// What one cluster's chats remember: its own memories and every cluster's, a collection
// watch folded into an id-keyed map. Stored records with no cache behind them, like
// the chats, so there is no provenance to guard.
import { useMemo } from 'react';

import { graphql } from '@/gql';
import type { MemoryCollectionWatchSubscription } from '@/gql/graphql';
import { collectionWatchReducer } from '@/lib/graphql/collection-watch';
import type { CollectionState } from '@/lib/graphql/collection-watch';
import { useFoldedSubscription, subscriptionPhase } from '@/lib/graphql/use-folded-subscription';
import type { SubscriptionPhase } from '@/lib/graphql/use-folded-subscription';

const MemoryCollectionWatchDocument = graphql(`
  subscription MemoryCollectionWatch($clusterID: ClusterID!) {
    memoryCollectionWatch(clusterID: $clusterID) {
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

export type Memory = NonNullable<MemoryCollectionWatchSubscription['memoryCollectionWatch']['memory']>;

function clusterFirstByName(a: Memory, b: Memory): number {
  return Number(a.clusterID === null) - Number(b.clusterID === null) || a.name.localeCompare(b.name);
}

type MemoriesState = CollectionState<Memory> & { lastSaved?: Memory };

const reduceMemories = collectionWatchReducer(
  ({ memoryCollectionWatch: { type, memory } }: MemoryCollectionWatchSubscription) => ({ type, item: memory }),
);

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
  phase: SubscriptionPhase;
} {
  // `unshared`: the omnibox's button and the dialog both watch, and the dialog
  // mounts into a stream already running.
  const { data, connected } = useFoldedSubscription(
    { query: MemoryCollectionWatchDocument, variables: { clusterID: clusterID ?? '' }, pause: !clusterID },
    (prev: MemoriesState | undefined, frames) => {
      let { lastSaved } = prev ?? {};
      let snapshotComplete = prev?.snapshotComplete ?? false;
      frames.forEach(({ memoryCollectionWatch: { type, memory } }) => {
        if (type === 'Bookmark') snapshotComplete = true;
        else if (snapshotComplete && memory && type !== 'Deleted') lastSaved = memory;
      });
      return { ...reduceMemories(prev, frames), lastSaved };
    },
    { unshared: true },
  );

  const items = data?.items;
  const memories = useMemo(() => [...(items?.values() ?? [])].toSorted(clusterFirstByName), [items]);

  return { memories, lastSaved: data?.lastSaved, phase: subscriptionPhase(!!data?.snapshotComplete, connected) };
}
