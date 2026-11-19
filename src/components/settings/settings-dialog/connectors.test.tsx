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

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type {
  ChatGPTActions,
  ChatGPTConnection,
  Connector,
  OpenRouterActions,
  OpenRouterConnection,
} from '@/lib/connectors';

const { state } = vi.hoisted(() => ({ state: { connectors: [] as Connector[], loaded: true } }));
vi.mock('@/lib/connectors', () => ({ useConnectors: () => ({ connectors: state.connectors, loaded: state.loaded }) }));

const { ConnectorsSettings } = await import('./connectors');

function connector(name: string, connected: boolean, rest: Partial<Connector> = {}): Connector {
  return { id: name.toLowerCase(), name, detail: `${name} detail`, connected, ...rest };
}

beforeEach(() => {
  state.connectors = [connector('GitHub', true), connector('Google', false)];
  state.loaded = true;
  localStorage.clear();
  sessionStorage.clear();
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

describe('the ChatGPT row', () => {
  const actions: ChatGPTActions = { connect: vi.fn(), cancel: vi.fn(), disconnect: vi.fn(), openUsage: vi.fn() };

  function chatgpt(connection: Partial<ChatGPTConnection>): Connector {
    const c: ChatGPTConnection = {
      phase: 'SignedOut',
      email: null,
      failure: null,
      registered: false,
      revocationUnconfirmed: false,
      ...connection,
    };
    const connected = c.phase === 'Connected';
    return {
      id: 'chatgpt',
      name: 'ChatGPT',
      detail: connected ? (c.email ?? '') : 'OpenAI models on your ChatGPT plan',
      connected,
      chatgpt: { connection: c, actions },
    };
  }

  const row = () => screen.getByRole('listitem');

  beforeEach(() => vi.clearAllMocks());

  it('draws nothing before the watch answers', () => {
    state.loaded = false;
    const { container } = render(<ConnectorsSettings />);
    expect(container).toBeEmptyDOMElement();
  });

  it('offers Continue with ChatGPT while signed out, with no menu before a registration', async () => {
    state.connectors = [chatgpt({})];
    render(<ConnectorsSettings />);

    await userEvent.setup().click(within(row()).getByRole('button', { name: 'Continue with ChatGPT' }));
    expect(actions.connect).toHaveBeenCalledWith(false);
    expect(within(row()).queryByRole('button', { name: 'Actions for ChatGPT' })).toBeNull();
  });

  it('waits for the browser while signing in, and cancels on a press', async () => {
    state.connectors = [chatgpt({ phase: 'SigningIn', registered: true })];
    render(<ConnectorsSettings />);

    expect(row()).toHaveTextContent('Waiting for your browser…');
    await userEvent.setup().click(within(row()).getByRole('button', { name: 'Cancel' }));
    expect(actions.cancel).toHaveBeenCalledOnce();
    expect(within(row()).queryByRole('button', { name: 'Actions for ChatGPT' })).toBeNull();
  });

  it('shows the account once connected, and offers Manage usage and Disconnect', async () => {
    const user = userEvent.setup();
    state.connectors = [chatgpt({ phase: 'Connected', email: 'ada@\u202eexample.com', registered: true })];
    render(<ConnectorsSettings />);

    expect(within(row()).getByTitle('An invisible character')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Actions for ChatGPT' }));
    const items = within(await screen.findByRole('menu')).getAllByRole('menuitem');
    expect(items.map((i) => i.textContent)).toEqual(['Manage usage', 'Disconnect']);
    await user.click(items[0]);
    expect(actions.openUsage).toHaveBeenCalledOnce();
    await user.click(screen.getByRole('button', { name: 'Actions for ChatGPT' }));
    await user.click(await screen.findByRole('menuitem', { name: 'Disconnect' }));
    expect(actions.disconnect).toHaveBeenCalledOnce();
  });

  it.each([
    ['Declined', 'You declined on ChatGPT’s page.'],
    ['SessionEnded', 'ChatGPT ended the session. Connect again.'],
    ['NotSaved', 'This computer could not save the sign-in.'],
  ] as const)('says why it failed (%s), and offers the button again', (failure, text) => {
    state.connectors = [chatgpt({ phase: 'Failed', failure })];
    render(<ConnectorsSettings />);

    expect(row()).toHaveTextContent(text);
    expect(within(row()).getByRole('button', { name: 'Continue with ChatGPT' })).toBeEnabled();
  });

  it('offers Use a different account once registered', async () => {
    const user = userEvent.setup();
    state.connectors = [chatgpt({ phase: 'Failed', failure: 'AccountMismatch', registered: true })];
    render(<ConnectorsSettings />);

    await user.click(screen.getByRole('button', { name: 'Actions for ChatGPT' }));
    await user.click(await screen.findByRole('menuitem', { name: 'Use a different account' }));
    expect(actions.connect).toHaveBeenCalledWith(true);
  });

  it('says when OpenAI did not confirm a sign-out, and opens its settings', async () => {
    state.connectors = [chatgpt({ revocationUnconfirmed: true, registered: true })];
    render(<ConnectorsSettings />);

    expect(row()).toHaveTextContent('Signed out on this computer. ChatGPT did not confirm it.');
    await userEvent.setup().click(within(row()).getByRole('button', { name: 'Open ChatGPT settings' }));
    expect(actions.openUsage).toHaveBeenCalledOnce();
  });

  describe('the first connection', () => {
    const dialog = () => screen.queryByRole('dialog', { name: "You're using your ChatGPT plan" });

    it('is confirmed once, in the window that connected', async () => {
      const user = userEvent.setup();
      state.connectors = [chatgpt({})];
      const view = render(<ConnectorsSettings />);
      await user.click(within(row()).getByRole('button', { name: 'Continue with ChatGPT' }));

      state.connectors = [chatgpt({ phase: 'Connected', email: 'ada@example.com' })];
      view.rerender(<ConnectorsSettings />);

      expect(await screen.findByRole('dialog', { name: "You're using your ChatGPT plan" })).toBeInTheDocument();
      await user.click(screen.getByRole('button', { name: 'Got it' }));
      await waitFor(() => expect(dialog()).toBeNull());
      expect(localStorage.getItem('kstack:chatgpt-plan-confirmed')).not.toBeNull();
    });

    // A hidden tab's pane unmounts: the sign-in can finish while another tab is open.
    it('is shown when the pane remounts after the sign-in finished elsewhere', async () => {
      const user = userEvent.setup();
      state.connectors = [chatgpt({})];
      const first = render(<ConnectorsSettings />);
      await user.click(within(row()).getByRole('button', { name: 'Continue with ChatGPT' }));
      first.unmount();

      state.connectors = [chatgpt({ phase: 'Connected', email: 'ada@example.com' })];
      const second = render(<ConnectorsSettings />);
      expect(await screen.findByRole('dialog', { name: "You're using your ChatGPT plan" })).toBeInTheDocument();
      await user.click(screen.getByRole('button', { name: 'Got it' }));
      await waitFor(() => expect(dialog()).toBeNull());
      second.unmount();

      // Dismissed, the window has nothing pending, whatever the shared key says.
      localStorage.clear();
      render(<ConnectorsSettings />);
      expect(dialog()).toBeNull();
    });

    // Cancelled here, then connected elsewhere: the dialog is the other window's.
    it('is not shown once the sign-in sent here has ended', async () => {
      const user = userEvent.setup();
      state.connectors = [chatgpt({})];
      const view = render(<ConnectorsSettings />);
      await user.click(within(row()).getByRole('button', { name: 'Continue with ChatGPT' }));
      state.connectors = [chatgpt({ phase: 'SigningIn' })];
      view.rerender(<ConnectorsSettings />);
      state.connectors = [chatgpt({ phase: 'SignedOut' })];
      view.rerender(<ConnectorsSettings />);

      state.connectors = [chatgpt({ phase: 'Connected', email: 'ada@example.com' })];
      view.rerender(<ConnectorsSettings />);

      expect(dialog()).toBeNull();
    });

    // The attempt failed while the pane was unmounted: mounting onto it ends it.
    it('is not shown after remounting onto a failed sign-in', async () => {
      const user = userEvent.setup();
      state.connectors = [chatgpt({})];
      const first = render(<ConnectorsSettings />);
      await user.click(within(row()).getByRole('button', { name: 'Continue with ChatGPT' }));
      first.unmount();

      state.connectors = [chatgpt({ phase: 'Failed', failure: 'Declined' })];
      const second = render(<ConnectorsSettings />);
      state.connectors = [chatgpt({ phase: 'Connected', email: 'ada@example.com' })];
      second.rerender(<ConnectorsSettings />);

      expect(dialog()).toBeNull();
    });

    it('is not shown again once another window has shown it', async () => {
      localStorage.setItem('kstack:chatgpt-plan-confirmed', '1');
      const user = userEvent.setup();
      state.connectors = [chatgpt({})];
      const view = render(<ConnectorsSettings />);
      await user.click(within(row()).getByRole('button', { name: 'Continue with ChatGPT' }));

      state.connectors = [chatgpt({ phase: 'Connected', email: 'ada@example.com' })];
      view.rerender(<ConnectorsSettings />);

      expect(dialog()).toBeNull();
    });

    it('is dismissed by Escape as by Got it', async () => {
      const user = userEvent.setup();
      state.connectors = [chatgpt({})];
      const view = render(<ConnectorsSettings />);
      await user.click(within(row()).getByRole('button', { name: 'Continue with ChatGPT' }));
      state.connectors = [chatgpt({ phase: 'Connected', email: 'ada@example.com' })];
      view.rerender(<ConnectorsSettings />);
      await screen.findByRole('dialog', { name: "You're using your ChatGPT plan" });

      await user.keyboard('{Escape}');

      await waitFor(() => expect(dialog()).toBeNull());
      expect(localStorage.getItem('kstack:chatgpt-plan-confirmed')).not.toBeNull();
    });

    // Storage that refuses reads and writes cannot say the confirmation was seen,
    // so it is shown.
    it('is shown when storage refuses', async () => {
      const user = userEvent.setup();
      const get = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new Error('refused');
      });
      const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
        throw new Error('refused');
      });
      try {
        state.connectors = [chatgpt({})];
        const view = render(<ConnectorsSettings />);
        await user.click(within(row()).getByRole('button', { name: 'Continue with ChatGPT' }));
        state.connectors = [chatgpt({ phase: 'Connected', email: 'ada@example.com' })];
        view.rerender(<ConnectorsSettings />);

        expect(await screen.findByRole('dialog', { name: "You're using your ChatGPT plan" })).toBeInTheDocument();
        await user.click(screen.getByRole('button', { name: 'Got it' }));
      } finally {
        get.mockRestore();
        set.mockRestore();
      }
    });

    it('is not shown by a window that did not connect', () => {
      state.connectors = [chatgpt({})];
      const view = render(<ConnectorsSettings />);

      state.connectors = [chatgpt({ phase: 'Connected', email: 'ada@example.com' })];
      view.rerender(<ConnectorsSettings />);

      expect(dialog()).toBeNull();
      expect(localStorage.getItem('kstack:chatgpt-plan-confirmed')).toBeNull();
    });
  });
});

describe('the OpenRouter row', () => {
  const actions: OpenRouterActions = { connect: vi.fn(), cancel: vi.fn(), disconnect: vi.fn(), openKeys: vi.fn() };

  function openRouter(connection: Partial<OpenRouterConnection>): Connector {
    const c: OpenRouterConnection = { phase: 'SignedOut', failure: null, ...connection };
    return {
      id: 'openrouter',
      name: 'OpenRouter',
      detail: 'Many providers on one account',
      connected: c.phase === 'Connected',
      openrouter: { connection: c, actions },
    };
  }

  const row = () => screen.getByRole('listitem');
  const menuButton = () => screen.queryByRole('button', { name: 'Actions for OpenRouter' });

  beforeEach(() => vi.clearAllMocks());

  it('offers Connect while signed out, with no menu', async () => {
    state.connectors = [openRouter({})];
    render(<ConnectorsSettings />);

    expect(row()).toHaveTextContent('Many providers on one account');
    await userEvent.setup().click(within(row()).getByRole('button', { name: 'Connect' }));
    expect(actions.connect).toHaveBeenCalledOnce();
    expect(menuButton()).toBeNull();
  });

  it('waits for the browser while signing in, and cancels on a press', async () => {
    state.connectors = [openRouter({ phase: 'SigningIn' })];
    render(<ConnectorsSettings />);

    expect(row()).toHaveTextContent('Waiting for the browser…');
    await userEvent.setup().click(within(row()).getByRole('button', { name: 'Cancel' }));
    expect(actions.cancel).toHaveBeenCalledOnce();
    expect(within(row()).queryByRole('button', { name: 'Connect' })).toBeNull();
    expect(menuButton()).toBeNull();
  });

  it('names the key once connected, and offers its settings and Disconnect', async () => {
    const user = userEvent.setup();
    state.connectors = [openRouter({ phase: 'Connected' })];
    render(<ConnectorsSettings />);

    expect(row()).toHaveTextContent('Key "Kstack"');
    expect(within(row()).queryByRole('button', { name: 'Connect' })).toBeNull();
    await user.click(menuButton()!);
    const items = within(await screen.findByRole('menu')).getAllByRole('menuitem');
    expect(items.map((i) => i.textContent)).toEqual(['Open OpenRouter settings', 'Disconnect']);
    expect(items[1]).toHaveAttribute('data-variant', 'destructive');
    await user.click(items[0]);
    expect(actions.openKeys).toHaveBeenCalledOnce();
  });

  describe('Disconnect', () => {
    const confirm = () => screen.queryByRole('dialog', { name: 'Disconnect OpenRouter?' });

    async function openConfirm() {
      const user = userEvent.setup();
      state.connectors = [openRouter({ phase: 'Connected' })];
      render(<ConnectorsSettings />);
      await user.click(menuButton()!);
      await user.click(await screen.findByRole('menuitem', { name: 'Disconnect' }));
      return { user, dialog: await screen.findByRole('dialog', { name: 'Disconnect OpenRouter?' }) };
    }

    it('says where the key stays live, and disconnects only once confirmed', async () => {
      const { user, dialog } = await openConfirm();

      expect(dialog).toHaveTextContent(
        'Kstack will forget this key. It stays active on OpenRouter until you delete the key labelled "Kstack" in your OpenRouter settings.',
      );
      expect(actions.disconnect).not.toHaveBeenCalled();
      await user.click(within(dialog).getByRole('button', { name: 'Disconnect' }));

      expect(actions.disconnect).toHaveBeenCalledOnce();
      await waitFor(() => expect(confirm()).toBeNull());
    });

    it('opens the settings from the confirm, and Cancel sends nothing', async () => {
      const { user, dialog } = await openConfirm();

      await user.click(within(dialog).getByRole('button', { name: 'Open OpenRouter settings' }));
      expect(actions.openKeys).toHaveBeenCalledOnce();
      await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));

      await waitFor(() => expect(confirm()).toBeNull());
      expect(actions.disconnect).not.toHaveBeenCalled();
    });

    it('is dismissed by Escape, sending nothing', async () => {
      const { user } = await openConfirm();

      await user.keyboard('{Escape}');

      await waitFor(() => expect(confirm()).toBeNull());
      expect(actions.disconnect).not.toHaveBeenCalled();
    });
  });

  it('says the environment key is in use, and offers nothing', () => {
    state.connectors = [openRouter({ phase: 'FromEnvironment' })];
    render(<ConnectorsSettings />);

    expect(row()).toHaveTextContent('Using OPENROUTER_API_KEY');
    expect(within(row()).getByText('OPENROUTER_API_KEY').tagName).toBe('CODE');
    expect(within(row()).queryByRole('button')).toBeNull();
  });

  it.each([
    ['TimedOut', 'The sign-in timed out.'],
    ['Unavailable', 'OpenRouter could not be reached.'],
    ['Refused', 'OpenRouter refused the sign-in.'],
    ['NotSaved', 'This computer could not save the key.'],
    ['KeyRejected', 'OpenRouter no longer accepts the key. Connect again.'],
  ] as const)('says why it failed (%s), and offers Connect again', async (failure, text) => {
    state.connectors = [openRouter({ phase: 'Failed', failure })];
    render(<ConnectorsSettings />);

    expect(row()).toHaveTextContent(text);
    await userEvent.setup().click(within(row()).getByRole('button', { name: 'Connect' }));
    expect(actions.connect).toHaveBeenCalledOnce();
  });

  it('says a key the keyring kept returns on restart, and offers Retry and the settings', async () => {
    const user = userEvent.setup();
    state.connectors = [openRouter({ phase: 'Failed', failure: 'NotDeleted' })];
    render(<ConnectorsSettings />);

    expect(row()).toHaveTextContent(
      'Kstack could not remove the key from this machine. It will be used again after a restart. Retry, or delete the key labelled "Kstack" on OpenRouter.',
    );
    expect(within(row()).queryByRole('button', { name: 'Connect' })).toBeNull();
    await user.click(within(row()).getByRole('button', { name: 'Retry' }));
    expect(actions.disconnect).toHaveBeenCalledOnce();
    await user.click(within(row()).getByRole('button', { name: 'Open OpenRouter settings' }));
    expect(actions.openKeys).toHaveBeenCalledOnce();
  });
});
