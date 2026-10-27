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

import { dragFrom } from './pointer-drag';

// jsdom doesn't implement pointer capture; the drag calls it, so stub it out.
if (!HTMLElement.prototype.setPointerCapture) {
  HTMLElement.prototype.setPointerCapture = () => {};
}

const setup = () => {
  const onMove = vi.fn();
  render(<div role="separator" aria-label="Resize" onPointerDown={(e) => dragFrom(e, 'ns-resize', onMove)} />);
  fireEvent.pointerDown(screen.getByRole('separator'), { pointerId: 1 });
  return onMove;
};

// A drag a test leaves mid-flight keeps its window listeners and the forced
// cursor, which the next test would inherit.
afterEach(() => {
  fireEvent.pointerUp(window);
});

describe('dragFrom', () => {
  it('follows the pointer across the window until it lifts', () => {
    const onMove = setup();

    fireEvent.pointerMove(window, { clientX: 10, clientY: 20 });
    expect(onMove).toHaveBeenCalledTimes(1);
    expect(onMove.mock.calls[0][0]).toMatchObject({ clientX: 10, clientY: 20 });

    fireEvent.pointerUp(window);
    fireEvent.pointerMove(window, { clientX: 30 });
    expect(onMove).toHaveBeenCalledTimes(1);
  });

  it('forces the cursor and disables selection for the drag alone', () => {
    document.body.style.cursor = 'pointer';
    setup();
    expect(document.body.style.cursor).toBe('ns-resize');
    expect(document.body.style.userSelect).toBe('none');

    fireEvent.pointerUp(window);
    expect(document.body.style.cursor).toBe('pointer');
    expect(document.body.style.userSelect).toBe('');
    document.body.style.cursor = '';
  });
});
