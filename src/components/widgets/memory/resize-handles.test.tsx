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
import { afterEach, describe, expect, it, vi } from 'vitest';

import { PopupResizeHandles } from './resize-handles';

// jsdom doesn't implement pointer capture; the handles call it, so stub it out.
if (!HTMLElement.prototype.setPointerCapture) {
  HTMLElement.prototype.setPointerCapture = () => {};
}

const handle = (name: string) => screen.getByRole('separator', { name: `Resize, ${name}` });

// A popup 400px wide at x 100 (centre 300), its top at y 50.
const setup = () => {
  const onWidth = vi.fn();
  const onHeight = vi.fn();
  render(
    <div data-testid="popup">
      <PopupResizeHandles onWidth={onWidth} onHeight={onHeight} label="Resize" />
    </div>,
  );
  vi.spyOn(screen.getByTestId('popup'), 'getBoundingClientRect').mockReturnValue(new DOMRect(100, 50, 400, 300));
  return { onWidth, onHeight };
};

const drag = (name: string, clientX: number, clientY: number) => {
  fireEvent.pointerDown(handle(name), { pointerId: 1 });
  fireEvent.pointerMove(window, { clientX, clientY });
};

// A drag a test leaves mid-flight keeps its window listeners and the forced cursor.
afterEach(() => {
  fireEvent.pointerUp(window);
});

describe('PopupResizeHandles', () => {
  it('grows the width on both sides from either side', () => {
    const { onWidth, onHeight } = setup();

    drag('right edge', 550, 0);
    expect(onWidth).toHaveBeenLastCalledWith(500);
    fireEvent.pointerUp(window);

    drag('left edge', 50, 0);
    expect(onWidth).toHaveBeenLastCalledWith(500);
    expect(onHeight).not.toHaveBeenCalled();
  });

  it('sets the height from the popup’s top', () => {
    const { onWidth, onHeight } = setup();

    drag('bottom edge', 0, 450);
    expect(onHeight).toHaveBeenLastCalledWith(400);
    expect(onWidth).not.toHaveBeenCalled();
  });

  it('sets both from a corner', () => {
    const { onWidth, onHeight } = setup();

    drag('bottom-left corner', 150, 450);
    expect(onWidth).toHaveBeenLastCalledWith(300);
    expect(onHeight).toHaveBeenLastCalledWith(400);
  });

  it('forces the edge’s cursor for the whole drag, then stops reporting', () => {
    const { onHeight } = setup();

    drag('bottom-right corner', 600, 450);
    expect(document.body.style.cursor).toBe('nwse-resize');

    fireEvent.pointerUp(window);
    expect(document.body.style.cursor).toBe('');
    fireEvent.pointerMove(window, { clientX: 600, clientY: 500 });
    expect(onHeight).toHaveBeenCalledTimes(1);
  });
});
