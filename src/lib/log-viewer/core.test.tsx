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
import type { CoreHandle, CoreRows } from './core';
import type { Anchor, Client, FetchResult, LogLine } from './types';

// Layout ---------------------------------------------------------------
//
// jsdom lays nothing out. The scroller is VIEW tall, every row ROW tall, and its
// scrollHeight is the sizer the render function draws; scrollTop clamps as a
// browser's does and fires `scroll`, which is how the virtualizer learns of a
// move. A test's own write to scrollTop is the user's hand. Content shrinking
// under the scroll clamps it too, on the next read, with its `scroll` a task
// later, as a browser's layout does.

const ROW = 20;
const VIEW = 100;

const scrollTops = new WeakMap<Element, number>();

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
      setTimeout(() => this.dispatchEvent(new Event('scroll')));
      return max;
    },
    set(value: number) {
      const max = Math.max(0, scrollHeightOf(this) - VIEW);
      const next = Math.min(Math.max(0, value), max);
      if (next === (scrollTops.get(this) ?? 0)) return;
      scrollTops.set(this, next);
      this.dispatchEvent(new Event('scroll'));
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
    cursor: timestamp,
    message: `${pod} line ${i}`,
    source: {
      namespace: 'ns',
      pod,
      container: 'app',
      previous: false,
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

type ScriptedClient = {
  [K in 'fetchSince' | 'fetchUntil' | 'fetchAfter' | 'fetchBefore']: Mock<Client[K]>;
} & {
  subscribe: Mock<Client['subscribe']>;
};

function scriptedClient(): ScriptedClient {
  return {
    fetchSince: vi.fn(async () => page([])),
    fetchUntil: vi.fn(async () => page([])),
    fetchAfter: vi.fn(async () => page([])),
    fetchBefore: vi.fn(async () => page([])),
    subscribe: vi.fn(() => () => {}),
  };
}

// Mounting --------------------------------------------------------------

type MountOptions = {
  anchor?: Anchor;
  onError?: (error: unknown) => void;
  loadMoreThreshold?: number;
  edgeRowHeight?: number;
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
    onError: options.onError,
    loadMoreThreshold: options.loadMoreThreshold ?? 1,
    edgeRowHeight: options.edgeRowHeight ?? 0,
  };
  const view = (viewClient: Client) => (
    <LogViewerCore
      ref={ref}
      client={viewClient}
      anchor={props.anchor}
      onError={props.onError}
      estimateRowHeight={() => ROW}
      overscan={0}
      firstPageSize={10}
      pageSize={10}
      loadMoreThreshold={props.loadMoreThreshold}
      edgeRowHeight={props.edgeRowHeight}
    >
      {(rows) => <Rows rows={rows} />}
    </LogViewerCore>
  );
  const result = render(view(client));
  const scroller = () => result.container.firstElementChild as HTMLElement;
  return {
    ref,
    setClient: (next: Client) => result.rerender(view(next)),
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

function lineCount() {
  return Number(screen.getByTestId('sizer').dataset.count);
}

function messages() {
  return shown().map(([, message]) => message);
}

// React commits an act's updates as it ends, and a commit can schedule the next
// frame's work (a landing's end, a page's flight), so run until no timer is left.
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
      expect(state()).toMatchObject({ loading: false, failed: false });
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

    it('says it failed, and not that it is at the end, after a failed landing', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockRejectedValueOnce(new Error('refused'));
      const { state } = mount(client, { anchor: TAIL, onError: vi.fn() });
      await flush();

      expect(state()).toMatchObject({ loading: false, failed: true, atEnd: false });
      expect(latestRows!.failed).toBe(true);
    });

    it('lands again after a failed landing at a jump', async () => {
      const client = scriptedClient();
      client.fetchUntil.mockRejectedValueOnce(new Error('refused'));
      client.fetchUntil.mockResolvedValueOnce(page(lines(0, 3)));
      const { ref, state } = mount(client, { anchor: TAIL, onError: vi.fn() });
      await flush();

      act(() => ref.current!.jumpTo(TAIL));
      await flush();
      expect(state().failed).toBe(false);
      expect(messages()).toEqual(lines(0, 3).map((l) => l.message));
    });

    it('reports a landing it cannot place once, with no scroll left queued behind it', async () => {
      const client = scriptedClient();
      const onError = vi.fn();
      client.fetchSince.mockResolvedValueOnce(page([line(1), { ...line(2), cursor: 'soon' }]));
      const { state } = mount(client, { anchor: { kind: 'At', at: line(1).timestamp }, onError });
      await flush();

      expect(onError).toHaveBeenCalledOnce();
      expect(onError).toHaveBeenCalledWith(expect.any(RangeError));
      expect(state().failed).toBe(true);
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

      // The window has more after until a page says otherwise; this one says none.
      await userScrollToBottom();
      expect(client.fetchSince).toHaveBeenLastCalledWith({ cursor: line(19).cursor, limit: 10 });
      expect(state()).toMatchObject({
        range: { from: line(15).timestamp, to: line(19).timestamp },
        atEnd: true,
      });
    });
  });

  describe('the handle', () => {
    it('jumps to each kind of anchor', async () => {
      const client = scriptedClient();
      client.fetchSince.mockResolvedValue(page(lines(0, 3)));
      client.fetchUntil.mockResolvedValue(page(lines(0, 3)));
      const { ref } = mount(client, { anchor: HEAD });
      await flush();

      act(() => ref.current!.jumpTo(TAIL));
      await flush();
      expect(client.fetchUntil).toHaveBeenCalledWith({ limit: 10 });

      // Each landing's own fetch is the first after the jump; pages after it may follow.
      const at = line(1).timestamp;
      let since = client.fetchSince.mock.calls.length;
      act(() => ref.current!.jumpTo({ kind: 'At', at }));
      await flush();
      expect(client.fetchBefore).toHaveBeenCalledWith({ cursor: at, limit: 10 });
      expect(client.fetchSince.mock.calls[since]).toEqual([{ cursor: at, limit: 10 }]);

      since = client.fetchSince.mock.calls.length;
      act(() => ref.current!.jumpTo(HEAD));
      await flush();
      expect(client.fetchSince.mock.calls[since]).toEqual([{ limit: 10 }]);
      expect(shown()[0]).toEqual([0, 'pod-1 line 0']);
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
