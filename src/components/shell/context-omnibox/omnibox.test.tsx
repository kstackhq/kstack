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

import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Mocks ---------------------------------------------------------------

// The scope the omnibox switches, and the dialog host the picker's footer asks.
const { useActiveKubeContextMock, setContext, openDialog } = vi.hoisted(() => ({
  useActiveKubeContextMock: vi.fn(),
  setContext: vi.fn(),
  openDialog: vi.fn(),
}));
// A context provider draws its readers again when its value changes; `kubeChanged`
// does that after a `serveContexts`.
const { kube } = vi.hoisted(() => ({ kube: { version: 0, listeners: new Set<() => void>() } }));
vi.mock('@/lib/active-kube-context', async () => {
  const { useSyncExternalStore } = await import('react');
  const subscribe = (listener: () => void) => {
    kube.listeners.add(listener);
    return () => kube.listeners.delete(listener);
  };
  return {
    useActiveKubeContext: () => {
      useSyncExternalStore(subscribe, () => kube.version);
      return useActiveKubeContextMock();
    },
  };
});
const kubeChanged = () =>
  act(() => {
    kube.version += 1;
    kube.listeners.forEach((l) => l());
  });
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

const { ContextOmnibox } = await import('./omnibox');
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
const statusButton = () => screen.getByRole('button', { name: /^cluster status:/i });

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

  it('is a 30px, 8px-radius box, bordered and white with no shadow', () => {
    render(<ContextOmnibox />);
    const box = screen.getByTestId('context-omnibox');
    expect(box).toHaveClass('h-7.5', 'w-full', 'max-w-88', 'rounded-lg', 'border', 'border-sidebar-border');
    expect(box).toHaveClass('bg-white', 'dark:bg-input/30');
    expect(box.className).not.toMatch(/shadow/);
  });

  it('draws the status and memory buttons as 28px circles around a 13px name', () => {
    render(<ContextOmnibox />);
    expect(screen.getByRole('button', { name: 'Memory' })).toHaveClass('size-7', 'rounded-full');
    expect(screen.getByRole('button', { name: /^Cluster status/ })).toHaveClass('size-7', 'rounded-full');
    expect(screen.getByTestId('cluster-trigger-name').parentElement).toHaveClass(
      'text-[13px]',
      'font-medium',
      'text-muted-foreground',
    );
  });

  // A strip of the box outside every control, its border included, would take a press and
  // open nothing.
  it('leaves no gap between its controls, their hit areas covering the margins and the border', () => {
    render(<ContextOmnibox />);
    expect(screen.getByTestId('context-omnibox').className).not.toMatch(/\b(gap|px|p)-/);
    for (const name of ['Memory', /^Cluster status/]) {
      // From inside the button's own 1px border: it, the 4px margin and the box's 1px
      // border beside it, and both borders above and below.
      const button = screen.getByRole('button', { name });
      expect(button).toHaveClass('border', 'border-transparent', 'mx-1', 'after:-inset-x-1.5', 'after:-inset-y-0.5');
    }
    expect(trigger()).toHaveClass('h-full', 'after:-inset-px');
  });

  // Over the content, the hit area would take the hover from the TLS badge and the status
  // dot, and their tooltips with it. `isolate` keeps it above the omnibox's own fill.
  it('lays each control’s hit area behind its content', () => {
    render(<ContextOmnibox />);
    for (const control of [
      screen.getByRole('button', { name: 'Memory' }),
      screen.getByRole('button', { name: /^Cluster status/ }),
      trigger(),
    ]) {
      expect(control).toHaveClass('relative', 'isolate', 'after:absolute', 'after:-z-10');
    }
  });

  // At the window's 600px minimum the box is about 80px: the two buttons' 56px, and room
  // for the badge's icon beside a truncated name.
  it('compacts to a narrow box, keeping every control and the TLS warning', () => {
    serveContexts(['prod'], { clusterEntry: { server: 'https://prod:6443', insecureSkipTLSVerify: true } });
    render(<ContextOmnibox />);
    expect(screen.getByTestId('context-omnibox')).toHaveClass('@container');
    for (const name of ['Memory', /^Cluster status/]) {
      expect(screen.getByRole('button', { name })).toHaveClass('@max-[14rem]:mx-0', '@max-[14rem]:after:-inset-0.5');
    }
    expect(screen.getByTestId('cluster-trigger-name')).toHaveClass('truncate');
    expect(screen.getByTestId('cluster-trigger-name').parentElement).toHaveClass('@max-[14rem]:gap-1');
    // The words give way to an icon, the warning's tooltip and spoken name kept.
    const badge = screen.getByTitle(/insecure-skip-tls-verify/);
    expect(badge).toHaveClass('shrink-0', '@max-[14rem]:px-0.5');
    expect(badge.querySelector('svg')).toHaveClass('hidden', '@max-[14rem]:block');
    expect(screen.getByText('Unverified TLS')).toHaveClass('@max-[14rem]:sr-only');
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
    expect(screen.getByRole('heading', { name: 'Notes' })).toBeInTheDocument();
  });

  it('keeps the brain icon gray at rest, primary only while a save pulses', () => {
    const button = () => screen.getByRole('button', { name: 'Memory' });
    const pulse = () => screen.queryByTestId('memory-save-pulse');
    // jsdom has no `AnimationEvent`, so React listens for the webkit-prefixed name there.
    const end = () => fireEvent(pulse()!, new Event('webkitAnimationEnd', { bubbles: true }));
    const save = (memory: object) =>
      act(() => {
        memoriesState.lastSaved = memory;
        memoriesState.listeners.forEach((listener) => listener());
      });

    memoriesState.current = [{ id: 'm0' }];
    render(<ContextOmnibox />);
    expect(button()).toHaveClass('text-muted-foreground');
    expect(button()).not.toHaveClass('text-primary');
    expect(pulse()).not.toBeInTheDocument();

    save({ id: 'm1' });
    expect(pulse()).toHaveClass('text-primary');
    expect(button()).toHaveClass('text-muted-foreground');
    end();
    expect(pulse()).not.toBeInTheDocument();
    expect(button()).not.toHaveClass('text-primary');

    // Another save of the same memory pulses again.
    save({ id: 'm1' });
    expect(pulse()).toBeInTheDocument();
  });

  it.each([
    ['name', () => screen.getByText('prod', { selector: '[data-testid="cluster-trigger-name"]' })],
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

  it('opens the cluster’s status, and not the picker, from the server icon', async () => {
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    await user.click(statusButton());
    expect(await screen.findByTestId('cluster-status')).toHaveAccessibleName('Cluster status');
    expect(screen.queryByText('Clusters', { selector: 'p' })).not.toBeInTheDocument();
  });

  // jsdom lays nothing out, so the positioner's alignment stands in for where it hangs.
  it('centres the status under the omnibox, as the picker and memory are', async () => {
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    await user.click(statusButton());
    const popup = await screen.findByTestId('cluster-status');
    await waitFor(() => expect(popup.closest('[data-align]')).toHaveAttribute('data-align', 'center'));
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

  it('colours the server icon’s dot with the window’s cluster’s verdict, its summary as the title', () => {
    const dot = () => within(statusButton()).getByTestId('cluster-dot');
    records.current = [clusterRecord({ context: 'prod' })];
    const { unmount } = render(<ContextOmnibox />);
    expect(dot()).toHaveAttribute('data-tone', 'ok');
    expect(dot()).toHaveAttribute('title', 'Active · Current');
    expect(statusButton()).toHaveAccessibleName('Cluster status: Active · Current');
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
    expect(screen.getByText('Unverified TLS').parentElement).toHaveAttribute(
      'title',
      expect.stringContaining('insecure-skip-tls-verify'),
    );
  });

  it('warns for a plain http server too', () => {
    serveContexts(['prod'], { clusterEntry: { server: 'http://localhost:8080', insecureSkipTLSVerify: false } });
    render(<ContextOmnibox />);
    expect(screen.getByText('Unverified TLS').parentElement).toHaveAttribute(
      'title',
      expect.stringContaining('plain http'),
    );
  });

  // The button's name replaces its contents for a screen reader, the badge's words with them.
  it.each([
    { reason: 'insecure-skip-tls-verify', entry: { server: 'https://prod:6443', insecureSkipTLSVerify: true } },
    { reason: 'a plain http server', entry: { server: 'http://localhost:8080', insecureSkipTLSVerify: false } },
  ])('announces the warning with the cluster for $reason', ({ entry }) => {
    serveContexts(['prod'], { clusterEntry: entry });
    render(<ContextOmnibox />);
    expect(trigger()).toHaveAccessibleName('Cluster: prod, unverified TLS');
  });

  it('announces a verified cluster by name alone', () => {
    serveContexts(['prod'], { clusterEntry: { server: 'https://prod:6443', insecureSkipTLSVerify: false } });
    render(<ContextOmnibox />);
    expect(trigger()).toHaveAccessibleName('Cluster: prod');
  });

  it('stays quiet for a verified cluster', () => {
    serveContexts(['prod'], { clusterEntry: { server: 'https://prod:6443', insecureSkipTLSVerify: false } });
    render(<ContextOmnibox />);
    expect(screen.queryByText('Unverified TLS')).not.toBeInTheDocument();
  });

  it('keeps the cluster in view while the context list grows', () => {
    render(<ContextOmnibox />);
    serveContexts(['prod', 'staging', 'qa']);
    kubeChanged();
    expect(trigger()).toHaveTextContent('prod');
  });

  it('keeps saying it is connecting while the context watch answers again', () => {
    serveContexts([], { phase: 'connecting' });
    render(<ContextOmnibox />);
    kubeChanged();
    expect(trigger()).toHaveTextContent('Connecting…');
  });

  it('says it is still connecting rather than "No kubeconfig"', () => {
    serveContexts([], { phase: 'connecting' });
    render(<ContextOmnibox />);
    expect(screen.getByRole('button', { name: 'Cluster: connecting' })).toHaveTextContent('Connecting…');
  });

  // About 22px is left for the words at the window's 600px minimum: the spinner and the
  // bare icon say it, and the button's name keeps the words.
  it.each([
    { phase: 'connecting' as const, words: 'Connecting…', name: 'Cluster: connecting' },
    { phase: 'live' as const, words: 'No kubeconfig', name: 'Cluster: no kubeconfig' },
  ])('drops "$words" in a narrow box, keeping it as the name', ({ phase, words, name }) => {
    serveContexts([], { phase });
    render(<ContextOmnibox />);
    expect(screen.getByText(words)).toHaveClass('@max-[14rem]:hidden');
    expect(screen.getByRole('button', { name })).toBeInTheDocument();
  });

  // The picker still opens, so a user with no kubeconfig reaches the dialog's orphaned caches.
  it('says there is no kubeconfig once connected with nothing in it, and still opens the picker', async () => {
    serveContexts([], { phase: 'live' });
    const user = userEvent.setup();
    render(<ContextOmnibox />);

    const empty = screen.getByRole('button', { name: 'Cluster: no kubeconfig' });
    expect(empty).toHaveTextContent('No kubeconfig');
    // No cluster, no status: the bare icon is the picker's.
    expect(screen.queryByRole('button', { name: /^cluster status/i })).not.toBeInTheDocument();
    expect(within(empty).getByTestId('cluster-glyph')).toBeInTheDocument();
    await user.click(empty);
    expect(await pickerOpened()).toBeInTheDocument();
  });
});
