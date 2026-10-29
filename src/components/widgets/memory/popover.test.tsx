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

import { Popover } from '@kubetail/ui/elements/popover';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Memory } from '@/lib/memories';

// The seams: the window's cluster and the kubeconfig's contexts in its order, the
// clusters Kstack knows by context, each cluster's memories watch, and urql's
// mutation hook.
const { kube, known, watched, saveMock, deleteMock } = vi.hoisted(() => ({
  kube: { context: '', contexts: [] as { name: string }[] },
  known: { current: [] as { id: string; spec: { source: { kubeconfig: { context: string } } } }[] },
  watched: {
    ids: [] as (string | undefined)[],
    memories: [] as unknown[],
    phase: 'live',
  },
  saveMock: vi.fn(),
  deleteMock: vi.fn(),
}));
vi.mock('@/lib/active-kube-context', () => ({ useActiveKubeContext: () => kube }));
vi.mock('@/lib/clusters', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/clusters')>()),
  useClusters: () => ({ clusters: known.current }),
}));
vi.mock('@/lib/memories', () => ({
  useMemories: (clusterID: string | undefined) => {
    watched.ids.push(clusterID);
    return { memories: watched.memories, phase: watched.phase };
  },
}));
vi.mock('urql', () => ({
  useMutation: (doc: unknown) => [{}, String(JSON.stringify(doc)).includes('memoryDelete') ? deleteMock : saveMock],
}));
const { MemoryPopover, MemoryPopoverContent } = await import('./popover');

// jsdom doesn't implement pointer capture; the resize handles call it, so stub it out.
if (!HTMLElement.prototype.setPointerCapture) {
  HTMLElement.prototype.setPointerCapture = () => {};
}

// The header's close button needs a `Popover` above it; the popup itself is
// not under test, so the content renders open and bare.
const record = (id: string, context: string) => ({ id, spec: { source: { kubeconfig: { context } } } });

const memory = (over: Partial<Memory> = {}): Memory => ({
  id: 'm1',
  clusterID: 'c1',
  name: 'pages',
  body: 'Treat its pods as prod.',
  writtenBy: 'Model',
  updatedAt: '2026-09-20T10:00:00Z',
  ...over,
});

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  known.current = [record('c1', 'Minikube'), record('c2', '2nd Cluster')];
  watched.ids = [];
  watched.memories = [];
  watched.phase = 'live';
  saveMock.mockResolvedValue({ data: { memorySave: { id: 'm9' } } });
  deleteMock.mockResolvedValue({ data: { memoryDelete: true } });
});

const renderContent = ({ context = 'Minikube', contexts = ['2nd Cluster', 'Minikube'] } = {}) => {
  kube.context = context;
  kube.contexts = contexts.map((name) => ({ name }));
  return render(
    <Popover open>
      <MemoryPopoverContent />
    </Popover>,
  );
};

const button = (name: string) => screen.getByRole('button', { name });
const banner = () => screen.queryByText(/Viewing .* memory\./);

describe('MemoryPopoverContent', () => {
  it("lists the window's cluster first, then the kubeconfig's other contexts", () => {
    renderContent();
    expect(button('Minikube')).toHaveAttribute('aria-pressed', 'true');
    expect(button('Minikube').compareDocumentPosition(button('2nd Cluster'))).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });

  it('selects General while the window has no cluster', () => {
    renderContent({ context: '', contexts: ['2nd Cluster'] });
    expect(button('General')).toHaveAttribute('aria-pressed', 'true');
    expect(banner()).not.toBeInTheDocument();
  });

  it("lists a cluster's own memories, and General the ones for every cluster", async () => {
    const user = userEvent.setup();
    watched.memories = [memory(), memory({ id: 'm2', clusterID: null, name: 'prefs', body: 'Answer briefly.' })];
    renderContent();
    expect(screen.getByText('Treat its pods as prod.')).toBeInTheDocument();
    expect(screen.queryByText('Answer briefly.')).not.toBeInTheDocument();

    await user.click(button('General'));
    expect(screen.getByText('Answer briefly.')).toBeInTheDocument();
    expect(screen.queryByText('Treat its pods as prod.')).not.toBeInTheDocument();
  });

  it("watches the selected cluster, and the window's for General", async () => {
    const user = userEvent.setup();
    renderContent();
    expect(watched.ids.at(-1)).toBe('c1');

    await user.click(button('2nd Cluster'));
    expect(watched.ids.at(-1)).toBe('c2');

    await user.click(button('General'));
    expect(watched.ids.at(-1)).toBe('c1');
  });

  it('treats a context named General as a cluster, never every cluster', async () => {
    const user = userEvent.setup();
    known.current = [record('c3', 'General')];
    watched.memories = [memory({ clusterID: 'c3' }), memory({ id: 'm2', clusterID: null, body: 'Answer briefly.' })];
    renderContent({ context: 'General', contexts: ['General'] });

    expect(watched.ids.at(-1)).toBe('c3');
    expect(screen.getByText('Treat its pods as prod.')).toBeInTheDocument();
    expect(screen.queryByText('Answer briefly.')).not.toBeInTheDocument();

    await user.click(button('Add memory'));
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'oncall');
    await user.type(screen.getByRole('textbox', { name: 'Body' }), 'Page payments.');
    await user.click(button('Save'));
    expect(saveMock).toHaveBeenCalledWith({
      input: { id: null, clusterID: 'c3', name: 'oncall', body: 'Page payments.' },
    });
  });

  it('says so for a context Kstack has not reached', () => {
    known.current = [];
    renderContent();
    expect(screen.getByText('Kstack has not reached this cluster yet.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add memory' })).not.toBeInTheDocument();
  });

  it('waits for the snapshot', () => {
    watched.phase = 'connecting';
    renderContent();
    expect(screen.queryByText('No saved memories yet.')).not.toBeInTheDocument();
  });

  it.each([
    ['Minikube', 'c1'],
    ['General', null],
  ])('adds a memory to %s', async (row, clusterID) => {
    const user = userEvent.setup();
    renderContent();
    await user.click(button(row));
    await user.click(button('Add memory'));
    expect(button('Add memory')).toBeDisabled();

    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'oncall');
    await user.type(screen.getByRole('textbox', { name: 'Body' }), 'Page payments.');
    await user.click(button('Save'));

    expect(saveMock).toHaveBeenCalledWith({ input: { id: null, clusterID, name: 'oncall', body: 'Page payments.' } });
    expect(screen.queryByRole('textbox', { name: 'Name' })).not.toBeInTheDocument();
  });

  it('saves an edit in the scope the memory is in', async () => {
    const user = userEvent.setup();
    watched.memories = [memory()];
    renderContent();
    await user.click(button('Actions for pages'));
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }));
    await user.clear(screen.getByRole('textbox', { name: 'Body' }));
    await user.type(screen.getByRole('textbox', { name: 'Body' }), 'Rewritten.');
    await user.click(button('Save'));

    expect(saveMock).toHaveBeenCalledWith({ input: { id: 'm1', clusterID: 'c1', name: 'pages', body: 'Rewritten.' } });
  });

  it('deletes through the sidecar', async () => {
    const user = userEvent.setup();
    watched.memories = [memory()];
    renderContent();
    await user.click(button('Actions for pages'));
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }));
    await user.click(await screen.findByRole('button', { name: 'Delete 1 memory' }));

    expect(deleteMock).toHaveBeenCalledWith({ id: 'm1' });
  });

  it('shows no banner on the default cluster or General', async () => {
    const user = userEvent.setup();
    renderContent();
    expect(banner()).not.toBeInTheDocument();

    await user.click(button('General'));
    expect(banner()).not.toBeInTheDocument();
  });

  it('banners another cluster, and Back returns to the default', async () => {
    const user = userEvent.setup();
    renderContent();

    await user.click(button('2nd Cluster'));
    expect(screen.getByText('Viewing 2nd Cluster’s memory.')).toHaveAttribute('title', 'Viewing 2nd Cluster’s memory.');

    await user.click(button('Back to Minikube'));
    expect(banner()).not.toBeInTheDocument();
    expect(button('Minikube')).toHaveAttribute('aria-pressed', 'true');
  });

  it('keeps instructions scoped to the cluster that saved them', async () => {
    const user = userEvent.setup();
    renderContent();

    await user.click(button('Add instructions'));
    await user.type(screen.getByRole('textbox'), 'Always use traefik.');
    await user.click(button('Save'));

    await user.click(button('2nd Cluster'));
    expect(screen.getByText('No instructions added')).toBeInTheDocument();

    await user.click(button('Back to Minikube'));
    expect(screen.getByText('Always use traefik.')).toBeInTheDocument();
  });

  it('discards an in-progress edit when the selected cluster changes', async () => {
    const user = userEvent.setup();
    renderContent();

    await user.click(button('Add instructions'));
    await user.type(screen.getByRole('textbox'), 'Half-typed...');

    await user.click(button('2nd Cluster'));
    await user.click(button('Back to Minikube'));
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.getByText('No instructions added')).toBeInTheDocument();
  });

  // jsdom lays nothing out, so this checks the styles that decide what a browser
  // does when the popover is narrower than the rail and the detail pane want.
  it('shrinks the rail before the detail pane', () => {
    localStorage.setItem('kstack:memory-popover-rail-width', '320');
    renderContent();
    const rail = screen.getByTestId('memory-rail');
    expect(rail.style.width).toBe('320px');
    expect(rail.style.minWidth).toBe('100px');
    expect(rail).not.toHaveClass('shrink-0');
    expect(screen.getByTestId('memory-detail')).toHaveClass('min-w-70');
  });
});

describe('MemoryPopover', () => {
  it('opens the memory content from its trigger', async () => {
    const user = userEvent.setup();
    render(
      <MemoryPopover anchorRef={{ current: null }}>
        <button type="button">Memory button</button>
      </MemoryPopover>,
    );
    expect(screen.queryByText('SAVED MEMORIES')).not.toBeInTheDocument();

    await user.click(button('Memory button'));
    expect(await screen.findByText('SAVED MEMORIES')).toBeInTheDocument();
    // Above the sidebar drawer (z-40) and its scrim (z-30).
    expect(screen.getByText('SAVED MEMORIES').closest('.z-50')).not.toBeNull();
  });

  it('starts at the omnibox’s width, at least its minimum, and remembers the size the user drags', async () => {
    const user = userEvent.setup();
    const popover = (
      <MemoryPopover anchorRef={{ current: null }}>
        <button type="button">Memory button</button>
      </MemoryPopover>
    );
    const { unmount } = render(popover);
    await user.click(button('Memory button'));
    const popup = await screen.findByTestId('memory-popup');
    expect(popup.style.width).toBe('max(var(--anchor-width), 480px)');
    expect(popup.style.height).toBe('min(490px, var(--available-height))');

    // jsdom lays nothing out, so the popup's box is all at 0: the centre and the top.
    fireEvent.pointerDown(screen.getByRole('separator', { name: 'Resize memory, bottom-right corner' }), {
      pointerId: 1,
    });
    fireEvent.pointerMove(window, { clientX: 300, clientY: 600 });
    fireEvent.pointerUp(window);
    expect(popup.style.width).toBe('600px');
    expect(popup.style.height).toBe('min(600px, var(--available-height))');

    unmount();
    render(popover);
    await user.click(button('Memory button'));
    expect((await screen.findByTestId('memory-popup')).style.width).toBe('600px');
  });
});
