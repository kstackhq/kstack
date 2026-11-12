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

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Layers } from 'lucide-react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { expectCodeText } from '@/test-utils';

import { ScopeMenu, ScopeTrigger } from './scope-menu';
import type { ScopeMenuEntry } from './scope-menu';

// Helpers -------------------------------------------------------------

const ALL: readonly ScopeMenuEntry[] = [
  { type: 'option', id: 'overview', label: 'Overview' },
  { type: 'label', key: 'cluster', label: 'Cluster' },
  { type: 'label', key: 'infra', label: 'Infrastructure' },
  { type: 'option', id: 'nodes', label: 'Nodes' },
  { type: 'option', id: 'namespaces', label: 'Namespaces', note: 'Not found' },
];

// Keeps the options starting with the query, and the labels while anything matches.
function entries(query: string): readonly ScopeMenuEntry[] {
  const q = query.toLowerCase();
  if (!q) return ALL;
  return ALL.filter((e) => e.type === 'option' && e.label.toLowerCase().startsWith(q));
}

const onPick = vi.fn<(id: string) => void>();

async function openMenu(current: string | undefined) {
  const user = userEvent.setup();
  render(
    <ScopeMenu
      trigger={<ScopeTrigger icon={Layers}>Scope</ScopeTrigger>}
      searchLabel="Jump to a resource"
      placeholder="Jump to a resource…"
      entries={entries}
      current={current}
      onPick={onPick}
    />,
  );
  await user.click(screen.getByRole('button', { name: 'Scope' }));
  // The popup moves focus to the box once it has opened.
  await waitFor(() => expect(screen.getByRole('combobox', { name: 'Jump to a resource' })).toHaveFocus());
  return user;
}

const options = () => screen.getAllByRole('option').map((o) => o.textContent);
const highlighted = () => screen.getAllByRole('option').find((o) => o.getAttribute('aria-selected') === 'true');

const scrollIntoView = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  // jsdom lays nothing out, so it has no scrollIntoView.
  Element.prototype.scrollIntoView = scrollIntoView;
});

// Tests ---------------------------------------------------------------

describe('ScopeMenu', () => {
  it('lists its options under their labels, with notes', async () => {
    await openMenu('nodes');
    expect(options()).toEqual(['Overview', 'Nodes', 'NamespacesNot found']);
    // Labels head the options; they are not options themselves.
    expect(screen.getByText('Cluster')).toBeInTheDocument();
    expect(screen.getByText('Infrastructure')).toBeInTheDocument();
  });

  it('asks for its entries again as the query changes', async () => {
    const user = await openMenu('nodes');
    await user.type(screen.getByRole('combobox', { name: 'Jump to a resource' }), 'na');
    expect(options()).toEqual(['NamespacesNot found']);
    expect(screen.queryByText('Cluster')).not.toBeInTheDocument();
  });

  it('says so when nothing matches', async () => {
    const user = await openMenu('nodes');
    await user.type(screen.getByRole('combobox', { name: 'Jump to a resource' }), 'zz');
    expect(screen.getByText('No matches')).toBeInTheDocument();
  });

  it('picks an option on a click, and closes', async () => {
    const user = await openMenu('nodes');
    await user.click(screen.getByRole('option', { name: /Overview/ }));
    expect(onPick).toHaveBeenCalledWith('overview');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  });

  it('opens with the chosen option highlighted', async () => {
    await openMenu('nodes');
    expect(highlighted()).toHaveTextContent('Nodes');
    const box = screen.getByRole('combobox', { name: 'Jump to a resource' });
    expect(box).toHaveAttribute('aria-activedescendant', highlighted()!.id);
  });

  it('opens on the first option with nothing chosen', async () => {
    await openMenu(undefined);
    expect(highlighted()).toHaveTextContent('Overview');
  });

  // Focus stays in the box; the arrows move the highlight over options, never labels.
  it('moves the highlight with the arrows, skipping labels and stopping at the ends', async () => {
    const user = await openMenu('overview');
    await user.keyboard('{ArrowDown}');
    expect(highlighted()).toHaveTextContent('Nodes');
    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}');
    expect(highlighted()).toHaveTextContent('Namespaces');
    await user.keyboard('{ArrowUp}{ArrowUp}{ArrowUp}');
    expect(highlighted()).toHaveTextContent('Overview');
    expect(screen.getByRole('combobox', { name: 'Jump to a resource' })).toHaveFocus();
  });

  it('picks the highlighted option on Enter, and closes', async () => {
    const user = await openMenu('overview');
    await user.keyboard('{ArrowDown}{Enter}');
    expect(onPick).toHaveBeenCalledWith('nodes');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  });

  it('highlights the first match as the query changes', async () => {
    const user = await openMenu('overview');
    await user.keyboard('n');
    expect(highlighted()).toHaveTextContent('Nodes');
    await user.keyboard('a{Enter}');
    expect(onPick).toHaveBeenCalledWith('namespaces');
  });

  it('moves nothing with the arrows when nothing matches', async () => {
    const user = await openMenu('nodes');
    await user.keyboard('zz');
    scrollIntoView.mockClear();
    await user.keyboard('{ArrowDown}{ArrowUp}');
    expect(scrollIntoView).not.toHaveBeenCalled();
    expect(screen.getByRole('combobox', { name: 'Jump to a resource' })).not.toHaveAttribute('aria-activedescendant');
  });

  it('picks nothing on Enter with no match', async () => {
    const user = await openMenu('nodes');
    await user.keyboard('zz{Enter}');
    expect(onPick).not.toHaveBeenCalled();
  });

  it('closes on Escape', async () => {
    const user = await openMenu('nodes');
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  });

  it('opens scrolled to the chosen option', async () => {
    await openMenu('namespaces');
    const chosen = screen.getByRole('option', { name: /Namespaces/ });
    await waitFor(() => expect(scrollIntoView.mock.contexts).toContain(chosen));
    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'center' });
  });

  it('keeps the highlight in view as the arrows move it', async () => {
    const user = await openMenu('overview');
    scrollIntoView.mockClear();
    await user.keyboard('{ArrowDown}');
    expect(scrollIntoView.mock.contexts).toEqual([screen.getByRole('option', { name: /Nodes/ })]);
    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest' });
  });

  // jsdom lays nothing out, so this checks the styles that bound it.
  it('caps its popup to the window, the list taking the shrink', async () => {
    await openMenu('nodes');
    const list = screen.getByRole('listbox');
    expect(list.parentElement!.style.maxHeight).toBe('var(--available-height)');
    expect(list).toHaveClass('min-h-0', 'overflow-y-auto');
  });

  it('names its keys in a footer', async () => {
    await openMenu('nodes');
    expect(screen.getByText('↑↓ navigate')).toBeInTheDocument();
    expect(screen.getByText('↵ open')).toBeInTheDocument();
    expect(screen.getByText('esc close')).toBeInTheDocument();
  });

  it('draws its search box as code text', async () => {
    await openMenu('nodes');
    expectCodeText(screen.getByRole('combobox', { name: 'Jump to a resource' }));
  });

  it('draws option labels through VisibleText', async () => {
    const user = userEvent.setup();
    render(
      <ScopeMenu
        trigger={<ScopeTrigger>Scope</ScopeTrigger>}
        searchLabel="Filter"
        placeholder="Filter"
        entries={() => [{ type: 'option', id: 'x', label: 'a\u200bb' }]}
        current={undefined}
        onPick={onPick}
      />,
    );
    await user.click(screen.getByRole('button', { name: 'Scope' }));
    expect(screen.getByRole('option')).toHaveTextContent('a\\u{200B}b');
  });
});
