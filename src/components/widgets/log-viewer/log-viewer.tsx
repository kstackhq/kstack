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

// The log viewer: the library's core behind a view's props. It builds the view's client
// once per mount (the caller keys it on the call's id, so a new call is a fresh landing
// and a re-render of the same call moves nothing), lands at `openAt`, starts pinned as
// `pinToEnd` says, draws each line as a row, and writes what is on screen into
// `viewport` for whoever pulls it.
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from 'react';

import { Button } from '@kubetail/ui/elements/button';
import { Spinner } from '@kubetail/ui/elements/spinner';

import { VisibleText } from '@/components/widgets/visible-text';
import { reportError } from '@/lib/error-bus';
import {
  type Client,
  type CoreHandle,
  type CoreRows,
  type Edge,
  type EdgeState,
  type LogLine,
  LogViewerCore,
  createCoreStore,
} from '@/lib/log-viewer';
import { createLogsClient } from '@/lib/logs-client';
import type { LogsViewAction } from '@/lib/logs-view';
import { EMPTY_LOG_VIEWPORT, type LogViewportHandle } from '@/lib/log-viewport';
import { usePersistedFlag } from '@/lib/persisted-flag';
import { useMeasureWidths } from './widths';
import { LOG_ROW_H, LOG_ROW_PX, LogViewerRow } from './row';
import { LogViewerToolbar } from './toolbar';
import { useMessageWidth } from './message-width';
import { LogViewerHeader } from './header';
import { type Column, useLogViewerColumns } from './columns';

export type LogViewerProps = {
  /** The view's cluster, which the client reads. */
  clusterId: string;
  query: LogsViewAction;
  /** Where the viewer lands on mount. */
  openAt: LogsViewAction['anchor'];
  /** Whether it starts pinned to the end. */
  pinToEnd: boolean;
  viewport: LogViewportHandle;
};

/** The advance of one `text-xs` mono character, for the wrap estimate; the measure corrects it. */
const CHAR_PX = 7.2;

const oneLine = () => LOG_ROW_PX;

const measureHeight = (el: Element) => el.getBoundingClientRect().height;

const PAGE_FAILED = 'Could not load more lines.';

// The client as the core sees it, recording the last failure for the viewer to draw: the
// core's `onError` reports once per run of failures, never says what failed, and nothing
// reports a success. A follow's error is thrown on, so the core still unpins.
function watchClient(client: Client, record: (error: string | null) => void): Client {
  return {
    fetch: (query) =>
      client.fetch(query).then(
        (page) => {
          record(null);
          return page;
        },
        (error: unknown) => {
          record(PAGE_FAILED);
          throw error;
        },
      ),
    follow: (after, onFrame) => {
      try {
        const stop = client.follow(after, onFrame);
        record(null);
        return stop;
      } catch (error) {
        record(error instanceof Error ? error.message : String(error));
        throw error;
      }
    },
  };
}

const onError = (error: unknown) =>
  reportError({
    source: 'network',
    message: `log viewer: ${error instanceof Error ? error.message : String(error)}`,
    cause: error,
  });

// Drawn while that edge has more. The core pages again only on the user's next scroll, so
// after an empty page — a read that spent its budget — the button reads on by hand.
function EdgeRow({
  edge,
  state,
  top,
  loadMore,
}: {
  edge: Edge;
  state: EdgeState;
  top: number;
  loadMore: (edge: Edge) => void;
}) {
  let body = null;
  if (state.loading) {
    body = 'Loading…';
  } else if (state.empty) {
    body = (
      <>
        No more in the last read
        <Button variant="link" size="sm" className="h-auto p-0 text-xs" onClick={() => loadMore(edge)}>
          Keep looking
        </Button>
      </>
    );
  }
  return (
    <div
      className={`${LOG_ROW_H} absolute inset-x-0 flex items-center gap-2 px-3 text-xs text-muted-foreground`}
      style={{ top }}
    >
      {body}
    </div>
  );
}

type RowsProps = {
  rows: CoreRows;
  columns: Column[];
  widths: ReturnType<typeof useMeasureWidths>;
  wrap: boolean;
  onRetry: () => void;
};

function Rows({ rows, columns, widths, wrap, onRetry }: RowsProps) {
  let status = null;
  if (rows.loading) {
    status = <Spinner size="sm" />;
  } else if (rows.failed) {
    status = (
      <>
        <p>Could not load lines.</p>
        <Button variant="outline" size="sm" onClick={onRetry}>
          Try again
        </Button>
      </>
    );
  } else if (rows.count === 0) {
    status = <p>No lines.</p>;
  }
  return (
    <div
      className="relative"
      style={{ height: rows.totalSize, minWidth: wrap ? undefined : widths.rowWidth || '100%' }}
    >
      {status && (
        <div className="absolute inset-x-0 top-0 flex flex-col items-center gap-2 p-4 text-sm text-muted-foreground">
          {status}
        </div>
      )}
      {rows.before.more && <EdgeRow edge="before" state={rows.before} top={0} loadMore={rows.loadMore} />}
      {rows.rows.map((row) => (
        <LogViewerRow
          key={row.key}
          line={row.line}
          rowKey={row.key}
          columns={columns}
          widths={widths.widths}
          rowWidth={widths.rowWidth}
          wrap={wrap}
          top={row.start}
          index={row.index}
          measureCell={widths.measureCell}
          measureRow={widths.measureRow}
          measureElement={wrap ? rows.measureElement : undefined}
        />
      ))}
      {rows.after.more && (
        <EdgeRow edge="after" state={rows.after} top={rows.totalSize - rows.edgeRowHeight} loadMore={rows.loadMore} />
      )}
    </div>
  );
}

export function LogViewer({ clusterId, query, openAt, pinToEnd, viewport }: LogViewerProps) {
  // Built once per mount. What the client runs on its own starts in the effect, so the
  // copy StrictMode throws away never starts.
  const [logs] = useState(() => createLogsClient(clusterId, query));
  const [error, setError] = useState<string | null>(null);
  const [client] = useState(() => watchClient(logs.client, setError));
  useEffect(() => logs.start(), [logs]);

  const [store] = useState(createCoreStore);
  const state = useSyncExternalStore(store.subscribe, store.getSnapshot);
  const core = useRef<CoreHandle>(null);
  const [columns, setColumns] = useLogViewerColumns();
  const widths = useMeasureWidths();
  const [wrap, persistWrap] = usePersistedFlag('log-viewer-wrap', false);
  // Chrome like the pin: a new view starts playing, which the mount's key gives for free.
  const [paused, setPaused] = useState(false);
  // Each landing mounts its own scroller; the header and the message width follow whichever is mounted.
  const [scroller, setScroller] = useState<HTMLDivElement | null>(null);

  const messageWidth = useMessageWidth(scroller, widths.widths, columns);
  const wrapped = useCallback(
    (line: LogLine) => Math.max(1, Math.ceil((line.message.length * CHAR_PX) / messageWidth)) * LOG_ROW_PX,
    [messageWidth],
  );
  // Flipping wrap changes every row's height. The core measures again in the same commit,
  // holding the reader's row as it was before the flip.
  const setWrap = (on: boolean) => {
    persistWrap(on);
    core.current?.measure();
  };

  // A closed view leaves no range behind for the composer to pull.
  useEffect(() => {
    const write = () => {
      const { range, atEnd, pinnedToEnd, visible } = store.getSnapshot();
      viewport.set({ range, atEnd, pinnedToEnd, visible, selection: [] });
    };
    write();
    const unsubscribe = store.subscribe(write);
    return () => {
      unsubscribe();
      viewport.set(EMPTY_LOG_VIEWPORT);
    };
  }, [store, viewport]);

  const muted = 'border-b px-3 py-1 text-xs text-muted-foreground';
  return (
    <>
      <LogViewerToolbar
        loading={state.loading}
        paused={paused}
        columns={columns}
        wrap={wrap}
        onStart={() => core.current?.jumpTo({ kind: 'Head', at: null })}
        onEnd={() => core.current?.jumpTo({ kind: 'Tail', at: null })}
        onPausedChange={setPaused}
        onColumnsChange={setColumns}
        onWrapChange={setWrap}
      />
      {state.skipped.length > 0 && (
        <p className={muted}>
          Not read:{' '}
          {state.skipped.map((stream, i) => (
            <span key={stream}>
              {i > 0 && ', '}
              <VisibleText text={stream} />
            </span>
          ))}
        </p>
      )}
      {state.ended !== null && (
        <p className={muted}>
          No new lines: <VisibleText text={state.ended} />
        </p>
      )}
      {/* A failed landing says so over the rows, and the landing Try again starts shows the spinner alone. */}
      {error !== null && !state.loading && !state.failed && (
        <p className={muted}>
          <VisibleText text={error} />
        </p>
      )}
      <LogViewerHeader
        columns={columns}
        widths={widths.widths}
        rowWidth={widths.rowWidth}
        wrap={wrap}
        scroller={scroller}
        measureCell={widths.measureCell}
      />
      {/* `relative` anchors Jump to now over the bottom of the lines. */}
      <div className="relative flex min-h-0 flex-1 flex-col">
        <LogViewerCore
          ref={core}
          scrollerRef={setScroller}
          client={client}
          anchor={openAt}
          pinToEnd={pinToEnd}
          paused={paused}
          store={store}
          firstPageSize={300}
          pageSize={250}
          edgeRowHeight={LOG_ROW_PX}
          estimateRowHeight={wrap ? wrapped : oneLine}
          measureElement={wrap ? measureHeight : undefined}
          onError={onError}
          className="min-h-0 flex-1"
        >
          {(rows) => (
            <Rows rows={rows} columns={columns} widths={widths} wrap={wrap} onRetry={() => core.current?.retry()} />
          )}
        </LogViewerCore>
        {/* Unpinned, the follow is closed and nothing knows whether lines arrived, so the button
          says where the live end is, not that anything is new. */}
        {!state.loading && !state.failed && !state.pinnedToEnd && (
          <Button
            variant="secondary"
            size="sm"
            className="absolute bottom-3 left-1/2 -translate-x-1/2 shadow"
            onClick={() => core.current?.jumpTo({ kind: 'Tail', at: null })}
          >
            Jump to now
          </Button>
        )}
      </div>
    </>
  );
}
