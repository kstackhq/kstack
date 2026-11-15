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

// A stand-in for the Tauri host and the sidecar, so `pnpm dev` renders real
// screens in a plain browser. Dev builds only: `main.tsx` installs it when no
// Tauri host is present, and a production build never imports it.
//
// Operations run against the sidecar's own schema. Fixtures supply what the
// screens show; every field they leave out resolves to a default for its type,
// so a new field in the schema never breaks the mock.

import type { Channel, InvokeArgs } from '@tauri-apps/api/core';
import { mockIPC, mockWindows } from '@tauri-apps/api/mocks';
import {
  type DocumentNode,
  type FieldNode,
  type GraphQLFieldResolver,
  type GraphQLOutputType,
  GraphQLEnumType,
  GraphQLList,
  GraphQLNonNull,
  GraphQLObjectType,
  GraphQLScalarType,
  buildSchema,
  executeSync,
  getNamedType,
  getOperationAST,
  parse,
} from 'graphql';

import schemaSDL from '../../../sidecar/graph/schema.graphqls?raw';
import * as F from './fixtures';

const schema = buildSchema(schemaSDL);

// The value a field resolves to when no fixture sets it. Exported for tests.
export function defaultFor(type: GraphQLOutputType): unknown {
  if (!(type instanceof GraphQLNonNull)) return null;
  const inner = type.ofType;
  if (inner instanceof GraphQLList) return [];
  if (inner instanceof GraphQLEnumType) return inner.getValues()[0].value;
  if (inner instanceof GraphQLScalarType) {
    switch (inner.name) {
      case 'Int':
      case 'Float':
        return 0;
      case 'Boolean':
        return false;
      case 'Time':
        return new Date().toISOString();
      case 'JSON':
        return {};
      default:
        return '';
    }
  }
  return {};
}

// A fixture's value when it has one (a function is called with the field's
// arguments), the type's default otherwise.
const fieldResolver: GraphQLFieldResolver<unknown, unknown> = (source, args, _ctx, info) => {
  const value = (source as Record<string, unknown> | null)?.[info.fieldName];
  if (typeof value === 'function') return (value as (a: unknown) => unknown)(args);
  return value === undefined ? defaultFor(info.returnType) : value;
};

const clusterByID = ({ id }: { id: string }) => F.CLUSTERS.find((c) => c.id === id) ?? null;
const cacheByID = ({ id }: { id: string }) => F.CACHES.find((c) => c.id === id) ?? null;

const queryRoot = {
  models: F.MODELS,
  clusters: F.CLUSTERS,
  cluster: clusterByID,
  clusterCaches: ({ clusterID }: { clusterID?: string }) =>
    F.CACHES.filter((c) => clusterID == null || c.clusterID === clusterID),
  clusterCache: cacheByID,
  authState: F.AUTH_STATE,
  sandboxPathResolved: true,
};

type Vars = Record<string, string>;

// What a subscription sends after `open`: a list of root values, one per frame.
// A delta watch's snapshot ends in its Bookmark.
const delta = (field: string, records: object[], extra: object = {}) => [
  ...records.map((r) => ({ type: 'Added', [field]: r, ...extra })),
  { type: 'Bookmark', ...extra },
];

const subscriptions: Record<string, (v: Vars) => object[]> = {
  clustersWatch: () => delta('cluster', F.CLUSTERS),
  clusterCachesWatch: () => delta('cache', F.CACHES),
  clusterCacheHealthWatch: () => F.CACHE_HEALTH,
  clusterCachedDataKindsWatch: (v) => delta('kind', F.KINDS, { cacheID: v.cacheID }),
  clusterCachedDataObjectsWatch: (v) =>
    delta('object', F.objectsFor(v.apiVersion, v.resource), {
      cacheID: v.cacheID,
      apiVersion: v.apiVersion,
      resource: v.resource,
    }),
  clusterCachedDataEventsWatch: (v) => delta('event', F.KUBE_EVENTS, { cacheID: v.cacheID }),
  clusterDataRateWatch: () => [F.DATA_RATE],
  chatsWatch: () => delta('chat', F.CHATS),
  chatMessagesWatch: (v) => delta('message', F.MESSAGES[v.chatID] ?? []),
  authStateWatch: () => [F.AUTH_STATE],
  // A follow with no lines sends nothing.
  logsFollow: () => [],
};

// A watch with no fixture still closes its snapshot, so no screen waits forever.
function framesFor(field: string, vars: Vars): object[] {
  const make = subscriptions[field];
  if (make) return make(vars);
  const frameType = schema.getSubscriptionType()!.getFields()[field]?.type;
  const named = frameType && getNamedType(frameType);
  const isWatch = named instanceof GraphQLObjectType && 'type' in named.getFields();
  return [isWatch ? { type: 'Bookmark' } : {}];
}

function run(document: DocumentNode, rootValue: unknown, variableValues: Vars) {
  return executeSync({ schema, document, rootValue, variableValues, fieldResolver });
}

function rootField(document: DocumentNode): string {
  return (getOperationAST(document)!.selectionSet.selections[0] as FieldNode).name.value;
}

let nextSubID = 1;
const liveSubs = new Set<number>();

function subscribe(query: string, variables: Vars, channel: Channel<string>) {
  const id = nextSubID++;
  liveSubs.add(id);
  const document = parse(query);
  const field = rootField(document);
  const send = (msg: object) => {
    if (liveSubs.has(id)) channel.onmessage(JSON.stringify(msg));
  };
  // After the ack, as the host's frames would be.
  setTimeout(() => {
    send({ type: 'open' });
    for (const frame of framesFor(field, variables)) {
      send({ type: 'next', payload: run(document, { [field]: frame }, variables) });
    }
  }, 0);
  return id;
}

function handle(cmd: string, payload?: InvokeArgs): unknown {
  const args = payload as Record<string, unknown>;
  switch (cmd) {
    case 'graphql_query': {
      const { query, variables = {} } = JSON.parse(args.body as string) as { query: string; variables?: Vars };
      const result = run(parse(query), queryRoot, variables);
      return { status: 200, body: JSON.stringify(result) };
    }
    case 'graphql_subscribe':
      return subscribe(args.query as string, args.variables as Vars, args.channel as Channel<string>);
    case 'graphql_unsubscribe':
      liveSubs.delete(args.id as number);
      return null;
    case 'log_window_subscribe':
      // No log windows open.
      return { page: 1 };
    default:
      // `ready`, window controls, log windows, the error forwarder: nothing to do.
      return null;
  }
}

export function installBrowserMock() {
  mockWindows('main');
  mockIPC(handle, { shouldMockEvents: true });
  // `?colorScheme=dark` (or `light`) stands in for the host's injected
  // `host.json`, so side-by-side frames can each hold a scheme.
  const colorSchemePreference = new URLSearchParams(window.location.search).get('colorScheme');
  if (colorSchemePreference) window.__KSTACK_HOST__ = { colorSchemePreference };
  console.info('[kstack] No Tauri host: serving sample data from the browser mock.');
}
