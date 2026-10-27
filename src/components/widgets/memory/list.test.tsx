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

import { useState } from 'react';

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { Memory } from '@/lib/memories';
import { MemoryList } from './list';

vi.mock('@/lib/dialog', () => ({ useDialogHost: () => null }));

const BODY = 'This cluster routes through traefik, not nginx.';

const memory = (i: number, over: Partial<Memory> = {}): Memory => ({
  id: `m${i}`,
  clusterID: 'c1',
  name: 'traefik-only',
  body: BODY,
  writtenBy: 'Model',
  updatedAt: '2026-09-20T10:00:00Z',
  ...over,
});
const FIVE = Array.from({ length: 5 }, (_, i) => memory(i));

// The rows are the parent's; this gives them somewhere to live. `refusal` and
// `failing` stand in for the sidecar's answers: each id in `failing` fails its
// first delete.
function List({
  initial = FIVE,
  adding: startAdding = false,
  refusal = null,
  failing = [],
}: {
  initial?: Memory[];
  adding?: boolean;
  refusal?: string | null;
  failing?: string[];
}) {
  const [memories, setMemories] = useState(initial);
  const [stuck, setStuck] = useState(failing);
  const [adding, setAdding] = useState(startAdding);
  return (
    <MemoryList
      scope="Minikube"
      memories={memories}
      adding={adding}
      onSave={async (m, name, body) => {
        if (refusal) return refusal;
        setMemories((prev) =>
          m
            ? prev.map((x) => (x.id === m.id ? { ...x, body, writtenBy: 'User' } : x))
            : [memory(prev.length + 10, { name, body, writtenBy: 'User' }), ...prev],
        );
        return null;
      }}
      onAddDone={() => setAdding(false)}
      onDelete={async (ids) => {
        const left = ids.filter((id) => stuck.includes(id));
        setStuck([]);
        setMemories((prev) => prev.filter((m) => !ids.includes(m.id) || left.includes(m.id)));
        return left;
      }}
    />
  );
}

const button = (name: string) => screen.getByRole('button', { name });
const checkbox = (i: number) => screen.getAllByRole('checkbox')[i];
const openMenu = (user: ReturnType<typeof userEvent.setup>) =>
  user.click(screen.getAllByRole('button', { name: 'Actions for traefik-only' })[0]);

describe('MemoryList', () => {
  it('edits a row in place from its menu', async () => {
    const user = userEvent.setup();
    render(<List />);

    await openMenu(user);
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }));
    const textarea = screen.getByRole('textbox', { name: 'Body' });
    expect(textarea).toHaveValue(BODY);

    await user.clear(textarea);
    await user.type(textarea, '  Uses nginx.  ');
    await user.click(button('Save'));

    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.getByText('Uses nginx.')).toBeInTheDocument();
    expect(screen.getAllByText(BODY)).toHaveLength(4);
  });

  it('keeps the row on Cancel, and disables Save on a blank draft', async () => {
    const user = userEvent.setup();
    render(<List />);

    await openMenu(user);
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }));
    await user.clear(screen.getByRole('textbox'));
    await user.type(screen.getByRole('textbox'), '   ');
    expect(button('Save')).toBeDisabled();

    await user.click(button('Cancel'));
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.getAllByText(BODY)).toHaveLength(5);
  });

  it('deletes one row from its menu after confirming', async () => {
    const user = userEvent.setup();
    render(<List />);

    await openMenu(user);
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }));
    expect(await screen.findByText('Delete 1 memory?')).toBeInTheDocument();

    await user.click(button('Delete 1 memory'));
    expect(screen.getAllByText(BODY)).toHaveLength(4);
  });

  it('keeps the row when the confirm is cancelled', async () => {
    const user = userEvent.setup();
    render(<List />);

    await openMenu(user);
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }));
    await user.click(await screen.findByRole('button', { name: 'Cancel' }));

    expect(screen.getAllByText(BODY)).toHaveLength(5);
  });

  it('swaps the heading for the bulk bar while any row is checked', async () => {
    const user = userEvent.setup();
    render(<List />);

    await user.click(checkbox(0));
    expect(screen.queryByText('SAVED MEMORIES')).not.toBeInTheDocument();
    expect(screen.getByText('1 selected')).toBeInTheDocument();
    expect(screen.getByText('5 memories in total')).toBeInTheDocument();

    await user.click(button('Cancel'));
    expect(screen.getByText('SAVED MEMORIES')).toBeInTheDocument();
  });

  it('selects every row from the bulk bar, and clears them on a second press', async () => {
    const user = userEvent.setup();
    render(<List />);

    await user.click(checkbox(0));
    // Once the bar shows, its select-all is the first checkbox.
    await user.click(checkbox(0));
    expect(screen.getByText('5 selected')).toBeInTheDocument();

    await user.click(checkbox(0));
    expect(screen.getByText('SAVED MEMORIES')).toBeInTheDocument();
  });

  it('deletes the selection after confirming, down to the empty state', async () => {
    const user = userEvent.setup();
    render(<List />);

    await user.click(checkbox(0));
    await user.click(checkbox(2));
    expect(screen.getByText('2 selected')).toBeInTheDocument();

    await user.click(button('Delete (2)'));
    expect(await screen.findByText('Delete 2 memories?')).toBeInTheDocument();
    await user.click(button('Delete 2 memories'));
    expect(screen.getAllByText(BODY)).toHaveLength(3);
    expect(screen.getByText('SAVED MEMORIES')).toBeInTheDocument();

    await user.click(checkbox(0));
    await user.click(checkbox(0));
    await user.click(button('Delete (3)'));
    await user.click(await screen.findByRole('button', { name: 'Delete 3 memories' }));
    expect(screen.getByText('No saved memories yet.')).toBeInTheDocument();
  });

  it('draws who wrote each memory, and when', () => {
    render(<List initial={[memory(0), memory(1, { name: 'prefs', writtenBy: 'User' })]} />);
    const date = new Date('2026-09-20T10:00:00Z').toLocaleDateString();
    expect(screen.getByText(`by Kstack · ${date}`)).toBeInTheDocument();
    expect(screen.getByText(`by you · ${date}`)).toBeInTheDocument();
  });

  it('draws a body as its characters, never markup', () => {
    render(<List initial={[memory(0, { body: '# Heading\n<b>bold</b>' })]} />);
    const body = screen.getByText(/# Heading/);
    expect(body).toHaveTextContent('<b>bold</b>');
    expect(body.querySelector('b')).toBeNull();
    expect(document.querySelector('h1')).toBeNull();
  });

  describe('adopting', () => {
    const line = 'Saving makes this your memory: Kstack will follow it.';

    it("says saving one of Kstack's memories makes it yours", async () => {
      const user = userEvent.setup();
      render(<List initial={[memory(0)]} />);
      await openMenu(user);
      await user.click(await screen.findByRole('menuitem', { name: 'Edit' }));
      expect(screen.getByText(line)).toBeInTheDocument();
    });

    it('says nothing for a memory already yours, or a new one', async () => {
      const user = userEvent.setup();
      const { unmount } = render(<List initial={[memory(0, { writtenBy: 'User' })]} />);
      await openMenu(user);
      await user.click(await screen.findByRole('menuitem', { name: 'Edit' }));
      expect(screen.queryByText(line)).toBeNull();
      unmount();

      render(<List adding />);
      expect(screen.queryByText(line)).toBeNull();
    });
  });

  it('keeps the draft on a refused save and says why', async () => {
    const user = userEvent.setup();
    render(<List refusal="Another memory here has this name." />);

    await openMenu(user);
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }));
    await user.clear(screen.getByRole('textbox', { name: 'Body' }));
    await user.type(screen.getByRole('textbox', { name: 'Body' }), 'my draft');
    await user.click(button('Save'));

    expect(screen.getByRole('textbox', { name: 'Body' })).toHaveValue('my draft');
    expect(screen.getByText('Another memory here has this name.')).toBeInTheDocument();
  });

  it('adds a memory, holding Save until the name and the body are there', async () => {
    const user = userEvent.setup();
    render(<List adding />);
    expect(button('Save')).toBeDisabled();

    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'oncall');
    await user.type(screen.getByRole('textbox', { name: 'Body' }), 'Page the payments team.');
    await user.click(button('Save'));

    expect(screen.queryByRole('textbox', { name: 'Name' })).not.toBeInTheDocument();
    expect(screen.getByText('Page the payments team.')).toBeInTheDocument();
  });

  it.each([
    ['Name', 'Bad Name', 'Lower-case letters, digits and single hyphens, at most 48 characters.'],
    // 251 two-byte characters: under 500 characters, over 500 bytes.
    ['Body', 'é'.repeat(251), 'At most 500 bytes.'],
  ])('draws the rule a %s breaks and holds Save', async (label, value, rule) => {
    const user = userEvent.setup();
    render(<List adding />);
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'oncall');
    await user.type(screen.getByRole('textbox', { name: 'Body' }), 'b');
    expect(button('Save')).toBeEnabled();

    const field = screen.getByRole('textbox', { name: label });
    await user.clear(field);
    await user.click(field);
    await user.paste(value);
    expect(screen.getByText(rule)).toBeInTheDocument();
    expect(button('Save')).toBeDisabled();
  });

  it('keeps the confirm and says so when a delete fails, and a retry deletes it', async () => {
    const user = userEvent.setup();
    render(<List failing={['m0']} />);

    await openMenu(user);
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }));
    await user.click(await screen.findByRole('button', { name: 'Delete 1 memory' }));
    expect(screen.getByText('1 memory could not be deleted.')).toBeInTheDocument();
    expect(screen.getAllByText(BODY)).toHaveLength(5);

    await user.click(button('Delete 1 memory'));
    expect(screen.queryByText('Delete 1 memory?')).not.toBeInTheDocument();
    expect(screen.getAllByText(BODY)).toHaveLength(4);
  });

  it('retries only what a bulk delete left behind', async () => {
    const user = userEvent.setup();
    render(<List failing={['m1']} />);

    await user.click(checkbox(0));
    await user.click(checkbox(2));
    await user.click(button('Delete (2)'));
    await user.click(await screen.findByRole('button', { name: 'Delete 2 memories' }));

    expect(screen.getAllByText(BODY)).toHaveLength(4);
    expect(screen.getByText('Delete 1 memory?')).toBeInTheDocument();
    expect(screen.getByText('1 memory could not be deleted.')).toBeInTheDocument();
    expect(screen.getByText('1 selected')).toBeInTheDocument();

    await user.click(button('Delete 1 memory'));
    expect(screen.getAllByText(BODY)).toHaveLength(3);
    expect(screen.getByText('SAVED MEMORIES')).toBeInTheDocument();
  });

  // jsdom applies no CSS, so this checks the classes that decide what a browser
  // hides: the trigger only fades, never leaving the tab order.
  it("keeps a row's actions in reach of the keyboard", async () => {
    const user = userEvent.setup();
    render(<List initial={[memory(0)]} />);
    const trigger = button('Actions for traefik-only');
    expect(trigger).not.toHaveClass('hidden');
    expect(trigger).toHaveClass('opacity-0', 'group-focus-within/row:opacity-100');
    expect(screen.getByRole('checkbox')).toHaveClass('invisible', 'group-focus-within/row:visible');

    trigger.focus();
    await user.keyboard('{Enter}');
    expect(await screen.findByRole('menuitem', { name: 'Edit' })).toBeInTheDocument();
  });

  it('drops a checked row the watch removes from the selection', async () => {
    const user = userEvent.setup();
    const props = {
      scope: 'Minikube',
      adding: false,
      onSave: async () => null,
      onAddDone: () => {},
      onDelete: async () => [],
    };
    const { rerender } = render(<MemoryList {...props} memories={FIVE} />);
    await user.click(checkbox(0));
    await user.click(checkbox(2));
    expect(screen.getByText('2 selected')).toBeInTheDocument();

    rerender(<MemoryList {...props} memories={FIVE.slice(1)} />);
    expect(screen.getByText('1 selected')).toBeInTheDocument();
    expect(screen.getByText('4 memories in total')).toBeInTheDocument();

    rerender(<MemoryList {...props} memories={FIVE.slice(2)} />);
    expect(screen.getByText('SAVED MEMORIES')).toBeInTheDocument();
  });
});
