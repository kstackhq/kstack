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
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Mocks ---------------------------------------------------------------

// The scope the omnibox switches, and the dialog host the picker's footer asks.
const { useActiveKubeContextMock, setContext, openDialog } = vi.hoisted(() => ({
  useActiveKubeContextMock: vi.fn(),
  setContext: vi.fn(),
  openDialog: vi.fn(),
}));
vi.mock('@/lib/active-kube-context', () => ({ useActiveKubeContext: useActiveKubeContextMock }));
vi.mock('@/lib/dialog', () => ({ useDialog: () => ({ openDialog }), useDialogHost: () => null }));

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
// The cluster records the dot reads its verdict from; the lookups over them are the real ones.
const { records } = vi.hoisted(() => ({ records: { current: [] as unknown[] } }));
vi.mock('@/lib/clusters', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/clusters')>()),
  useClusters: () => ({ clusters: records.current }),
}));
vi.mock('urql', () => ({ useMutation: () => [{}, vi.fn()] }));
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

// The picker navigates itself rather than going through `setContext`, so
// the router is the seam for what it writes — and for where the window is, which
// decides whether the switch also has a chat route to leave.
const { navigate, location } = vi.hoisted(() => ({ navigate: vi.fn(), location: { pathname: '/dashboard' } }));
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  useLocation: ({ select }: { select: (loc: { pathname: string }) => unknown }) => select(location),
}));

const { ContextOmnibox } = await import('./context-omnibox');
const { clusterRecord } = await import('@/test-utils');

// Helpers -------------------------------------------------------------

function serveContexts(
  names: string[],
  over: { context?: string; phase?: string; clusterEntry?: Record<string, unknown> | null } = {},
) {
  useActiveKubeContextMock.mockReturnValue({
    context: over.context ?? names[0] ?? '',
    contexts: names.map((name) => ({ name, cluster: name, user: name, clusterEntry: null })),
    active: { name: over.context ?? names[0] ?? '', clusterEntry: over.clusterEntry ?? null },
    phase: over.phase ?? 'live',
    setContext,
  });
}

const trigger = () => screen.getByRole('button', { name: /^cluster:/i });

// The picker portals in asynchronously, so wait for its heading rather than reading it.
const pickerOpened = () => screen.findByText('Clusters', { selector: 'p' });
const pickerRow = (name: string) =>
  screen.getByText(name, { selector: '[data-testid="cluster-name"]' }).closest('button')!;

beforeEach(() => {
  vi.clearAllMocks();
  location.pathname = '/dashboard';
  memoriesState.current = [];
  memoriesState.lastSaved = undefined;
  records.current = [];
  serveContexts(['prod', 'staging']);
});

// Tests ---------------------------------------------------------------

describe('ContextOmnibox', () => {
  it('reads as one control, the cluster the window is on', () => {
    render(<ContextOmnibox />);
    expect(screen.getByTestId('context-omnibox').contains(trigger())).toBe(true);
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
    expect(trigger()).toHaveTextContent('staging');
  });

  it('opens the memory popover from the brain icon', async () => {
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    await user.click(screen.getByRole('button', { name: 'Memory' }));
    expect(screen.getByText('Memory', { selector: 'p' })).toBeInTheDocument();
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

  it.each([
    ['name', () => screen.getByText('prod', { selector: '[data-testid="cluster-trigger-name"]' })],
    ['glyph', () => screen.getByTestId('cluster-trigger-glyph')],
    ['TLS badge', () => screen.getByText('Unverified TLS')],
  ])('opens the cluster picker from the trigger’s %s', async (_part, target) => {
    serveContexts(['prod'], { clusterEntry: { server: 'https://prod:6443', insecureSkipTLSVerify: true } });
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    await user.click(target());
    expect(await pickerOpened()).toBeInTheDocument();
  });

  it('opens the memory popover, and not the picker, from the brain icon', async () => {
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    await user.click(screen.getByRole('button', { name: 'Memory' }));
    expect(await screen.findByTestId('memory-popup')).toBeInTheDocument();
    expect(screen.queryByText('Clusters', { selector: 'p' })).not.toBeInTheDocument();
  });

  it('switches cluster from a picker row', async () => {
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    await user.click(trigger());
    await pickerOpened();
    await user.click(pickerRow('staging'));

    expect(navigate).toHaveBeenCalledTimes(1);
    expect(navigate.mock.calls[0][0].search({ kubeContext: 'prod' })).toEqual({
      kubeContext: 'staging',
      chat: undefined,
    });
  });

  it('colours the dot with the window’s cluster’s verdict, its summary as the title', () => {
    const dot = () => within(trigger()).getByTestId('cluster-dot');
    records.current = [clusterRecord({ context: 'prod' })];
    const { unmount } = render(<ContextOmnibox />);
    expect(dot()).toHaveAttribute('data-tone', 'ok');
    expect(dot()).toHaveAttribute('title', 'Active · Synced');
    unmount();

    records.current = [clusterRecord({ context: 'prod', connected: 'False' })];
    const second = render(<ContextOmnibox />);
    expect(dot()).toHaveAttribute('data-tone', 'error');
    expect(dot()).toHaveAttribute('title', 'Disconnected · Stalled');
    second.unmount();

    records.current = [clusterRecord({ context: 'staging' })];
    render(<ContextOmnibox />);
    expect(dot()).toHaveAttribute('data-tone', 'muted');
    expect(dot()).toHaveAttribute('title', 'Not reached');
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
    expect(screen.getByRole('button', { name: 'Cluster: connecting' })).toHaveTextContent('Connecting…');
  });

  // The picker still opens, so a user with no kubeconfig reaches the dialog's orphaned caches.
  it('says there is no kubeconfig once connected with nothing in it, and still opens the picker', async () => {
    serveContexts([], { phase: 'live' });
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    const empty = screen.getByRole('button', { name: 'Cluster: no kubeconfig' });
    expect(empty).toHaveTextContent('No kubeconfig');
    await user.click(empty);
    expect(await pickerOpened()).toBeInTheDocument();
  });
});
