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

import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Column } from '@/components/widgets/log-viewer-columns';
import { LogViewerHeader } from './log-viewer-header';

// jsdom keeps no scroll offsets, so `scrollLeft` is stored per element here.
const lefts = new WeakMap<Element, number>();
const original = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollLeft');

beforeEach(() => {
  Object.defineProperty(Element.prototype, 'scrollLeft', {
    configurable: true,
    get(this: Element) {
      return lefts.get(this) ?? 0;
    },
    set(this: Element, value: number) {
      lefts.set(this, value);
    },
  });
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(private readonly callback: () => void) {}

      observe() {
        this.callback();
      }

      // eslint-disable-next-line class-methods-use-this -- a fake has nothing to release
      disconnect() {}
    },
  );
});

afterEach(() => {
  if (original) Object.defineProperty(Element.prototype, 'scrollLeft', original);
  vi.unstubAllGlobals();
});

function scrollerOf({ offsetWidth = 300, clientWidth = 300 } = {}) {
  const el = document.createElement('div');
  Object.defineProperty(el, 'offsetWidth', { value: offsetWidth });
  Object.defineProperty(el, 'clientWidth', { value: clientWidth });
  return el;
}

const header = (columns: Column[], scroller: HTMLElement | null = null) => (
  <LogViewerHeader columns={columns} widths={new Map()} rowWidth={0} wrap={false} scroller={scroller} />
);

describe('LogViewerHeader', () => {
  it('names the position column, then the visible columns in order', () => {
    render(header(['Timestamp', 'Dot', 'Pod', 'Message']));
    const cells = [...screen.getByTestId('log-header').children].map((el) => el.textContent);
    expect(cells).toEqual(['Position', 'Timestamp', 'Source', 'Pod', 'Message']);
    // The position and the dot draw no name, only one for the screen reader.
    expect(screen.getByText('Position')).toHaveClass('sr-only');
    expect(screen.getByText('Source')).toHaveClass('sr-only');
  });

  it('cannot be scrolled by the user, and follows the scroller sideways', () => {
    const scroller = scrollerOf();
    const { container } = render(header(['Message'], scroller));
    const wrapper = container.firstElementChild as HTMLElement;
    expect(wrapper).toHaveClass('overflow-x-hidden');

    scroller.scrollLeft = 120;
    act(() => {
      scroller.dispatchEvent(new Event('scroll'));
    });
    expect(wrapper.scrollLeft).toBe(120);
  });

  it('reserves the scroller’s scrollbar width at its right edge', () => {
    render(header(['Message'], scrollerOf({ offsetWidth: 300, clientWidth: 285 })));
    expect(screen.getByTestId('log-header-gutter')).toHaveStyle({ width: '15px' });
  });

  it('draws no gutter for an overlay scrollbar', () => {
    render(header(['Message'], scrollerOf()));
    expect(screen.queryByTestId('log-header-gutter')).not.toBeInTheDocument();
  });
});
