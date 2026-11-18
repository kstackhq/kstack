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

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Connector } from '@/lib/connectors';

const { state } = vi.hoisted(() => ({ state: { connectors: [] as Connector[] } }));
vi.mock('@/lib/connectors', () => ({ useConnectors: () => state.connectors }));

const { ConnectorsSettings } = await import('./connectors');

function connector(name: string, connected: boolean, rest: Partial<Connector> = {}): Connector {
  return { id: name.toLowerCase(), name, detail: `${name} detail`, connected, ...rest };
}

beforeEach(() => {
  state.connectors = [connector('GitHub', true), connector('Google', false)];
});

describe('ConnectorsSettings', () => {
  it('lists every service in one list under its lede, by name over its detail', () => {
    state.connectors = [
      connector('GitHub', true),
      connector('OpenRouter', false),
      connector('AWS', true),
      connector('Google', false),
    ];
    render(<ConnectorsSettings />);

    expect(
      screen.getByText('Accounts Kstack signs in to. Model providers and tools use their credentials.'),
    ).toBeInTheDocument();
    expect(screen.queryByRole('heading')).toBeNull();
    expect(screen.getAllByRole('listitem').map((r) => r.textContent)).toEqual([
      'AWSAWS detail',
      'GitHubGitHub detail',
      'GoogleGoogle detailComing soon',
      'OpenRouterOpenRouter detailComing soon',
    ]);
  });

  it('spells out control characters in the name and the detail', () => {
    state.connectors = [connector('Git\u001b[2JHub', true, { detail: 'Signed in as \u202eoctocat' })];
    render(<ConnectorsSettings />);

    const row = screen.getByRole('listitem');
    expect(within(row).getAllByTitle('An invisible character')).toHaveLength(2);
    for (const raw of ['\u001b', '\u202e']) expect(row.textContent).not.toContain(raw);
  });

  it('offers a disabled Coming soon on an available service alone', () => {
    render(<ConnectorsSettings />);

    const row = (name: string) => screen.getByText(name).closest('li')!;
    expect(within(row('Google')).getByRole('button', { name: 'Coming soon' })).toBeDisabled();
    expect(within(row('GitHub')).queryByRole('button', { name: 'Coming soon' })).toBeNull();
  });

  describe('row menu', () => {
    async function openMenu(name: string) {
      await userEvent.setup().click(screen.getByRole('button', { name: `Actions for ${name}` }));
      return screen.findByRole('menu');
    }

    it('lists Manage, Reconnect and Disconnect, each disabled', async () => {
      state.connectors = [connector('Claude', true, { manageLabel: 'Manage on Claude' })];
      render(<ConnectorsSettings />);

      const menu = await openMenu('Claude');
      const items = within(menu).getAllByRole('menuitem');
      expect(items.map((i) => i.textContent)).toEqual(['Manage on Claude', 'Reconnect', 'Disconnect']);
      for (const item of items) expect(item).toHaveAttribute('aria-disabled', 'true');
      expect(within(menu).getByRole('separator')).toBeInTheDocument();
      expect(items[2]).toHaveAttribute('data-variant', 'destructive');
    });

    it('leaves out Manage for a service with no manage label', async () => {
      state.connectors = [connector('GitHub', true)];
      render(<ConnectorsSettings />);

      const items = within(await openMenu('GitHub')).getAllByRole('menuitem');
      expect(items.map((i) => i.textContent)).toEqual(['Reconnect', 'Disconnect']);
    });
  });
});
