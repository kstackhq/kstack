// Copyright 2026 The Kstack Authors
// Copyright 2024 The Kubetail Authors
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
// The headless log viewer: it keeps the lines, lands at an anchor, pages at either
// edge as the user scrolls, and hands its rows to a render function. It draws nothing and knows nothing of the app:
// lines come from a `Client`, errors go to `onError`, and what it knows is
// published through its handle.
import { useCallback, useEffect, useEffectEvent, useImperativeHandle, useLayoutEffect, useRef, useState } from 'react';
import type { ReactNode, Ref, RefObject } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import type { Virtualizer } from '@tanstack/react-virtual';

import { useBeforePaint } from './before-paint';
import { cursorNow } from './cursor';
import { Lines } from './lines';
import type { Edge } from './lines';
import { createStore } from './store';
import type { Anchor, Client, FetchResult, LogLine } from './types';

/** What the core publishes through its handle. */
export type CoreState = {
  loading: boolean;
  /** Whether the landing failed, which leaves the viewer with nothing to page from. `jumpTo` lands again. */
  failed: boolean;
  /** Whether the newest line is on screen: loaded to the end, with nothing more after. */
  atEnd: boolean;
  /** Timestamps of the first and last visible lines; null before any is drawn. */
  range: { from: string; to: string } | null;
  /** How many lines are on screen. */
  visible: number;
  /** The last page's lines about streams it could not read. */
  skipped: string[];
};

export const CORE_INITIAL_STATE: CoreState = {
  loading: true,
  failed: false,
  atEnd: false,
  range: null,
  visible: 0,
  skipped: [],
};

export type CoreRow = {
  key: number;
  index: number;
  size: number;
  start: number;
  line: LogLine;
};

/** What one edge of the window says about the log past it. */
export type EdgeState = {
  /** Whether there are more lines that way. */
  more: boolean;
  /** Whether the last page that way held no line but said there are more. */
  empty: boolean;
};

/** What the render function is handed. */
export type CoreRows = {
  loading: boolean;
  /** As `CoreState.failed`. */
  failed: boolean;
  before: EdgeState;
  after: EdgeState;
  /** The height to draw an edge row at while `more` is set; the core lays rows out below it. */
  edgeRowHeight: number;
  /** The total height of the rows and the edge rows, for the sizer. */
  totalSize: number;
  /** The rows to draw. */
  rows: CoreRow[];
  count: number;
  lineAt: (key: number) => LogLine | undefined;
  keyAt: (index: number) => number | undefined;
  /** -1 for a key not held. */
  indexOfKey: (key: number) => number;
  measureElement: (node: Element | null) => void;
  /**
   * Reads the next page toward `edge` now, for an edge row's button: with no row
   * drawn after an empty page, there is no scroll to read on at.
   */
  loadMore: (edge: Edge) => void;
};

export type CoreHandle = {
  /** Lands again at `anchor`. */
  jumpTo: (anchor: Anchor) => void;
  /** Re-measures every row, keeping the reader's place. */
  measure: () => void;
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => CoreState;
};

type Settings = {
  estimateRowHeight: (line: LogLine) => number;
  measureElement?: (
    element: Element,
    entry: ResizeObserverEntry | undefined,
    instance: Virtualizer<HTMLDivElement, Element>,
  ) => number;
  overscan: number;
  firstPageSize: number;
  pageSize: number;
  /** Pages toward an edge once at most this many rows lie between the visible range and it. */
  loadMoreThreshold: number;
  edgeRowHeight: number;
};

type VisibleRange = { startIndex: number; endIndex: number };

const NO_MORE: EdgeState = { more: false, empty: false };
const NONE_SKIPPED: string[] = [];

// Frames ------------------------------------------------------------------

/** The longest work held for the next frame waits when no frame comes: a minimized window gets none. */
export const HIDDEN_FLUSH_MS = 250;

/** Calls `callback` on the next frame or after `HIDDEN_FLUSH_MS`, whichever is first; answers a cancel. */
function onNextFrame(callback: () => void): () => void {
  let frame: number;
  let timer: ReturnType<typeof setTimeout>;
  const cancel = () => {
    cancelAnimationFrame(frame);
    clearTimeout(timer);
  };
  frame = requestAnimationFrame(() => {
    cancel();
    callback();
  });
  timer = setTimeout(() => {
    cancel();
    callback();
  }, HIDDEN_FLUSH_MS);
  return cancel;
}

// Paging ------------------------------------------------------------------
//
// Reads toward an edge once at most `threshold` rows lie between the visible
// range and it. It runs when the range moves, never because an edge changed: an empty page
// with a cursor leaves the range where it was, so reading on waits for the user's
// next scroll or `loadMore`.

type LoadMoreOptions = {
  range: VisibleRange | null;
  count: number;
  /** Nothing pages while the landing is still placing the rows. */
  settled: boolean;
  edges: Record<Edge, EdgeState>;
  load: Record<Edge, () => Promise<void>>;
  threshold: number;
  onError: (error: unknown) => void;
};

/** Answers `loadMore(edge)`: a page toward `edge` now, if there is one to read and none in flight. */
function useLoadMore({ range, count, settled, edges, load, threshold, onError }: LoadMoreOptions) {
  const inFlightRef = useRef<Record<Edge, boolean>>({ before: false, after: false });

  const loadMore = (edge: Edge) => {
    if (!settled || !edges[edge].more || inFlightRef.current[edge]) return;
    inFlightRef.current[edge] = true;
    load[edge]()
      .catch(onError)
      .finally(() => {
        // Held until the page has painted, so the range it moves does not ask again.
        onNextFrame(() => {
          inFlightRef.current[edge] = false;
        });
      });
  };

  const onRange = useEffectEvent(() => {
    if (!range) return;
    if (range.startIndex <= threshold) loadMore('before');
    if (range.endIndex >= count - 1 - threshold) loadMore('after');
  });

  const startIndex = range?.startIndex;
  const endIndex = range?.endIndex;
  useEffect(() => {
    onRange();
  }, [startIndex, endIndex, settled]);

  return loadMore;
}

// Keeping the reader's place ----------------------------------------------
//
// Lines added above the rows in view, the edge row above them coming or going, or
// a re-measure all move the rows on screen. `hold` is called just before such a
// change and notes the first visible row by its key and how far into it the
// scroll sits, read against the layout still on screen; the commit that draws the
// change puts that row back. Only the first hold before a commit counts, since a
// second is read against lines the screen does not show yet.

type KeepPlaceOptions = {
  scrollElRef: RefObject<HTMLDivElement | null>;
  virtualizer: Virtualizer<HTMLDivElement, Element>;
  lines: Lines;
};

function useKeepPlace({ scrollElRef, virtualizer, lines }: KeepPlaceOptions) {
  const heldRef = useRef<{ key: number; into: number } | null>(null);

  const hold = useCallback(() => {
    const scrollEl = scrollElRef.current;
    if (heldRef.current || !scrollEl) return;
    // The virtualizer's measurements are the last commit's, so the item and its key are what is on screen.
    const item = virtualizer.getVirtualItemForOffset(scrollEl.scrollTop);
    if (item) heldRef.current = { key: item.key as number, into: scrollEl.scrollTop - item.start };
  }, [scrollElRef, virtualizer]);

  useLayoutEffect(() => {
    const scrollEl = scrollElRef.current;
    const held = heldRef.current;
    heldRef.current = null;
    if (!scrollEl) return;
    const index = held ? lines.indexOfKey(held.key) : -1;
    if (!held || index < 0 || index >= lines.length) return;
    scrollEl.scrollTop = virtualizer.measurementsCache[index].start + held.into;
  });

  return hold;
}

// One landing -------------------------------------------------------------
//
// `LogViewerCore` keys it, so a jump or a new client starts over.

type LandingProps = {
  ref: Ref<{ measure: () => void }>;
  client: Client;
  anchor: Anchor;
  settings: Settings;
  onState: (state: CoreState) => void;
  onError: (error: unknown) => void;
  className?: string;
  children: (rows: CoreRows) => ReactNode;
};

function Landing({ ref, client, anchor, settings, onState, onError, className, children }: LandingProps) {
  const scrollElRef = useRef<HTMLDivElement>(null);
  const [count, setCount] = useState(0);
  const holdRef = useRef(() => {});
  const [lines] = useState(() => new Lines(setCount, () => holdRef.current()));
  // Loading ends a frame after the landing settles, landed or failed.
  const [loading, setLoading] = useState(true);
  const [landed, setLanded] = useState(false);
  const [failed, setFailed] = useState(false);
  const [before, setBefore] = useState(NO_MORE);
  const [after, setAfter] = useState(NO_MORE);
  // Lines go on being written past a landing, so the after edge reads as more
  // until a page after says otherwise.
  const [behind, setBehind] = useState(false);
  const [skipped, setSkipped] = useState<string[]>([]);

  const { estimateRowHeight } = settings;
  const estimateSize = useCallback((index: number) => estimateRowHeight(lines.at(index)), [estimateRowHeight, lines]);
  // Keys shift only when lines are added, which changes `count`, and the virtualizer re-reads them then.
  const getItemKey = useCallback((index: number) => lines.keyAt(index), [lines]);

  // The virtualizer mutates in place, so the compiler leaves this component unmemoized.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count,
    getScrollElement: () => scrollElRef.current,
    estimateSize,
    getItemKey,
    measureElement: settings.measureElement,
    overscan: settings.overscan,
    scrollMargin: before.more ? settings.edgeRowHeight : 0,
    useScrollendEvent: true,
    useFlushSync: false,
  });
  // The visible rows, without overscan. `virtualizer.range` is filled lazily, so it can lag a render.
  const visibleRange = virtualizer.calculateRange();

  const afterEdge = behind ? { ...after, more: true } : after;
  const loadedToEnd = !loading && landed && !afterEdge.more;
  const edgeRows = Number(before.more) + Number(afterEdge.more);
  const totalSize = (count ? virtualizer.getTotalSize() : 0) + edgeRows * settings.edgeRowHeight;
  const beforePaint = useBeforePaint();
  const hold = useKeepPlace({ scrollElRef, virtualizer, lines });
  useLayoutEffect(() => {
    holdRef.current = hold;
  }, [hold]);

  // Before the page's lines are added, so `Lines` can tell whether any were.
  const readPage = (edge: Edge, result: FetchResult) => {
    const more = result.nextCursor !== null;
    // The edge row above the rows can come or go.
    if (edge === 'before') hold();
    (edge === 'before' ? setBefore : setAfter)({ more, empty: more && !result.lines.length });
    lines.resumeFrom(edge, result.nextCursor);
    setSkipped(result.skipped.length ? result.skipped : NONE_SKIPPED);
  };

  // Fetches the anchor's page, places it with key 0 at the anchor, and scrolls there.
  const land = useEffectEvent(async () => {
    const limit = settings.firstPageSize;
    const askedAt = cursorNow();
    switch (anchor.kind) {
      case 'Head': {
        const result = await client.fetchSince({ limit });
        readPage('after', result);
        lines.reset(result.lines, 'first');
        break;
      }
      case 'Tail': {
        const result = await client.fetchUntil({ limit });
        readPage('before', result);
        lines.reset(result.lines, 'last');
        // Queued once the lines are in, so a reset that throws leaves no scroll queued.
        if (result.lines.length) {
          await beforePaint(() => {
            const scrollEl = scrollElRef.current;
            if (scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
          });
        }
        break;
      }
      case 'At': {
        const cursor = anchor.at;
        const [older, since] = await Promise.all([
          client.fetchBefore({ cursor, limit }),
          client.fetchSince({ cursor, limit }),
        ]);
        // Each edge's page is read just before its lines go in, since `reset` forgets both cursors.
        readPage('after', since);
        lines.reset(since.lines, 'first');
        readPage('before', older);
        const added = lines.prepend(older.lines);
        const skippedByEither = [...older.skipped, ...since.skipped];
        setSkipped(skippedByEither.length ? skippedByEither : NONE_SKIPPED);
        if (lines.length) {
          await beforePaint(() => {
            const scrollEl = scrollElRef.current;
            // With nothing since the moment, the last line older is as far as it goes.
            const at = Math.min(added, lines.length - 1);
            if (scrollEl) scrollEl.scrollTop = virtualizer.measurementsCache[at].start;
          });
        }
        break;
      }
      default:
        throw new Error(`unknown anchor ${String(anchor.kind)}`);
    }
    // With no line after and no page to read on from, the after edge resumes from
    // the moment the landing asked, so lines written while it was in flight are read.
    if (!lines.from('after')) lines.resumeFrom('after', askedAt);
  });

  const failLanding = useEffectEvent((error: unknown) => {
    setFailed(true);
    onError(error);
  });

  // Refs, not the effect's own variables: StrictMode runs the effect twice, and the
  // landing started by the first run must see the second's cleanup.
  const startedRef = useRef(false);
  const liveRef = useRef(false);
  const cancelLoadedRef = useRef(() => {});
  useEffect(() => {
    liveRef.current = true;
    if (!startedRef.current) {
      startedRef.current = true;
      land()
        .then(() => {
          if (!liveRef.current) return;
          setLanded(true);
          setBehind(true);
        })
        .catch((error) => {
          if (liveRef.current) failLanding(error);
        })
        .finally(() => {
          // Loading ends once the landing has painted.
          if (liveRef.current) cancelLoadedRef.current = onNextFrame(() => setLoading(false));
        });
    }
    return () => {
      liveRef.current = false;
      cancelLoadedRef.current();
    };
  }, []);

  // A page from the edge line keeps that line's moment, from a page's cursor leaves it out (`PageFrom`).
  const loadBefore = async () => {
    const from = lines.from('before');
    const options = { cursor: from?.cursor, limit: settings.pageSize };
    const result = await (from?.keep ? client.fetchUntil(options) : client.fetchBefore(options));
    readPage('before', result);
    // A page of lines already held is as empty as a page with none.
    if (!lines.prepend(result.lines) && result.nextCursor !== null) setBefore({ more: true, empty: true });
  };

  const loadAfter = async () => {
    const from = lines.from('after');
    const options = { cursor: from?.cursor, limit: settings.pageSize };
    const result = await (from?.keep ? client.fetchSince(options) : client.fetchAfter(options));
    setBehind(false);
    readPage('after', result);
    // A page of lines already held is as empty as a page with none.
    if (!lines.append(result.lines) && result.nextCursor !== null) setAfter({ more: true, empty: true });
  };

  const loadMore = useLoadMore({
    range: visibleRange,
    count,
    settled: !loading,
    edges: { before, after: afterEdge },
    load: { before: loadBefore, after: loadAfter },
    threshold: settings.loadMoreThreshold,
    onError,
  });

  useImperativeHandle(
    ref,
    () => ({
      measure: () => {
        hold();
        virtualizer.measure();
      },
    }),
    [hold, virtualizer],
  );

  // What the handle publishes.
  const shown = visibleRange !== null && visibleRange.endIndex < count ? visibleRange : null;
  const from = shown ? lines.at(shown.startIndex).timestamp : null;
  const to = shown ? lines.at(shown.endIndex).timestamp : null;
  const visible = shown ? shown.endIndex - shown.startIndex + 1 : 0;
  const atEnd = loadedToEnd && (count === 0 || visibleRange?.endIndex === count - 1);
  useEffect(() => {
    onState({
      loading,
      failed,
      atEnd,
      range: from !== null && to !== null ? { from, to } : null,
      visible,
      skipped,
    });
  }, [onState, loading, failed, atEnd, from, to, visible, skipped]);

  const held = (index: number) => index >= 0 && index < count;
  const lineAt = (key: number) => {
    const index = lines.indexOfKey(key);
    return held(index) ? lines.at(index) : undefined;
  };
  const rows: CoreRows = {
    loading,
    failed,
    before,
    after: afterEdge,
    edgeRowHeight: settings.edgeRowHeight,
    totalSize,
    rows: count
      ? virtualizer.getVirtualItems().map(({ key, index, size, start }) => ({
          key: key as number,
          index,
          size,
          start,
          line: lines.at(index),
        }))
      : [],
    count,
    lineAt,
    keyAt: (index) => (held(index) ? lines.keyAt(index) : undefined),
    indexOfKey: (key) => (lineAt(key) ? lines.indexOfKey(key) : -1),
    measureElement: virtualizer.measureElement,
    loadMore,
  };

  return (
    <div ref={scrollElRef} className={className ? `overflow-auto ${className}` : 'overflow-auto'}>
      {children(rows)}
    </div>
  );
}

// LogViewerCore -----------------------------------------------------------

export type LogViewerCoreProps = {
  ref?: Ref<CoreHandle>;
  client: Client;
  /** Where the viewer lands on mount. Read once: a new landing is `jumpTo`, or a new mount; a new `client` lands again where the last landing did. */
  anchor: Anchor;
  onError?: (error: unknown) => void;
  estimateRowHeight: Settings['estimateRowHeight'];
  measureElement?: Settings['measureElement'];
  overscan?: number;
  firstPageSize?: number;
  pageSize?: number;
  loadMoreThreshold?: number;
  edgeRowHeight?: number;
  className?: string;
  children: (rows: CoreRows) => ReactNode;
};

function ignore() {}

export function LogViewerCore({
  ref,
  client,
  anchor,
  onError = ignore,
  estimateRowHeight,
  measureElement,
  overscan = 3,
  firstPageSize = 150,
  pageSize = 100,
  loadMoreThreshold = 50,
  edgeRowHeight = 0,
  className,
  children,
}: LogViewerCoreProps) {
  const [landing, setLanding] = useState({ id: 0, anchor });
  const [landedClient, setLandedClient] = useState(client);
  if (client !== landedClient) {
    setLandedClient(client);
    setLanding((prev) => ({ ...prev, id: prev.id + 1 }));
  }

  const [published] = useState(() => createStore(CORE_INITIAL_STATE));
  const landingRef = useRef<{ measure: () => void }>(null);
  useImperativeHandle(
    ref,
    () => ({
      jumpTo: (next) => setLanding((prev) => ({ id: prev.id + 1, anchor: next })),
      measure: () => landingRef.current?.measure(),
      subscribe: published.subscribe,
      getSnapshot: published.get,
    }),
    [published],
  );

  return (
    <Landing
      key={landing.id}
      ref={landingRef}
      client={client}
      anchor={landing.anchor}
      settings={{
        estimateRowHeight,
        measureElement,
        overscan,
        firstPageSize,
        pageSize,
        loadMoreThreshold,
        edgeRowHeight,
      }}
      onState={published.set}
      onError={onError}
      className={className}
    >
      {children}
    </Landing>
  );
}
