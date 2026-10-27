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

import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ResizeHandle } from './resize-handle';

// Helpers -------------------------------------------------------------

// jsdom doesn't implement pointer capture; the handle calls it, so stub it out.
if (!HTMLElement.prototype.setPointerCapture) {
  HTMLElement.prototype.setPointerCapture = () => {};
}

const handle = () => screen.getByRole('separator', { name: 'Resize' });

const drag = (clientX: number) => {
  fireEvent.pointerDown(handle(), { pointerId: 1 });
  fireEvent.pointerMove(window, { clientX });
};

beforeEach(() => {
  // The right-edge arithmetic measures against the window.
  Object.defineProperty(window, 'innerWidth', { value: 1000, configurable: true });
});

// A drag that a test leaves mid-flight keeps its window listeners and the forced
// cursor, which the next test would inherit.
afterEach(() => {
  fireEvent.pointerUp(window);
});

// Tests ---------------------------------------------------------------

describe('ResizeHandle', () => {
  it('reports the pointer’s x for a pane anchored at the left', () => {
    const onResize = vi.fn();
    render(<ResizeHandle edge="right" onResize={onResize} label="Resize" />);

    drag(300);
    expect(onResize).toHaveBeenCalledWith(300);
  });

  it('measures from the pane’s left edge, wherever the pane sits', () => {
    const onResize = vi.fn();
    render(
      <div>
        <ResizeHandle edge="right" onResize={onResize} label="Resize" />
      </div>,
    );
    vi.spyOn(handle().parentElement!, 'getBoundingClientRect').mockReturnValue(new DOMRect(120, 0, 140, 400));

    drag(300);
    expect(onResize).toHaveBeenCalledWith(180);
  });

  it('reports what is left of the window for a pane anchored at the right', () => {
    const onResize = vi.fn();
    render(<ResizeHandle edge="left" onResize={onResize} label="Resize" />);

    drag(300);
    expect(onResize).toHaveBeenCalledWith(700);
  });

  it('adds the offset between the pane’s box and the border being dragged', () => {
    const onResize = vi.fn();
    render(<ResizeHandle edge="right" offset={8} onResize={onResize} label="Resize" />);

    drag(300);
    expect(onResize).toHaveBeenCalledWith(308);
  });

  it('forces the resize cursor for the whole drag, then restores it', () => {
    render(<ResizeHandle edge="right" onResize={() => {}} label="Resize" />);
    expect(document.body.style.cursor).toBe('');

    // Held even when the pointer strays off the thin strip.
    drag(300);
    expect(document.body.style.cursor).toBe('col-resize');

    fireEvent.pointerUp(window);
    expect(document.body.style.cursor).toBe('');
  });

  it('stops reporting once the pointer is released', () => {
    const onResize = vi.fn();
    render(<ResizeHandle edge="right" onResize={onResize} label="Resize" />);

    drag(300);
    fireEvent.pointerUp(window);
    fireEvent.pointerMove(window, { clientX: 400 });
    expect(onResize).toHaveBeenCalledTimes(1);
  });

  it('rides the edge it resizes from, straddling a right-edge border', () => {
    const { rerender } = render(<ResizeHandle edge="right" offset={8} onResize={() => {}} label="Resize" />);
    expect(handle().style.right).toBe('4px');

    // A border on the pane's own edge puts half the strip outside the pane.
    rerender(<ResizeHandle edge="right" onResize={() => {}} label="Resize" />);
    expect(handle().style.right).toBe('-4px');

    rerender(<ResizeHandle edge="left" onResize={() => {}} label="Resize" />);
    expect(handle()).toHaveClass('left-0');
  });
});
