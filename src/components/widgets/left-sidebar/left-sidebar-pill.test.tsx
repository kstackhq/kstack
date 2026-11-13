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
import { createRootRoute, createRoute, Outlet } from '@tanstack/react-router';
import { act, screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { renderWithRouter } from '@/test-utils';

import { LeftSidebarPill } from './left-sidebar-pill';

// Helpers -------------------------------------------------------------

// The pill plus two destination pages, so navigation can be observed by which
// page renders. The toggle reads the provider the layout mounts.
function buildTree() {
  const root = createRootRoute({
    component: () => (
      <SidebarProvider>
        <LeftSidebarPill />
        <Outlet />
      </SidebarProvider>
    ),
  });
  const chat = createRoute({
    getParentRoute: () => root,
    path: '/chat',
    component: () => <div>chat-page</div>,
  });
  const dashboard = createRoute({
    getParentRoute: () => root,
    path: '/dashboard',
    component: () => <div>dashboard-page</div>,
  });
  return root.addChildren([chat, dashboard]);
}

// Tests ---------------------------------------------------------------

describe('LeftSidebarPill', () => {
  it('carries the toggle and the mode switch', async () => {
    await renderWithRouter(buildTree(), '/chat');
    const pill = screen.getByTestId('left-sidebar-pill');
    expect(pill.contains(screen.getByRole('button', { name: /toggle sidebar/i }))).toBe(true);
    expect(screen.getByRole('link', { name: 'Chat' })).toHaveAttribute('href', '/chat');
    expect(screen.getByRole('link', { name: 'Dashboard' })).toHaveAttribute('href', '/dashboard');
  });

  it('marks only the current route active', async () => {
    await renderWithRouter(buildTree(), '/dashboard');
    expect(screen.getByRole('link', { name: 'Dashboard' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('link', { name: 'Chat' })).not.toHaveAttribute('aria-current');
  });

  it('navigates between modes from the collapsed pill', async () => {
    await renderWithRouter(buildTree(), '/chat');
    expect(screen.getByText('chat-page')).toBeInTheDocument();

    act(() => {
      screen.getByRole('link', { name: 'Dashboard' }).click();
    });

    await waitFor(() => expect(screen.getByText('dashboard-page')).toBeInTheDocument());
  });

  it('sits in the row as an ordinary flex item', async () => {
    await renderWithRouter(buildTree(), '/chat');
    // Same footprint role as the open card: the page takes the rest of the row.
    expect(screen.getByTestId('left-sidebar-pill')).toHaveClass('shrink-0');
    expect(screen.getByTestId('left-sidebar-pill')).not.toHaveClass('absolute', 'fixed');
  });
});
