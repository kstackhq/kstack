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

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { FollowFrame } from '@/lib/log-viewer';
import type { LogsViewAction } from '@/lib/logs-view';
import { mockTauriCore } from '@/test-utils';

const { invokeMock, channels, channelFor, factory } = mockTauriCore();
vi.mock('@tauri-apps/api/core', () => factory());

const { reportError } = vi.hoisted(() => ({ reportError: vi.fn() }));
vi.mock('@/lib/error-bus', () => ({ reportError, errorMessage: String, toError: (e: unknown) => e }));

const { createGraphqlClient } = await import('@/lib/graphql/client');
const { createLogsClient } = await import('./logs-client');

// The action as the chat's query hands it over, `__typename`s and all.
const action = {
  __typename: 'LogsViewAction',
  sources: [
    {
      __typename: 'LogsViewSource',
      namespace: 'prod',
      kind: 'Deployment',
      name: 'webapp',
      containers: ['app'],
      allContainers: false,
      previous: false,
    },
  ],
  filters: [{ __typename: 'LogsViewFilter', field: 'Node', values: ['node-a'] }],
  grep: 'error',
  anchor: { __typename: 'LogsViewAnchor', kind: 'Tail', at: null },
  pinToEnd: true,
} as unknown as LogsViewAction;

const VIEW = {
  sources: [
    {
      namespace: 'prod',
      kind: 'Deployment',
      name: 'webapp',
      containers: ['app'],
      allContainers: false,
      previous: false,
    },
  ],
  filters: [{ field: 'Node', values: ['node-a'] }],
  grep: 'error',
};

// Line 2 carries a hole before it.
const hole = { after: '2026-10-17T11:00:00Z', before: '2026-10-17T12:00:02Z' };

const record = (i: number) => ({
  __typename: 'LogRecord',
  timestamp: `2026-10-17T12:00:0${i}Z`,
  message: `line ${i}`,
  cursor: `c${i}`,
  stream: { __typename: 'LogStream', namespace: 'prod', pod: 'webapp-1', container: 'app', previous: false },
  place: { __typename: 'LogPlace', node: 'node-a', region: 'r', zone: 'z', os: 'linux', arch: 'amd64' },
  gap: i === 2 ? { __typename: 'LogGap', ...hole } : null,
});

const line = (i: number) => ({
  timestamp: `2026-10-17T12:00:0${i}Z`,
  message: `line ${i}`,
  cursor: `c${i}`,
  stream: { namespace: 'prod', pod: 'webapp-1', container: 'app', previous: false },
  place: { node: 'node-a', region: 'r', zone: 'z', os: 'linux', arch: 'amd64' },
  gap: i === 2 ? hole : null,
});

const failed = vi.fn();

const dialed = () =>
  invokeMock.mock.calls
    .filter(([cmd]) => cmd === 'graphql_subscribe')
    .map(([, arg]) => (arg as { variables: unknown }).variables);

const push = (msg: object) => channelFor('logsFollow').onmessage!(JSON.stringify(msg));
const frame = (logsFollow: object) => push({ type: 'next', payload: { data: { logsFollow } } });

// The async `graphql_subscribe` ack, without advancing fake timers.
const flush = async () => {
  await Promise.resolve();
  await Promise.resolve();
};

beforeEach(() => {
  let id = 0;
  invokeMock.mockReset();
  channels.length = 0;
  invokeMock.mockImplementation(async (cmd: string) => {
    if (cmd === 'graphql_subscribe') {
      id += 1;
      return id;
    }
    return undefined;
  });
  reportError.mockReset();
  failed.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('createLogsClient', () => {
  it('reads no page until the log fetch API is built', async () => {
    await expect(
      createLogsClient(createGraphqlClient(), '7', action).fetch({ mode: 'Head', cursor: null, at: null, limit: 10 }),
    ).rejects.toThrow('not implemented');
  });

  it('follows from the cursor, handing on lines and checkpoints', async () => {
    const logs = createLogsClient(createGraphqlClient(), 'cluster-1', action);
    const frames: FollowFrame[] = [];
    const stop = logs.follow('live', (f) => frames.push(f), failed);
    await flush();
    expect(dialed()).toEqual([{ id: 'cluster-1', view: VIEW, after: 'live' }]);

    push({ type: 'open' });
    frame({ __typename: 'LogsFollowFrame', type: 'Line', record: record(3), cursor: null, reason: '' });
    frame({ __typename: 'LogsFollowFrame', type: 'Checkpoint', record: null, cursor: 'k1', reason: '' });
    expect(frames).toEqual([
      { kind: 'Line', line: line(3) },
      { kind: 'Checkpoint', cursor: 'k1' },
    ]);
    stop();
  });

  it('reconnects a watch that died from the last checkpoint, not the last line', async () => {
    vi.useFakeTimers();
    const logs = createLogsClient(createGraphqlClient(), 'cluster-1', action);
    const stop = logs.follow('live', () => {}, failed);
    await flush();
    push({ type: 'open' });
    frame({ type: 'Checkpoint', record: null, cursor: 'k1', reason: '' });
    frame({ type: 'Line', record: record(4), cursor: null, reason: '' });

    // A watch that dies once open is the exchange's to reconnect, not a failed follow.
    push({ type: 'next', payload: { errors: [{ message: 'tailer lost' }], extensions: { watchFailed: true } } });
    await vi.advanceTimersByTimeAsync(1_000);
    expect(dialed().at(-1)).toEqual({ id: 'cluster-1', view: VIEW, after: 'k1' });
    expect(failed).not.toHaveBeenCalled();
    stop();
  });

  it('hands on `Ended` and never follows again', async () => {
    vi.useFakeTimers();
    const logs = createLogsClient(createGraphqlClient(), 'cluster-1', action);
    const frames: FollowFrame[] = [];
    logs.follow('live', (f) => frames.push(f), failed);
    await flush();
    push({ type: 'open' });
    frame({ type: 'Ended', record: null, cursor: null, reason: 'the cluster was removed' });
    push({ type: 'complete' });
    await vi.advanceTimersByTimeAsync(60_000);

    expect(frames).toEqual([{ kind: 'Ended', reason: 'the cluster was removed' }]);
    expect(dialed()).toHaveLength(1);
    expect(invokeMock).toHaveBeenCalledWith('graphql_unsubscribe', { id: 1 });
  });

  it('fails a follow the sidecar could not open, in its words, and never follows again', async () => {
    vi.useFakeTimers();
    const logs = createLogsClient(createGraphqlClient(), 'cluster-1', action);
    const frames: FollowFrame[] = [];
    logs.follow('live', (f) => frames.push(f), failed);
    await flush();
    push({ type: 'open' });
    push({ type: 'next', payload: { data: null, errors: [{ message: 'the cluster has no cache' }] } });
    push({ type: 'complete' });
    await vi.advanceTimersByTimeAsync(60_000);

    expect(failed).toHaveBeenCalledOnce();
    expect(failed.mock.calls[0][0]).toEqual(new Error('the cluster has no cache'));
    expect(frames).toEqual([]);
    expect(dialed()).toHaveLength(1);
  });

  it('stops the subscription when the follow is stopped', async () => {
    const logs = createLogsClient(createGraphqlClient(), 'cluster-1', action);
    const frames: FollowFrame[] = [];
    const stop = logs.follow('live', (f) => frames.push(f), failed);
    await flush();
    stop();
    expect(invokeMock).toHaveBeenCalledWith('graphql_unsubscribe', { id: 1 });
    frame({ type: 'Line', record: record(5), cursor: null, reason: '' });
    expect(frames).toEqual([]);
  });
});
