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

import { createRootRoute, createRoute, Outlet } from '@tanstack/react-router';
import { act, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { renderWithRouter } from '@/test-utils';

// Mocks ---------------------------------------------------------------

// The bell and the gear have suites of their own; here they only mark their place.
vi.mock('./notification-button', () => ({
  NotificationButton: () => <div data-testid="notification-button" />,
}));
vi.mock('./settings-button', () => ({
  SettingsButton: () => <div data-testid="settings-button" />,
}));

const { ModeRail } = await import('./rail');

// Helpers -------------------------------------------------------------

// The rail plus two destination pages, so navigation can be observed by which page
// renders.
function buildTree() {
  const root = createRootRoute({
    component: () => (
      <>
        <ModeRail />
        <Outlet />
      </>
    ),
  });
  const chat = createRoute({ getParentRoute: () => root, path: '/chat', component: () => <div>chat-page</div> });
  const dashboard = createRoute({
    getParentRoute: () => root,
    path: '/dashboard',
    component: () => <div>dashboard-page</div>,
  });
  return root.addChildren([chat, dashboard]);
}

// Tests ---------------------------------------------------------------

describe('ModeRail', () => {
  it('links to the chat and dashboard routes in the View nav', async () => {
    await renderWithRouter(buildTree(), '/chat');
    const nav = screen.getByRole('navigation', { name: 'View' });
    const chat = screen.getByRole('link', { name: 'Chat' });
    const dashboard = screen.getByRole('link', { name: 'Dashboard' });
    expect(chat).toHaveAttribute('href', '/chat');
    expect(dashboard).toHaveAttribute('href', '/dashboard');
    expect(nav.contains(chat) && nav.contains(dashboard)).toBe(true);
  });

  it('names each mode in a tooltip and keeps its icon out of the name', async () => {
    await renderWithRouter(buildTree(), '/chat');
    ['Chat', 'Dashboard'].forEach((name) => {
      const link = screen.getByRole('link', { name });
      expect(link).toHaveAttribute('title', name);
      expect(link.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
    });
  });

  it('marks only the current mode active', async () => {
    await renderWithRouter(buildTree(), '/dashboard');
    expect(screen.getByRole('link', { name: 'Dashboard' })).toHaveAttribute('data-status', 'active');
    expect(screen.getByRole('link', { name: 'Dashboard' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('link', { name: 'Chat' })).not.toHaveAttribute('aria-current');
  });

  it('switches modes', async () => {
    await renderWithRouter(buildTree(), '/chat');
    act(() => {
      screen.getByRole('link', { name: 'Dashboard' }).click();
    });
    await waitFor(() => expect(screen.getByText('dashboard-page')).toBeInTheDocument());
    expect(screen.getByRole('link', { name: 'Dashboard' })).toHaveAttribute('aria-current', 'page');
  });

  it('puts the modes at the top and notifications and settings at the bottom', async () => {
    await renderWithRouter(buildTree(), '/chat');
    const rail = screen.getByTestId('mode-rail');
    const [top, bottom] = Array.from(rail.children);
    expect(top).toBe(screen.getByRole('navigation', { name: 'View' }));
    expect(Array.from(bottom.children)).toEqual([
      screen.getByTestId('notification-button'),
      screen.getByTestId('settings-button'),
    ]);
    // The rail is a column, so the bottom group sits at its foot.
    expect(rail).toHaveClass('flex-col', 'justify-between');
  });
});
