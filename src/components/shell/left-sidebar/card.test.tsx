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
import { describe, expect, it, vi } from 'vitest';

import { LeftSidebarCard } from './card';

// jsdom doesn't implement pointer capture; the handle calls it, so stub it out.
if (!HTMLElement.prototype.setPointerCapture) {
  HTMLElement.prototype.setPointerCapture = () => {};
}

// Tests ---------------------------------------------------------------

describe('LeftSidebarCard', () => {
  it('scrolls the nav inside the column', () => {
    const { container } = render(<LeftSidebarCard width={256} onResize={() => {}} nav={<div>nav-content</div>} />);
    const scroller = container.querySelector('[data-slot="sidebar-content"]')!;
    expect(scroller.contains(screen.getByText('nav-content'))).toBe(true);
  });

  it('is a tinted column of the width it is given, as an ordinary flex item', () => {
    render(<LeftSidebarCard width={300} onResize={() => {}} />);
    const card = screen.getByTestId('left-sidebar-card');
    // The layout holds the width, so the app bar's gutter reads the same value live.
    expect(card.style.width).toBe('300px');
    expect(card).toHaveClass('shrink-0', 'bg-app-bar', 'p-2');
    expect(card).not.toHaveClass('absolute', 'fixed');
  });

  it('carries no logo, toggle or header of its own', () => {
    render(<LeftSidebarCard width={256} onResize={() => {}} />);
    // The toggle is the app bar's and the mode switch the rail's.
    expect(screen.queryByTestId('app-logo')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /toggle sidebar/i })).not.toBeInTheDocument();
  });

  it('reports the width a drag of its right edge implies', () => {
    const onResize = vi.fn();
    render(<LeftSidebarCard width={256} onResize={onResize} />);
    // The column's fill is its edge, so the pointer's x is its width.
    fireEvent.pointerDown(screen.getByRole('separator', { name: 'Resize sidebar' }), { pointerId: 1 });
    fireEvent.pointerMove(window, { clientX: 300 });
    expect(onResize).toHaveBeenLastCalledWith(300);
    fireEvent.pointerUp(window);
  });
});
