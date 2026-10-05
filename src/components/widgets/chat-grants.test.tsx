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

import type { ChatGrants as Grants } from '@/lib/chat-grants';
import type { SandboxFolders } from '@/lib/sandbox-folders';

const { grants, folders } = vi.hoisted(() => ({
  grants: { current: undefined as unknown as Grants },
  folders: { current: {} as Pick<SandboxFolders, 'chat'> },
}));
vi.mock('@/lib/chat-grants', () => ({ useChatGrants: () => grants.current }));
vi.mock('@/lib/sandbox-folders', () => ({ useSandboxFolders: () => folders.current }));

const { ChatGrants } = await import('./chat-grants');

const remove = vi.fn();
const held = (over: Partial<Grants> = {}): Grants => ({
  rules: [
    { id: 'r1', line: 'Allow cluster writes in dev / web' },
    { id: 'r2', line: 'Allow cluster writes in "say ​\\"hi\\""' },
  ],
  removing: null,
  error: null,
  remove,
  ...over,
});

beforeEach(() => {
  vi.clearAllMocks();
  grants.current = held();
  folders.current = { chat: [] };
});

describe('ChatGrants', () => {
  it('draws nothing for a chat with no rules', () => {
    grants.current = held({ rules: [] });
    const { container } = render(<ChatGrants chatID="c1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it('draws the count, then one row per rule through VisibleText', async () => {
    render(<ChatGrants chatID="c1" />);
    const trigger = screen.getByRole('button', { name: /2 allowed/ });
    await userEvent.click(trigger);
    const list = screen.getByRole('list', { name: 'Allowed for this chat' });
    expect(list.querySelectorAll('li')).toHaveLength(2);
    expect(list).toHaveTextContent('Allow cluster writes in dev / web');
    expect(list.querySelector('mark')).not.toBeNull();
  });

  it('removes a rule, its button down while in flight, and shows a refusal', async () => {
    render(<ChatGrants chatID="c1" />);
    await userEvent.click(screen.getByRole('button', { name: /2 allowed/ }));
    await userEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0]);
    expect(remove).toHaveBeenCalledWith('r1');

    grants.current = held({ removing: 'r1', error: 'not found' });
    render(<ChatGrants chatID="c1" />);
    await userEvent.click(screen.getAllByRole('button', { name: /2 allowed/ })[1]);
    const buttons = screen.getAllByRole('button', { name: 'Remove' });
    expect(buttons.at(-2)).toBeDisabled();
    expect(screen.getByRole('alert')).toHaveTextContent('not found');
  });

  it("draws a folder grant's refused reason under its line", async () => {
    grants.current = held({
      rules: [
        { id: 'r1', line: 'Allow cluster writes in dev / web' },
        { id: 'f1', line: 'Allow reads of /Users/ren/code' },
      ],
    });
    folders.current = {
      chat: [{ id: 'f1', path: '/Users/ren/code', write: false, refused: 'This folder does not exist.' }],
    };
    render(<ChatGrants chatID="c1" />);
    await userEvent.click(screen.getByRole('button', { name: /2 allowed/ }));
    const rows = within(screen.getByRole('list', { name: 'Allowed for this chat' })).getAllByRole('listitem');
    expect(within(rows[0]).queryByText('This folder does not exist.')).toBeNull();
    expect(within(rows[1]).getByText('This folder does not exist.')).toBeInTheDocument();
  });
});
