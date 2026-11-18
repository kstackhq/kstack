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

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useEffect } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { mockTauriCore } from '@/test-utils';

const { invokeMock, factory } = mockTauriCore();
vi.mock('@tauri-apps/api/core', () => factory());

// The tab's own behavior is its test's; here it only says when it is mounted.
const { clustersMounted } = vi.hoisted(() => ({ clustersMounted: vi.fn() }));
vi.mock('./clusters', () => ({
  ClustersSettings: () => {
    useEffect(() => {
      clustersMounted(true);
      return () => clustersMounted(false);
    }, []);
    return <p>The clusters</p>;
  },
}));

vi.mock('./sandbox', () => ({ SandboxSettings: () => <p>The sandbox</p> }));

const { ThemeProvider } = await import('@/lib/theme');
const { SettingsDialog, TABS } = await import('./dialog');

function renderDialog(onOpenChange = vi.fn()) {
  render(
    <ThemeProvider>
      <SettingsDialog open onOpenChange={onOpenChange} />
    </ThemeProvider>,
  );
  return onOpenChange;
}

const NAMES = ['General', 'Clusters', 'Connectors', 'Providers', 'Memory', 'Sandbox', 'Permissions'];

beforeEach(() => {
  invokeMock.mockReset();
  invokeMock.mockResolvedValue(undefined);
});

describe('SettingsDialog', () => {
  it('lists its tabs in a vertical tab list, General selected on open', async () => {
    renderDialog();
    await screen.findByRole('dialog', { name: 'Settings' });

    const list = screen.getByRole('tablist');
    expect(list).toHaveAttribute('aria-orientation', 'vertical');
    expect(screen.getAllByRole('tab').map((t) => t.textContent)).toEqual(NAMES);
    expect(screen.getByRole('tab', { name: 'General', selected: true })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'General' })).toBeInTheDocument();
    expect(screen.getByRole('group', { name: 'Appearance' })).toBeInTheDocument();
  });

  const comingSoon = TABS.filter((t) => !t.content).map((t) => t.label);

  it.each(comingSoon)('shows %s by its name, and Coming soon', async (name) => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(await screen.findByRole('tab', { name }));

    expect(screen.getByRole('tab', { name, selected: true })).toBeInTheDocument();
    const panel = screen.getByRole('tabpanel');
    expect(panel).toHaveTextContent(name);
    expect(panel).toHaveTextContent('Coming soon');
  });

  it('draws the Sandbox tab', async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.click(await screen.findByRole('tab', { name: 'Sandbox' }));
    expect(screen.getByRole('heading', { name: 'Sandbox' })).toBeInTheDocument();
    expect(screen.getByRole('tabpanel')).toHaveTextContent('The sandbox');
  });

  // Each row's cache size is a live watch; it must not run behind another tab.
  it('draws the Clusters tab while it is selected, and lets it go after', async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(await screen.findByRole('tab', { name: 'Clusters' }));
    expect(screen.getByRole('tabpanel')).toHaveTextContent('The clusters');
    expect(clustersMounted).toHaveBeenLastCalledWith(true);

    await user.click(screen.getByRole('tab', { name: 'General' }));
    expect(clustersMounted).toHaveBeenLastCalledWith(false);
    expect(screen.queryByText('The clusters')).toBeNull();
  });

  it('moves between tabs with the arrow keys', async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(await screen.findByRole('tab', { name: 'General' }));
    await user.keyboard('{ArrowDown}');

    expect(screen.getByRole('tab', { name: 'Clusters' })).toHaveFocus();
    expect(screen.getByRole('heading', { name: 'Clusters' })).toBeInTheDocument();

    await user.keyboard('{ArrowUp}');
    expect(screen.getByRole('tab', { name: 'General', selected: true })).toHaveFocus();
  });

  it('closes from the Close button in the pane header', async () => {
    const user = userEvent.setup();
    const onOpenChange = renderDialog();

    await user.click(await screen.findByRole('button', { name: 'Close' }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
