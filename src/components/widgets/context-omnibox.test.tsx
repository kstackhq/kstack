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
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

// Mocks ---------------------------------------------------------------

// The scope the omnibox switches, and the dialog host the icon asks.
const { useActiveKubeContextMock, setContext, openDialog } = vi.hoisted(() => ({
  useActiveKubeContextMock: vi.fn(),
  setContext: vi.fn(),
  openDialog: vi.fn(),
}));
vi.mock('@/lib/active-kube-context', () => ({ useActiveKubeContext: useActiveKubeContextMock }));
vi.mock('@/lib/dialog', () => ({ useDialog: () => ({ openDialog }) }));

// The focused log view, which a switch clears.
const { clearView } = vi.hoisted(() => ({ clearView: vi.fn() }));
vi.mock('@/lib/logs-view', () => ({ useLogsView: () => ({ clear: clearView }) }));

// What the window's cluster remembers, which lights the memory button. A save
// notifies like the real watch does, so a memoized button re-renders for it.
const { memoriesState } = vi.hoisted(() => ({
  memoriesState: {
    current: [] as unknown[],
    lastSaved: undefined as object | undefined,
    listeners: new Set<() => void>(),
  },
}));
vi.mock('@/lib/active-cluster', () => ({ useActiveCluster: () => ({ clusterID: 'c1' }) }));
vi.mock('@/lib/memories', async () => {
  const { useSyncExternalStore } = await import('react');
  const subscribe = (listener: () => void) => {
    memoriesState.listeners.add(listener);
    return () => memoriesState.listeners.delete(listener);
  };
  return {
    useMemories: () => ({
      memories: memoriesState.current,
      lastSaved: useSyncExternalStore(subscribe, () => memoriesState.lastSaved),
      phase: 'live',
    }),
  };
});

// The cluster segment navigates itself rather than going through `setContext`, so
// the router is the seam for what it writes — and for where the window is, which
// decides whether the switch also has a chat route to leave.
const { navigate, location } = vi.hoisted(() => ({ navigate: vi.fn(), location: { pathname: '/dashboard' } }));
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  useLocation: ({ select }: { select: (loc: { pathname: string }) => unknown }) => select(location),
}));

const { ContextOmnibox } = await import('./context-omnibox');

// Helpers -------------------------------------------------------------

function serveContexts(
  names: string[],
  over: { context?: string; phase?: string; clusterEntry?: Record<string, unknown> | null } = {},
) {
  useActiveKubeContextMock.mockReturnValue({
    context: over.context ?? names[0] ?? '',
    contexts: names.map((name) => ({ name })),
    active: { name: over.context ?? names[0] ?? '', clusterEntry: over.clusterEntry ?? null },
    phase: over.phase ?? 'live',
    setContext,
  });
}

const clusterSegment = () => screen.getByRole('button', { name: /^cluster:/i });

// The menu portals in asynchronously, so wait for the item rather than reading it.
const menuItem = (name: string) => screen.findByRole('menuitemradio', { name });

// base-ui scrolls the highlighted item into view; jsdom has no implementation.
beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

beforeEach(() => {
  vi.clearAllMocks();
  location.pathname = '/dashboard';
  memoriesState.current = [];
  memoriesState.lastSaved = undefined;
  serveContexts(['prod', 'staging']);
});

// Tests ---------------------------------------------------------------

describe('ContextOmnibox', () => {
  it('reads as one control, the cluster the window is on', () => {
    render(<ContextOmnibox />);
    expect(screen.getByTestId('context-omnibox').contains(clusterSegment())).toBe(true);
  });

  it('opts out of the app bar’s drag region', () => {
    render(<ContextOmnibox />);
    // Pressing a control's padding should no more move the window than a
    // browser's address bar does.
    expect(screen.getByTestId('context-omnibox')).toHaveAttribute('data-tauri-drag-region', 'false');
  });

  it('shows the cluster in view', () => {
    serveContexts(['prod', 'staging'], { context: 'staging' });
    render(<ContextOmnibox />);
    expect(clusterSegment()).toHaveTextContent('staging');
  });

  it('opens the memory dialog from the brain icon', async () => {
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    await user.click(screen.getByRole('button', { name: 'Memory' }));
    expect(openDialog).toHaveBeenCalledWith('memories');
  });

  it('lights the brain icon while the cluster sees any memory', () => {
    const { unmount } = render(<ContextOmnibox />);
    expect(screen.getByRole('button', { name: 'Memory' })).toHaveAttribute('data-lit', 'false');
    unmount();

    memoriesState.current = [{ id: 'm1' }];
    render(<ContextOmnibox />);
    expect(screen.getByRole('button', { name: 'Memory' })).toHaveAttribute('data-lit', 'true');
  });

  it('pulses the brain icon once per memory saved, until the animation ends', () => {
    const pulse = () => screen.queryByTestId('memory-save-pulse');
    // jsdom has no `AnimationEvent`, so React listens for the webkit-prefixed name there.
    const end = () => fireEvent(pulse()!, new Event('webkitAnimationEnd', { bubbles: true }));
    const save = (memory: object) =>
      act(() => {
        memoriesState.lastSaved = memory;
        memoriesState.listeners.forEach((listener) => listener());
      });

    render(<ContextOmnibox />);
    expect(pulse()).not.toBeInTheDocument();

    save({ id: 'm1' });
    expect(pulse()).toBeInTheDocument();
    end();
    expect(pulse()).not.toBeInTheDocument();

    // Another save of the same memory pulses again.
    save({ id: 'm1' });
    expect(pulse()).toBeInTheDocument();
  });

  it('opens the clusters dialog from the icon', async () => {
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    await user.click(screen.getByRole('button', { name: 'Clusters' }));
    expect(openDialog).toHaveBeenCalledWith('clusters');
  });

  // A chat belongs to a cluster, so the dashboard panel's open chat does not follow a
  // cluster switch — and the rest of the window's scope does.
  it('switches cluster from the quick dropdown, closing the panel’s chat', async () => {
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    await user.click(clusterSegment());
    await user.click(await menuItem('staging'));

    expect(navigate).toHaveBeenCalledTimes(1);
    const { to, search } = navigate.mock.calls[0][0];
    expect(to).toBe('.');
    expect(search({ kubeContext: 'prod', resource: 'pods', chat: 'c1' })).toEqual({
      kubeContext: 'staging',
      resource: 'pods',
      chat: undefined,
    });
    expect(setContext).not.toHaveBeenCalled();
  });

  it('clears the focused log view on a switch', async () => {
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    await user.click(clusterSegment());
    await user.click(await menuItem('staging'));

    expect(clearView).toHaveBeenCalledTimes(1);
  });

  // In chat mode the open chat is the route, so the switch has to leave it: a chat of
  // the cluster the window just left would otherwise greet it with the out-of-scope
  // notice, which is for arriving at such a chat rather than for stepping off one.
  it('leaves an open chat for the new cluster’s unstarted one', async () => {
    location.pathname = '/chat/c1';
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    await user.click(clusterSegment());
    await user.click(await menuItem('staging'));

    const { to, search } = navigate.mock.calls[0][0];
    expect(to).toBe('/chat');
    expect(search({ kubeContext: 'prod' })).toEqual({ kubeContext: 'staging', chat: undefined });
  });

  it('warns when the cluster is reached with its certificate unchecked', () => {
    serveContexts(['prod'], { clusterEntry: { server: 'https://prod:6443', insecureSkipTLSVerify: true } });
    render(<ContextOmnibox />);
    // The one place the app says a connection goes unverified.
    expect(screen.getByText('Unverified TLS')).toHaveAttribute(
      'title',
      expect.stringContaining('insecure-skip-tls-verify'),
    );
  });

  it('warns for a plain http server too', () => {
    serveContexts(['prod'], { clusterEntry: { server: 'http://localhost:8080', insecureSkipTLSVerify: false } });
    render(<ContextOmnibox />);
    expect(screen.getByText('Unverified TLS')).toHaveAttribute('title', expect.stringContaining('plain http'));
  });

  it('stays quiet for a verified cluster', () => {
    serveContexts(['prod'], { clusterEntry: { server: 'https://prod:6443', insecureSkipTLSVerify: false } });
    render(<ContextOmnibox />);
    expect(screen.queryByText('Unverified TLS')).not.toBeInTheDocument();
  });

  it('says it is still connecting rather than "No kubeconfig"', () => {
    serveContexts([], { phase: 'connecting' });
    render(<ContextOmnibox />);
    expect(screen.getByTestId('kube-context-connecting')).toBeInTheDocument();
  });

  it('says there is no kubeconfig once connected with nothing in it', () => {
    serveContexts([], { phase: 'live' });
    render(<ContextOmnibox />);
    expect(screen.getByTestId('kube-context-empty')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^cluster:/i })).not.toBeInTheDocument();
  });
});
