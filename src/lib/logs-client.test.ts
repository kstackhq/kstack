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

import type { LogsViewAction } from '@/lib/logs-view';
import { DELAY_MS, createLogsClient } from './logs-client';

const NOW = Date.parse('2026-10-15T12:00:00.000Z');

const action = (sources: LogsViewAction['sources']): LogsViewAction => ({
  sources,
  filters: [],
  grep: '',
  anchor: { kind: 'Tail', at: null },
  pinToEnd: true,
});

const source = (over: Partial<LogsViewAction['sources'][number]>): LogsViewAction['sources'][number] => ({
  namespace: 'prod',
  kind: 'Deployment',
  name: 'webapp',
  containers: [],
  allContainers: false,
  previous: false,
  ...over,
});

// The newest page, with the fake's latency run out.
async function newest(logs: ReturnType<typeof createLogsClient>, limit = 300) {
  const pending = logs.client.fetch({ mode: 'Tail', cursor: null, at: null, limit });
  await vi.advanceTimersByTimeAsync(DELAY_MS);
  return pending;
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
});

afterEach(() => {
  vi.useRealTimers();
});

describe('createLogsClient', () => {
  it('writes one stream per source and container, in the source’s namespace under its name', async () => {
    const logs = createLogsClient(
      'cluster-1',
      action([
        source({ name: 'webapp', containers: ['app', 'istio-proxy'] }),
        source({ namespace: 'jobs', kind: 'Pod', name: 'db-0', previous: true }),
      ]),
    );
    const page = await newest(logs);
    const streams = new Set(page.lines.map(({ stream }) => JSON.stringify(stream)));
    expect([...streams].map((s) => JSON.parse(s))).toEqual(
      expect.arrayContaining([
        { namespace: 'prod', pod: 'webapp', container: 'app', previous: false },
        { namespace: 'prod', pod: 'webapp', container: 'istio-proxy', previous: false },
        { namespace: 'jobs', pod: 'db-0', container: 'app', previous: true },
      ]),
    );
    expect(streams.size).toBe(3);
    expect(page.lines.every(({ place }) => place.node !== '')).toBe(true);
  });

  it('answers nearly three hours of history ending now', async () => {
    const logs = createLogsClient('cluster-1', action([source({})]));
    const page = await newest(logs, 1);
    expect(page.lines[0].timestamp).toBe('2026-10-15T12:00:00.000Z');
    expect(page.before.more).toBe(true);

    const pending = logs.client.fetch({ mode: 'Head', cursor: null, at: null, limit: 1 });
    await vi.advanceTimersByTimeAsync(DELAY_MS);
    expect((await pending).lines[0].timestamp).toBe('2026-10-15T09:13:22.000Z');
  });

  it('grows at two lines a second from start until its stop, and not before', async () => {
    const logs = createLogsClient('cluster-1', action([source({})]));
    const last = async () => (await newest(logs, 1)).lines[0].cursor;
    const before = await last();
    await vi.advanceTimersByTimeAsync(2000);
    expect(await last()).toBe(before);

    const stop = logs.start();
    await vi.advanceTimersByTimeAsync(1000);
    const grown = await newest(logs, 10);
    expect(grown.lines.findIndex((line) => line.cursor === before)).toBe(grown.lines.length - 3);

    stop();
    const stopped = await last();
    await vi.advanceTimersByTimeAsync(2000);
    expect(await last()).toBe(stopped);
  });
});
