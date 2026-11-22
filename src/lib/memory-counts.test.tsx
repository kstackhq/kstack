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

import { renderHook } from '@testing-library/react';
import { expect, it, vi } from 'vitest';

import { EVERY } from '@/lib/cluster-scope';

const { state } = vi.hoisted(() => ({ state: { data: null as { clusterID: string | null; count: number }[] | null } }));
vi.mock('@/lib/graphql/use-watch-subscription', () => ({
  useWatchSubscription: (_args: unknown, reduce: (prev: unknown, frames: unknown[]) => unknown) => ({
    data: state.data === null ? undefined : reduce(undefined, [{ memoryCountsWatch: state.data }]),
  }),
  perFrame: (step: (prev: unknown, frame: unknown) => unknown) => (prev: unknown, frames: unknown[]) =>
    frames.reduce(step, prev),
}));

const { useMemoryCounts } = await import('./memory-counts');

it('reads the latest full count snapshot, including the global scope', () => {
  const { result, rerender } = renderHook(useMemoryCounts);
  expect(result.current).toBeUndefined();
  state.data = [
    { clusterID: null, count: 2 },
    { clusterID: 'other', count: 4 },
  ];
  rerender();
  expect(result.current).toEqual(
    new Map([
      [EVERY, 2],
      ['other', 4],
    ]),
  );
  state.data = [{ clusterID: null, count: 1 }];
  rerender();
  expect(result.current).toEqual(new Map([[EVERY, 1]]));
});
