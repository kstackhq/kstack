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

// The one place the app makes a log `Client`: `logsFollow` over the app's urql client, for
// one view of one cluster. Operation errors reach the error bus through
// `errorReportExchange`; a follow's also calls `onFailed`, for the window to see. A page is
// read on the log fetch API, which is not built yet (docs/specs/log-fetch-api).

import type { Client as UrqlClient } from 'urql';

import { graphql } from '@/gql';
import type { LogsFollowSubscription as LogsFollowResult, LogsViewInput } from '@/gql/graphql';
import type { Client, Cursor, FollowFrame, LogLine } from '@/lib/log-viewer';
import type { LogsViewAction } from '@/lib/logs-view';

// The viewer keeps its own request, so a page's echo of it is not selected.
const LogsFetchQuery = graphql(`
  query LogsFetch($id: ClusterID!, $request: LogsFetchInput!) {
    logsFetch(id: $id, request: $request) {
      records {
        timestamp
        message
        truncatedBytes
        cursor
        stream {
          namespace
          pod
          container
          previous
        }
        place {
          node
          region
          zone
          os
          arch
        }
        gap {
          after
          before
        }
      }
      page {
        prev
        next
        follow
      }
      notices {
        code
        message
      }
    }
  }
`);

const LogsFollowSubscription = graphql(`
  subscription LogsFollow($id: ClusterID!, $request: LogsFollowInput!) {
    logsFollow(id: $id, request: $request) {
      type
      record {
        timestamp
        message
        truncatedBytes
        cursor
        stream {
          namespace
          pod
          container
          previous
        }
        place {
          node
          region
          zone
          os
          arch
        }
        gap {
          after
          before
        }
      }
      notice {
        code
        message
      }
      ended {
        code
        message
      }
    }
  }
`);

// Field by field, since the action came off a query and carries `__typename`s an input refuses.
export function viewInput({ sources, filters, grep }: LogsViewAction): LogsViewInput {
  return {
    sources: sources.map(({ namespace, kind, name, containers, allContainers, previous }) => ({
      namespace,
      kind,
      name,
      containers,
      allContainers,
      previous,
    })),
    filters: filters.map(({ field, values }) => ({ field, values })),
    grep,
  };
}

// Field by field for the same reason: the library's types are the wire's without `__typename`.
type LogRecord = NonNullable<LogsFollowResult['logsFollow']['record']>;

function lineOf({ timestamp, message, cursor, stream, place, gap }: LogRecord): LogLine {
  return {
    timestamp,
    message,
    cursor,
    stream: { namespace: stream.namespace, pod: stream.pod, container: stream.container, previous: stream.previous },
    place: { node: place.node, region: place.region, zone: place.zone, os: place.os, arch: place.arch },
    gap: gap ? { after: gap.after, before: gap.before } : null,
  };
}

export function createLogsClient(urql: UrqlClient, clusterId: string, action: LogsViewAction): Client {
  const view = viewInput(action);
  return {
    fetch: async () => {
      throw new Error('not implemented');
    },

    follow: (after: Cursor, onFrame: (frame: FollowFrame) => void, onFailed: (error: unknown) => void) => {
      // A reconnect goes on from the last checkpoint: a line's cursor would skip the other
      // streams' unsent lines older than it. What it sends again the window drops by cursor.
      let resumeFrom = after;
      let ended = false;
      const subscription = urql
        .subscription(
          LogsFollowSubscription,
          { id: clusterId, view, after },
          { reconnectVariables: () => ({ id: clusterId, view, after: resumeFrom }) },
        )
        .subscribe(({ data, error }) => {
          if (ended) return;
          const frame = data?.logsFollow;
          if (!frame) {
            // A follow the sidecar could not open answers errors and no frame, then completes.
            // Stop here, before the reconnect the `complete` brings: the window reopens it
            // from a page. A watch that dies once open is the exchange's to reconnect.
            if (error) {
              ended = true;
              subscription.unsubscribe();
              onFailed(new Error(error.graphQLErrors[0]?.message ?? error.message));
            }
            return;
          }
          if (frame.type === 'Line') {
            if (frame.record) onFrame({ kind: 'Line', line: lineOf(frame.record) });
          } else if (frame.type === 'Checkpoint') {
            if (frame.cursor) {
              resumeFrom = frame.cursor;
              onFrame({ kind: 'Checkpoint', cursor: frame.cursor });
            }
          } else {
            // Following again would end the same way, so the reconnect the server's
            // `complete` brings must not happen.
            ended = true;
            onFrame({ kind: 'Ended', reason: frame.reason });
            subscription.unsubscribe();
          }
        });
      return () => {
        ended = true;
        subscription.unsubscribe();
      };
    },
  };
}
