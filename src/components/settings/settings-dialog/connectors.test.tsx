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
  it('groups connected services above available ones, each with its hint', () => {
    render(<ConnectorsSettings />);

    const headings = screen.getAllByRole('group').map((g) => within(g).getByRole('heading').textContent);
    expect(headings).toEqual(['Connected', 'Available']);
    expect(screen.getByRole('group', { name: 'Connected' })).toHaveTextContent(
      'Services Kstack signs in to. Providers and tools use their credentials.',
    );
    expect(screen.getByRole('group', { name: 'Available' })).toHaveTextContent(
      'Connect a service to use its credentials in Kstack.',
    );
  });

  it.each([
    ['Connected', 'Available', connector('Google', false)],
    ['Available', 'Connected', connector('GitHub', true)],
  ])('draws no %s group with no service in it', (empty, drawn, only) => {
    state.connectors = [only];
    render(<ConnectorsSettings />);

    expect(screen.queryByRole('group', { name: empty })).toBeNull();
    expect(screen.getByRole('group', { name: drawn })).toBeInTheDocument();
  });

  it('lists each service by its name over its detail', () => {
    state.connectors = [connector('GitHub', true), connector('AWS', true), connector('Google', false)];
    render(<ConnectorsSettings />);

    const rows = within(screen.getByRole('group', { name: 'Connected' })).getAllByRole('listitem');
    expect(rows.map((r) => r.textContent)).toEqual(['GitHubGitHub detail', 'AWSAWS detail']);
    const available = within(screen.getByRole('group', { name: 'Available' })).getByRole('listitem');
    expect(available).toHaveTextContent('GoogleGoogle detail');
  });

  it('spells out control characters in the name and the detail', () => {
    state.connectors = [connector('Git\u001b[2JHub', true, { detail: 'Signed in as \u202eoctocat' })];
    render(<ConnectorsSettings />);

    const row = screen.getByRole('listitem');
    expect(within(row).getAllByTitle('An invisible character')).toHaveLength(2);
    for (const raw of ['\u001b', '\u202e']) expect(row.textContent).not.toContain(raw);
  });

  it('offers a disabled Connect on an available service alone', () => {
    render(<ConnectorsSettings />);

    const connect = within(screen.getByRole('group', { name: 'Available' })).getByRole('button', { name: 'Connect' });
    expect(connect).toBeDisabled();
    expect(
      within(screen.getByRole('group', { name: 'Connected' })).queryByRole('button', { name: 'Connect' }),
    ).toBeNull();
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
