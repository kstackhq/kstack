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
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Memory } from '@/lib/memories';

// The seams: the window's context, the cluster Kstack knows for it, its memories
// watch, and urql's mutation hook.
const { kube, active, watched, saveMock, deleteMock } = vi.hoisted(() => ({
  kube: { context: '' },
  active: { clusterID: undefined as string | undefined },
  watched: {
    ids: [] as (string | undefined)[],
    memories: [] as unknown[],
    phase: 'live',
  },
  saveMock: vi.fn(),
  deleteMock: vi.fn(),
}));
vi.mock('@/lib/active-kube-context', () => ({ useActiveKubeContext: () => kube }));
vi.mock('@/lib/active-cluster', () => ({ useActiveCluster: () => active }));
vi.mock('@/lib/memories', () => ({
  useMemories: (clusterID: string | undefined) => {
    watched.ids.push(clusterID);
    return { memories: watched.memories, phase: watched.phase };
  },
}));
vi.mock('urql', () => ({
  useMutation: (doc: unknown) => [{}, JSON.stringify(doc).includes('memoryDelete') ? deleteMock : saveMock],
}));
const { MemoryPopover, MemoryPopoverContent } = await import('./popover');

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
  kube.context = 'Minikube';
  active.clusterID = 'c1';
  watched.ids = [];
  watched.memories = [];
  watched.phase = 'live';
  saveMock.mockResolvedValue({ data: { memorySave: { id: 'm9' } } });
  deleteMock.mockResolvedValue({ data: { memoryDelete: true } });
});

// The header's close button needs a `Popover` above it; the popup itself is
// not under test, so the content renders open and bare.
const renderContent = () =>
  render(
    <Popover open>
      <MemoryPopoverContent />
    </Popover>,
  );

const button = (name: string) => screen.getByRole('button', { name });

describe('MemoryPopoverContent', () => {
  it("lists the window's cluster's own memories, never the ones for every cluster", () => {
    watched.memories = [memory(), memory({ id: 'm2', clusterID: null, name: 'prefs', body: 'Answer briefly.' })];
    renderContent();
    expect(watched.ids.at(-1)).toBe('c1');
    expect(screen.getByText('Treat its pods as prod.')).toBeInTheDocument();
    expect(screen.queryByText('Answer briefly.')).not.toBeInTheDocument();
  });

  it("names the window's cluster in its heading", () => {
    renderContent();
    expect(screen.getByText('Minikube memory')).toBeInTheDocument();
    expect(screen.getByText(/^Notes the chats on Minikube read\./)).toBeInTheDocument();
  });

  it('draws no cluster rail', () => {
    renderContent();
    expect(screen.queryByRole('button', { name: 'General' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Minikube' })).not.toBeInTheDocument();
  });

  it('asks for a cluster while the window has none', () => {
    kube.context = '';
    active.clusterID = undefined;
    renderContent();
    expect(screen.getByText('Pick a cluster to see its memory.')).toBeInTheDocument();
    expect(screen.getByText('Memory')).toBeInTheDocument();
    expect(screen.getByText(/^Notes the chats on this cluster read\./)).toBeInTheDocument();
    expect(button('Add memory')).toBeDisabled();
  });

  it('says so for a context Kstack has not reached', () => {
    active.clusterID = undefined;
    renderContent();
    expect(screen.getByText('Kstack has not reached this cluster yet.')).toBeInTheDocument();
    expect(button('Add memory')).toBeDisabled();
  });

  it('waits for the snapshot', () => {
    watched.phase = 'connecting';
    renderContent();
    expect(screen.queryByText('No saved memories yet.')).not.toBeInTheDocument();
  });

  it('footers a link to every memory beside Add memory', () => {
    renderContent();
    const footer = screen.getByRole('contentinfo');
    expect(footer).toContainElement(button('Manage all memories'));
    expect(footer).toContainElement(button('Add memory'));
  });

  it('adds a memory to the cluster', async () => {
    const user = userEvent.setup();
    renderContent();
    await user.click(button('Add memory'));
    expect(button('Add memory')).toBeDisabled();

    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'oncall');
    await user.type(screen.getByRole('textbox', { name: 'Body' }), 'Page payments.');
    await user.click(button('Save'));

    expect(saveMock).toHaveBeenCalledWith({
      input: { id: null, clusterID: 'c1', name: 'oncall', body: 'Page payments.' },
    });
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

  it('saves instructions', async () => {
    const user = userEvent.setup();
    renderContent();

    await user.click(button('Add instructions'));
    await user.type(screen.getByRole('textbox'), 'Always use traefik.');
    await user.click(button('Save'));
    expect(screen.getByText('Always use traefik.')).toBeInTheDocument();
  });
});

describe('MemoryPopover', () => {
  // A new element each call, so a rerender reads the seams again.
  const popover = () => (
    <MemoryPopover>
      <button type="button">Memory button</button>
    </MemoryPopover>
  );

  it('opens the memory content from its trigger', async () => {
    const user = userEvent.setup();
    render(popover());
    expect(screen.queryByText('SAVED MEMORIES')).not.toBeInTheDocument();

    await user.click(button('Memory button'));
    expect(await screen.findByText('SAVED MEMORIES')).toBeInTheDocument();
    // Above the sidebar drawer (z-40) and its scrim (z-30).
    expect(screen.getByText('SAVED MEMORIES').closest('.z-50')).not.toBeNull();
  });

  // jsdom lays nothing out, so this checks the styles that place and size it.
  it('hangs at a fixed size, with no resize handles', async () => {
    const user = userEvent.setup();
    render(popover());
    await user.click(button('Memory button'));
    const popup = await screen.findByTestId('memory-popup');
    expect(popup).toHaveClass('w-155');
    expect(popup.style.height).toBe('min(476px, var(--available-height))');
    expect(screen.queryByRole('separator')).not.toBeInTheDocument();
  });

  it("drops the instructions when the window's cluster changes", async () => {
    const user = userEvent.setup();
    const { rerender } = render(popover());
    await user.click(button('Memory button'));
    await user.click(await screen.findByRole('button', { name: 'Add instructions' }));
    await user.type(screen.getByRole('textbox'), 'Always use traefik.');
    await user.click(button('Save'));

    kube.context = '2nd Cluster';
    active.clusterID = 'c2';
    rerender(popover());
    expect(screen.queryByText('Always use traefik.')).not.toBeInTheDocument();
    expect(screen.getByText('No instructions added')).toBeInTheDocument();
  });
});
