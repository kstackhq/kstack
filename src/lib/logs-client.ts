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

// The one place the app makes a log `Client`. It answers the fake until the sidecar
// serves the wire: a log under the view's own pods, so every rule of the viewer shows on
// screen, with the panel's header saying the lines are invented.

import type { Client } from '@/lib/log-viewer';
import { FakeClient, type FakeStream } from '@/lib/log-viewer/fake-client';
import type { LogsViewAction } from '@/lib/logs-view';

export type LogsClient = {
  client: Client;
  /** Starts what the client does on its own, and answers its stop. The viewer's mount calls it. */
  start: () => () => void;
};

/** Nearly three hours of history, so a moment an hour ago lands with lines on both sides. */
const HISTORY_LINES = 5000;
const SPACING_MS = 2000;
/** Two lines a second. */
const GROWTH_MS = 500;
/** Long enough to watch a page arrive: the loading overlay and the edge rows' *Loading…* stay on screen. */
export const DELAY_MS = 2000;

// `clusterId` is the real client's; the fake reads only the action.
export function createLogsClient(_clusterId: string, action: LogsViewAction): LogsClient {
  const streams: FakeStream[] = action.sources.flatMap((source, i) =>
    (source.containers.length > 0 ? source.containers : ['app']).map((container) => ({
      stream: { namespace: source.namespace, pod: source.name, container, previous: source.previous },
      place: { node: `fake-node-${i}`, region: '', zone: '', os: 'linux', arch: 'amd64' },
    })),
  );
  const fake = new FakeClient({ lines: HISTORY_LINES, spacingMs: SPACING_MS, delayMs: DELAY_MS, streams });
  return {
    client: fake,
    start: () => {
      const timer = setInterval(() => fake.append(1), GROWTH_MS);
      return () => clearInterval(timer);
    },
  };
}
