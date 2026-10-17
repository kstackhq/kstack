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

// The one place the app makes a log `Client`: `logsFetch` and `logsFollow` over the app's
// urql client, for one view of one cluster. Operation errors reach the error bus through
// `errorReportExchange`; a fetch's also rejects, and a follow's calls `onFailed`, for the
// window to see.

import type { Client as UrqlClient } from 'urql';

import { graphql } from '@/gql';
import type { LogsFetchQuery as LogsFetchResult, LogsViewInput } from '@/gql/graphql';
import type { Client, Cursor, FollowFrame, LogLine } from '@/lib/log-viewer';
import type { LogsViewAction } from '@/lib/logs-view';

const LogsFetchQuery = graphql(`
  query LogsFetch(
    $id: ClusterID!
    $view: LogsViewInput!
    $mode: LogsFetchMode!
    $cursor: String
    $at: Time
    $limit: Int!
  ) {
    logsFetch(id: $id, view: $view, mode: $mode, cursor: $cursor, at: $at, limit: $limit) {
      records {
        timestamp
        message
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
      }
      before {
        cursor
        more
      }
      after {
        cursor
        more
      }
      anchor
      skipped
    }
  }
`);

const LogsFollowSubscription = graphql(`
  subscription LogsFollow($id: ClusterID!, $view: LogsViewInput!, $after: String) {
    logsFollow(id: $id, view: $view, after: $after) {
      type
      record {
        timestamp
        message
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
      }
      cursor
      reason
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
type LogRecord = LogsFetchResult['logsFetch']['records'][number];

function lineOf({ timestamp, message, cursor, stream, place }: LogRecord): LogLine {
  return {
    timestamp,
    message,
    cursor,
    stream: { namespace: stream.namespace, pod: stream.pod, container: stream.container, previous: stream.previous },
    place: { node: place.node, region: place.region, zone: place.zone, os: place.os, arch: place.arch },
  };
}

export function createLogsClient(urql: UrqlClient, clusterId: string, action: LogsViewAction): Client {
  const view = viewInput(action);
  return {
    fetch: async ({ mode, cursor, at, limit }) => {
      const result = await urql
        .query(LogsFetchQuery, { id: clusterId, view, mode, cursor, at, limit }, { requestPolicy: 'network-only' })
        .toPromise();
      if (result.error) throw result.error;
      if (!result.data) throw new Error('logsFetch answered no page');
      const { records, before, after, anchor, skipped } = result.data.logsFetch;
      return {
        lines: records.map(lineOf),
        before: { cursor: before.cursor ?? null, more: before.more },
        after: { cursor: after.cursor ?? null, more: after.more },
        anchor: anchor ?? null,
        skipped,
      };
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
