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

import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Profiler, useSyncExternalStore } from 'react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import type { LogLine } from '@/lib/log-viewer';
import { createLogViewportHandle, EMPTY_LOG_VIEWPORT } from '@/lib/log-viewport';
import type { LogViewportHandle } from '@/lib/log-viewport';
import type { LogWindow } from '@/lib/log-windows';
import type { LogsViewAction } from '@/lib/logs-view';
import { expectProseText, fakeLogWindows } from '@/test-utils';

// One seam: urql's hooks. The outbox is the real one, since what the composer draws
// is its entry, and so is the catalog it picks from.
const { sendMock, cancelMock, catalog, reexecute } = vi.hoisted(() => ({
  sendMock: vi.fn(),
  cancelMock: vi.fn(),
  reexecute: vi.fn(),
  catalog: { current: {} },
}));

vi.mock('urql', () => ({
  useMutation: (doc: { kind?: string }) => [{}, doc.kind === 'cancel' ? cancelMock : sendMock],
  useQuery: () => [catalog.current, reexecute],
}));
vi.mock('@/gql', () => ({
  graphql: (source: string) => ({ kind: source.includes('chatCancel') ? 'cancel' : 'send' }),
}));

const { ChatOutboxProvider, useChatOutbox } = await import('@/lib/chat-outbox');
const { LogsViewProvider, useLogsView } = await import('@/lib/logs-view');
const { RightSidebarProvider, useRightSidebar } = await import('@/lib/right-sidebar');
const { LogWindowsContext } = await import('@/lib/log-windows');
const { ChatComposer } = await import('./composer');

const accepted = () => ({ data: { chatSend: { id: 'm2', chatID: 'c1', seq: 2, status: 'Streaming' } } });
const refused = (code: string) => ({ error: { graphQLErrors: [{ extensions: { code } }] } });
const dropped = () => ({ error: { networkError: new Error('sidecar unreachable'), graphQLErrors: [] } });

const onCreated = vi.fn();
const composerRendered = vi.fn();

type Props = Partial<Parameters<typeof ChatComposer>[0]>;

// A line the user selected in the focused view.
const selectedLine = (i: number): LogLine => ({
  timestamp: `2026-10-17T09:14:0${i}.000Z`,
  cursor: `c${i}`,
  message: `line ${i}`,
  truncatedBytes: 0,
  stream: { namespace: 'payments', pod: 'webapp-x', container: 'app', previous: false },
  place: { node: '', region: '', zone: '', os: '', arch: '' },
  gap: null,
});

// What a test does to the window's focused view and right sidebar, as the
// transcript card's Expand and the viewer would.
function ViewControls() {
  const { set, viewport } = useLogsView();
  const { close } = useRightSidebar();
  const focus = (chatId: string) => set({ clusterId: '1', chatId, callId: 'call-1', action: {} as LogsViewAction });
  const selected = useSyncExternalStore(viewport.subscribe, () => viewport.get().selection.length);
  return (
    <>
      <span data-testid="viewer-selection">{selected}</span>
      <button type="button" onClick={() => focus('c1')}>
        Focus c1
      </button>
      <button type="button" onClick={() => focus('c2')}>
        Focus c2
      </button>
      <button
        type="button"
        onClick={() =>
          viewport.set({ ...EMPTY_LOG_VIEWPORT, selection: [0, 1, 2].map(selectedLine), selectedCount: 3 })
        }
      >
        Select three
      </button>
      <button
        type="button"
        onClick={() =>
          viewport.set({
            ...EMPTY_LOG_VIEWPORT,
            selection: Array.from({ length: 212 }, (_, i) => selectedLine(i)),
            selectedCount: 212,
          })
        }
      >
        Select 212
      </button>
      {/* A following tail: the range moves, the selection array stays. */}
      <button type="button" onClick={() => viewport.set({ ...viewport.get(), visible: viewport.get().visible + 1 })}>
        Tail
      </button>
      <button type="button" onClick={close}>
        Close sidebar
      </button>
    </>
  );
}

// The log window the app window's provider would pick for chat c1, if any: its call
// and the mirror of its viewport.
const inWindow: { current: { window: LogWindow; viewport: LogViewportHandle } | null } = { current: null };
const logWindows = {
  ...fakeLogWindows().value,
  shownWindowOf: (chatId: string) => (chatId === 'c1' ? inWindow.current : null),
};

// The providers outlive the composer, as the layout's do. `replaced` swaps the
// key, so the composer unmounts and a fresh one opens on the same chat.
function Harness({ replaced = false, ...props }: Props & { replaced?: boolean }) {
  return (
    <RightSidebarProvider mode={props.mode ?? 'chat'}>
      <LogsViewProvider>
        <ViewControls />
        <LogWindowsContext.Provider value={logWindows}>
          <ChatOutboxProvider>
            <Profiler id="composer" onRender={composerRendered}>
              <ChatComposer
                key={replaced ? 'second' : 'first'}
                chatID="c1"
                mode="chat"
                clusterID="1"
                phase="live"
                last={null}
                onCreated={onCreated}
                sandboxDisabled={false}
                networkEnabled={false}
                {...props}
              />
            </Profiler>
            {/* The entry the composer writes into, which the transcript's Ask again reads. */}
            <SendStatus chatID={props.chatID ?? 'c1'} />
          </ChatOutboxProvider>
        </LogWindowsContext.Provider>
      </LogsViewProvider>
    </RightSidebarProvider>
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

const PLACEHOLDER = 'Ask about this cluster, or paste a manifest, log or kubectl output…';
const box = () => screen.getByPlaceholderText(PLACEHOLDER);
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
  const sandboxButton = () => screen.queryByRole('button', { name: /sandbox/i });

  it('draws attach, mention and slash commands as placeholders that do nothing', async () => {
    renderComposer();
    const names = ['Add attachment', 'Mention a resource', 'Slash commands'];
    names.forEach((name) => expect(button(name)).toHaveAttribute('aria-disabled', 'true'));
    await userEvent.click(button('Add attachment'));
    expect(sendMock).not.toHaveBeenCalled();
  });

  // The button's own states are `sandbox-popover.test.tsx`'s.
  it('draws the sandbox button where there is a sandbox', () => {
    const view = renderComposer({ sandboxAvailable: true, sandboxDisabled: true });
    expect(sandboxButton()).toHaveTextContent('Outside the sandbox');
    // A chat that has not started runs in the sandbox.
    view.rerender({ chatID: null, sandboxAvailable: true, sandboxDisabled: undefined });
    expect(sandboxButton()).toHaveTextContent('Sandbox');
    expect(sandboxButton()).toBeEnabled();

    view.rerender({ sandboxAvailable: false, sandboxDisabled: false });
    expect(sandboxButton()).toBeNull();
    view.rerender({ sandboxAvailable: undefined, sandboxDisabled: false });
    expect(sandboxButton()).toBeNull();
  });

  // A send reserves its turn under the switches as they are, so it waits for a
  // switch in flight to land.
  it('holds Send while a switch is in flight', () => {
    renderComposer({ sandboxAvailable: true, sandboxDisabled: true, switching: true });
    type('hello');
    expect(button('Send')).toBeDisabled();
    fireEvent.keyDown(box(), { key: 'Enter' });
    expect(sendMock).not.toHaveBeenCalled();
  });

  // The chat's own rules sit in the toolbar, for an open chat holding any.
  it("draws the chat's rules for an open chat that holds any", () => {
    catalog.current = {
      ...answered,
      data: { ...answered.data, chatGrants: [{ id: 'r1', line: 'Allow cluster writes in dev / web' }] },
    };
    const view = renderComposer();
    expect(screen.getByRole('button', { name: /1 allowed/ })).toBeInTheDocument();

    view.rerender({ chatID: null });
    expect(screen.queryByRole('button', { name: /allowed/ })).toBeNull();
  });

  const networkButton = () => screen.queryByRole('button', { name: /^(no network|network on)$/i });

  // The network switch sits beside the sandbox switch, for the open chat in it:
  // outside the sandbox it changes nothing.
  it('draws the network switch for an open chat in the sandbox', () => {
    const view = renderComposer({ sandboxAvailable: true, networkAvailable: true });
    expect(networkButton()).toHaveTextContent('No network');

    view.rerender({ sandboxAvailable: true, networkAvailable: true, networkEnabled: true });
    expect(networkButton()).toHaveTextContent('Network on');
    view.rerender({ sandboxAvailable: true, networkAvailable: true, networkEnabled: undefined });
    expect(networkButton()).toBeDisabled();

    view.rerender({ sandboxAvailable: true, networkAvailable: true, sandboxDisabled: true });
    expect(networkButton()).toBeNull();
    view.rerender({ sandboxAvailable: false, networkAvailable: true });
    expect(networkButton()).toBeNull();
    view.rerender({ chatID: null, sandboxAvailable: true, networkAvailable: true });
    expect(networkButton()).toBeNull();
  });

  it('hands a press of the network switch to the pane', async () => {
    const onSwitchNetwork = vi.fn();
    renderComposer({ sandboxAvailable: true, networkAvailable: true, networkEnabled: true, onSwitchNetwork });
    await act(async () => {
      fireEvent.click(networkButton()!);
    });
    expect(onSwitchNetwork).toHaveBeenCalledWith(false);
  });

  // The sidecar refuses a send whose network switch differs from the chat's too.
  it('sends the network switch it shows, and holds Send until the list delivers it', async () => {
    const view = renderComposer({ sandboxAvailable: true, networkAvailable: true, networkEnabled: undefined });
    type('hello');
    expect(button('Send')).toBeDisabled();

    view.rerender({ sandboxAvailable: true, networkAvailable: true, networkEnabled: true });
    await click('Send');
    expect(sendMock.mock.calls[0][0]).toMatchObject({ networkEnabled: true, networkThisTurn: false });
  });

  it('disables the network switch where network is unavailable, saying why', () => {
    renderComposer({ sandboxAvailable: true, networkAvailable: false, networkReason: 'pasta not found' });
    expect(networkButton()).toBeDisabled();
    expect(screen.getByText('No network on this machine: pasta not found')).toBeInTheDocument();
  });

  it('says when the network switch changed under a send, and keeps the draft', async () => {
    sendMock.mockResolvedValue(refused('KSTACK_CHAT_NETWORK_CHANGED'));
    renderComposer({ sandboxAvailable: true, networkAvailable: true });
    type('hello');
    await click('Send');
    expect(screen.getByText("This chat's network switch changed. Check it, then send again.")).toBeInTheDocument();
    expect(box()).toHaveValue('hello');
  });

  it('keeps spellcheck in the box but no other suggestions', () => {
    renderComposer();
    expectProseText(box());
  });

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
    expect(screen.getByPlaceholderText(PLACEHOLDER)).toBeInTheDocument();

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
    renderComposer({ sandboxAvailable: true, sandboxDisabled: true });
    type('hello');
    await click('Send');
    expect(sendMock.mock.calls[0][0]).toMatchObject({ sandboxDisabled: true });
  });

  it('holds Send for an open chat until the list delivers its switch', () => {
    renderComposer({ sandboxAvailable: true, sandboxDisabled: undefined });
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
    renderComposer({ sandboxAvailable: true });
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

// What the user looks at rides the send: the focused view, when it is this chat's
// and the sidebar beside the chat shows it. The chip above the box says so.
describe('the view', () => {
  beforeEach(() => localStorage.clear());

  const unsend = "Don't send the selected lines";

  const sent = async () => {
    type('what is this?');
    await click('Send');
    return sendMock.mock.calls[0][0];
  };

  it("rides with this chat's view open in the sidebar, with the selected lines", async () => {
    renderComposer();
    fireEvent.click(button('Focus c1'));
    expect(screen.getByText('With the view in the sidebar')).toBeInTheDocument();
    fireEvent.click(button('Select three'));
    expect(screen.getByText('With the view in the sidebar and 3 selected lines')).toBeInTheDocument();

    expect((await sent()).snapshot).toMatchObject({
      callID: 'call-1',
      selectedCount: 3,
      selected: [{ message: 'line 0' }, { message: 'line 1' }, { message: 'line 2' }],
    });
  });

  it('takes the selected lines out of the send on ×, and leaves them selected', async () => {
    renderComposer();
    fireEvent.click(button('Focus c1'));
    expect(screen.queryByRole('button', { name: unsend })).toBeNull();
    fireEvent.click(button('Select three'));
    fireEvent.click(button(unsend));

    expect(screen.getByText('With the view in the sidebar (3 selected lines not sent)')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: unsend })).toBeNull();
    expect(screen.getByTestId('viewer-selection')).toHaveTextContent('3');
    expect((await sent()).snapshot).toMatchObject({ callID: 'call-1', selected: [], selectedCount: 0 });
  });

  it('says how many selected lines are sent past the cap', async () => {
    renderComposer();
    fireEvent.click(button('Focus c1'));
    fireEvent.click(button('Select 212'));

    expect(
      screen.getByText('With the view in the sidebar and 212 selected lines (the first 50 sent)'),
    ).toBeInTheDocument();
    expect((await sent()).snapshot).toMatchObject({ selectedCount: 212 });
  });

  // The viewer writes a new array for every new selection, a click on the same cell
  // included, so selecting the same lines again is a new selection.
  it('rides a new selection after ×, of the same lines too', async () => {
    renderComposer();
    fireEvent.click(button('Focus c1'));
    fireEvent.click(button('Select three'));
    fireEvent.click(button(unsend));
    fireEvent.click(button('Select three'));

    expect(screen.getByText('With the view in the sidebar and 3 selected lines')).toBeInTheDocument();
    expect((await sent()).snapshot).toMatchObject({ selectedCount: 3 });
  });

  it('rides every send while it is selected', async () => {
    const view = renderComposer();
    fireEvent.click(button('Focus c1'));
    fireEvent.click(button('Select three'));
    type('what is this?');
    await click('Send');
    view.rerender({ last: { seq: 2, status: 'Complete' } });
    type('why would that be nil?');
    await click('Send');

    expect(sendMock).toHaveBeenCalledTimes(2);
    expect(sendMock.mock.calls[1][0].snapshot).toMatchObject({ selectedCount: 3 });
  });

  it('retries a held send with the snapshot it holds, whatever × did since', async () => {
    sendMock.mockResolvedValue(dropped());
    renderComposer();
    fireEvent.click(button('Focus c1'));
    fireEvent.click(button('Select three'));
    type('what is this?');
    await click('Send');
    fireEvent.click(button(unsend));

    sendMock.mockResolvedValue(accepted());
    await click('Retry');
    expect(sendMock.mock.calls[1][0].snapshot).toMatchObject({ selectedCount: 3 });
  });

  it('does not draw again as a following tail writes the viewport', () => {
    renderComposer();
    fireEvent.click(button('Focus c1'));
    fireEvent.click(button('Select three'));
    const renders = composerRendered.mock.calls.length;
    fireEvent.click(button('Tail'));
    fireEvent.click(button('Tail'));
    expect(composerRendered).toHaveBeenCalledTimes(renders);
  });

  it('does not ride with the sidebar closed', async () => {
    renderComposer();
    fireEvent.click(button('Focus c1'));
    fireEvent.click(button('Close sidebar'));
    expect(screen.queryByText(/With the view/)).toBeNull();
    expect((await sent()).snapshot).toBeNull();
  });

  it("does not ride with another chat's view", async () => {
    renderComposer();
    fireEvent.click(button('Focus c2'));
    expect(screen.queryByText(/With the view/)).toBeNull();
    expect((await sent()).snapshot).toBeNull();
  });

  it('does not ride on a send that creates a chat', async () => {
    renderComposer({ chatID: null });
    fireEvent.click(button('Focus c1'));
    expect(screen.queryByText(/With the view/)).toBeNull();
    expect((await sent()).snapshot).toBeNull();
  });

  it('does not ride on the dashboard', async () => {
    renderComposer({ mode: 'dashboard' });
    fireEvent.click(button('Focus c1'));
    expect(screen.queryByText(/With the view/)).toBeNull();
    expect((await sent()).snapshot).toBeNull();
  });
});

// A log window's selection reaches the composer as its mirror, which the app window's
// provider folds from the window's pushes.
describe('the view in a window', () => {
  beforeEach(() => localStorage.clear());

  const unsend = "Don't send the selected lines";
  const sent = async () => {
    type('what is this?');
    await click('Send');
    return sendMock.mock.calls[0][0];
  };

  // The window on call-9 of chat c1, its mirror holding `carried` lines of `count` selected.
  const windowWith = (carried: number, count = carried) => {
    const viewport = createLogViewportHandle();
    viewport.set({
      ...EMPTY_LOG_VIEWPORT,
      visible: 5,
      selection: Array.from({ length: carried }, (_, i) => selectedLine(i)),
      selectedCount: count,
    });
    inWindow.current = {
      window: { label: 'logs-1', clusterId: '1', chatId: 'c1', callId: 'call-9', focusSeq: 1 },
      viewport,
    };
    return viewport;
  };

  beforeEach(() => {
    inWindow.current = null;
  });

  it("rides the mirror of this chat's window when the sidebar shows none", async () => {
    windowWith(2);
    renderComposer();
    expect(screen.getByText('With the view in a window and 2 selected lines')).toBeInTheDocument();
    expect((await sent()).snapshot).toMatchObject({
      callID: 'call-9',
      lines: 5,
      selectedCount: 2,
      selected: [{ message: 'line 0' }, { message: 'line 1' }],
    });
  });

  it("prefers the sidebar's view of this chat", async () => {
    windowWith(2);
    renderComposer();
    fireEvent.click(button('Focus c1'));
    expect(screen.getByText('With the view in the sidebar')).toBeInTheDocument();
    expect((await sent()).snapshot).toMatchObject({ callID: 'call-1', selectedCount: 0 });
  });

  it('draws each row of the chip, and × sends neither lines nor count', async () => {
    const viewport = windowWith(0);
    renderComposer();
    expect(screen.getByText('With the view in a window')).toBeInTheDocument();

    act(() => viewport.set({ ...viewport.get(), selection: [0, 1, 2].map(selectedLine), selectedCount: 70 }));
    expect(screen.getByText('With the view in a window and 70 selected lines (the first 3 sent)')).toBeInTheDocument();

    fireEvent.click(button(unsend));
    expect(screen.getByText('With the view in a window (70 selected lines not sent)')).toBeInTheDocument();
    expect((await sent()).snapshot).toMatchObject({ callID: 'call-9', selected: [], selectedCount: 0 });
  });

  it.each([
    ['with no chat', { chatID: null }],
    ['on the dashboard', { mode: 'dashboard' as const }],
    ['for another chat', { chatID: 'c2' }],
  ])('does not ride %s', async (_, props) => {
    windowWith(2);
    renderComposer(props);
    expect(screen.queryByText(/With the view/)).toBeNull();
    expect((await sent()).snapshot).toBeNull();
  });
});
