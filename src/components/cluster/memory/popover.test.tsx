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
import { render, screen, waitFor } from '@testing-library/react';
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

// The popover's parts need a `Popover` above them; the popup itself is not
// under test, so the content renders open and bare.
const renderContent = () =>
  render(
    <Popover open>
      <MemoryPopoverContent />
    </Popover>,
  );

const button = (name: string) => screen.getByRole('button', { name });

describe('MemoryPopoverContent', () => {
  it("lists the window's cluster's own notes, never the ones for every cluster", () => {
    watched.memories = [memory(), memory({ id: 'm2', clusterID: null, name: 'prefs', body: 'Answer briefly.' })];
    renderContent();
    expect(watched.ids.at(-1)).toBe('c1');
    expect(screen.getByText('Treat its pods as prod.')).toBeInTheDocument();
    expect(screen.queryByText('Answer briefly.')).not.toBeInTheDocument();
  });

  it("names the window's cluster in its header, with no close button", () => {
    renderContent();
    expect(screen.getByText('Memory')).toBeInTheDocument();
    expect(screen.getByText('Chats on Minikube read this.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Close' })).not.toBeInTheDocument();
  });

  it('draws the instructions over the notes', () => {
    renderContent();
    const instructions = screen.getByRole('textbox', { name: 'Instructions' });
    const notes = screen.getByRole('heading', { name: 'Notes', level: 3 });
    expect(instructions.compareDocumentPosition(notes)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    expect(screen.getByText('Facts Kstack knows about this cluster.')).toBeInTheDocument();
  });

  it('asks for a cluster while the window has none', () => {
    kube.context = '';
    active.clusterID = undefined;
    renderContent();
    expect(screen.getByText('Pick a cluster to see its memory.')).toBeInTheDocument();
    expect(screen.getByText('Chats on this cluster read this.')).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(button('Add note')).toBeDisabled();
  });

  it('says so for a context Kstack has not reached', () => {
    active.clusterID = undefined;
    renderContent();
    expect(screen.getByText('Kstack has not reached this cluster yet.')).toBeInTheDocument();
    expect(button('Add note')).toBeDisabled();
  });

  it('waits for the snapshot, never drawing the empty state', () => {
    watched.phase = 'connecting';
    renderContent();
    expect(screen.queryByText('No notes yet.')).not.toBeInTheDocument();
    // The spinner; the hidden "Saved" marker is left out of the tree.
    expect(screen.getByRole('status')).not.toHaveTextContent('Saved');
  });

  it('footers a link to Memory settings beside Add note, on a muted strip', () => {
    renderContent();
    const footer = screen.getByRole('contentinfo');
    expect(footer).toHaveClass('bg-muted/50');
    expect(footer).toContainElement(button('Open Memory settings'));
    expect(footer).toContainElement(button('Add note'));
  });

  it('adds a note to the cluster, holding Add note while it is open', async () => {
    const user = userEvent.setup();
    renderContent();
    await user.click(button('Add note'));
    expect(button('Add note')).toBeDisabled();

    await user.type(screen.getByRole('textbox', { name: 'New note' }), 'Page payments.');
    await user.keyboard('{Meta>}{Enter}{/Meta}');

    expect(saveMock).toHaveBeenCalledWith({
      input: { id: null, clusterID: 'c1', name: 'page-payments', body: 'Page payments.' },
    });
    expect(screen.queryByRole('textbox', { name: 'New note' })).not.toBeInTheDocument();
    expect(button('Add note')).toBeEnabled();
  });

  it("saves an edit under the note's own name", async () => {
    const user = userEvent.setup();
    watched.memories = [memory()];
    renderContent();
    await user.click(button('Edit note: Treat its pods as prod.'));
    await user.clear(screen.getByRole('textbox', { name: 'Note' }));
    await user.type(screen.getByRole('textbox', { name: 'Note' }), 'Rewritten.');
    await user.keyboard('{Meta>}{Enter}{/Meta}');

    expect(saveMock).toHaveBeenCalledWith({ input: { id: 'm1', clusterID: 'c1', name: 'pages', body: 'Rewritten.' } });
  });

  it('deletes through the sidecar', async () => {
    const user = userEvent.setup();
    watched.memories = [memory()];
    renderContent();
    await user.click(button('Delete note'));
    await user.click((await screen.findAllByRole('button', { name: 'Delete note' })).at(-1)!);

    expect(deleteMock).toHaveBeenCalledWith({ id: 'm1' });
  });
});

describe('MemoryPopover', () => {
  // The pill it hangs from, as the omnibox hands it in.
  const pill = { current: null };
  // A new element each call, so a rerender reads the seams again.
  const popover = () => (
    <MemoryPopover anchorRef={pill}>
      <button type="button">Memory button</button>
    </MemoryPopover>
  );

  it('opens the memory content from its trigger', async () => {
    const user = userEvent.setup();
    render(popover());
    expect(screen.queryByText('Chats on Minikube read this.')).not.toBeInTheDocument();

    await user.click(button('Memory button'));
    expect(await screen.findByText('Chats on Minikube read this.')).toBeInTheDocument();
  });

  // jsdom lays nothing out, so this checks the styles that place and size it.
  it("hangs at the omnibox's width and a capped height", async () => {
    const user = userEvent.setup();
    render(popover());
    await user.click(button('Memory button'));
    const popup = await screen.findByTestId('memory-popup');
    expect(popup.style.width).toBe('var(--anchor-width)');
    expect(popup.style.height).toBe('min(476px, var(--available-height))');
  });

  it('opens with nothing inside it focused', async () => {
    const user = userEvent.setup();
    watched.memories = [memory()];
    render(popover());
    await user.click(button('Memory button'));
    const popup = await screen.findByTestId('memory-popup');
    await waitFor(() => expect(popup).toHaveFocus());
    expect(screen.getByRole('textbox', { name: 'Instructions' })).not.toHaveFocus();
  });

  it('closes on Escape, but not while a note is being edited', async () => {
    const user = userEvent.setup();
    watched.memories = [memory()];
    render(popover());
    await user.click(button('Memory button'));
    await user.click(await screen.findByRole('button', { name: 'Edit note: Treat its pods as prod.' }));
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('textbox', { name: 'Note' })).not.toBeInTheDocument();
    expect(screen.getByTestId('memory-popup')).toBeInTheDocument();

    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByTestId('memory-popup')).not.toBeInTheDocument());
  });

  describe('dismissed with a note open', () => {
    const open = async (user: ReturnType<typeof userEvent.setup>) => {
      render(popover());
      await user.click(button('Memory button'));
      await user.click(await screen.findByRole('button', { name: 'Edit note: Treat its pods as prod.' }));
    };
    const note = () => screen.getByRole('textbox', { name: 'Note' });
    const popup = () => screen.queryByTestId('memory-popup');

    beforeEach(() => {
      watched.memories = [memory({ writtenBy: 'User' })];
    });

    it('closes at once with nothing to save', async () => {
      const user = userEvent.setup();
      await open(user);
      await user.click(document.body);
      await waitFor(() => expect(popup()).not.toBeInTheDocument());
      expect(saveMock).not.toHaveBeenCalled();
    });

    it('saves the draft first, then closes on the next press', async () => {
      const user = userEvent.setup();
      await open(user);
      await user.type(note(), ' Always.');
      await user.click(document.body);
      expect(saveMock).toHaveBeenCalledOnce();
      await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Note' })).not.toBeInTheDocument());
      expect(popup()).toBeInTheDocument();

      await user.click(document.body);
      await waitFor(() => expect(popup()).not.toBeInTheDocument());
    });

    it('stays open with the draft and its reason when the save is refused', async () => {
      const user = userEvent.setup();
      saveMock.mockResolvedValue({ error: { graphQLErrors: [{ extensions: { code: 'KSTACK_MEMORY_SECRET' } }] } });
      await open(user);
      await user.type(note(), ' token=abc');
      await user.click(document.body);
      expect(await screen.findByText('This looks like a credential. Notes never keep one.')).toBeInTheDocument();

      await user.click(document.body);
      expect(popup()).toBeInTheDocument();
      expect(note()).toHaveValue('Treat its pods as prod. token=abc');
    });

    it('stays open while the draft is over 500 bytes', async () => {
      const user = userEvent.setup();
      await open(user);
      await user.clear(note());
      await user.paste('é'.repeat(251));
      await user.click(document.body);
      expect(popup()).toBeInTheDocument();
      expect(screen.getByText('At most 500 bytes.')).toBeInTheDocument();
      expect(saveMock).not.toHaveBeenCalled();
    });
  });

  it("drops the instructions when the window's cluster changes", async () => {
    const user = userEvent.setup();
    const { rerender } = render(popover());
    await user.click(button('Memory button'));
    await user.type(await screen.findByRole('textbox', { name: 'Instructions' }), 'Always use traefik.');

    kube.context = '2nd Cluster';
    active.clusterID = 'c2';
    rerender(popover());
    expect(screen.getByRole('textbox', { name: 'Instructions' })).toHaveValue('');
  });
});
