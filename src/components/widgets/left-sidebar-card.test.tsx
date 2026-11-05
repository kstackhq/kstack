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

import { SidebarProvider } from '@kubetail/ui/elements/sidebar';
import { fireEvent, render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it } from 'vitest';

import { LeftSidebarCard } from './left-sidebar-card';

// Helpers -------------------------------------------------------------

beforeEach(() => {
  localStorage.clear();
});

// jsdom doesn't implement pointer capture; the handle calls it, so stub it out.
if (!HTMLElement.prototype.setPointerCapture) {
  HTMLElement.prototype.setPointerCapture = () => {};
}

// The toggle in the card's header reads the provider the layout mounts.
const renderCard = (props: { header?: ReactNode; nav?: ReactNode } = {}) =>
  render(
    <SidebarProvider>
      <LeftSidebarCard {...props} />
    </SidebarProvider>,
  );

// The card's width in the content row — the row the page shares.
const cardWidth = () => screen.getByTestId('left-sidebar-card').style.width;

// Tests ---------------------------------------------------------------

describe('LeftSidebarCard', () => {
  it('renders the nav in the card', () => {
    renderCard({ nav: <div>nav-content</div> });
    const card = screen.getByTestId('left-sidebar-card');
    expect(card.contains(screen.getByText('nav-content'))).toBe(true);
  });

  it('sits in the row as an ordinary flex item', () => {
    renderCard();
    // The row's height bounds the card, and the page takes the remaining width.
    expect(screen.getByTestId('left-sidebar-card')).toHaveClass('shrink-0');
    expect(screen.getByTestId('left-sidebar-card')).not.toHaveClass('absolute', 'fixed');
  });

  it('resizes by dragging the handle and persists the width', () => {
    renderCard();
    // Defaults to the library width until dragged.
    expect(cardWidth()).toBe('256px');

    // The handle is centered on the visible border (8px inside the card's right
    // edge), so the resulting width is the pointer x plus that padding.
    fireEvent.pointerDown(screen.getByRole('separator', { name: 'Resize sidebar' }), { pointerId: 1 });
    fireEvent.pointerMove(window, { clientX: 300 });
    expect(cardWidth()).toBe('308px');
    expect(localStorage.getItem('kstack:sidebar-width')).toBe('308');

    fireEvent.pointerUp(window);
    // Releasing detaches the listeners: further movement is ignored.
    fireEvent.pointerMove(window, { clientX: 400 });
    expect(cardWidth()).toBe('308px');
  });

  it('forces the resize cursor for the whole drag, then restores it', () => {
    renderCard();
    expect(document.body.style.cursor).toBe('');

    fireEvent.pointerDown(screen.getByRole('separator', { name: 'Resize sidebar' }), { pointerId: 1 });
    // Held even when the pointer strays off the thin handle.
    fireEvent.pointerMove(window, { clientX: 300 });
    expect(document.body.style.cursor).toBe('col-resize');

    fireEvent.pointerUp(window);
    expect(document.body.style.cursor).toBe('');
  });

  it('clamps the dragged width to the min/max bounds', () => {
    renderCard();
    fireEvent.pointerDown(screen.getByRole('separator', { name: 'Resize sidebar' }), { pointerId: 1 });

    fireEvent.pointerMove(window, { clientX: 9999 });
    expect(cardWidth()).toBe('480px');
    fireEvent.pointerMove(window, { clientX: 10 });
    expect(cardWidth()).toBe('200px');
  });

  it('restores the last-saved width on mount (new windows inherit it)', () => {
    localStorage.setItem('kstack:sidebar-width', '320');
    renderCard();
    expect(cardWidth()).toBe('320px');
  });

  it('pins the header above the scroll area, so a long nav cannot take it away', () => {
    const { container } = renderCard({ header: <div>header-content</div>, nav: <div>nav-content</div> });
    const scroller = container.querySelector('[data-slot="sidebar-content"]')!;
    const top = container.querySelector('[data-slot="sidebar-header"]')!;

    expect(top.contains(screen.getByText('header-content'))).toBe(true);
    expect(scroller.contains(screen.getByText('header-content'))).toBe(false);
    expect(scroller.contains(screen.getByText('nav-content'))).toBe(true);
  });

  it('keeps the toggle at the card’s fixed top, beside the header', () => {
    const { container } = renderCard({ header: <div>header-content</div> });
    const top = container.querySelector('[data-slot="sidebar-header"]')!;
    expect(top.contains(screen.getByRole('button', { name: /toggle sidebar/i }))).toBe(true);
  });

  it('opens with the named logo, and closes the row with the toggle', () => {
    renderCard();
    const logo = screen.getByTestId('app-logo');
    const toggle = screen.getByRole('button', { name: /toggle sidebar/i });

    expect(logo).toHaveTextContent('Kstack');
    // Logo left, toggle right — the row's two ends.
    expect(Array.from(logo.parentElement!.children)).toEqual([logo, toggle]);
    expect(logo.parentElement).toHaveClass('justify-between');
  });

  it('pads the card above and either side of its contents', () => {
    const { container } = renderCard({ nav: <div>nav-content</div> });
    // The header's own padding is the top and sides; the scroller matches the
    // sides and stays flush with the bottom, so a long list runs off the edge.
    expect(container.querySelector('[data-slot="sidebar-header"]')).toHaveClass('p-2');
    expect(container.querySelector('[data-slot="sidebar-content"]')).toHaveClass('px-2');
  });
});
