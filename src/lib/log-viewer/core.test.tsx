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
import { act, render, screen } from '@testing-library/react';
import { createRef, useEffect } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Mock } from 'vitest';

import { HIDDEN_FLUSH_MS, LogViewerCore } from './core';
import { FakeClient } from './fake-client';
import type { CoreHandle, CoreRows } from './core';
import type { Anchor, Client, Cursor, FetchResult, LogLine } from './types';

// Layout ---------------------------------------------------------------
//
// jsdom lays nothing out. The scroller is VIEW tall, every row ROW tall, and its
// scrollHeight is the sizer the render function draws; scrollTop clamps as a
// browser's does and fires `scroll`, which is how the virtualizer and the pin
// learn of a move. A test's own write to scrollTop is the user's hand. Content
// shrinking under the scroll clamps it too, on the next read, with its `scroll`
// a task later, as a browser's layout does. `holdScrollEvents` keeps the events
// back and answers a release that fires one per scroller, as a browser coalesces them.

const ROW = 20;
const VIEW = 100;

const scrollTops = new WeakMap<Element, number>();

let heldScrolls: Set<Element> | null = null;

function holdScrollEvents() {
  const held = new Set<Element>();
  heldScrolls = held;
  return () => {
    heldScrolls = null;
    held.forEach((el) => el.dispatchEvent(new Event('scroll')));
  };
}

function scrollHeightOf(el: Element) {
  const sizer = el.firstElementChild as HTMLElement | null;
  return sizer ? Number.parseFloat(sizer.style.height || '0') : 0;
}

// What jsdom defines itself, put back after each test: `restoreAllMocks` leaves a defineProperty in place.
const STUBBED = ['clientHeight', 'offsetHeight', 'scrollHeight', 'scrollTop', 'scrollTo'] as const;
const unstubbed = STUBBED.map((name) => [name, Object.getOwnPropertyDescriptor(HTMLElement.prototype, name)] as const);

beforeEach(() => {
  vi.useFakeTimers();
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, get: () => VIEW });
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => VIEW });
  Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
    configurable: true,
    get() {
      return scrollHeightOf(this);
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
    configurable: true,
    get() {
      const top = scrollTops.get(this) ?? 0;
      const max = Math.max(0, scrollHeightOf(this) - VIEW);
      if (top <= max) return top;
      scrollTops.set(this, max);
      setTimeout(() => {
        if (heldScrolls) heldScrolls.add(this);
        else this.dispatchEvent(new Event('scroll'));
      });
      return max;
    },
    set(value: number) {
      const max = Math.max(0, scrollHeightOf(this) - VIEW);
      const next = Math.min(Math.max(0, value), max);
      if (next === (scrollTops.get(this) ?? 0)) return;
      scrollTops.set(this, next);
      if (heldScrolls) heldScrolls.add(this);
      else this.dispatchEvent(new Event('scroll'));
    },
  });
  HTMLElement.prototype.scrollTo = function scrollToTop(options?: ScrollToOptions | number) {
    if (typeof options === 'object' && options.top !== undefined) this.scrollTop = options.top;
  };
  vi.stubGlobal(
    'ResizeObserver',
    class {
      // eslint-disable-next-line class-methods-use-this -- a fake has nothing to observe
      observe() {}

      // eslint-disable-next-line class-methods-use-this -- a fake has nothing to release
      unobserve() {}

      // eslint-disable-next-line class-methods-use-this -- a fake has nothing to release
      disconnect() {}
    },
  );
});

afterEach(() => {
  unstubbed.forEach(([name, descriptor]) => {
    if (descriptor) Object.defineProperty(HTMLElement.prototype, name, descriptor);
    else delete (HTMLElement.prototype as Partial<HTMLElement>)[name];
  });
  heldScrolls = null;
  vi.restoreAllMocks();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

// Lines and a scripted client ------------------------------------------

const T0 = Date.parse('2026-10-08T14:00:00Z');

function line(i: number, pod = 'pod-1'): LogLine {
  const timestamp = new Date(T0 + i * 1000).toISOString();
  return {
    timestamp,
    // Not a timestamp, as the sidecar's is not: the core reads nothing from it.
    cursor: `${pod}#${i}`,
    message: `${pod} line ${i}`,
    stream: {
      namespace: 'ns',
      pod,
      container: 'app',
      previous: false,
    },
    place: {
      node: 'node-1',
      region: '',
      zone: '',
      os: 'linux',
      arch: 'amd64',
    },
  };
}

function lines(from: number, to: number, pod = 'pod-1') {
  return Array.from({ length: to - from }, (_, i) => line(from + i, pod));
}

function page(pageLines: LogLine[], nextCursor: string | null = null, skipped: string[] = []): FetchResult {
  return { lines: pageLines, nextCursor, skipped };
}

type Subscription = { after: Cursor | null; onLine: (line: LogLine) => void; open: boolean };

type ScriptedClient = {
  [K in 'fetchSince' | 'fetchUntil' | 'fetchAfter' | 'fetchBefore']: Mock<Client[K]>;
} & {
  subscribe: Mock<Client['subscribe']>;
  subscriptions: Subscription[];
  /** The one subscription open now; fails the test when there is not exactly one. */
  live: () => Subscription;
};

function scriptedClient(): ScriptedClient {
  const subscriptions: Subscription[] = [];
  return {
    fetchSince: vi.fn(async () => page([])),
    fetchUntil: vi.fn(async () => page([])),
    fetchAfter: vi.fn(async () => page([])),
    fetchBefore: vi.fn(async () => page([])),
    subscribe: vi.fn((after: Cursor | null, onLine: (line: LogLine) => void) => {
      const subscription = { after, onLine, open: true };
      subscriptions.push(subscription);
      return () => {
        subscription.open = false;
      };
    }),
    subscriptions,
    live: () => {
      const open = subscriptions.filter((s) => s.open);
      expect(open).toHaveLength(1);
      return open[0];
    },
  };
}

// Mounting --------------------------------------------------------------

type MountOptions = {
  anchor?: Anchor;
  pinToEnd?: boolean;
  paused?: boolean;
  onError?: (error: unknown) => void;
  loadMoreThreshold?: number;
  edgeRowHeight?: number;
  pinTolerance?: number;
};

const HEAD: Anchor = { kind: 'Head', at: null };
const TAIL: Anchor = { kind: 'Tail', at: null };

/** What the render function was last handed. */
let latestRows: CoreRows | null = null;

function Rows({ rows }: { rows: CoreRows }) {
  useEffect(() => {
    latestRows = rows;
  });
  return (
    <div data-testid="sizer" data-count={rows.count} style={{ height: `${rows.totalSize}px` }}>
      {rows.before.empty && (
        <div>
          No more in the last read; scroll to keep looking
          <button type="button" onClick={() => rows.loadMore('before')}>
            Read on before
          </button>
        </div>
      )}
      {rows.rows.map((row) => (
        <div key={row.key} data-testid="row" data-key={row.key}>
          {row.line.message}
        </div>
      ))}
      {rows.after.empty && (
        <div>
          No more in the last read; scroll to keep looking
          <button type="button" onClick={() => rows.loadMore('after')}>
            Read on after
          </button>
        </div>
      )}
    </div>
  );
}

function mount(client: Client, options: MountOptions = {}) {
  const ref = createRef<CoreHandle>();
  const props = {
    anchor: options.anchor ?? HEAD,
    pinToEnd: options.pinToEnd ?? false,
    onError: options.onError,
    loadMoreThreshold: options.loadMoreThreshold ?? 1,
    edgeRowHeight: options.edgeRowHeight ?? 0,
    pinTolerance: options.pinTolerance ?? 0,
  };
  const view = (paused: boolean, viewClient: Client = client) => (
    <LogViewerCore
      ref={ref}
      client={viewClient}
      anchor={props.anchor}
      pinToEnd={props.pinToEnd}
      paused={paused}
      onError={props.onError}
      estimateRowHeight={() => ROW}
      overscan={0}
      firstPageSize={10}
      pageSize={10}
      loadMoreThreshold={props.loadMoreThreshold}
      pinTolerance={props.pinTolerance}
      edgeRowHeight={props.edgeRowHeight}
    >
      {(rows) => <Rows rows={rows} />}
    </LogViewerCore>
  );
  const result = render(view(options.paused ?? false));
  const scroller = () => result.container.firstElementChild as HTMLElement;
  return {
    ref,
    setPaused: (paused: boolean) => result.rerender(view(paused)),
    setClient: (next: Client) => result.rerender(view(options.paused ?? false, next)),
    state: () => ref.current!.getSnapshot(),
    scroller,
    /** The user's hand on the scroller. */
    userScroll: async (top: number) => {
      await act(async () => {
        scroller().scrollTop = top;
      });
      await flush();
    },
    /** Scrolls to the bottom as far as the rows drawn reach. */
    userScrollToBottom: async () => {
      await act(async () => {
        scroller().scrollTop = scroller().scrollHeight;
      });
      await flush();
    },
  };
}

/** Sends follow frames, then lets the core flush them. */
async function deliver(subscription: Subscription, ...frames: LogLine[]) {
  act(() => frames.forEach((frame) => subscription.onLine(frame)));
  await flush();
}

function lineCount() {
  return Number(screen.getByTestId('sizer').dataset.count);
}

function messages() {
  return shown().map(([, message]) => message);
}

// React commits an act's updates as it ends, and a commit can schedule the next
// frame's work (a landing's end, a follow's flush), so run until no timer is left.
async function flush() {
  for (let round = 0; round < 10; round += 1) {
    // eslint-disable-next-line no-await-in-loop -- each round waits on the commit before it
    await act(() => vi.advanceTimersByTimeAsync(20));
    if (vi.getTimerCount() === 0) return;
  }
}

function shown() {
  return screen.queryAllByTestId('row').map((row) => [Number(row.dataset.key), row.textContent]);
}

// Tests -----------------------------------------------------------------

describe('LogViewerCore', () => {
  describe('landing', () => {
    it('opens at Head from the first line, at key 0, and says when it has loaded', async () => {
      const client = scriptedClient();
      client.fetchSince.mockResolvedValueOnce(page(lines(0, 3)));
      const { state } = mount(client, { anchor: HEAD });

      expect(state().loading).toBe(true);
      expect(client.fetchSince).toHaveBeenCalledWith({ limit: 10 });
      await flush();

      expect(shown()).toEqual([
        [0, 'pod-1 line 0'],
        [1, 'pod-1 line 1'],
        [2, 'pod-1 line 2'],
      ]);
      expect(state().loading).toBe(false);
    });

    it('opens at Tail with the newest line at key 0, scrolled to the bottom', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10), 'older'));
      const { scroller } = mount(client, { anchor: TAIL });
      await flush();

      expect(client.fetchUntil).toHaveBeenCalledWith({ limit: 10 });
      expect(scroller().scrollTop).toBe(10 * ROW - VIEW);
      expect(shown().at(-1)).toEqual([0, 'pod-1 line 9']);
      expect(shown()[0]).toEqual([-4, 'pod-1 line 5']);
    });

    it('opens At a moment with that moment’s line at key 0, scrolled to it', async () => {
      const client = scriptedClient();
      const at = line(20).timestamp;
      client.fetchBefore.mockResolvedValueOnce(page(lines(10, 20), 'older'));
      client.fetchSince.mockResolvedValueOnce(page(lines(20, 30), 'newer'));
      const { scroller } = mount(client, { anchor: { kind: 'At', at } });
      await flush();

      expect(client.fetchBefore).toHaveBeenCalledWith({ cursor: at, limit: 10 });
      expect(client.fetchSince).toHaveBeenCalledWith({ cursor: at, limit: 10 });
      expect(scroller().scrollTop).toBe(10 * ROW);
      expect(shown()[0]).toEqual([0, 'pod-1 line 20']);
    });

    it('lands unpinned at Head when asked to pin with more after, rather than following from there', async () => {
      const client = scriptedClient();
      client.fetchSince.mockResolvedValueOnce(page(lines(0, 10), line(9).cursor));
      const { state, scroller } = mount(client, { anchor: HEAD, pinToEnd: true });
      await flush();

      expect(client.subscribe).not.toHaveBeenCalled();
      expect(scroller().scrollTop).toBe(0);
      expect(state()).toMatchObject({ pinnedToEnd: false, following: false, atEnd: false });
    });

    it('lands pinned at Head when the first page holds the whole log, and follows', async () => {
      const client = scriptedClient();
      client.fetchSince.mockResolvedValueOnce(page(lines(0, 3)));
      const { state } = mount(client, { anchor: HEAD, pinToEnd: true });
      await flush();

      expect(state()).toMatchObject({ pinnedToEnd: true, following: true });
      // Five seconds back would be before the oldest line held, which bounds it.
      expect(client.live().after).toBe(line(0).timestamp);
    });

    it('reports nothing from a landing a jump replaced while it was in flight', async () => {
      const client = scriptedClient();
      const onError = vi.fn();
      let refuse: (error: Error) => void = () => {};
      client.fetchSince.mockReturnValueOnce(
        new Promise((_, reject) => {
          refuse = reject;
        }),
      );
      const { ref, state } = mount(client, { anchor: HEAD, onError });
      act(() => ref.current!.jumpTo(TAIL));
      await flush();

      await act(async () => refuse(new Error('refused')));
      await flush();
      expect(onError).not.toHaveBeenCalled();
      expect(state()).toMatchObject({ loading: false, following: true });
    });

    it('ends loading on a timer while no frame comes, as in a minimized window', async () => {
      vi.stubGlobal('requestAnimationFrame', () => 0);
      const client = scriptedClient();
      client.fetchSince.mockResolvedValueOnce(page(lines(0, 3)));
      const { state } = mount(client, { anchor: HEAD });
      await act(async () => {});
      expect(state().loading).toBe(true);

      await act(() => vi.advanceTimersByTimeAsync(HIDDEN_FLUSH_MS));
      expect(state().loading).toBe(false);
    });
  });

  describe('paging', () => {
    it('loads more before and after as the range reaches an edge, keeping the rows in view', async () => {
      const client = scriptedClient();
      client.fetchBefore.mockResolvedValueOnce(page(lines(10, 20), 'older'));
      client.fetchSince.mockResolvedValueOnce(page(lines(20, 30), 'newer'));
      const { userScroll, userScrollToBottom } = mount(client, { anchor: { kind: 'At', at: line(20).timestamp } });
      await flush();

      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 11)));
      await userScroll(0);
      // From the edge line, keeping its moment; the line already held is dropped.
      expect(client.fetchUntil).toHaveBeenLastCalledWith({ cursor: line(10).cursor, limit: 10 });
      // Prepended above, so the row the user was reading stays at the top.
      expect(shown()[0]).toEqual([-10, 'pod-1 line 10']);

      client.fetchSince.mockResolvedValueOnce(page(lines(29, 35)));
      await userScrollToBottom();
      expect(client.fetchSince).toHaveBeenLastCalledWith({ cursor: line(29).cursor, limit: 10 });
      await userScrollToBottom();
      expect(shown().at(-1)).toEqual([14, 'pod-1 line 34']);
    });

    it('takes an empty page with a cursor as more to read, and reads on only at the user’s next scroll', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page(lines(100, 110), 'older'));
      const { userScroll } = mount(client, { anchor: TAIL });
      await flush();

      client.fetchUntil.mockResolvedValueOnce(page([], 'budget-spent'));
      await userScroll(0);
      expect(client.fetchUntil).toHaveBeenCalledTimes(2);
      expect(screen.getByText('No more in the last read; scroll to keep looking')).toBeInTheDocument();

      await act(() => vi.advanceTimersByTimeAsync(10_000));
      expect(client.fetchUntil).toHaveBeenCalledTimes(2);

      client.fetchBefore.mockResolvedValueOnce(page([], 'budget-spent'));
      await userScroll(3 * ROW);
      await userScroll(0);
      expect(client.fetchBefore).toHaveBeenCalledOnce();
      expect(client.fetchBefore).toHaveBeenLastCalledWith({ cursor: 'budget-spent', limit: 10 });
    });

    it('reads on past an empty landing at the edge row’s button alone, since there is no row to scroll', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page([], 'budget-spent'));
      mount(client, { anchor: TAIL });
      await flush();
      expect(client.fetchBefore).not.toHaveBeenCalled();

      client.fetchBefore.mockResolvedValueOnce(page(lines(0, 3)));
      await act(async () => screen.getByRole('button', { name: 'Read on before' }).click());
      await flush();
      expect(client.fetchBefore).toHaveBeenCalledOnce();
      expect(client.fetchBefore).toHaveBeenCalledWith({ cursor: 'budget-spent', limit: 10 });
      expect(messages()).toEqual(lines(0, 3).map((l) => l.message));
    });

    it('reads on again in a minimized window, where no frame comes to end the page before', async () => {
      vi.stubGlobal('requestAnimationFrame', () => 0);
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page([], line(10).cursor));
      mount(client, { anchor: TAIL });
      await act(() => vi.advanceTimersByTimeAsync(HIDDEN_FLUSH_MS));

      client.fetchBefore.mockResolvedValue(page([], line(5).cursor));
      await act(async () => screen.getByRole('button', { name: 'Read on before' }).click());
      await act(() => vi.advanceTimersByTimeAsync(HIDDEN_FLUSH_MS));
      await act(async () => screen.getByRole('button', { name: 'Read on before' }).click());
      expect(client.fetchBefore).toHaveBeenCalledTimes(2);
      expect(client.fetchBefore).toHaveBeenLastCalledWith({ cursor: line(5).cursor, limit: 10 });
    });

    it('keeps an At landing’s empty page before with its cursor, beside lines after', async () => {
      const client = scriptedClient();
      const at = line(20).timestamp;
      client.fetchBefore.mockResolvedValueOnce(page([], 'budget-spent'));
      client.fetchSince.mockResolvedValueOnce(page(lines(20, 30)));
      mount(client, { anchor: { kind: 'At', at } });
      client.fetchBefore.mockResolvedValueOnce(page(lines(0, 3)));
      await flush();

      // Landed at the top, so the next page before is read at once.
      expect(client.fetchBefore).toHaveBeenLastCalledWith({ cursor: 'budget-spent', limit: 10 });
    });

    it('hands a failed fetch to onError once and keeps the rows it has', async () => {
      const client = scriptedClient();
      const onError = vi.fn();
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10), 'older'));
      const { userScroll } = mount(client, { anchor: TAIL, onError });
      await flush();

      const failure = new Error('socket closed');
      client.fetchUntil.mockRejectedValueOnce(failure);
      await userScroll(0);

      expect(onError).toHaveBeenCalledOnce();
      expect(onError).toHaveBeenCalledWith(failure);
      expect(shown()[0]).toEqual([-9, 'pod-1 line 0']);
    });

    it('hands a failed landing to onError and stops loading', async () => {
      const client = scriptedClient();
      const onError = vi.fn();
      client.fetchSince.mockRejectedValueOnce(new Error('refused'));
      const { state } = mount(client, { onError });
      await flush();

      expect(onError).toHaveBeenCalledOnce();
      expect(state().loading).toBe(false);
    });

    it('neither follows nor says it is at the end after a failed pinned landing', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockRejectedValueOnce(new Error('refused'));
      const { state } = mount(client, { anchor: TAIL, pinToEnd: true, onError: vi.fn() });
      await flush();

      expect(client.subscribe).not.toHaveBeenCalled();
      expect(state()).toMatchObject({
        loading: false,
        failed: true,
        pinnedToEnd: false,
        following: false,
        atEnd: false,
      });
      expect(latestRows!.failed).toBe(true);
    });

    it('lands again after a failed landing at a jump', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockRejectedValueOnce(new Error('refused'));
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 3)));
      const { ref, state } = mount(client, { anchor: TAIL, pinToEnd: true, onError: vi.fn() });
      await flush();

      act(() => ref.current!.jumpTo(TAIL));
      await flush();
      expect(state()).toMatchObject({ failed: false, pinnedToEnd: true, following: true });
    });

    it('keeps the rows in view when an empty last page removes the edge row above them', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10), 'older'));
      const { state, scroller, userScroll } = mount(client, {
        anchor: TAIL,
        edgeRowHeight: ROW,
        loadMoreThreshold: 3,
      });
      await flush();

      // Below the edge row, line 1 at the top of the view.
      await userScroll(2 * ROW);
      expect(client.fetchUntil).toHaveBeenLastCalledWith({ cursor: line(0).cursor, limit: 10 });
      expect(scroller().scrollTop).toBe(ROW);
      expect(state().range?.from).toBe(line(1).timestamp);
    });

    it('keeps the rows in view when pages before and after land in the same commit', async () => {
      const client = scriptedClient();
      const at = line(20).timestamp;
      client.fetchBefore.mockResolvedValueOnce(page(lines(15, 20), 'older'));
      client.fetchSince.mockResolvedValueOnce(page(lines(20, 25), line(24).cursor));
      client.fetchUntil.mockResolvedValueOnce(page(lines(5, 15)));
      client.fetchSince.mockResolvedValueOnce(page(lines(25, 35)));
      const { state, scroller } = mount(client, { anchor: { kind: 'At', at }, loadMoreThreshold: 5 });
      await flush();

      expect(client.fetchUntil).toHaveBeenLastCalledWith({ cursor: line(15).cursor, limit: 10 });
      expect(client.fetchSince).toHaveBeenLastCalledWith({ cursor: line(24).cursor, limit: 10 });
      expect(lineCount()).toBe(30);
      expect(scroller().scrollTop).toBe(15 * ROW);
      expect(state().range?.from).toBe(line(20).timestamp);
    });

    it('leaves no stale scroll behind when a last page before is exactly as tall as the edge row it removes', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10), 'older'));
      client.fetchSince.mockResolvedValueOnce(page([], 'newer'));
      const { scroller, userScroll, userScrollToBottom } = mount(client, {
        anchor: TAIL,
        edgeRowHeight: ROW,
        loadMoreThreshold: 3,
      });
      await flush();

      client.fetchUntil.mockResolvedValueOnce(page([line(-1)]));
      await userScroll(2 * ROW);
      expect(scroller().scrollTop).toBe(2 * ROW);

      // The page after adds rows below; the scroll stays where the user left it.
      const bottom = scroller().scrollHeight - VIEW;
      client.fetchAfter.mockResolvedValueOnce(page(lines(10, 13)));
      await userScrollToBottom();
      expect(client.fetchAfter).toHaveBeenLastCalledWith({ cursor: 'newer', limit: 10 });
      expect(scroller().scrollTop).toBe(bottom);
    });

    it('publishes the streams the last page could not read', async () => {
      const client = scriptedClient();
      client.fetchSince.mockResolvedValueOnce(
        page(lines(0, 20), null, ['api-7f9c/istio-proxy: container is waiting to start']),
      );
      const { state } = mount(client);
      await flush();

      expect(state().skipped).toEqual(['api-7f9c/istio-proxy: container is waiting to start']);
    });
  });

  describe('following', () => {
    async function pinnedAtTail(client: ScriptedClient, options: MountOptions = {}) {
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10), 'older'));
      const mounted = mount(client, { anchor: TAIL, pinToEnd: true, ...options });
      await flush();
      return mounted;
    }

    it('opens the follow five seconds before the newest line while pinned, and keeps the end in view as lines arrive', async () => {
      const client = scriptedClient();
      const { state, scroller } = await pinnedAtTail(client);

      expect(client.live().after).toBe(line(4).timestamp);
      await deliver(client.live(), line(10), line(11));

      expect(shown().at(-1)).toEqual([2, 'pod-1 line 11']);
      expect(scroller().scrollTop).toBe(12 * ROW - VIEW);
      expect(state()).toMatchObject({ pinnedToEnd: true, following: true, atEnd: true });
    });

    it('opens no follow when the view says not to pin, and pages toward the end instead', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10), 'older'));
      const { state } = mount(client, { anchor: TAIL, pinToEnd: false });
      await flush();

      // A Tail landing scrolls to the bottom, and that must not pin it. Landed at
      // the edge of a window with more after, it reads the next page.
      expect(client.subscribe).not.toHaveBeenCalled();
      expect(client.fetchSince).toHaveBeenCalledWith({ cursor: line(9).cursor, limit: 10 });
      expect(state()).toMatchObject({ pinnedToEnd: false, following: false });
    });

    it('re-pins on a user scroll to the bottom only once the last page after had nothing more', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10), 'older'));
      client.fetchSince.mockResolvedValueOnce(page(lines(10, 12), 'newer'));
      const { state, userScrollToBottom } = mount(client, { anchor: TAIL, pinToEnd: false });
      await flush();
      expect(state().pinnedToEnd).toBe(false);

      client.fetchSince.mockResolvedValueOnce(page(lines(12, 13)));
      await userScrollToBottom();
      expect(client.fetchSince).toHaveBeenLastCalledWith({ cursor: line(11).cursor, limit: 10 });
      expect(state().pinnedToEnd).toBe(false);

      await userScrollToBottom();
      expect(state()).toMatchObject({ pinnedToEnd: true, following: true, atEnd: true });
      expect(client.live().after).toBe(line(7).timestamp);
    });

    it('re-pins when the page a scroll to the bottom asked for finds no more', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10), 'older'));
      client.fetchSince.mockResolvedValueOnce(page([], 'newer'));
      const { state, scroller, userScroll, userScrollToBottom } = mount(client, { anchor: TAIL, pinToEnd: false });
      await flush();

      client.fetchAfter.mockResolvedValueOnce(page([]));
      await userScroll(scroller().scrollTop - ROW);
      await userScrollToBottom();
      expect(client.fetchAfter).toHaveBeenLastCalledWith({ cursor: 'newer', limit: 10 });
      expect(state()).toMatchObject({ pinnedToEnd: true, following: true });
    });

    it('does not pin when the page that finds no more was not asked for by a scroll to the bottom', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10), 'older'));
      const { state } = mount(client, { anchor: TAIL, pinToEnd: false });
      await flush();

      expect(client.fetchSince).toHaveBeenCalledOnce();
      expect(state()).toMatchObject({ pinnedToEnd: false, following: false });
    });

    it('does not pin when removing the edge row after clamps the scroll to the bottom', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10), 'older'));
      const { state, scroller } = mount(client, { anchor: TAIL, pinToEnd: false, edgeRowHeight: ROW });
      await flush();

      // The landing scrolled past the rows to the edge row; the page after found no more.
      expect(client.fetchSince).toHaveBeenCalledOnce();
      expect(scroller().scrollTop).toBe(scroller().scrollHeight - VIEW);
      await flush();

      expect(client.subscribe).not.toHaveBeenCalled();
      expect(state()).toMatchObject({ pinnedToEnd: false, following: false });
    });

    it('unpins on a scroll up, closes the follow and says it is no longer at the end', async () => {
      const client = scriptedClient();
      const { state, scroller, userScroll } = await pinnedAtTail(client);
      const follow = client.live();

      await userScroll(scroller().scrollTop - ROW);

      expect(follow.open).toBe(false);
      expect(state()).toMatchObject({ pinnedToEnd: false, following: false, atEnd: false });
    });

    it('unpins on a scroll up between follow batches arriving every frame', async () => {
      const client = scriptedClient();
      const { state, scroller } = await pinnedAtTail(client);
      const follow = client.live();

      // One frame: the batch is appended and the end brought into view.
      act(() => follow.onLine(line(10)));
      await act(() => vi.advanceTimersByTimeAsync(16));
      const bottom = scroller().scrollTop;
      expect(bottom).toBe(11 * ROW - VIEW);

      // Before the next frame, the user scrolls up, and the next batch is already on its way.
      act(() => {
        scroller().scrollTop = bottom - 2 * ROW;
        follow.onLine(line(11));
      });
      await flush();

      expect(follow.open).toBe(false);
      expect(scroller().scrollTop).toBe(bottom - 2 * ROW);
      expect(state()).toMatchObject({ pinnedToEnd: false, following: false });
    });

    it('appends a follow batch on a timer while no frame comes, as in a minimized window', async () => {
      const client = scriptedClient();
      await pinnedAtTail(client);
      const follow = client.live();
      vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 0);

      act(() => follow.onLine(line(10)));
      await act(() => vi.advanceTimersByTimeAsync(HIDDEN_FLUSH_MS - 1));
      expect(lineCount()).toBe(10);

      await act(() => vi.advanceTimersByTimeAsync(1));
      expect(lineCount()).toBe(11);
    });

    it('drops a batch still waiting when the follow closes', async () => {
      const client = scriptedClient();
      const { setPaused } = await pinnedAtTail(client);
      const follow = client.live();

      act(() => {
        follow.onLine(line(10));
        setPaused(true);
      });
      await act(() => vi.advanceTimersByTimeAsync(HIDDEN_FLUSH_MS));
      expect(follow.open).toBe(false);
      expect(lineCount()).toBe(10);
    });

    it('unpins on a scroll up whose event arrives with the follow’s, coalesced into one', async () => {
      const client = scriptedClient();
      const { state, scroller } = await pinnedAtTail(client);
      const follow = client.live();
      const bottom = scroller().scrollTop;

      const release = holdScrollEvents();
      await deliver(follow, ...lines(10, 20));
      expect(scroller().scrollTop).toBe(bottom + 10 * ROW);
      scroller().scrollTop = bottom + 9 * ROW;
      // One event, reading a position above where the core left the scroller and below where the last event read it.
      await act(async () => release());
      await flush();

      expect(follow.open).toBe(false);
      expect(state()).toMatchObject({ pinnedToEnd: false, following: false });
      expect(scroller().scrollTop).toBe(bottom + 9 * ROW);
    });

    it('keeps a scroll up whose event has not arrived when the follow’s lines land, and unpins when it does', async () => {
      const client = scriptedClient();
      const { state, scroller } = await pinnedAtTail(client);
      const follow = client.live();
      const bottom = scroller().scrollTop;

      const release = holdScrollEvents();
      scroller().scrollTop = bottom - ROW;
      await deliver(follow, ...lines(10, 20));
      expect(scroller().scrollTop).toBe(bottom - ROW);
      await act(async () => release());
      await flush();

      expect(follow.open).toBe(false);
      expect(state()).toMatchObject({ pinnedToEnd: false, following: false });
    });

    it('keeps the row of a scroll up whose event has not arrived when a page lands above it, and unpins', async () => {
      const client = scriptedClient();
      let answer: (result: FetchResult) => void = () => {};
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10), 'older')).mockReturnValueOnce(
        new Promise((resolve) => {
          answer = resolve;
        }),
      );
      const { state, scroller } = mount(client, { anchor: TAIL, pinToEnd: true, loadMoreThreshold: 10 });
      await flush();
      expect(client.fetchUntil).toHaveBeenCalledTimes(2);
      const bottom = scroller().scrollTop;

      const release = holdScrollEvents();
      scroller().scrollTop = bottom - ROW;
      await act(async () => answer(page(lines(-10, 0))));
      await flush();
      expect(scroller().scrollTop).toBe(bottom - ROW + 10 * ROW);
      await act(async () => release());
      await flush();

      expect(state()).toMatchObject({ pinnedToEnd: false, following: false });
      expect(scroller().scrollTop).toBe(bottom - ROW + 10 * ROW);
    });

    it('unpins on a scroll up right after a scroll to the bottom, both before a render', async () => {
      const client = scriptedClient();
      const { state, scroller, userScroll } = await pinnedAtTail(client);
      const bottom = scroller().scrollTop;
      await userScroll(bottom - ROW);
      expect(state().pinnedToEnd).toBe(false);

      await act(async () => {
        scroller().scrollTop = bottom;
        scroller().scrollTop = bottom - ROW;
      });
      await flush();
      expect(state()).toMatchObject({ pinnedToEnd: false, following: false });
    });

    it('unpins on a scroll up smaller than the pin tolerance, and stays where the user left it', async () => {
      const client = scriptedClient();
      const { state, scroller, userScroll } = await pinnedAtTail(client, { pinTolerance: 10 });
      const follow = client.live();
      const bottom = scroller().scrollTop;

      await userScroll(bottom - 3);
      expect(follow.open).toBe(false);
      expect(state()).toMatchObject({ pinnedToEnd: false, following: false });
      expect(scroller().scrollTop).toBe(bottom - 3);
    });

    it('pins on a scroll down that ends within the pin tolerance of the bottom', async () => {
      const client = scriptedClient();
      const { state, scroller, userScroll } = await pinnedAtTail(client, { pinTolerance: 10 });
      const bottom = scroller().scrollTop;
      await userScroll(bottom - ROW);
      expect(state().pinnedToEnd).toBe(false);

      await userScroll(bottom - 5);
      expect(state()).toMatchObject({ pinnedToEnd: true, following: true });
    });

    it('lets the user unpin right after a core scroll that changed nothing', async () => {
      const client = scriptedClient();
      const { ref, state, scroller, userScroll } = await pinnedAtTail(client);
      const follow = client.live();
      const bottom = scroller().scrollTop;

      // A re-measure at the end scrolls to where it already is, so no event fires;
      // what the core recorded must not be held against the user's next scroll.
      act(() => ref.current!.measure());
      await flush();
      expect(scroller().scrollTop).toBe(bottom);

      await userScroll(bottom - ROW);
      expect(follow.open).toBe(false);
      expect(state()).toMatchObject({ pinnedToEnd: false, following: false });
    });

    it('catches up from five seconds before the newest line, then re-pins and follows from there', async () => {
      const client = scriptedClient();
      const { state, scroller, userScroll, userScrollToBottom } = await pinnedAtTail(client);
      await deliver(client.live(), line(10));

      // The catch-up re-reads lines it holds, which are dropped.
      client.fetchAfter.mockResolvedValueOnce(page(lines(6, 13)));
      await userScroll(scroller().scrollTop - ROW);
      expect(client.fetchAfter).toHaveBeenCalledWith({
        cursor: line(5).timestamp,
        limit: 10,
      });

      await userScrollToBottom();
      expect(messages()).toEqual(lines(8, 13).map((l) => l.message));
      expect(state().pinnedToEnd).toBe(true);
      expect(client.live().after).toBe(line(7).timestamp);
    });

    it('takes a catch-up page of lines it already holds as empty, and offers to read on', async () => {
      const client = scriptedClient();
      const { scroller, userScroll } = await pinnedAtTail(client);
      await deliver(client.live(), line(10));

      client.fetchAfter.mockResolvedValueOnce(page(lines(6, 11), 'budget-spent'));
      await userScroll(scroller().scrollTop - ROW);
      expect(lineCount()).toBe(11);

      client.fetchAfter.mockResolvedValueOnce(page(lines(11, 13)));
      await act(async () => screen.getByRole('button', { name: 'Read on after' }).click());
      await flush();
      expect(client.fetchAfter).toHaveBeenLastCalledWith({ cursor: 'budget-spent', limit: 10 });
      expect(lineCount()).toBe(13);
    });

    it('follows on resume whatever a catch-up page asked during the pause says when it lands late', async () => {
      const client = scriptedClient();
      const { state, scroller, setPaused, userScrollToBottom } = await pinnedAtTail(client, { edgeRowHeight: ROW });
      act(() => setPaused(true));
      await flush();

      // Into the after edge row the pause drew: a catch-up page, still pinned.
      let answer: (result: FetchResult) => void = () => {};
      client.fetchAfter.mockReturnValueOnce(
        new Promise((resolve) => {
          answer = resolve;
        }),
      );
      await userScrollToBottom();
      expect(client.fetchAfter).toHaveBeenCalledOnce();
      expect(state().pinnedToEnd).toBe(true);

      act(() => setPaused(false));
      await flush();
      const follow = client.live();
      // Lines it holds, and more to read: stale, since the follow reads from the after edge itself.
      await act(async () => answer(page(lines(6, 10), line(9).cursor)));
      await flush();

      expect(follow.open).toBe(true);
      expect(state()).toMatchObject({ pinnedToEnd: true, following: true });
      await deliver(follow, line(10));
      expect(scroller().scrollTop + VIEW).toBe(scroller().scrollHeight);
      expect(messages().at(-1)).toBe('pod-1 line 10');
    });

    it('follows on resume after a catch-up page during the pause found more to read', async () => {
      const client = scriptedClient();
      const { state, setPaused, userScrollToBottom } = await pinnedAtTail(client, { edgeRowHeight: ROW });
      act(() => setPaused(true));
      await flush();

      client.fetchAfter.mockResolvedValueOnce(page(lines(10, 12), line(11).cursor));
      await userScrollToBottom();
      expect(lineCount()).toBe(12);

      act(() => setPaused(false));
      await flush();
      expect(state()).toMatchObject({ pinnedToEnd: true, following: true });
      expect(client.live().after).toBe(line(6).timestamp);
    });

    it('closes the follow while paused and leaves the viewport where it is, then reopens five seconds back', async () => {
      const client = scriptedClient();
      const { state, scroller, setPaused } = await pinnedAtTail(client);
      const follow = client.live();
      const top = scroller().scrollTop;

      act(() => setPaused(true));
      await flush();
      expect(follow.open).toBe(false);
      expect(scroller().scrollTop).toBe(top);
      expect(client.fetchAfter).not.toHaveBeenCalled();
      expect(state()).toMatchObject({ pinnedToEnd: true, following: false });

      act(() => setPaused(false));
      await flush();
      expect(client.live().after).toBe(line(4).timestamp);
      expect(state().following).toBe(true);
    });

    it('re-pins while paused as a follow closed at the end, still behind the live end', async () => {
      const client = scriptedClient();
      const { state, scroller, setPaused, userScroll, userScrollToBottom } = await pinnedAtTail(client, {
        edgeRowHeight: ROW,
      });
      act(() => setPaused(true));
      await flush();

      // Unpinned, with a catch-up page that finds nothing more: the edge row goes.
      await userScroll(scroller().scrollTop - ROW);
      expect(state().pinnedToEnd).toBe(false);
      expect(latestRows!.after.more).toBe(false);

      // Lines go on being written past a pinned window that is paused.
      await userScrollToBottom();
      expect(state()).toMatchObject({ pinnedToEnd: true, following: false, atEnd: false });
      expect(latestRows!.after.more).toBe(true);

      act(() => setPaused(false));
      await flush();
      expect(state()).toMatchObject({ pinnedToEnd: true, following: true });
    });

    it('closes the follow on a pause pressed after the landing settled but before its loading ended', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10), 'older'));
      const { state, setPaused } = mount(client, { anchor: TAIL, pinToEnd: true });
      // The landing settles; the frame that ends its loading has not come.
      await act(async () => {});
      expect(state().loading).toBe(true);

      // Behind, it catches up from the overlap rather than calling the landing the end.
      act(() => setPaused(true));
      await flush();
      expect(client.subscribe).not.toHaveBeenCalled();
      expect(state()).toMatchObject({ loading: false, pinnedToEnd: true, following: false });
      expect(client.fetchAfter).toHaveBeenCalledWith({
        cursor: line(4).timestamp,
        limit: 10,
      });

      act(() => setPaused(false));
      await flush();
      expect(client.live().after).toBe(line(4).timestamp);
    });

    it('hands a follow it cannot open to onError, and opens none', async () => {
      const client = scriptedClient();
      const onError = vi.fn();
      client.fetchUntil.mockResolvedValueOnce(page([...lines(0, 9), { ...line(9), timestamp: 'soon' }], 'older'));
      mount(client, { anchor: TAIL, pinToEnd: true, onError });
      await flush();

      expect(onError).toHaveBeenCalledWith(expect.any(RangeError));
      expect(client.subscribe).not.toHaveBeenCalled();
    });

    it('follows on resume after an empty landing made while paused, from the moment the landing asked', async () => {
      const client = scriptedClient();
      const mountedAt = Date.now();
      const { state, setPaused } = mount(client, { anchor: TAIL, pinToEnd: true, paused: true });
      await flush();
      const landedAt = Date.now();
      expect(client.subscribe).not.toHaveBeenCalled();

      // Holding nothing, the follow has no newest line to open before: it opens at the
      // moment the landing asked, so lines written during the pause are read.
      await act(() => vi.advanceTimersByTimeAsync(60_000));
      act(() => setPaused(false));
      await flush();
      const from = Date.parse(client.live().after!);
      expect(from).toBeGreaterThanOrEqual(mountedAt);
      expect(from).toBeLessThanOrEqual(landedAt);
      expect(state().following).toBe(true);

      await deliver(client.live(), line(0));
      expect(messages()).toEqual(['pod-1 line 0']);
    });

    it('brings the lines written while an empty landing was in flight', async () => {
      const log = new FakeClient({ lines: 0, delayMs: 100 });
      const { state } = mount(log, { anchor: TAIL, pinToEnd: true });

      await act(() => vi.advanceTimersByTimeAsync(50));
      log.append();
      await flush();
      expect(state().following).toBe(true);
      expect(messages()).toEqual(['line 0']);
    });

    it('brings the lines written during a pause when it reopens over an empty window', async () => {
      const log = new FakeClient({ lines: 0 });
      const { setPaused } = mount(log, { anchor: TAIL, pinToEnd: true, paused: true });
      await flush();

      await act(() => vi.advanceTimersByTimeAsync(60_000));
      log.append(2);
      act(() => setPaused(false));
      await flush();
      expect(messages()).toEqual(['line 0', 'line 1']);
    });

    it('brings back a line received but not yet appended when a pause closes a follow over an empty window', async () => {
      const log = new FakeClient({ lines: 0 });
      const { setPaused } = mount(log, { anchor: TAIL, pinToEnd: true });
      await flush();

      await act(() => vi.advanceTimersByTimeAsync(1000));
      act(() => {
        log.append();
        setPaused(true);
      });
      await act(() => vi.advanceTimersByTimeAsync(HIDDEN_FLUSH_MS));
      expect(lineCount()).toBe(0);

      act(() => setPaused(false));
      await flush();
      expect(messages()).toEqual(['line 0']);
    });

    it('opens the follow no earlier than the oldest line held, leaving what is older to the page before', async () => {
      const client = scriptedClient();
      // Ten lines within a second: five seconds back would be before the window.
      const window = lines(100, 110).map((l, i) => ({
        ...l,
        timestamp: new Date(T0 + 100_000 + i * 100).toISOString(),
      }));
      client.fetchUntil.mockResolvedValueOnce(page(window, line(100).cursor));
      const { userScroll } = mount(client, { anchor: TAIL, pinToEnd: true });
      await flush();
      expect(client.live().after).toBe(window[0].timestamp);

      client.fetchUntil.mockResolvedValueOnce(page([line(98), line(99, 'pod-2'), line(100)]));
      await userScroll(0);
      expect(client.fetchUntil).toHaveBeenLastCalledWith({ cursor: line(100).cursor, limit: 10 });
      expect(lineCount()).toBe(12);
    });

    it('drops a replayed line by its cursor, and keeps an older line from another container', async () => {
      const client = scriptedClient();
      await pinnedAtTail(client);
      const follow = client.live();

      await deliver(follow, line(10), line(11));
      // A reconnect replays from the cursor the follow opened with.
      await deliver(follow, line(10), line(11), line(12));
      await deliver(follow, line(11, 'pod-2'));

      expect(client.subscribe).toHaveBeenCalledOnce();
      expect(messages().slice(-4)).toEqual(['pod-1 line 10', 'pod-1 line 11', 'pod-1 line 12', 'pod-2 line 11']);
    });

    it('keeps a line a container wrote twice at one moment, by its cursor, and drops a replay', async () => {
      const client = scriptedClient();
      await pinnedAtTail(client);
      const follow = client.live();

      await deliver(follow, line(10));
      await deliver(follow, { ...line(10), cursor: 'pod-1#10#1' }, line(10));
      expect(messages().slice(-2)).toEqual(['pod-1 line 10', 'pod-1 line 10']);
      expect(lineCount()).toBe(12);
    });

    it('prepends a page before less the lines held, whatever the follow brought', async () => {
      const client = scriptedClient();
      const { userScroll } = await pinnedAtTail(client);
      await deliver(client.live(), line(10, 'pod-2'), line(11));

      client.fetchUntil.mockResolvedValueOnce(page([...lines(0, 3, 'pod-2'), ...lines(5, 8)]));
      await userScroll(0);
      expect(lineCount()).toBe(10 + 2 + 3);

      await userScroll(0);
      expect(messages()).toEqual(['pod-2 line 0', 'pod-2 line 1', 'pod-2 line 2', 'pod-1 line 0', 'pod-1 line 1']);
    });
  });

  describe('what it publishes', () => {
    it('follows the viewport: the visible range, how many lines, and whether the newest is on screen', async () => {
      const client = scriptedClient();
      client.fetchSince.mockResolvedValueOnce(page(lines(0, 20)));
      const { state, userScroll, userScrollToBottom } = mount(client, { anchor: HEAD });
      await flush();

      expect(state()).toMatchObject({
        range: { from: line(0).timestamp, to: line(4).timestamp },
        visible: 5,
        atEnd: false,
      });

      await userScroll(5 * ROW);
      expect(state().range).toEqual({ from: line(5).timestamp, to: line(9).timestamp });

      // Unpinned, the window has more after until a page says otherwise; this one
      // says none, and the user's scroll to the bottom pins.
      await userScrollToBottom();
      expect(client.fetchSince).toHaveBeenLastCalledWith({ cursor: line(19).cursor, limit: 10 });
      expect(state()).toMatchObject({
        range: { from: line(15).timestamp, to: line(19).timestamp },
        atEnd: true,
        pinnedToEnd: true,
      });
    });
  });

  describe('the handle', () => {
    it('jumps to each kind of anchor, pinning at Tail alone', async () => {
      const client = scriptedClient();
      client.fetchSince.mockResolvedValue(page(lines(0, 3)));
      client.fetchUntil.mockResolvedValue(page(lines(0, 3)));
      const { ref, state } = mount(client, { anchor: HEAD });
      await flush();

      act(() => ref.current!.jumpTo(TAIL));
      await flush();
      expect(client.fetchUntil).toHaveBeenCalledWith({ limit: 10 });
      expect(state()).toMatchObject({ pinnedToEnd: true, following: true });
      const follow = client.live();

      // Each landing's own fetch is the first after the jump; pages after it may follow.
      const at = line(1).timestamp;
      let since = client.fetchSince.mock.calls.length;
      act(() => ref.current!.jumpTo({ kind: 'At', at }));
      await flush();
      expect(client.fetchBefore).toHaveBeenCalledWith({ cursor: at, limit: 10 });
      expect(client.fetchSince.mock.calls[since]).toEqual([{ cursor: at, limit: 10 }]);
      expect(follow.open).toBe(false);
      expect(state().pinnedToEnd).toBe(false);

      since = client.fetchSince.mock.calls.length;
      act(() => ref.current!.jumpTo(HEAD));
      await flush();
      expect(client.fetchSince.mock.calls[since]).toEqual([{ limit: 10 }]);
      expect(shown()[0]).toEqual([0, 'pod-1 line 0']);
    });

    it('re-measures and keeps the end in view', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 10)));
      const { ref, scroller } = mount(client, { anchor: TAIL, pinToEnd: true });
      await flush();

      act(() => ref.current!.measure());
      await flush();
      expect(scroller().scrollTop).toBe(10 * ROW - VIEW);
    });

    it('re-measures and keeps the reader where they were inside the first visible row', async () => {
      const client = scriptedClient();
      client.fetchSince.mockResolvedValueOnce(page(lines(0, 20)));
      const { ref, scroller, userScroll } = mount(client, { anchor: HEAD });
      await flush();
      await userScroll(5 * ROW + 5);

      act(() => ref.current!.measure());
      await flush();
      expect(scroller().scrollTop).toBe(5 * ROW + 5);
    });

    it('re-measures and keeps the reader’s row when a page before lands before the re-measure is drawn', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockResolvedValueOnce(page(lines(10, 20), line(10).cursor));
      const { ref, state, scroller, userScroll } = mount(client, { anchor: TAIL });
      await flush();

      let answer: (result: FetchResult) => void = () => {};
      client.fetchUntil.mockReturnValueOnce(
        new Promise((resolve) => {
          answer = resolve;
        }),
      );
      await userScroll(ROW);
      expect(client.fetchUntil).toHaveBeenCalledTimes(2);

      await act(async () => {
        ref.current!.measure();
        answer(page(lines(0, 10)));
      });
      await flush();
      expect(scroller().scrollTop).toBe(11 * ROW);
      expect(state().range?.from).toBe(line(11).timestamp);
    });

    it('lands again when the client changes', async () => {
      const first = scriptedClient();
      const second = scriptedClient();
      second.fetchSince.mockResolvedValueOnce(page(lines(50, 52)));
      const { setClient } = mount(first, { anchor: HEAD });
      await flush();

      setClient(second);
      await flush();
      expect(second.fetchSince).toHaveBeenCalledWith({ limit: 10 });
      expect(messages()).toEqual(['pod-1 line 50', 'pod-1 line 51']);
    });

    it('lands a new client where the last jump did, not at the anchor it mounted with', async () => {
      const first = scriptedClient();
      const second = scriptedClient();
      const { ref, setClient } = mount(first, { anchor: HEAD });
      await flush();
      const at = line(5).timestamp;
      act(() => ref.current!.jumpTo({ kind: 'At', at }));
      await flush();

      setClient(second);
      await flush();
      expect(second.fetchBefore).toHaveBeenCalledWith({ cursor: at, limit: 10 });
      expect(second.fetchSince).toHaveBeenCalledWith({ cursor: at, limit: 10 });
    });
  });
});
