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

const { useChatsMock, renameMock, deleteMock } = vi.hoisted(() => ({
  useChatsMock: vi.fn(),
  renameMock: vi.fn(),
  deleteMock: vi.fn(),
}));

vi.mock('@/lib/chats', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/chats')>()),
  useChats: useChatsMock,
}));
vi.mock('urql', () => ({
  useMutation: (doc: { __kind?: string }) => [{ fetching: false }, doc.__kind === 'delete' ? deleteMock : renameMock],
}));
vi.mock('@/gql', () => ({
  graphql: (source: string) => ({ __kind: source.includes('chatDelete') ? 'delete' : 'rename' }),
}));
// The window's cluster: the list is the active cluster's chats and nobody else's.
const { useActiveClusterMock } = vi.hoisted(() => ({ useActiveClusterMock: vi.fn() }));
vi.mock('@/lib/active-cluster', () => ({ useActiveCluster: useActiveClusterMock }));
vi.mock('@tanstack/react-router', () => ({
  Link: ({ to, params, search, children, ...rest }: Record<string, unknown> & { children: React.ReactNode }) => {
    const path =
      typeof to === 'string' && params ? to.replace('$chatId', (params as { chatId: string }).chatId) : String(to);
    // A dashboard row writes its search with an updater, so call it the way the
    // router would — over a stand-in previous search — and put the result on the href.
    const next = typeof search === 'function' ? (search as (p: unknown) => Record<string, unknown>)(PREV_SEARCH) : null;
    return (
      <a href={next ? `${path}?${new URLSearchParams(next as Record<string, string>)}` : path} {...rest}>
        {children}
      </a>
    );
  },
}));

// What a row's `search` updater is handed: the rest of the window's scope, which it
// must carry rather than replace.
const PREV_SEARCH = { kubeContext: 'prod', resource: 'pods' };

const { ChatNav } = await import('./chat-nav');

const chat = (id: string, title: string, mode = 'Chat', clusterID = '1') => ({
  id,
  title,
  mode,
  clusterID,
  createdAt: '2026-09-01T00:00:00Z',
  updatedAt: '2026-09-01T10:00:00Z',
  awaitingApproval: false,
  sandboxDisabled: false,
});

function renderNav(chats = [chat('c1', 'First chat')], phase = 'live', mode: 'chat' | 'dashboard' = 'chat') {
  useChatsMock.mockReturnValue({ chats, phase });
  return render(<ChatNav mode={mode} />);
}

const menuFor = async (user: ReturnType<typeof userEvent.setup>, title: string) => {
  await user.click(screen.getByRole('button', { name: `Actions for ${title}` }));
};

beforeEach(() => {
  vi.clearAllMocks();
  useActiveClusterMock.mockReturnValue({ clusterID: '1', phase: 'live' });
  renameMock.mockResolvedValue({ data: { chatRename: chat('c1', 'renamed') } });
  deleteMock.mockResolvedValue({ data: { chatDelete: true } });
});

describe('ChatNav', () => {
  it('offers a new chat without writing one', () => {
    renderNav();
    expect(screen.getByRole('link', { name: /new chat/i })).toHaveAttribute('href', '/chat');
  });

  it('waits for the snapshot before saying there are no chats', () => {
    renderNav([], 'connecting');
    expect(screen.queryByText('No chats yet.')).not.toBeInTheDocument();
  });

  it('says there are no chats once the snapshot has closed', () => {
    renderNav([], 'live');
    expect(screen.getByText('No chats yet.')).toBeInTheDocument();
  });

  it('links each chat to its own route', () => {
    renderNav([chat('c1', 'First chat'), chat('c2', 'Second chat')]);
    expect(screen.getByRole('link', { name: 'First chat' })).toHaveAttribute('href', '/chat/c1');
    expect(screen.getByRole('link', { name: 'Second chat' })).toHaveAttribute('href', '/chat/c2');
  });

  it('marks a chat a request is waiting in, on either mode', () => {
    (['chat', 'dashboard'] as const).forEach((mode) => {
      const kind = mode === 'chat' ? 'Chat' : 'Dashboard';
      const { unmount } = renderNav(
        [{ ...chat('c1', 'First chat', kind), awaitingApproval: true }, chat('c2', 'Second chat', kind)],
        'live',
        mode,
      );
      const first = screen.getByRole('link', { name: /First chat/ });
      expect(within(first).getByRole('img', { name: 'Waiting on you' })).toBeInTheDocument();
      expect(screen.getAllByRole('img', { name: 'Waiting on you' })).toHaveLength(1);
      unmount();
    });
  });

  it('lists only the chats of its own mode', () => {
    renderNav([chat('c1', 'A chat chat'), chat('c2', 'A dashboard chat', 'Dashboard')]);
    expect(screen.getByRole('link', { name: 'A chat chat' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'A dashboard chat' })).not.toBeInTheDocument();

    screen.getByRole('link', { name: /new chat/i });
  });

  // A chat belongs to one cluster, so the list is the window's cluster's — switching
  // cluster switches the rows, and a window on no cluster lists none.
  it('lists only the active cluster’s chats', () => {
    const view = renderNav([chat('c1', 'On prod'), chat('c2', 'On staging', 'Chat', '2')]);
    expect(screen.getByRole('link', { name: 'On prod' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'On staging' })).not.toBeInTheDocument();

    useActiveClusterMock.mockReturnValue({ clusterID: '2', phase: 'live' });
    view.rerender(<ChatNav mode="chat" />);
    expect(screen.getByRole('link', { name: 'On staging' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'On prod' })).not.toBeInTheDocument();

    useActiveClusterMock.mockReturnValue({ clusterID: undefined, phase: 'live' });
    view.rerender(<ChatNav mode="chat" />);
    expect(screen.getByText('No chats yet.')).toBeInTheDocument();
  });

  // The rows are the window's cluster's, so a chats snapshot that lands first has
  // every row filtered out — "No chats yet." there would be about a cluster the
  // window has not been told it is on.
  it('waits for the clusters snapshot too before saying there are no chats', () => {
    useActiveClusterMock.mockReturnValue({ clusterID: undefined, phase: 'connecting' });
    renderNav([chat('c1', 'First chat')], 'live');

    expect(screen.queryByText('No chats yet.')).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'First chat' })).not.toBeInTheDocument();
  });

  // The spinner gates on the unfiltered phase; the empty text on what is left after
  // the filter, so a mode with none of its own says so even when the other has some.
  it('says there are no chats when only the other mode has any', () => {
    renderNav([chat('c1', 'A chat chat')], 'live', 'dashboard');
    expect(screen.getByText('No chats yet.')).toBeInTheDocument();
  });

  // On the dashboard a chat is a search param, not a route, and the rest of the
  // window's scope has to survive the click.
  it('points a dashboard row at the panel, keeping the rest of the search', () => {
    renderNav([chat('c2', 'A dashboard chat', 'Dashboard')], 'live', 'dashboard');

    const href = screen.getByRole('link', { name: 'A dashboard chat' }).getAttribute('href')!;
    const [path, query] = href.split('?');
    expect(path).toBe('/dashboard');
    expect(Object.fromEntries(new URLSearchParams(query))).toEqual({ ...PREV_SEARCH, chat: 'c2' });
  });

  // The panel's header is where a dashboard chat starts — by typing in the composer.
  it('offers no new chat on the dashboard', () => {
    renderNav([], 'live', 'dashboard');
    expect(screen.queryByRole('link', { name: /new chat/i })).not.toBeInTheDocument();
  });

  it('renames in place, committing on Enter', async () => {
    const user = userEvent.setup();
    renderNav();
    await menuFor(user, 'First chat');
    await user.click(await screen.findByRole('menuitem', { name: /rename/i }));

    const input = screen.getByRole('textbox', { name: /chat title/i });
    await user.clear(input);
    await user.type(input, 'Renamed{Enter}');
    expect(renameMock).toHaveBeenCalledWith({ id: 'c1', title: 'Renamed' });
  });

  it('cancels a rename on Escape, writing nothing', async () => {
    const user = userEvent.setup();
    renderNav();
    await menuFor(user, 'First chat');
    await user.click(await screen.findByRole('menuitem', { name: /rename/i }));

    await user.type(screen.getByRole('textbox', { name: /chat title/i }), 'Renamed{Escape}');
    expect(renameMock).not.toHaveBeenCalled();
    expect(screen.getByRole('link', { name: 'First chat' })).toBeInTheDocument();
  });

  it('leaves the row as it was when a rename fails', async () => {
    renameMock.mockResolvedValue({ error: new Error('nope') });
    const user = userEvent.setup();
    renderNav();
    await menuFor(user, 'First chat');
    await user.click(await screen.findByRole('menuitem', { name: /rename/i }));
    await user.type(screen.getByRole('textbox', { name: /chat title/i }), '{Enter}');

    // The row is the watch's, so a refused rename simply leaves it alone.
    expect(await screen.findByRole('link', { name: 'First chat' })).toBeInTheDocument();
  });

  it('asks before deleting, since deletion is instant and permanent', async () => {
    const user = userEvent.setup();
    renderNav();
    await menuFor(user, 'First chat');
    await user.click(await screen.findByRole('menuitem', { name: /delete/i }));

    expect(deleteMock).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Delete chat' }));
    expect(deleteMock).toHaveBeenCalledWith({ id: 'c1' });
  });

  it('writes nothing when the confirm is dismissed', async () => {
    const user = userEvent.setup();
    renderNav();
    await menuFor(user, 'First chat');
    await user.click(await screen.findByRole('menuitem', { name: /delete/i }));
    await user.click(screen.getByRole('button', { name: 'Keep chat' }));

    expect(deleteMock).not.toHaveBeenCalled();
  });

  it('leaves the row in the list when a delete fails', async () => {
    deleteMock.mockResolvedValue({ error: new Error('nope') });
    const user = userEvent.setup();
    renderNav();
    await menuFor(user, 'First chat');
    await user.click(await screen.findByRole('menuitem', { name: /delete/i }));
    await user.click(screen.getByRole('button', { name: 'Delete chat' }));

    expect(await screen.findByRole('link', { name: 'First chat' })).toBeInTheDocument();
  });
});
