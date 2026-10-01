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

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

// One seam: urql's hooks. The outbox is the real one, since what the composer draws
// is its entry, and so is the catalog it picks from.
const { sendMock, cancelMock, catalog, reexecute } = vi.hoisted(() => ({
  sendMock: vi.fn(),
  cancelMock: vi.fn(),
  reexecute: vi.fn(),
  catalog: { current: {} },
}));

vi.mock('urql', () => ({
  useMutation: (doc: { __kind?: string }) => [{}, doc.__kind === 'cancel' ? cancelMock : sendMock],
  useQuery: () => [catalog.current, reexecute],
}));
vi.mock('@/gql', () => ({
  graphql: (source: string) => ({ __kind: source.includes('chatCancel') ? 'cancel' : 'send' }),
}));

const { ChatOutboxProvider, useChatOutbox } = await import('@/lib/chat-outbox');
const { ChatComposer } = await import('./chat-composer');

const accepted = () => ({ data: { chatSend: { id: 'm2', chatID: 'c1', seq: 2, status: 'Streaming' } } });
const refused = (code: string) => ({ error: { graphQLErrors: [{ extensions: { code } }] } });
const dropped = () => ({ error: { networkError: new Error('sidecar unreachable'), graphQLErrors: [] } });

const onCreated = vi.fn();

type Props = Partial<Parameters<typeof ChatComposer>[0]>;

// The provider outlives the composer, as the layout's does. `replaced` swaps the
// key, so the composer unmounts and a fresh one opens on the same chat.
function Harness({ replaced = false, ...props }: Props & { replaced?: boolean }) {
  return (
    <ChatOutboxProvider>
      <ChatComposer
        key={replaced ? 'second' : 'first'}
        chatID="c1"
        mode="chat"
        clusterID="1"
        phase="live"
        last={null}
        onCreated={onCreated}
        sandboxDisabled={false}
        {...props}
      />
      {/* The entry the composer writes into, which the transcript's Ask again reads. */}
      <SendStatus chatID={props.chatID ?? 'c1'} />
    </ChatOutboxProvider>
  );
}

function SendStatus({ chatID }: { chatID: string | null }) {
  const { send, pick } = useChatOutbox('chat', chatID);
  return (
    <>
      <span data-testid="send-status">{send.status}</span>
      <span data-testid="pick">{pick?.model.id ?? ''}</span>
    </>
  );
}

function renderComposer(props: Props & { replaced?: boolean } = {}) {
  const view = render(<Harness {...props} />);
  return {
    ...view,
    rerender: (next: Props & { replaced?: boolean }) => view.rerender(<Harness {...props} {...next} />),
  };
}

const box = () => screen.getByPlaceholderText('Message…');
const button = (name: string) => screen.getByRole('button', { name });

const type = (text: string) => fireEvent.change(box(), { target: { value: text } });

const segment = (label: string) => screen.getByRole('button', { name: new RegExp(`^${label}:`) });

// The menu portals in asynchronously, so wait for the item rather than reading it.
const menuItem = (name: string) => screen.findByRole('menuitemradio', { name });

const click = (name: string) =>
  act(async () => {
    fireEvent.click(button(name));
  });

const fake = {
  provider: { id: 'fake', label: 'Fake' },
  id: 'fake',
  label: 'Fake model',
  efforts: ['low', 'high'],
  defaultEffort: 'high',
};
const plain = {
  provider: { id: 'fake', label: 'Fake' },
  id: 'plain',
  label: 'Plain model',
  efforts: [],
  defaultEffort: '',
};
const opus = {
  provider: { id: 'anthropic', label: 'Anthropic' },
  id: 'claude-opus-5',
  label: 'Opus 5',
  efforts: ['low', 'high'],
  defaultEffort: 'high',
};

const answered = { fetching: false, data: { models: [fake, plain] } };

// base-ui scrolls the highlighted item into view; jsdom has no implementation.
beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

beforeEach(() => {
  vi.clearAllMocks();
  catalog.current = answered;
  sendMock.mockResolvedValue(accepted());
  cancelMock.mockResolvedValue({ data: { chatCancel: true } });
});

describe('ChatComposer', () => {
  it('sends the draft and clears the box', async () => {
    renderComposer();
    type('hello');
    await click('Send');

    expect(sendMock).toHaveBeenCalledTimes(1);
    expect(sendMock.mock.calls[0][0]).toMatchObject({
      chatID: 'c1',
      mode: 'Chat',
      clusterID: '1',
      content: 'hello',
    });
    expect(box()).toHaveValue('');
  });

  it('does not send an empty message', async () => {
    renderComposer();
    type('   ');
    expect(button('Send')).toBeDisabled();
  });

  it('sends on Enter and inserts a newline on Shift+Enter', async () => {
    renderComposer();
    type('hello');
    await act(async () => {
      fireEvent.keyDown(box(), { key: 'Enter', shiftKey: true });
    });
    expect(sendMock).not.toHaveBeenCalled();

    await act(async () => {
      fireEvent.keyDown(box(), { key: 'Enter' });
    });
    expect(sendMock).toHaveBeenCalledTimes(1);
  });

  it('lets an IME confirm a character with Enter without sending', async () => {
    renderComposer();
    type('にほんご');
    await act(async () => {
      fireEvent.keyDown(box(), { key: 'Enter', isComposing: true });
    });
    expect(sendMock).not.toHaveBeenCalled();
  });

  it('sends nothing while the transcript is still connecting, and sends while reconnecting', async () => {
    const view = renderComposer({ phase: 'connecting' });
    type('hello');
    expect(button('Send')).toBeDisabled();

    view.rerender({ phase: 'reconnecting' });
    await click('Send');
    expect(sendMock).toHaveBeenCalledTimes(1);
  });

  it('holds the box read-only while a send is in flight', async () => {
    let resolve!: (value: unknown) => void;
    sendMock.mockReturnValue(
      new Promise((r) => {
        resolve = r;
      }),
    );
    renderComposer();
    type('hello');
    await click('Send');

    expect(box()).toHaveAttribute('readonly');
    expect(box()).toHaveValue('hello');
    expect(button('Send')).toBeDisabled();

    await act(async () => {
      resolve(accepted());
    });
    expect(box()).not.toHaveAttribute('readonly');
    expect(box()).toHaveValue('');
  });

  // The row is committed but the watch has not delivered it: Send here would go out
  // against a "last message" that is still the one from before.
  it('stays disabled after the send resolves until the accepted row lands', async () => {
    const view = renderComposer();
    type('hello');
    await click('Send');

    type('another');
    expect(button('Send')).toBeDisabled();

    view.rerender({ last: { seq: 2, status: 'Complete' } });
    expect(button('Send')).toBeEnabled();
  });

  it('offers Cancel in Send’s place while the last message is streaming', async () => {
    renderComposer({ last: { seq: 2, status: 'Streaming' } });
    expect(screen.queryByRole('button', { name: 'Send' })).not.toBeInTheDocument();

    await click('Cancel');
    expect(cancelMock).toHaveBeenCalledWith({ chatID: 'c1' });
  });

  it('treats an answer waiting on a command like a streaming one', async () => {
    renderComposer({ last: { seq: 2, status: 'WaitingApproval' } });
    expect(screen.queryByRole('button', { name: 'Send' })).not.toBeInTheDocument();
    type('and another thing');
    await act(async () => {
      fireEvent.keyDown(box(), { key: 'Enter' });
    });
    expect(sendMock).not.toHaveBeenCalled();
    await click('Cancel');
    expect(cancelMock).toHaveBeenCalledWith({ chatID: 'c1' });
  });

  // One turn per chat: Enter has to refuse for the same reason the button is a Cancel.
  it('sends nothing on Enter while the answer is still streaming', async () => {
    renderComposer({ last: { seq: 2, status: 'Streaming' } });
    type('and another thing');
    await act(async () => {
      fireEvent.keyDown(box(), { key: 'Enter' });
    });
    expect(sendMock).not.toHaveBeenCalled();
  });

  it('reports the chat the first send created', async () => {
    renderComposer({ chatID: null, mode: 'dashboard' });
    type('hello');
    await click('Send');

    expect(sendMock.mock.calls[0][0]).toMatchObject({ chatID: null, mode: 'Dashboard' });
    expect(onCreated).toHaveBeenCalledWith('c1');
    expect(box()).toHaveValue('');
  });

  // A chat is filed under a cluster, so there is nothing to start one under until
  // the window is on one. The two reasons for the same disabled button are told apart
  // by the placeholder: at startup the clusters watch simply has not answered yet.
  it('cannot start a chat with no cluster, and says so once the clusters watch has answered', () => {
    const view = renderComposer({ chatID: null, clusterID: undefined, phase: 'connecting' });
    expect(screen.getByPlaceholderText('Message…')).toBeInTheDocument();

    view.rerender({ phase: 'live' });
    const empty = screen.getByPlaceholderText('Pick a cluster to start a chat');
    fireEvent.change(empty, { target: { value: 'hello' } });
    expect(button('Send')).toBeDisabled();
  });

  // The chat's own cluster is what a send into it carries, so it goes out whatever
  // the window is on.
  it('sends into an open chat with no active cluster', async () => {
    renderComposer({ clusterID: '2' });
    type('hello');
    await click('Send');

    expect(sendMock.mock.calls[0][0]).toMatchObject({ chatID: 'c1', clusterID: '2' });
  });

  it('reports nothing once the composer that sent has gone', async () => {
    let resolve!: (value: unknown) => void;
    sendMock.mockReturnValue(
      new Promise((r) => {
        resolve = r;
      }),
    );
    const view = renderComposer({ chatID: null });
    type('hello');
    await click('Send');

    view.unmount();
    await act(async () => {
      resolve(accepted());
    });
    expect(onCreated).not.toHaveBeenCalled();
  });

  // The composer of a chat that has not started stays mounted across a cluster
  // switch, and the send it has in flight is filed under the cluster it left with —
  // following it would open that chat under a cluster it does not belong to.
  it('reports nothing once the window has switched cluster', async () => {
    let resolve!: (value: unknown) => void;
    sendMock.mockReturnValue(
      new Promise((r) => {
        resolve = r;
      }),
    );
    const view = renderComposer({ chatID: null });
    type('hello');
    await click('Send');

    view.rerender({ chatID: null, clusterID: '2' });
    await act(async () => {
      resolve(accepted());
    });
    expect(onCreated).not.toHaveBeenCalled();
  });

  it('keeps the draft when the send is refused', async () => {
    sendMock.mockResolvedValue(refused('KSTACK_CONFLICT'));
    renderComposer();
    type('hello');
    await click('Send');

    expect(box()).toHaveValue('hello');
    expect(box()).not.toHaveAttribute('readonly');
    expect(button('Send')).toBeEnabled();
  });

  // The line names the model the chat is too long for, since another may still
  // take it: the draft stays and Send stays open for the switch — and the switch
  // does not move the line onto the new model, which has not refused anything.
  it('draws a full chat with the model it is too long for', async () => {
    const user = userEvent.setup();
    sendMock.mockResolvedValue(refused('KSTACK_CHAT_CONTEXT_FULL'));
    renderComposer();
    type('hello');
    await click('Send');

    expect(screen.getByText(/^This chat is longer than Fake model can read\./)).toBeInTheDocument();
    expect(box()).toHaveValue('hello');
    expect(button('Send')).toBeEnabled();

    await user.click(segment('Model'));
    await user.click(await menuItem('Plain model'));
    expect(screen.getByText(/^This chat is longer than Fake model can read\./)).toBeInTheDocument();
  });

  it('holds a send that got no answer, and retries it unchanged', async () => {
    sendMock.mockResolvedValue(dropped());
    renderComposer();
    type('hello');
    await click('Send');

    expect(screen.queryByRole('button', { name: 'Send' })).not.toBeInTheDocument();
    expect(box()).toHaveAttribute('readonly');

    sendMock.mockResolvedValue(accepted());
    await click('Retry');
    expect(sendMock).toHaveBeenCalledTimes(2);
    expect(sendMock.mock.calls[1][0]).toEqual(sendMock.mock.calls[0][0]);
    expect(box()).toHaveValue('');
  });

  // The sidecar refuses a send whose switch differs from the chat's, so the send
  // says which one the composer showed.
  it('sends the switch it shows', async () => {
    renderComposer({ sandboxDisabled: true });
    type('hello');
    await click('Send');
    expect(sendMock.mock.calls[0][0]).toMatchObject({ sandboxDisabled: true });
  });

  it('holds Send for an open chat until the list delivers its switch', () => {
    renderComposer({ sandboxDisabled: undefined });
    type('hello');
    expect(button('Send')).toBeDisabled();
  });

  it('starts a chat sandboxed', async () => {
    renderComposer({ chatID: null, sandboxDisabled: undefined });
    type('hello');
    await click('Send');
    expect(sendMock.mock.calls[0][0]).toMatchObject({ sandboxDisabled: false });
  });

  it('says when the switch changed under a send, and keeps the draft', async () => {
    sendMock.mockResolvedValue(refused('KSTACK_CHAT_SANDBOX_CHANGED'));
    renderComposer();
    type('hello');
    await click('Send');
    expect(screen.getByText("This chat's sandbox switch changed. Check it, then send again.")).toBeInTheDocument();
    expect(box()).toHaveValue('hello');
    expect(button('Send')).toBeEnabled();
  });

  it('follows a retry that created the chat', async () => {
    sendMock.mockResolvedValue(dropped());
    renderComposer({ chatID: null });
    type('hello');
    await click('Send');

    sendMock.mockResolvedValue(accepted());
    await click('Retry');
    expect(onCreated).toHaveBeenCalledWith('c1');
  });

  it('returns a discarded send to the box as an ordinary draft', async () => {
    sendMock.mockResolvedValue(dropped());
    renderComposer();
    type('hello');
    await click('Send');

    await click('Discard');
    expect(box()).toHaveValue('hello');
    expect(box()).not.toHaveAttribute('readonly');
    expect(button('Send')).toBeEnabled();
  });

  // The draft and the send are the outbox's, so a second composer on the same chat
  // reads what the first left, and a send that lands after the first has gone still
  // clears what it delivered.
  it('keeps its draft across a remount', () => {
    const view = renderComposer();
    type('half a question');

    view.rerender({ replaced: true });
    expect(box()).toHaveValue('half a question');
  });

  it('clears a delivered draft even once the composer that sent it has gone', async () => {
    let resolve!: (value: unknown) => void;
    sendMock.mockReturnValue(
      new Promise((r) => {
        resolve = r;
      }),
    );
    const view = renderComposer();
    type('hello');
    await click('Send');

    view.rerender({ replaced: true });
    expect(box()).toHaveValue('hello');
    await act(async () => {
      resolve(accepted());
    });
    expect(box()).toHaveValue('');
  });

  // The entry's one send is what Ask again and the next submit wait on, and only a
  // composer watching the messages can see the accepted row arrive.
  it('settles the send once the row it is awaiting lands', async () => {
    const view = renderComposer();
    type('hello');
    await click('Send');
    expect(button('Send')).toBeDisabled();

    view.rerender({ last: { seq: 2, status: 'Complete' } });

    expect(screen.getByTestId('send-status')).toHaveTextContent('idle');
  });

  // The two selects sit beside Send, on every chat: what answers, and how hard it
  // thinks.
  it('picks the model, then its effort', async () => {
    const user = userEvent.setup();
    renderComposer();
    expect(segment('Model')).toHaveAccessibleName('Model: Fake model');
    expect(segment('Effort')).toHaveAccessibleName('Effort: high');

    await user.click(segment('Effort'));
    await user.click(await menuItem('low'));
    expect(segment('Effort')).toHaveAccessibleName('Effort: low');

    type('hello');
    await click('Send');
    expect(sendMock.mock.calls[0][0]).toMatchObject({ providerID: 'fake', modelID: 'fake', effort: 'low' });
  });

  // The levels are the provider's own words, so the old pick may not exist on the
  // new model.
  it("resets the effort to the new model's default", async () => {
    const user = userEvent.setup();
    renderComposer();
    await user.click(segment('Effort'));
    await user.click(await menuItem('low'));

    await user.click(segment('Model'));
    await user.click(await menuItem('Plain model'));
    // Picking leaves the menu open, and while one is open the rest of the page takes
    // no pointer events.
    await user.keyboard('{Escape}');
    await user.click(screen.getByRole('button', { name: 'Model: Plain model' }));
    await user.click(await menuItem('Fake model'));

    expect(await screen.findByRole('button', { name: /^Effort:/ })).toHaveAccessibleName('Effort: high');
  });

  // A heading per provider, and only once there is more than one to tell apart.
  it('groups the models by provider when there is more than one', async () => {
    const user = userEvent.setup();
    catalog.current = { fetching: false, data: { models: [fake, plain, opus] } };
    renderComposer();

    await user.click(segment('Model'));
    await menuItem('Opus 5');
    const menu = screen.getByRole('menu');
    expect(within(menu).getByText('Fake')).toBeInTheDocument();
    expect(within(menu).getByText('Anthropic')).toBeInTheDocument();
    // Catalog order: the fake's two, then Anthropic's one.
    expect(screen.getAllByRole('menuitemradio').map((item) => item.textContent)).toEqual([
      'Fake model',
      'Plain model',
      'Opus 5',
    ]);
  });

  it('draws no provider heading for a catalog of one provider', async () => {
    const user = userEvent.setup();
    renderComposer();

    await user.click(segment('Model'));
    await menuItem('Fake model');
    expect(within(screen.getByRole('menu')).queryByText('Fake')).toBeNull();
  });

  it('hides the effort select for a model with no such knob', async () => {
    const user = userEvent.setup();
    renderComposer();

    await user.click(segment('Model'));
    await user.click(await menuItem('Plain model'));

    expect(screen.queryByRole('button', { name: /^Effort:/ })).toBeNull();
  });

  it('says when a request waits out of view, and Show only shows it', () => {
    const onShowWaiting = vi.fn();
    renderComposer({ onShowWaiting });

    expect(screen.getByText('An agent is waiting on you.')).toBeInTheDocument();
    fireEvent.click(button('Show'));
    expect(onShowWaiting).toHaveBeenCalledTimes(1);
  });

  it('says nothing of a waiting request with none out of view', () => {
    renderComposer();

    expect(screen.queryByText('An agent is waiting on you.')).toBeNull();
  });

  // A chat can move to any model, whichever provider wrote its answers so far.
  it("lists every catalog model in an open chat's select", async () => {
    const user = userEvent.setup();
    catalog.current = { fetching: false, data: { models: [fake, plain, opus] } };
    renderComposer();

    await user.click(segment('Model'));
    await menuItem('Opus 5');
    expect(screen.getAllByRole('menuitemradio').map((item) => item.textContent)).toEqual([
      'Fake model',
      'Plain model',
      'Opus 5',
    ]);
  });

  // No action in a session stores a pick the list lacks, so the state is set
  // directly: a pick made under one catalog, then a composer mounted under another.
  // The guard is what makes the composer correct whatever the outbox holds.
  describe('a stored pick the catalog no longer has', () => {
    const picked = async (view: ReturnType<typeof renderComposer>) => {
      const user = userEvent.setup();
      await user.click(segment('Model'));
      await user.click(await menuItem('Plain model'));
      expect(screen.getByTestId('pick')).toHaveTextContent('plain');
      return view;
    };

    it('is written back to the seed, and Send waits on the write-back', async () => {
      catalog.current = { fetching: false, data: { models: [fake, plain] } };
      const view = await picked(renderComposer());

      catalog.current = { fetching: false, data: { models: [fake] } };
      view.rerender({ replaced: true });
      type('hello');

      expect(segment('Model')).toHaveAccessibleName('Model: Fake model');
      expect(screen.getByTestId('pick')).toHaveTextContent('fake');
      expect(button('Send')).toBeEnabled();
      await click('Send');
      expect(sendMock.mock.calls[0][0]).toMatchObject({ providerID: 'fake', modelID: 'fake', effort: 'high' });
    });

    it('leaves Send disabled and the pick alone with nothing to seed', async () => {
      catalog.current = { fetching: false, data: { models: [fake, plain] } };
      const view = await picked(renderComposer());

      catalog.current = { fetching: false, data: { models: [] } };
      view.rerender({ replaced: true });
      fireEvent.change(screen.getByRole('textbox'), { target: { value: 'hello' } });

      expect(button('Send')).toBeDisabled();
      expect(screen.queryByRole('button', { name: /^Model:/ })).toBeNull();
      expect(screen.getByTestId('pick')).toHaveTextContent('plain');
    });

    it('retries a held send with the model it holds', async () => {
      catalog.current = { fetching: false, data: { models: [fake, plain] } };
      const view = await picked(renderComposer());
      sendMock.mockResolvedValue(dropped());
      type('hello');
      await click('Send');

      catalog.current = { fetching: false, data: { models: [fake] } };
      view.rerender({ replaced: true });
      sendMock.mockResolvedValue(accepted());
      await click('Retry');

      expect(sendMock).toHaveBeenCalledTimes(2);
      expect(sendMock.mock.calls[1][0]).toEqual(sendMock.mock.calls[0][0]);
      expect(sendMock.mock.calls[1][0]).toMatchObject({ modelID: 'plain' });
    });
  });

  // An empty catalog means no key is set and no local daemon answered at startup,
  // and the box says so — both, and the restart, since discovery does not run again.
  it('says what would fill the catalog when it is empty', () => {
    catalog.current = { fetching: false, data: { models: [] } };
    renderComposer();

    const textarea = screen.getByRole('textbox');
    expect(textarea).toHaveAttribute('placeholder', 'Set an API key, then restart Kstack');
    fireEvent.change(textarea, { target: { value: 'hello' } });
    expect(button('Send')).toBeDisabled();
    expect(screen.queryByRole('button', { name: /^Model:/ })).toBeNull();
  });

  // A catalog that fails after the watch is already live — or one that had failed
  // before this composer mounted — is the same predicament, and gets the same one ask.
  it('asks once for a catalog that failed with the watch already live', () => {
    catalog.current = { fetching: false, error: new Error('refused') };
    const view = renderComposer({ phase: 'live' });
    expect(reexecute).toHaveBeenCalledTimes(1);

    catalog.current = { fetching: true };
    view.rerender({ phase: 'live' });
    catalog.current = { fetching: false, error: new Error('refused') };
    view.rerender({ phase: 'live' });
    expect(reexecute).toHaveBeenCalledTimes(1);

    // The watch going live again is the evidence the sidecar is answering, so the
    // chance comes back with it.
    view.rerender({ phase: 'reconnecting' });
    view.rerender({ phase: 'live' });
    expect(reexecute).toHaveBeenCalledTimes(2);
  });

  // The catalog is a query, so nothing re-runs it when the sidecar comes back — and
  // without one there is no pick and nothing can be sent. The watch this composer
  // already waits on is the signal that the sidecar is answering again.
  it('asks for the catalog again when its watch reconnects', async () => {
    catalog.current = { fetching: false, error: new Error('unreachable') };
    const view = renderComposer({ phase: 'reconnecting' });
    type('hello');
    expect(button('Send')).toBeDisabled();
    expect(reexecute).not.toHaveBeenCalled();

    // The failure stands until the query is answered again, so the reconnection is
    // what asks, and the answer is what enables Send.
    view.rerender({ phase: 'live' });
    expect(reexecute).toHaveBeenCalledTimes(1);

    // A failure the sidecar answered with is not a reason to ask again: the ask
    // clears it and the answer sets it, which is a request loop.
    catalog.current = { fetching: true };
    view.rerender({ phase: 'live' });
    catalog.current = { fetching: false, error: new Error('refused') };
    view.rerender({ phase: 'live' });
    expect(reexecute).toHaveBeenCalledTimes(1);

    catalog.current = answered;
    view.rerender({ phase: 'live' });
    expect(button('Send')).toBeEnabled();
  });
});
