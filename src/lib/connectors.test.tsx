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

import type { ChatGPTConnection, OpenRouterConnection } from './connectors';

// The seams: each watch's frames by the subscription its document names, folded
// by the hook's own reducer, and each mutation, by the operation its document names.
const { watch, mutations } = vi.hoisted(() => ({
  watch: { frames: {} } as { frames: Record<string, unknown[]>; options?: unknown },
  mutations: {} as Record<string, ReturnType<typeof vi.fn>>,
}));
vi.mock('@/lib/graphql/use-watch-subscription', () => ({
  perFrame:
    <D, R>(step: (prev: R | undefined, frame: D) => R) =>
    (prev: R | undefined, frames: D[]) =>
      frames.reduce<R | undefined>(step, prev),
  useWatchSubscription: (
    args: { query: { name: string } },
    reduce: (prev: unknown, frames: unknown[]) => unknown,
    options: unknown,
  ) => {
    watch.options = options;
    const frames = watch.frames[args.query.name];
    return { data: frames ? reduce(undefined, frames) : undefined, connected: true };
  },
}));
vi.mock('@/gql', () => ({
  graphql: (source: string) => ({ name: /(?:mutation|subscription) (\w+)/.exec(source)![1] }),
}));
vi.mock('urql', () => ({
  useMutation: (doc: { name: string }) => {
    mutations[doc.name] ??= vi.fn();
    return [{}, mutations[doc.name]];
  },
}));

const { useConnectors, useOpenChatGPTUsage } = await import('./connectors');

function connection(c: Partial<ChatGPTConnection>): ChatGPTConnection {
  return { phase: 'SignedOut', email: null, failure: null, registered: false, revocationUnconfirmed: false, ...c };
}

function openRouter(c: Partial<OpenRouterConnection> = {}): OpenRouterConnection {
  return { phase: 'SignedOut', failure: null, ...c };
}

// Serves both watches' frames: ChatGPT's, then OpenRouter's, which defaults to signed out.
function serve(chatgpt: ChatGPTConnection[], openrouter: OpenRouterConnection[] = [openRouter()]) {
  watch.frames = {
    ChatGPTConnectionWatch: chatgpt.map((c) => ({ chatGPTConnectionWatch: c })),
    OpenRouterConnectionWatch: openrouter.map((c) => ({ openRouterConnectionWatch: c })),
  };
}

beforeEach(() => {
  watch.frames = {};
  for (const k of Object.keys(mutations)) delete mutations[k];
});

describe('useConnectors', () => {
  it('lists the planned connectors and has not loaded before the first frame', () => {
    const { result } = renderHook(() => useConnectors());

    expect(result.current.loaded).toBe(false);
    expect(result.current.connectors).toEqual([
      { id: 'github', name: 'GitHub', detail: 'Code resources and Copilot models', connected: false },
      { id: 'aws', name: 'AWS', detail: 'Cloud resources and Bedrock models', connected: false },
      { id: 'google', name: 'Google', detail: 'Cloud resources and Gemini models', connected: false },
      { id: 'azure', name: 'Azure', detail: 'Cloud resources and Foundry models', connected: false },
      { id: 'chatgpt', name: 'ChatGPT', detail: 'OpenAI models on your ChatGPT plan', connected: false },
      { id: 'openrouter', name: 'OpenRouter', detail: 'Many providers on one account', connected: false },
    ]);
    expect(watch.options).toEqual({ unshared: true });
  });

  it('folds the watch into the ChatGPT entry alone, the account as its detail once connected', () => {
    const live = connection({ phase: 'Connected', email: 'ada@example.com', registered: true });
    serve([connection({}), live]);

    const { result } = renderHook(() => useConnectors());

    expect(result.current.loaded).toBe(true);
    const chatgpt = result.current.connectors.find((c) => c.id === 'chatgpt')!;
    expect(chatgpt).toMatchObject({ connected: true, detail: 'ada@example.com' });
    expect(chatgpt.chatgpt?.connection).toBe(live);
    expect(result.current.connectors.filter((c) => c.chatgpt)).toHaveLength(1);
  });

  it("keeps the entry's own detail for a connection whose email is unknown", () => {
    serve([connection({ phase: 'Connected' })]);

    const { result } = renderHook(() => useConnectors());

    expect(result.current.connectors.find((c) => c.id === 'chatgpt')).toMatchObject({
      connected: true,
      detail: 'OpenAI models on your ChatGPT plan',
    });
  });

  it("keeps the entry's own detail while not connected", () => {
    serve([connection({ phase: 'Failed', failure: 'Declined' })]);

    const { result } = renderHook(() => useConnectors());

    const chatgpt = result.current.connectors.find((c) => c.id === 'chatgpt')!;
    expect(chatgpt).toMatchObject({ connected: false, detail: 'OpenAI models on your ChatGPT plan' });
  });

  it('runs each action as its mutation', () => {
    serve([connection({})]);
    const { result } = renderHook(() => useConnectors());
    const actions = result.current.connectors.find((c) => c.chatgpt)!.chatgpt!.actions;

    actions.connect(true);
    actions.cancel();
    actions.disconnect();
    actions.openUsage();

    expect(mutations.ChatGPTConnect).toHaveBeenCalledWith({ fresh: true });
    expect(mutations.ChatGPTConnectCancel).toHaveBeenCalledWith({});
    expect(mutations.ChatGPTDisconnect).toHaveBeenCalledWith({});
    expect(mutations.ChatGPTUsageOpen).toHaveBeenCalledWith({});
  });

  it('has not loaded until both watches have their first frame', () => {
    watch.frames = { ChatGPTConnectionWatch: [{ chatGPTConnectionWatch: connection({}) }] };
    const { result, rerender } = renderHook(() => useConnectors());
    expect(result.current.loaded).toBe(false);
    expect(result.current.connectors.some((c) => c.chatgpt ?? c.openrouter)).toBe(false);

    watch.frames = { OpenRouterConnectionWatch: [{ openRouterConnectionWatch: openRouter() }] };
    rerender();
    expect(result.current.loaded).toBe(false);
  });

  it('folds the OpenRouter watch into its entry alone, connected in the Connected phase', () => {
    const live = openRouter({ phase: 'Connected' });
    serve([connection({})], [openRouter({ phase: 'SigningIn' }), live]);

    const { result } = renderHook(() => useConnectors());

    const entry = result.current.connectors.find((c) => c.id === 'openrouter')!;
    expect(entry).toMatchObject({ connected: true, detail: 'Many providers on one account' });
    expect(entry.openrouter?.connection).toBe(live);
    expect(result.current.connectors.filter((c) => c.openrouter)).toHaveLength(1);
  });

  it.each(['SignedOut', 'SigningIn', 'FromEnvironment', 'Failed'] as const)(
    'leaves the OpenRouter entry unconnected while %s',
    (phase) => {
      serve([connection({})], [openRouter({ phase })]);

      const { result } = renderHook(() => useConnectors());

      expect(result.current.connectors.find((c) => c.id === 'openrouter')).toMatchObject({ connected: false });
    },
  );

  it('runs each OpenRouter action as its mutation', () => {
    serve([connection({})]);
    const { result } = renderHook(() => useConnectors());
    const actions = result.current.connectors.find((c) => c.openrouter)!.openrouter!.actions;

    actions.connect();
    actions.cancel();
    actions.disconnect();
    actions.openKeys();

    expect(mutations.OpenRouterConnect).toHaveBeenCalledWith({});
    expect(mutations.OpenRouterConnectCancel).toHaveBeenCalledWith({});
    expect(mutations.OpenRouterDisconnect).toHaveBeenCalledWith({});
    expect(mutations.OpenRouterKeysOpen).toHaveBeenCalledWith({});
  });
});

describe('useOpenChatGPTUsage', () => {
  it("asks the sidecar to open ChatGPT's usage settings", () => {
    const { result } = renderHook(() => useOpenChatGPTUsage());

    result.current();

    expect(mutations.ChatGPTUsageOpen).toHaveBeenCalledWith({});
  });
});
