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

import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { StrictMode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Client, FetchQuery, FollowFrame, LogLine, Page } from '@/lib/log-viewer';
import type { LogsClient } from '@/lib/logs-client';
import type { LogsViewAction } from '@/lib/logs-view';
import { createLogViewportHandle } from '@/lib/log-viewport';

// The seam: the client the viewer builds is the test's.
const { built } = vi.hoisted(() => ({ built: { current: null as LogsClient | null, calls: [] as unknown[][] } }));
vi.mock('@/lib/logs-client', () => ({
  createLogsClient: (...args: unknown[]) => {
    built.calls.push(args);
    return built.current;
  },
}));

const { reportError } = vi.hoisted(() => ({ reportError: vi.fn() }));
vi.mock('@/lib/error-bus', () => ({ reportError }));

const { LogViewer } = await import('./log-viewer');
const { LOG_ROW_PX } = await import('./row');

// --- Layout. jsdom lays nothing out: the scroller is 200px tall, its content the sizer the
// viewer draws, and `scrollTop` clamps and fires `scroll` as a browser's does. ---

const VIEW = 200;
// The core's scroller: focusable, with its own overflow. A popover trigger is focusable too.
const SCROLLER = '[tabindex="0"][style*="overflow"]';
const isScroller = (el: HTMLElement) => el.matches(SCROLLER);
const sizerHeight = (el: HTMLElement) =>
  parseFloat((el.firstElementChild as HTMLElement | null)?.style.height ?? '') || 0;
const tops = new WeakMap<HTMLElement, number>();
const stubbed: [string, PropertyDescriptor | undefined][] = [];

function stub(name: string, descriptor: PropertyDescriptor) {
  stubbed.push([name, Object.getOwnPropertyDescriptor(HTMLElement.prototype, name)]);
  Object.defineProperty(HTMLElement.prototype, name, { configurable: true, ...descriptor });
}

function stubLayout() {
  const height = (el: HTMLElement) => (isScroller(el) ? VIEW : 0);
  stub('offsetHeight', {
    get: function get(this: HTMLElement) {
      return height(this);
    },
  });
  stub('clientHeight', {
    get: function get(this: HTMLElement) {
      return height(this);
    },
  });
  stub('offsetWidth', { get: () => 300 });
  stub('scrollHeight', {
    get: function get(this: HTMLElement) {
      return isScroller(this) ? Math.max(VIEW, sizerHeight(this)) : 0;
    },
  });
  stub('scrollTop', {
    get: function get(this: HTMLElement) {
      return tops.get(this) ?? 0;
    },
    set: function set(this: HTMLElement, value: number) {
      const next = Math.min(Math.max(0, Math.round(value)), Math.max(0, sizerHeight(this) - VIEW));
      if (next === (tops.get(this) ?? 0)) return;
      tops.set(this, next);
      this.dispatchEvent(new Event('scroll'));
    },
  });
  stub('scrollTo', {
    value: function scrollTo(this: HTMLElement, options: ScrollToOptions) {
      if (options.top !== undefined) this.scrollTop = options.top;
    },
  });
  vi.stubGlobal(
    'ResizeObserver',
    class {
      // eslint-disable-next-line class-methods-use-this -- the stub's sizes never change
      observe() {}

      // eslint-disable-next-line class-methods-use-this -- the stub's sizes never change
      unobserve() {}

      // eslint-disable-next-line class-methods-use-this -- a fake has nothing to release
      disconnect() {}
    },
  );
}

function unstubLayout() {
  stubbed.reverse().forEach(([name, descriptor]) => {
    if (descriptor) Object.defineProperty(HTMLElement.prototype, name, descriptor);
    else delete (HTMLElement.prototype as unknown as Record<string, unknown>)[name];
  });
  stubbed.length = 0;
}

// --- Lines, pages and a client the test answers by hand. Line `i` is stamped `i` seconds
// past 09:00 local, today. ---

function line(i: number, over: Partial<LogLine['stream']> = {}): LogLine {
  return {
    timestamp: new Date(2026, 9, 15, 9, 0, i).toISOString(),
    cursor: `c${i}`,
    message: `line ${i}`,
    stream: { namespace: 'prod', pod: 'webapp', container: 'app', previous: false, ...over },
    place: { node: '', region: '', zone: '', os: '', arch: '' },
  };
}

const lines = (from: number, to: number) => Array.from({ length: to - from }, (_, k) => line(from + k));

function page(from: number, to: number, over: Partial<Page> = {}): Page {
  return {
    lines: lines(from, to),
    before: { cursor: `before-${from}`, more: true },
    after: { cursor: `after-${to}`, more: true },
    anchor: null,
    skipped: [],
    ...over,
  };
}

const START = { cursor: null, more: false };
const END = { cursor: 'live-end', more: false };

type Fetch = { query: FetchQuery; resolve: (p: Page) => void; reject: (error: unknown) => void };
type Follow = { onFrame: (frame: FollowFrame) => void; closed: boolean };

function scripted() {
  const fetches: Fetch[] = [];
  const follows: Follow[] = [];
  const refusal = { next: null as Error | null };
  const client: Client = {
    fetch: (query) =>
      new Promise<Page>((resolve, reject) => {
        fetches.push({ query, resolve, reject });
      }),
    follow: (_after, onFrame) => {
      if (refusal.next) {
        const error = refusal.next;
        refusal.next = null;
        throw error;
      }
      const follow: Follow = { onFrame, closed: false };
      follows.push(follow);
      return () => {
        follow.closed = true;
      };
    },
  };
  const stop = vi.fn();
  const start = vi.fn(() => stop);
  built.current = { client, start };
  return { fetches, follows, refusal, start, stop, last: () => fetches[fetches.length - 1] };
}

// --- Mounting. ---

const source = (over: Partial<LogsViewAction['sources'][number]> = {}): LogsViewAction['sources'][number] => ({
  namespace: 'prod',
  kind: 'Deployment',
  name: 'webapp',
  containers: [],
  allContainers: false,
  previous: false,
  ...over,
});

const action = (over: Partial<LogsViewAction> = {}): LogsViewAction => ({
  sources: [source()],
  filters: [],
  grep: '',
  anchor: { kind: 'Tail', at: null },
  pinToEnd: true,
  ...over,
});

function mount(query: LogsViewAction = action()) {
  const viewport = createLogViewportHandle();
  const view = render(
    <StrictMode>
      <LogViewer
        clusterId="cluster-1"
        query={query}
        openAt={query.anchor}
        pinToEnd={query.pinToEnd}
        viewport={viewport}
      />
    </StrictMode>,
  );
  return { ...view, viewport };
}

const rowTexts = () => screen.queryAllByTestId('log-row').map((row) => row.textContent ?? '');

async function frame(count = 1) {
  for (let i = 0; i < count; i += 1) {
    // eslint-disable-next-line no-await-in-loop -- each frame is its own act
    await act(async () => {
      await vi.advanceTimersByTimeAsync(16);
    });
  }
}

async function answer(fetch: Fetch, p: Page) {
  await act(async () => {
    fetch.resolve(p);
  });
  await frame(2);
}

async function refuse(fetch: Fetch) {
  await act(async () => {
    fetch.reject(new Error('fetch refused'));
  });
  await frame(2);
}

beforeEach(() => {
  vi.useFakeTimers({
    toFake: [
      'setTimeout',
      'clearTimeout',
      'setInterval',
      'clearInterval',
      'requestAnimationFrame',
      'cancelAnimationFrame',
      'Date',
    ],
  });
  vi.setSystemTime(new Date(2026, 9, 15, 12, 0, 0));
  built.calls = [];
  reportError.mockReset();
  stubLayout();
});

afterEach(() => {
  cleanup();
  localStorage.clear();
  unstubLayout();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('LogViewer', () => {
  it('opens at the tail with the last page drawn as rows', async () => {
    const s = scripted();
    mount();
    expect(built.calls[0]).toEqual(['cluster-1', action()]);
    expect(s.fetches[0].query).toEqual({ mode: 'Tail', cursor: null, at: null, limit: 300 });

    await answer(s.fetches[0], page(0, 20, { before: START, after: END }));
    expect(rowTexts().at(-1)).toContain('line 19');
    expect(rowTexts().at(-1)).toContain('09:00:19.000');
  });

  it('draws Loading over the header and the rows until the landing ends', async () => {
    const s = scripted();
    mount();
    expect(screen.getByText('Loading')).toBeInTheDocument();
    await answer(s.fetches[0], page(0, 20, { before: START, after: END }));
    expect(screen.queryByText('Loading')).not.toBeInTheDocument();
  });

  it('opens at a moment with that line marked as where it landed', async () => {
    const s = scripted();
    const at = line(30).timestamp;
    mount(action({ anchor: { kind: 'At', at }, pinToEnd: false }));
    expect(s.fetches[0].query).toEqual({ mode: 'Around', cursor: null, at, limit: 300 });

    await answer(s.fetches[0], page(20, 40, { anchor: 10 }));
    const landed = screen.getAllByTestId('log-row').find((row) => row.textContent?.includes('line 30'))!;
    expect(landed.querySelector('[data-col="Pos"]')).toHaveTextContent(/^0$/);
    expect(landed.querySelector('[data-col="Pos"]')).toHaveClass('border-l-2');
  });

  it('adds a row for a followed line and keeps the bottom while pinned', async () => {
    const s = scripted();
    const { container } = mount();
    await answer(s.fetches[0], page(0, 20, { before: START, after: END }));
    expect(s.follows.filter((f) => !f.closed)).toHaveLength(1);

    await act(async () => {
      s.follows.find((f) => !f.closed)!.onFrame({ kind: 'Line', line: line(20) });
    });
    await frame(2);
    expect(rowTexts().at(-1)).toContain('line 20');
    const scroller = container.querySelector<HTMLElement>(SCROLLER)!;
    expect(scroller.scrollTop).toBe(scroller.scrollHeight - VIEW);
  });

  it('writes what is on screen into the viewport, with nothing selected', async () => {
    const s = scripted();
    const { viewport } = mount();
    await answer(s.fetches[0], page(0, 20, { before: START, after: END }));

    // Pinned to the end of 20 lines, the view shows the last it has room for.
    const shown = Math.ceil(VIEW / LOG_ROW_PX);
    expect(viewport.get()).toEqual({
      range: { from: line(20 - shown).timestamp, to: line(19).timestamp },
      atEnd: true,
      pinnedToEnd: true,
      visible: shown,
      selection: [],
    });
  });

  it('starts the client once mounted, and on unmount stops it and empties the viewport', async () => {
    const s = scripted();
    const { viewport, unmount } = mount();
    await answer(s.fetches[0], page(0, 20, { before: START, after: END }));
    expect(s.start).toHaveBeenCalled();
    expect(s.stop.mock.calls.length).toBe(s.start.mock.calls.length - 1);

    unmount();
    expect(s.stop.mock.calls.length).toBe(s.start.mock.calls.length);
    expect(viewport.get()).toEqual({ range: null, atEnd: false, pinnedToEnd: false, visible: 0, selection: [] });
  });

  describe('errors', () => {
    it('keeps the rows when a page fails and says so, until a page lands', async () => {
      const s = scripted();
      mount(action({ pinToEnd: false }));
      await answer(s.fetches[0], page(100, 120, { after: END }));
      // Unpinned, it pages both ways at once.
      expect(s.fetches).toHaveLength(3);
      const [, before, after] = s.fetches;

      await refuse(before);
      expect(screen.getByText('Could not load more lines.')).toBeInTheDocument();
      expect(rowTexts().length).toBeGreaterThan(0);
      expect(reportError).toHaveBeenCalledWith(expect.objectContaining({ source: 'network' }));

      await answer(after, page(120, 120, { after: { cursor: 'on', more: false } }));
      expect(screen.queryByText('Could not load more lines.')).not.toBeInTheDocument();
    });

    it('draws a refused follow’s reason, spelled out', async () => {
      const s = scripted();
      s.refusal.next = new Error('follow refused: pod\u202Egone');
      mount();
      await answer(s.fetches[0], page(0, 20, { before: START, after: END }));
      const reason = screen.getByText(/follow refused: pod/);
      expect(reason.querySelector('mark')).not.toBeNull();
    });

    it('draws why the follow ended', async () => {
      const s = scripted();
      mount();
      await answer(s.fetches[0], page(0, 20, { before: START, after: END }));
      await act(async () => {
        s.follows[s.follows.length - 1].onFrame({ kind: 'Ended', reason: 'the pod was deleted' });
      });
      await frame(2);
      expect(screen.getByText(/No new lines:/)).toHaveTextContent('No new lines: the pod was deleted');
    });

    it('offers Try again after a failed landing, which lands at the view’s anchor unpinned as it said', async () => {
      const s = scripted();
      const { viewport } = mount(action({ pinToEnd: false }));
      await refuse(s.fetches[0]);
      expect(screen.getByText('Could not load lines.')).toBeInTheDocument();
      expect(screen.queryByText('Could not load more lines.')).not.toBeInTheDocument();

      fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
      await frame();
      expect(s.last().query).toEqual({ mode: 'Tail', cursor: null, at: null, limit: 300 });
      expect(screen.queryByText('Could not load more lines.')).not.toBeInTheDocument();
      expect(screen.queryByText('Could not load lines.')).not.toBeInTheDocument();

      await answer(s.last(), page(0, 20, { before: START, after: END }));
      expect(rowTexts().length).toBeGreaterThan(0);
      expect(viewport.get().pinnedToEnd).toBe(false);
      expect(screen.queryByText('Could not load more lines.')).not.toBeInTheDocument();
    });

    it('says when a view has no lines', async () => {
      const s = scripted();
      mount();
      await answer(s.fetches[0], page(0, 0, { before: START, after: END }));
      expect(screen.getByText('No lines.')).toBeInTheDocument();
    });
  });

  describe('edge rows', () => {
    it('say Loading… only while a page that way is in flight', async () => {
      const s = scripted();
      mount(action({ pinToEnd: false }));
      await answer(s.fetches[0], page(100, 120, { after: END }));
      expect(screen.getAllByText('Loading…')).toHaveLength(2);

      // Behind, the after edge still has more, and says nothing with no page asked.
      await answer(
        s.fetches.find((f) => f.query.mode === 'Head')!,
        page(120, 120, { after: { cursor: 'on', more: false } }),
      );
      expect(screen.getAllByText('Loading…')).toHaveLength(1);
    });

    it('offer Keep looking after an empty page, which pages that way', async () => {
      const s = scripted();
      mount(action({ pinToEnd: false }));
      await answer(s.fetches[0], page(100, 120, { after: END }));
      await answer(
        s.fetches.find((f) => f.query.mode === 'Tail' && f.query.cursor !== null)!,
        page(100, 100),
      );
      expect(screen.getByText('No more in the last read')).toBeInTheDocument();

      const asked = s.fetches.length;
      fireEvent.click(screen.getByRole('button', { name: 'Keep looking' }));
      expect(s.fetches.length).toBe(asked + 1);
      expect(s.last().query).toMatchObject({ mode: 'Tail', cursor: 'before-100' });
    });
  });

  it('names the streams a page could not read', async () => {
    const s = scripted();
    mount();
    await answer(s.fetches[0], page(0, 20, { before: START, after: END, skipped: ['prod/db-0/app: forbidden'] }));
    expect(screen.getByText(/Not read:/)).toHaveTextContent('Not read: prod/db-0/app: forbidden');
  });

  it('draws the pod and the container as separate cells once both are picked', async () => {
    localStorage.setItem('kstack:log-viewer-columns', '["Pod","Container"]');
    const s = scripted();
    mount(action({ sources: [source({ allContainers: true })] }));
    await answer(s.fetches[0], page(0, 20, { before: START, after: END }));
    const last = screen.getAllByTestId('log-row').at(-1)!;
    expect(last.querySelector('[data-col="Pod"]')).toHaveTextContent(/^webapp$/);
    expect(last.querySelector('[data-col="Container"]')).toHaveTextContent(/^app$/);
  });

  describe('the toolbar', () => {
    const press = (name: string) => fireEvent.click(screen.getByRole('button', { name }));
    const open = (s: ReturnType<typeof scripted>) => s.follows.filter((f) => !f.closed);

    it('is disabled while the view loads', () => {
      scripted();
      mount();
      expect(screen.getByRole('button', { name: 'Jump to the start' })).toBeDisabled();
    });

    it('jumps to the start, and to the end, which pins', async () => {
      const s = scripted();
      const { viewport } = mount(action({ pinToEnd: false }));
      await answer(s.fetches[0], page(0, 20, { before: START, after: END }));

      press('Jump to the start');
      await frame();
      expect(s.last().query).toMatchObject({ mode: 'Head', cursor: null });
      await answer(s.last(), page(0, 20, { before: START, after: END }));
      expect(viewport.get().pinnedToEnd).toBe(false);

      press('Jump to the end');
      await frame();
      expect(s.last().query).toMatchObject({ mode: 'Tail', cursor: null });
      await answer(s.last(), page(0, 20, { before: START, after: END }));
      expect(viewport.get().pinnedToEnd).toBe(true);
    });

    it('pauses at the bottom with no lines arriving, and plays again from there', async () => {
      const s = scripted();
      mount();
      await answer(s.fetches[0], page(0, 20, { before: START, after: END }));
      expect(open(s)).toHaveLength(1);

      press('Pause');
      await frame();
      expect(open(s)).toHaveLength(0);
      expect(rowTexts().at(-1)).toContain('line 19');

      press('Play');
      await frame();
      expect(open(s)).toHaveLength(1);
      await act(async () => {
        open(s)[0].onFrame({ kind: 'Line', line: line(20) });
      });
      await frame(2);
      expect(rowTexts().at(-1)).toContain('line 20');
    });

    it('keeps the columns and wrap the user picks for the next window', async () => {
      const s = scripted();
      mount();
      await answer(s.fetches[0], page(0, 20, { before: START, after: END }));
      press('View');
      await frame(2);
      fireEvent.click(screen.getByRole('checkbox', { name: 'Pod' }));
      fireEvent.click(screen.getByRole('checkbox', { name: 'Wrap' }));
      await frame();
      expect(screen.getAllByTestId('log-row').at(-1)!.querySelector('[data-col="Pod"]')).not.toBeNull();
      expect(localStorage.getItem('kstack:log-viewer-columns')).toBe('["Timestamp","Dot","Pod"]');
      expect(localStorage.getItem('kstack:log-viewer-wrap')).toBe('true');
    });
  });

  describe('wrap', () => {
    // A wrapped row measures by its box; the long one is three lines tall here.
    const rowHeight = (el: Element) => (el.textContent?.includes('long') ? 60 : 20);

    beforeEach(() => {
      stub('getBoundingClientRect', {
        value: function rect(this: HTMLElement) {
          return { height: this.dataset.testid === 'log-row' ? rowHeight(this) : 0, width: 0 } as DOMRect;
        },
      });
      // A browser's observer reports a size a frame after it starts watching, which is when
      // the virtualizer takes a row it skipped while its own scroll settled.
      vi.stubGlobal(
        'ResizeObserver',
        class {
          constructor(private readonly callback: ResizeObserverCallback) {}

          observe(target: Element) {
            requestAnimationFrame(() => this.callback([{ target } as ResizeObserverEntry], this));
          }

          // eslint-disable-next-line class-methods-use-this -- the stub's sizes never change
          unobserve() {}

          // eslint-disable-next-line class-methods-use-this -- a fake has nothing to release
          disconnect() {}
        },
      );
    });

    const withLong = () =>
      page(0, 20, {
        before: START,
        after: END,
        lines: lines(0, 20).map((l, i) => (i === 18 ? { ...l, message: `long ${'x'.repeat(300)}` } : l)),
      });
    const topOf = (text: string) =>
      screen.getAllByTestId('log-row').find((row) => row.textContent?.includes(text))!.style.transform;

    it('lets a long message’s row grow, and the next row starts below it', async () => {
      localStorage.setItem('kstack:log-viewer-wrap', 'true');
      const s = scripted();
      mount();
      await answer(s.fetches[0], withLong());
      const start = Number(/translateY\((\d+)px\)/.exec(topOf('long'))![1]);
      expect(topOf('line 19')).toBe(`translateY(${start + 60}px)`);
      expect(screen.getAllByTestId('log-row')[0].querySelector('[data-col="Message"]')).toHaveClass(
        'whitespace-pre-wrap',
      );
    });

    it('keeps every row one line tall with wrap off', async () => {
      const s = scripted();
      mount();
      await answer(s.fetches[0], withLong());
      const start = Number(/translateY\((\d+)px\)/.exec(topOf('long'))![1]);
      expect(topOf('line 19')).toBe(`translateY(${start + LOG_ROW_PX}px)`);
    });
  });

  it('names the columns over the rows', async () => {
    const s = scripted();
    mount();
    await answer(s.fetches[0], page(0, 20, { before: START, after: END }));
    expect(screen.getByTestId('log-header')).toHaveTextContent('PositionTimestampSourceMessage');
  });

  it('draws the default columns, with no pod', async () => {
    const s = scripted();
    mount();
    await answer(s.fetches[0], page(0, 20, { before: START, after: END }));
    const last = screen.getAllByTestId('log-row').at(-1)!;
    expect([...last.querySelectorAll<HTMLElement>('[data-col]')].map((el) => el.dataset.col)).toEqual([
      'Pos',
      'Timestamp',
      'Dot',
      'Message',
    ]);
  });
});
