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

import { useMemo } from 'react';

import { graphql } from '@/gql';
import type { MemoryCountsWatchSubscription } from '@/gql/graphql';
import { EVERY } from '@/lib/cluster-scope';
import { perFrame, useWatchSubscription } from '@/lib/graphql/use-watch-subscription';

const MemoryCountsWatch = graphql(`
  subscription MemoryCountsWatch {
    memoryCountsWatch {
      clusterID
      count
    }
  }
`);

export function useMemoryCounts(): Map<string, number> | undefined {
  const { data } = useWatchSubscription(
    { query: MemoryCountsWatch },
    perFrame<MemoryCountsWatchSubscription, MemoryCountsWatchSubscription['memoryCountsWatch']>(
      (_prev, frame) => frame.memoryCountsWatch,
    ),
  );
  return useMemo(() => data && new Map(data.map(({ clusterID, count }) => [clusterID ?? EVERY, count])), [data]);
}
