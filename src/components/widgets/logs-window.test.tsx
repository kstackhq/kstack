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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Chat, ChatMessage, ChatToolCall } from '@/lib/chats';
import type { Cluster } from '@/lib/clusters';
import type { WatchPhase } from '@/lib/graphql/use-watch-subscription';
import type { LogsViewAction } from '@/lib/logs-view';
import {
  clusterRecord,
  mockTauriCore,
  mockTauriWindow,
  NON_MAC_USER_AGENT,
  restoreUserAgent,
  setUserAgent,
} from '@/test-utils';

const { factory } = mockTauriWindow();
vi.mock('@tauri-apps/api/window', () => factory());

const { invokeMock, factory: coreFactory } = mockTauriCore();
vi.mock('@tauri-apps/api/core', () => coreFactory());

// The watches, as the route reads them. A change through `publish` re-renders their
// readers, as a frame does.
type Watched = {
  chats: { chats: Chat[]; phase: WatchPhase };
  messages: { messages: ChatMessage[]; phase: WatchPhase };
  clusters: { clusters: Cluster[] | null; connected: boolean };
};
const { state, listeners } = vi.hoisted(() => {
  const watched: Watched = {
    chats: { chats: [], phase: 'connecting' },
    messages: { messages: [], phase: 'connecting' },
    clusters: { clusters: null, connected: true },
  };
  return { state: watched, listeners: new Set<() => void>() };
});
vi.mock('@/lib/chats', async (importOriginal) => {
  const { useSyncExternalStore } = await import('react');
  const subscribe = (l: () => void) => {
    listeners.add(l);
    return () => listeners.delete(l);
  };
  return {
    ...(await importOriginal<typeof import('@/lib/chats')>()),
    useChats: () => useSyncExternalStore(subscribe, () => state.chats),
    useChatMessages: () => useSyncExternalStore(subscribe, () => state.messages),
  };
});
vi.mock('@/lib/clusters', async (importOriginal) => {
  const { useSyncExternalStore } = await import('react');
  const subscribe = (l: () => void) => {
    listeners.add(l);
    return () => listeners.delete(l);
  };
  return {
    ...(await importOriginal<typeof import('@/lib/clusters')>()),
    useClusters: () => useSyncExternalStore(subscribe, () => state.clusters),
  };
});

function publish(change: () => void) {
  act(() => {
    change();
    listeners.forEach((l) => l());
  });
}

// The viewer has a suite of its own; here it says what it was handed, and counts its
// mounts, since a remount loses its scroll, pause and selection.
const { viewerMounts } = vi.hoisted(() => ({ viewerMounts: { count: 0 } }));
vi.mock('@/components/widgets/log-viewer', async () => {
  const { useEffect } = await import('react');
  return {
    LogViewer: ({
      clusterId,
      query,
      onGrepApplied,
    }: {
      clusterId: string;
      query: { grep: string };
      onGrepApplied: (grep: string) => void;
    }) => {
      useEffect(() => {
        viewerMounts.count += 1;
      }, []);
      return (
        <div data-testid="log-viewer" data-cluster={clusterId} data-grep={query.grep}>
          <button type="button" onClick={() => onGrepApplied('panic')}>
            apply panic
          </button>
        </div>
      );
    },
  };
});

const { onError } = await import('@/lib/error-bus');
const { LogsWindow } = await import('./logs-window');

const action: LogsViewAction = {
  sources: [
    { namespace: 'prod', kind: 'Deployment', name: 'web', containers: [], allContainers: false, previous: false },
  ],
  filters: [],
  grep: 'error',
  anchor: { kind: 'Tail', at: null },
  pinToEnd: true,
  resolvedAt: '2026-10-08T14:04:00Z',
};

const prod = clusterRecord({ context: 'prod' });

const call = (id: string, logsView: LogsViewAction | null) =>
  ({
    id,
    action: { logsView, logsRead: null, logsWatch: logsView ? null : {} },
    background: null,
  }) as unknown as ChatToolCall;

const chat = (title = 'Why is web down') => ({ id: 'chat-1', title, clusterID: prod.id }) as unknown as Chat;

function live({ calls = [call('call-1', action)], title }: { calls?: ChatToolCall[]; title?: string } = {}) {
  state.chats = { chats: [chat(title)], phase: 'live' };
  state.messages = { messages: [{ id: 'm1', toolCalls: calls } as unknown as ChatMessage], phase: 'live' };
  state.clusters = { clusters: [prod], connected: true };
}

type Answer = { chatId: string; callId: string; grep: string | null; generation: number };
const answer: Answer = { chatId: 'chat-1', callId: 'call-1', grep: null, generation: 1 };

function answering(view: () => Promise<Answer>) {
  invokeMock.mockImplementation((cmd: string) => (cmd === 'log_window_view' ? view() : Promise.resolve()));
}

// Lets the answer to `log_window_view` land.
const settle = () => act(async () => {});

const titles = () => invokeMock.mock.calls.filter(([cmd]) => cmd === 'log_window_title').map(([, arg]) => arg);

beforeEach(() => {
  invokeMock.mockReset();
  answering(() => Promise.resolve(answer));
  setUserAgent(NON_MAC_USER_AGENT);
  state.chats = { chats: [], phase: 'connecting' };
  state.messages = { messages: [], phase: 'connecting' };
  state.clusters = { clusters: null, connected: true };
});

afterEach(() => {
  restoreUserAgent();
});

describe('LogsWindow', () => {
  it('draws the bar and Loading… before the answer', () => {
    answering(() => new Promise(() => {}));
    live();
    render(<LogsWindow />);
    expect(screen.getByTestId('logs-window-bar')).toBeInTheDocument();
    expect(screen.getByText('Loading…')).toBeInTheDocument();
  });

  it.each([
    [
      'chats',
      () => {
        state.chats = { chats: [], phase: 'connecting' };
      },
    ],
    [
      'messages',
      () => {
        state.messages = { messages: [], phase: 'connecting' };
      },
    ],
    [
      'clusters',
      () => {
        state.clusters = { clusters: null, connected: true };
      },
    ],
  ])('draws Loading… until the %s bookmark lands', async (_, unbooked) => {
    live();
    unbooked();
    render(<LogsWindow />);
    await settle();
    expect(screen.getByText('Loading…')).toBeInTheDocument();
    expect(screen.queryByTestId('log-viewer')).not.toBeInTheDocument();
  });

  it('keeps drawing Loading… while a watch reconnects before its bookmark', async () => {
    live();
    state.messages = { messages: [], phase: 'connecting' };
    render(<LogsWindow />);
    await settle();
    expect(screen.getByText('Loading…')).toBeInTheDocument();
  });

  it('keeps drawing the view through a reconnecting phase', async () => {
    live();
    state.chats = { ...state.chats, phase: 'reconnecting' };
    state.messages = { ...state.messages, phase: 'reconnecting' };
    render(<LogsWindow />);
    expect(await screen.findByTestId('log-viewer')).toBeInTheDocument();
  });

  it('draws a refused view, reports it, and asks again on Try again', async () => {
    live();
    const reported = vi.fn();
    const off = onError(reported);
    answering(() => Promise.reject(new Error('ipc down')));
    render(<LogsWindow />);
    expect(await screen.findByText('This log window could not load.')).toBeInTheDocument();
    expect(reported).toHaveBeenCalledWith(expect.objectContaining({ source: 'network', message: 'ipc down' }));
    off();

    answering(() => Promise.resolve(answer));
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByTestId('log-viewer')).toBeInTheDocument();
    expect(invokeMock.mock.calls.filter(([cmd]) => cmd === 'log_window_view')).toHaveLength(2);
  });

  it('drops an answer, or a refusal, that lands after the window unmounts', async () => {
    live();
    const reported = vi.fn();
    const off = onError(reported);
    let settleView!: { resolve: (a: Answer) => void; reject: (e: unknown) => void };
    answering(
      () =>
        new Promise<Answer>((resolve, reject) => {
          settleView = { resolve, reject };
        }),
    );
    const first = render(<LogsWindow />);
    const late = settleView;
    first.unmount();
    late.resolve(answer);
    await settle();

    const second = render(<LogsWindow />);
    const refused = settleView;
    second.unmount();
    refused.reject(new Error('ipc down'));
    await settle();
    expect(reported).not.toHaveBeenCalled();
    off();
  });

  it('says the chat was deleted when the map lacks it', async () => {
    live();
    state.chats = { chats: [], phase: 'live' };
    render(<LogsWindow />);
    expect(await screen.findByText('This chat was deleted.')).toBeInTheDocument();
  });

  it('says the view is not available for a call the chat lacks, or one with no view', async () => {
    live({ calls: [call('call-2', action)] });
    const { unmount } = render(<LogsWindow />);
    expect(await screen.findByText('This log view is not available.')).toBeInTheDocument();
    unmount();

    live({ calls: [call('call-1', null)] });
    render(<LogsWindow />);
    expect(await screen.findByText('This log view is not available.')).toBeInTheDocument();
  });

  it("draws the header with no buttons over the viewer, on the chat's cluster with the action's grep", async () => {
    live();
    render(<LogsWindow />);
    const viewer = await screen.findByTestId('log-viewer');
    expect(viewer).toHaveAttribute('data-cluster', prod.id);
    expect(viewer).toHaveAttribute('data-grep', 'error');
    expect(screen.getByRole('heading')).toHaveTextContent('Logs: Deployment web in prod');
    expect(screen.queryByRole('button', { name: 'Close log view' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Open in window' })).not.toBeInTheDocument();
    expect(screen.getByTestId('logs-window-cluster')).toHaveTextContent('prod');
    expect(screen.getByTestId('logs-window-chat')).toHaveTextContent('Why is web down');
  });

  it("runs the pair's grep over the action's, and the user's over both", async () => {
    live();
    answering(() => Promise.resolve({ ...answer, grep: 'warn' }));
    render(<LogsWindow />);
    expect(await screen.findByTestId('log-viewer')).toHaveAttribute('data-grep', 'warn');
    fireEvent.click(screen.getByRole('button', { name: 'apply panic' }));
    expect(screen.getByTestId('log-viewer')).toHaveAttribute('data-grep', 'panic');
    expect(screen.getByText(/matching/)).toHaveTextContent('matching /panic/');
  });

  it('draws no cluster in the bar when the map lacks it', async () => {
    live();
    state.clusters = { clusters: [], connected: true };
    render(<LogsWindow />);
    await screen.findByTestId('log-viewer');
    expect(screen.queryByTestId('logs-window-cluster')).not.toBeInTheDocument();
  });
});

describe('LogsWindow across a reconnect', () => {
  // A reconnect masks a watch's data until its new snapshot's bookmark lands.
  const reconnect = () =>
    publish(() => {
      state.chats = { chats: [], phase: 'connecting' };
      state.messages = { messages: [], phase: 'connecting' };
      state.clusters = { clusters: null, connected: false };
    });

  it('keeps the view mounted, with the bar and the grep applied here, until the snapshots land', async () => {
    live();
    render(<LogsWindow />);
    await screen.findByTestId('log-viewer');
    fireEvent.click(screen.getByRole('button', { name: 'apply panic' }));
    viewerMounts.count = 0;

    reconnect();
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
    expect(screen.getByTestId('log-viewer')).toHaveAttribute('data-grep', 'panic');
    expect(screen.getByTestId('logs-window-cluster')).toHaveTextContent('prod');
    expect(screen.getByTestId('logs-window-chat')).toHaveTextContent('Why is web down');

    publish(() => live());
    expect(screen.getByTestId('log-viewer')).toHaveAttribute('data-grep', 'panic');
    expect(viewerMounts.count).toBe(0);
  });

  it('draws Loading…, not a view it let go of, when a notice was up', async () => {
    live();
    state.chats = { chats: [], phase: 'live' };
    render(<LogsWindow />);
    await screen.findByText('This chat was deleted.');

    reconnect();
    expect(screen.getByText('Loading…')).toBeInTheDocument();
    expect(screen.queryByTestId('log-viewer')).not.toBeInTheDocument();
  });

  it('lets go of the view once the snapshots say its chat is gone', async () => {
    live();
    render(<LogsWindow />);
    await screen.findByTestId('log-viewer');
    reconnect();
    publish(() => {
      live();
      state.chats = { chats: [], phase: 'live' };
    });
    expect(screen.getByText('This chat was deleted.')).toBeInTheDocument();

    reconnect();
    expect(screen.getByText('Loading…')).toBeInTheDocument();
  });
});

describe("LogsWindow's title", () => {
  it('sends the first source, the chat title and the cluster name, and again when one changes', async () => {
    live();
    render(<LogsWindow />);
    await screen.findByTestId('log-viewer');
    expect(titles()).toEqual([{ parts: ['Deployment web in prod', 'Why is web down', 'prod'] }]);

    publish(() => {
      state.chats = { chats: [chat('Web is down')], phase: 'live' };
    });
    expect(titles().at(-1)).toEqual({ parts: ['Deployment web in prod', 'Web is down', 'prod'] });
    expect(titles()).toHaveLength(2);
  });

  it('sends an empty part for a missing cluster or an empty chat title', async () => {
    live({ title: '' });
    state.clusters = { clusters: [], connected: true };
    render(<LogsWindow />);
    await screen.findByTestId('log-viewer');
    expect(titles()).toEqual([{ parts: ['Deployment web in prod', '', ''] }]);
  });

  it('sends none from a notice', async () => {
    live();
    state.chats = { chats: [], phase: 'live' };
    render(<LogsWindow />);
    await screen.findByText('This chat was deleted.');
    expect(titles()).toEqual([]);
  });
});
