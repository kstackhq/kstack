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
import { createRootRoute, createRoute, Link, Outlet } from '@tanstack/react-router';
import { act, fireEvent, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { mockMatchMedia, renderWithRouter } from '@/test-utils';

import { LeftSidebar } from './sidebar';

// Mocks ---------------------------------------------------------------

// This suite is about which of the two the switch shows; each stub keeps the toggle
// so collapsing and reopening can be driven the way the user does it. The real
// ones are covered beside them (`card.test.tsx`, `pill.test.tsx`).
vi.mock('./card', async () => {
  const { LeftSidebarToggle } = await import('./toggle');
  return {
    LeftSidebarCard: ({ header, nav }: { header?: ReactNode; nav?: ReactNode }) => (
      <div data-testid="left-sidebar-card">
        <LeftSidebarToggle />
        {header}
        {nav}
      </div>
    ),
  };
});
vi.mock('./pill', async () => {
  const { LeftSidebarToggle } = await import('./toggle');
  return {
    LeftSidebarPill: () => (
      <div data-testid="left-sidebar-pill">
        <LeftSidebarToggle />
      </div>
    ),
  };
});

// Helpers -------------------------------------------------------------

// The switch reads open/collapsed state from the provider the layout mounts, and
// closes an overlaid card on navigation — hence the router around it. The link is
// the one the overlay would carry.
function renderSidebar(props: { header?: ReactNode; nav?: ReactNode } = {}) {
  const root = createRootRoute({
    component: () => (
      <SidebarProvider>
        <LeftSidebar {...props} nav={props.nav ?? <Link to="/dashboard">go-dashboard</Link>} />
        <Outlet />
      </SidebarProvider>
    ),
  });
  const chat = createRoute({ getParentRoute: () => root, path: '/chat', component: () => null });
  const dashboard = createRoute({ getParentRoute: () => root, path: '/dashboard', component: () => null });
  return renderWithRouter(root.addChildren([chat, dashboard]), '/chat');
}

const clickToggle = () => fireEvent.click(screen.getByRole('button', { name: /toggle sidebar/i }));

// The library's own narrow signal reads `window.innerWidth` on every change event,
// so a width has to move with the media query for it to follow.
function narrowWindow(fireChange: (matches: boolean) => void) {
  return (narrow: boolean) => {
    Object.defineProperty(window, 'innerWidth', { value: narrow ? 600 : 1200, configurable: true });
    fireChange(narrow);
  };
}

const cardVisible = () => screen.queryByTestId('left-sidebar-card') !== null;
const overlaid = () => screen.queryByTestId('sidebar-overlay') !== null;
const pillVisible = () => screen.queryByTestId('left-sidebar-pill') !== null;

// Tests ---------------------------------------------------------------

describe('LeftSidebar', () => {
  it('shows the card by default and passes it the header and nav', async () => {
    await renderSidebar({ header: <div>header-content</div>, nav: <div>nav-content</div> });
    const card = screen.getByTestId('left-sidebar-card');
    expect(card.contains(screen.getByText('header-content'))).toBe(true);
    expect(card.contains(screen.getByText('nav-content'))).toBe(true);
    expect(pillVisible()).toBe(false);
  });

  it('swaps the card for the pill when collapsed, and back from the pill', async () => {
    await renderSidebar();
    clickToggle();
    expect(cardVisible()).toBe(false);
    expect(pillVisible()).toBe(true);

    clickToggle();
    expect(cardVisible()).toBe(true);
    expect(pillVisible()).toBe(false);
  });

  it('reopens the card on the keyboard shortcut', async () => {
    await renderSidebar();
    clickToggle();
    expect(cardVisible()).toBe(false);

    // `Cmd/Ctrl+B` is the library's own binding, beside the pill's toggle.
    fireEvent.keyDown(window, { key: 'b', ctrlKey: true });
    expect(cardVisible()).toBe(true);
    expect(pillVisible()).toBe(false);
  });

  it('remembers a manual collapse across a narrow→wide breakpoint round-trip', async () => {
    // `matches` here means "below the medium breakpoint".
    const setNarrow = narrowWindow(mockMatchMedia());
    await renderSidebar();
    expect(cardVisible()).toBe(true);
    clickToggle();
    expect(cardVisible()).toBe(false);

    // Narrow below md, then widen back: the sidebar must stay collapsed, honoring
    // the user's choice rather than auto-reopening.
    setNarrow(true);
    expect(cardVisible()).toBe(false);
    expect(pillVisible()).toBe(true);
    setNarrow(false);
    expect(cardVisible()).toBe(false);
    expect(pillVisible()).toBe(true);
  });

  it('collapses when the window narrows, rather than throwing a drawer over the page', async () => {
    const setNarrow = narrowWindow(mockMatchMedia());
    await renderSidebar();

    setNarrow(true);
    expect(cardVisible()).toBe(false);
    expect(pillVisible()).toBe(true);

    // Back in the row once there is space for it beside the page.
    setNarrow(false);
    expect(cardVisible()).toBe(true);
    expect(overlaid()).toBe(false);
  });

  it('floats the card over the page when the toggle asks for it while narrow', async () => {
    const setNarrow = narrowWindow(mockMatchMedia());
    await renderSidebar();
    setNarrow(true);

    clickToggle();
    // The pill keeps the row's width, so the page does not shift under the pointer.
    expect(pillVisible()).toBe(true);
    expect(cardVisible()).toBe(true);
    expect(overlaid()).toBe(true);
  });

  it('leaves a drawer behind when the window widens again', async () => {
    const setNarrow = narrowWindow(mockMatchMedia());
    await renderSidebar();
    setNarrow(true);
    clickToggle();
    expect(overlaid()).toBe(true);

    // The card belongs in the row up here, whatever the drawer was doing.
    setNarrow(false);
    expect(overlaid()).toBe(false);
    expect(cardVisible()).toBe(true);
  });

  it('keeps the keyboard in the drawer, and hands focus back on close', async () => {
    const setNarrow = narrowWindow(mockMatchMedia());
    await renderSidebar();
    setNarrow(true);
    // Focused first, the way a keyboard user reaches it: jsdom's click moves no
    // focus of its own, and it is where focus has to come back to.
    const toggle = screen.getByRole('button', { name: /toggle sidebar/i });
    toggle.focus();
    clickToggle();

    const drawer = screen.getByTestId('sidebar-overlay');
    expect(drawer).toHaveAttribute('aria-modal', 'true');
    expect(drawer.contains(document.activeElement)).toBe(true);

    fireEvent.click(screen.getByTestId('sidebar-scrim'));
    expect(document.activeElement).toBe(toggle);
  });

  it('closes the overlaid card from the scrim', async () => {
    const setNarrow = narrowWindow(mockMatchMedia());
    await renderSidebar();
    setNarrow(true);
    clickToggle();

    fireEvent.click(screen.getByTestId('sidebar-scrim'));
    expect(cardVisible()).toBe(false);
    expect(pillVisible()).toBe(true);
  });

  it('closes the overlaid card on Escape', async () => {
    const setNarrow = narrowWindow(mockMatchMedia());
    await renderSidebar();
    setNarrow(true);
    clickToggle();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(cardVisible()).toBe(false);
  });

  // A dialog opened from inside the drawer (the chat list's delete confirm) dismisses
  // on Escape by stopping the keydown at `document`. This listener is on `window`,
  // later in the bubble path, so one keypress closes the dialog alone — nothing else
  // in the code records that ordering.
  it('leaves the card open when an overlay inside it consumed the Escape', async () => {
    const setNarrow = narrowWindow(mockMatchMedia());
    await renderSidebar();
    setNarrow(true);
    clickToggle();

    const consume = (e: Event) => e.stopPropagation();
    document.addEventListener('keydown', consume);
    fireEvent.keyDown(screen.getByTestId('sidebar-overlay'), { key: 'Escape' });
    document.removeEventListener('keydown', consume);

    expect(cardVisible()).toBe(true);
  });

  it('closes the overlaid card when a link in it navigates', async () => {
    const setNarrow = narrowWindow(mockMatchMedia());
    await renderSidebar();
    setNarrow(true);
    clickToggle();
    expect(overlaid()).toBe(true);

    // Otherwise it sits over the page it just opened.
    await act(async () => {
      screen.getByRole('link', { name: 'go-dashboard' }).click();
    });
    expect(cardVisible()).toBe(false);
    expect(pillVisible()).toBe(true);
  });

  it('reopens the drawer from the pill’s toggle', async () => {
    const setNarrow = narrowWindow(mockMatchMedia());
    await renderSidebar();
    setNarrow(true);
    clickToggle();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(cardVisible()).toBe(false);

    // The toggle means the same thing at every width.
    clickToggle();
    expect(overlaid()).toBe(true);
  });
});
