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
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Model } from './models';

// The watch's seam: the frames it has delivered, folded by the hook's own
// reducer, and the options it was opened with.
const { watch } = vi.hoisted(() => ({
  watch: {} as { frames?: unknown[]; options?: unknown },
}));
vi.mock('@/lib/graphql/use-watch-subscription', () => ({
  perFrame:
    <D, R>(step: (prev: R | undefined, frame: D) => R) =>
    (prev: R | undefined, frames: D[]) =>
      frames.reduce<R | undefined>(step, prev),
  useWatchSubscription: (_args: unknown, reduce: (prev: unknown, frames: unknown[]) => unknown, options: unknown) => {
    watch.options = options;
    return { data: watch.frames ? reduce(undefined, watch.frames) : undefined, connected: true };
  },
}));
vi.mock('@/gql', () => ({ graphql: () => ({}) }));

const { modelOf, providersOf, seedPick, useModels } = await import('./models');

const opus: Model = {
  provider: { id: 'anthropic', label: 'Anthropic' },
  id: 'claude-opus-5',
  label: 'Opus 5',
  efforts: ['low', 'high'],
  defaultEffort: 'high',
};
const fake: Model = {
  provider: { id: 'fake', label: 'Fake' },
  id: 'fake',
  label: 'Fake model',
  efforts: [],
  defaultEffort: '',
};

beforeEach(() => {
  watch.frames = undefined;
});

describe('useModels', () => {
  it('has answered nothing before the first frame', () => {
    const { result } = renderHook(() => useModels());
    expect(result.current).toEqual({ models: [], loaded: false });
  });

  it('serves the catalog in the order the sidecar sent it', () => {
    watch.frames = [{ modelsWatch: [opus, fake] }];
    const { result } = renderHook(() => useModels());
    expect(result.current.loaded).toBe(true);
    expect(result.current.models.map((m) => m.id)).toEqual(['claude-opus-5', 'fake']);
  });

  // A sidecar with no provider answers with nothing, which is a different thing
  // from not having answered: one disables Send with a placeholder, the other waits.
  it('has answered an empty catalog', () => {
    watch.frames = [{ modelsWatch: [] }];
    const { result } = renderHook(() => useModels());
    expect(result.current).toEqual({ models: [], loaded: true });
  });

  // A connection that comes or goes changes the list: the latest frame is the
  // catalog.
  it('follows the latest frame', () => {
    watch.frames = [{ modelsWatch: [opus] }, { modelsWatch: [opus, fake] }];
    const { result } = renderHook(() => useModels());
    expect(result.current.models).toEqual([opus, fake]);
  });

  // A gauge's first frame is its snapshot, so each reader opens its own stream.
  it('opens a stream of its own', () => {
    renderHook(() => useModels());
    expect(watch.options).toEqual({ unshared: true });
  });
});

describe('seedPick', () => {
  it("starts on the chat's last answer, at the effort it ran", () => {
    expect(seedPick([opus, fake], { providerID: 'anthropic', id: 'claude-opus-5', effort: 'low' })).toEqual({
      model: { providerID: 'anthropic', id: 'claude-opus-5' },
      effort: 'low',
    });
  });

  it("starts on the catalog's first model when there is no answer to follow", () => {
    expect(seedPick([opus, fake], null)).toEqual({
      model: { providerID: 'anthropic', id: 'claude-opus-5' },
      effort: 'high',
    });
  });

  // The levels are the provider's own words, so the same name on another model is
  // not the same level — and inheriting it would quietly change what a turn costs.
  it("takes the fallback model's own default when the answer's model is gone", () => {
    const cheap = { ...opus, id: 'claude-sonnet-5', defaultEffort: 'low' };
    expect(seedPick([cheap, fake], { providerID: 'anthropic', id: 'claude-opus-5', effort: 'high' })).toEqual({
      model: { providerID: 'anthropic', id: 'claude-sonnet-5' },
      effort: 'low',
    });
  });

  // A disconnected ChatGPT plan lists nothing: the chat stays on it, unavailable,
  // rather than moving to another provider unasked.
  it('keeps the answer whose provider lists nothing', () => {
    expect(seedPick([opus], { providerID: 'chatgpt', id: 'gpt-6.1-sol', effort: 'medium' })).toEqual({
      model: { providerID: 'chatgpt', id: 'gpt-6.1-sol' },
      effort: 'medium',
    });
  });

  it("takes the model's default when the effort it ran is gone", () => {
    expect(seedPick([opus], { providerID: 'anthropic', id: 'claude-opus-5', effort: 'xhigh' })?.effort).toBe('high');
  });

  it('has nothing to pick from in an empty catalog', () => {
    expect(seedPick([], null)).toBeUndefined();
  });
});

describe('modelOf', () => {
  it('finds a model by provider and id', () => {
    expect(modelOf([opus, fake], { providerID: 'fake', id: 'fake' })).toBe(fake);
  });

  it('is null for a model the catalog no longer offers', () => {
    expect(modelOf([opus], { providerID: 'fake', id: 'fake' })).toBeNull();
    expect(modelOf([opus], null)).toBeNull();
  });
});

describe('providersOf', () => {
  it('lists each provider once, in the order its first model comes', () => {
    const haiku = { ...opus, id: 'claude-haiku-5' };
    expect(providersOf([opus, fake, haiku])).toEqual([opus.provider, fake.provider]);
    expect(providersOf([])).toEqual([]);
  });
});
