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

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Model } from './models';

const { state, reexecute } = vi.hoisted(() => ({ state: { current: {} }, reexecute: vi.fn() }));
vi.mock('urql', () => ({ useQuery: () => [state.current, reexecute] }));
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
  vi.clearAllMocks();
  state.current = { fetching: true };
});

describe('useModels', () => {
  it('has answered nothing while the query is in flight', () => {
    const { result } = renderHook(() => useModels());
    expect(result.current).toMatchObject({ models: [], loaded: false, failed: false });
  });

  it('serves the catalog in the order the sidecar sent it', () => {
    state.current = { fetching: false, data: { models: [opus, fake] } };
    const { result } = renderHook(() => useModels());
    expect(result.current.loaded).toBe(true);
    expect(result.current.models.map((m) => m.id)).toEqual(['claude-opus-5', 'fake']);
  });

  // A sidecar with no key answers with nothing, which is a different thing from not
  // having answered: one disables Send with a placeholder, the other waits.
  it('has answered an empty catalog', () => {
    state.current = { fetching: false, data: { models: [] } };
    const { result } = renderHook(() => useModels());
    expect(result.current).toMatchObject({ models: [], loaded: true, failed: false });
  });

  // A sidecar that could not be reached has answered nothing, so it must not read as
  // an empty catalog: that would leave the composer saying there is no key, with
  // nothing to run it again.
  it('is failed, not loaded, when the query could not be answered', () => {
    state.current = { fetching: false, error: new Error('unreachable') };
    const { result } = renderHook(() => useModels());
    expect(result.current).toMatchObject({ models: [], loaded: false, failed: true });
  });

  it('asks the sidecar again on retry, past whatever the last answer was', () => {
    state.current = { fetching: false, error: new Error('unreachable') };
    const { result } = renderHook(() => useModels());

    act(() => result.current.retry());

    expect(reexecute).toHaveBeenCalledWith({ requestPolicy: 'network-only' });
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
    expect(seedPick([cheap], { providerID: 'fake', id: 'fake', effort: 'high' })).toEqual({
      model: { providerID: 'anthropic', id: 'claude-sonnet-5' },
      effort: 'low',
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
