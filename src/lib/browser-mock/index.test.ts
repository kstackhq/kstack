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

import { Channel, invoke } from '@tauri-apps/api/core';
import { clearMocks } from '@tauri-apps/api/mocks';
import {
  GraphQLBoolean,
  GraphQLEnumType,
  GraphQLFloat,
  GraphQLInt,
  GraphQLList,
  GraphQLNonNull,
  GraphQLObjectType,
  GraphQLScalarType,
  GraphQLString,
} from 'graphql';

import { defaultFor, installBrowserMock } from '.';

type Frame = { type: string; payload?: { data: Record<string, Record<string, unknown>> } };

beforeEach(() => {
  vi.spyOn(console, 'info').mockImplementation(() => {});
  installBrowserMock();
});

afterEach(() => {
  clearMocks();
  delete window.__KSTACK_HOST__;
  window.history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

async function query(q: string, variables: Record<string, unknown> = {}) {
  const res = await invoke<{ status: number; body: string }>('graphql_query', {
    body: JSON.stringify({ query: q, variables }),
  });
  expect(res.status).toBe(200);
  return JSON.parse(res.body) as { data: Record<string, unknown>; errors?: unknown };
}

// Subscribes and collects frames until `count` have arrived.
async function subscribe(q: string, variables: Record<string, unknown>, count: number) {
  const frames: Frame[] = [];
  const channel = new Channel<string>();
  // oxlint-disable-next-line unicorn/prefer-add-event-listener -- Tauri's `Channel` is no `EventTarget`
  channel.onmessage = (raw) => frames.push(JSON.parse(raw) as Frame);
  const id = await invoke<number>('graphql_subscribe', { query: q, variables, channel });
  await vi.waitFor(() => expect(frames).toHaveLength(count));
  return { id, frames };
}

const fieldOf = (frame: Frame, field: string) => frame.payload!.data[field];

describe('installBrowserMock', () => {
  it('answers ready and host commands with nothing', async () => {
    await expect(invoke('ready')).resolves.toBeNull();
    await expect(invoke('log_window_subscribe', {})).resolves.toEqual({ page: 1 });
  });

  it('runs a query over the fixtures, defaulting fields they leave out', async () => {
    const res = await query(`query {
      models { id label provider { id llmDialect } }
      clusters { id spec { name } createdAt deletionRequestedAt }
      cluster(id: "c-staging") { id }
      missing: cluster(id: "nope") { id }
      clusterCaches(clusterID: "c-prod") { id }
      allCaches: clusterCaches { id }
      clusterCache(id: "102") { clusterID }
      missingCache: clusterCache(id: "nope") { id }
      authState { authenticated identity { email } }
      sandboxPathResolved
      sandbox { __typename }
    }`);
    expect(res.errors).toBeUndefined();
    expect(res.data).toMatchObject({
      models: [{ id: 'claude-sonnet-5-5', provider: { id: 'anthropic', llmDialect: null } }, {}],
      cluster: { id: 'c-staging' },
      missing: null,
      clusterCaches: [{ id: '101' }],
      clusterCache: { clusterID: 'c-staging' },
      missingCache: null,
      authState: { authenticated: true },
    });
    expect(res.data.clusters).toHaveLength(3);
    expect(res.data.allCaches).toHaveLength(2);
    const d = res.data as { sandboxPathResolved: boolean };
    expect(d.sandboxPathResolved).toBe(true);
  });

  it.each([
    ['nullable', GraphQLString, null],
    ['a list', new GraphQLNonNull(new GraphQLList(GraphQLString)), []],
    ['an enum', new GraphQLNonNull(new GraphQLEnumType({ name: 'E', values: { First: {}, Second: {} } })), 'First'],
    ['Int', new GraphQLNonNull(GraphQLInt), 0],
    ['Float', new GraphQLNonNull(GraphQLFloat), 0],
    ['Boolean', new GraphQLNonNull(GraphQLBoolean), false],
    ['JSON', new GraphQLNonNull(new GraphQLScalarType({ name: 'JSON' })), {}],
    ['String', new GraphQLNonNull(GraphQLString), ''],
    ['an object', new GraphQLNonNull(new GraphQLObjectType({ name: 'O', fields: { a: { type: GraphQLString } } })), {}],
  ])('defaults %s', (_name, type, want) => {
    expect(defaultFor(type)).toEqual(want);
  });

  it('defaults Time to now', () => {
    vi.useFakeTimers({ now: new Date('2026-01-02T03:04:05Z') });
    try {
      expect(defaultFor(new GraphQLNonNull(new GraphQLScalarType({ name: 'Time' })))).toBe('2026-01-02T03:04:05.000Z');
    } finally {
      vi.useRealTimers();
    }
  });

  it('streams a delta watch snapshot closed by its Bookmark', async () => {
    const { frames } = await subscribe('subscription { clustersWatch { type cluster { id } } }', {}, 5);
    expect(frames[0]).toEqual({ type: 'open' });
    expect(frames.slice(1).map((f) => fieldOf(f, 'clustersWatch').type)).toEqual([
      'Added',
      'Added',
      'Added',
      'Bookmark',
    ]);
  });

  it('streams a kind’s objects with their provenance', async () => {
    const q = `subscription ($id: ClusterID!, $cacheID: ObjectID!, $apiVersion: String!, $resource: String!) {
      clusterCachedDataObjectsWatch(id: $id, cacheID: $cacheID, apiVersion: $apiVersion, resource: $resource) {
        type cacheID resource object { name rawJSON }
      }
    }`;
    const vars = { id: 'c-prod', cacheID: '101', apiVersion: 'apps/v1', resource: 'deployments' };
    const { frames } = await subscribe(q, vars, 8);
    const first = fieldOf(frames[1], 'clusterCachedDataObjectsWatch');
    expect(first).toMatchObject({ type: 'Added', cacheID: '101', resource: 'deployments' });
    expect((first.object as { name: string }).name).toBe('checkout-api');
    expect(fieldOf(frames[7], 'clusterCachedDataObjectsWatch').type).toBe('Bookmark');
  });

  it('closes an unknown kind’s objects watch at once', async () => {
    const q = `subscription ($apiVersion: String!, $resource: String!) {
      clusterCachedDataObjectsWatch(id: "c-prod", cacheID: "101", apiVersion: $apiVersion, resource: $resource) { type }
    }`;
    const { frames } = await subscribe(q, { apiVersion: 'example.com/v1', resource: 'widgets' }, 2);
    expect(fieldOf(frames[1], 'clusterCachedDataObjectsWatch').type).toBe('Bookmark');
  });

  it.each([
    ['clusterCachesWatch { type }', {}, 4],
    ['clusterCacheHealthWatch { cacheID }', {}, 3],
    ['clusterCachedDataKindsWatch(id: "c-prod", cacheID: "101") { type }', {}, 23],
    ['clusterCachedDataEventsWatch(id: "c-prod", cacheID: "101") { type }', {}, 5],
    ['clusterDataRateWatch(id: "c-prod") { intervalSeconds }', {}, 2],
    ['chatsWatch { type }', {}, 6],
    ['authStateWatch { authenticated }', {}, 2],
    ['eventsWatch(id: "101") { type }', {}, 2],
    ['sandboxExecutablesWatch { probing }', {}, 2],
  ])('streams %s', async (selection, vars, count) => {
    const { frames } = await subscribe(`subscription { ${selection} }`, vars, count);
    expect(frames.at(-1)!.type).toBe('next');
  });

  it.each([
    ['chat-1', 4],
    ['none', 2],
  ])('streams chat %s’s messages', async (chatID, count) => {
    const q = 'subscription ($chatID: ChatID!) { chatMessagesWatch(chatID: $chatID) { type } }';
    const { frames } = await subscribe(q, { chatID }, count);
    expect(fieldOf(frames.at(-1)!, 'chatMessagesWatch').type).toBe('Bookmark');
  });

  it('closes a watch with no fixture with a Bookmark, and a gauge with defaults', async () => {
    const watch = await subscribe('subscription { eventsWatch(id: "101") { type } }', {}, 2);
    expect(fieldOf(watch.frames[1], 'eventsWatch')).toEqual({ type: 'Bookmark' });
    const gauge = await subscribe('subscription { sandboxExecutablesWatch { probing probes } }', {}, 2);
    expect(fieldOf(gauge.frames[1], 'sandboxExecutablesWatch')).toEqual({ probing: false, probes: 0 });
  });

  it('sends a log follow nothing past open', async () => {
    const q = 'subscription { logsFollow(id: "c-prod", request: {}) { __typename } }';
    const { frames } = await subscribe(q, {}, 1);
    expect(frames).toEqual([{ type: 'open' }]);
  });

  it('sends nothing to a subscription dropped before its frames', async () => {
    const frames: string[] = [];
    const channel = new Channel<string>();
    // oxlint-disable-next-line unicorn/prefer-add-event-listener -- Tauri's `Channel` is no `EventTarget`
    channel.onmessage = (raw) => frames.push(raw);
    vi.useFakeTimers();
    try {
      const id = await invoke<number>('graphql_subscribe', {
        query: 'subscription { chatsWatch { type } }',
        variables: {},
        channel,
      });
      await invoke('graphql_unsubscribe', { id });
      await vi.runAllTimersAsync();
    } finally {
      vi.useRealTimers();
    }
    expect(frames).toEqual([]);
  });

  it('injects the color scheme the URL names', () => {
    clearMocks();
    window.history.replaceState(null, '', '/?colorScheme=dark');
    installBrowserMock();
    expect(window.__KSTACK_HOST__).toEqual({ colorSchemePreference: 'dark' });
  });

  it('leaves the host file alone without a color scheme', () => {
    expect(window.__KSTACK_HOST__).toBeUndefined();
  });
});
