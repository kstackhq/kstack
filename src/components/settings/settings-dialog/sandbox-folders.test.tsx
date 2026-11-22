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

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { SandboxFolder, SandboxFolders } from '@/lib/sandbox-folders';
import type { SandboxPath, SandboxPathEntry } from '@/lib/sandbox-path';

const { folders, mac } = vi.hoisted(() => ({ folders: { current: {} as SandboxFolders }, mac: { current: false } }));
vi.mock('@/lib/sandbox-folders', () => ({ useSandboxFolders: () => folders.current }));
vi.mock('@/lib/platform', () => ({ isMacOS: () => mac.current }));

const { SandboxFolderGroups } = await import('./sandbox-folders');

const folder = (path: string, extra: Partial<SandboxFolder> = {}): SandboxFolder => ({
  id: path,
  path,
  write: false,
  refused: null,
  ...extra,
});

const entry = (dir: string, state: SandboxPathEntry['state'], extra: Partial<SandboxPathEntry> = {}) => ({
  dir,
  target: dir,
  state,
  source: 'Shell' as const,
  shared: false,
  ...extra,
});

function withFolders(over: Partial<SandboxFolders> = {}) {
  folders.current = {
    always: [],
    chat: [],
    never: [],
    closed: [],
    wide: ['/Users/ren', '/Users'],
    rulesHeld: false,
    granting: false,
    revoking: new Set(),
    grantError: null,
    revokeError: null,
    grant: vi.fn(async () => true),
    revoke: vi.fn(async () => {}),
    ...over,
  };
}

function pathOf(over: Partial<SandboxPath> = {}): SandboxPath {
  return {
    entries: [],
    fault: null,
    resolved: true,
    shell: null,
    importedAt: null,
    changing: new Set(),
    refreshing: false,
    changeError: null,
    refreshError: null,
    include: vi.fn(async () => {}),
    remove: vi.fn(async () => {}),
    refresh: vi.fn(async () => true),
    ...over,
  };
}

const group = (name: RegExp) => screen.getByRole('button', { name }).closest('section')!;

beforeEach(() => {
  mac.current = false;
  withFolders();
});

describe('SandboxFolderGroups', () => {
  it('lists each always grant with its access and a refused reason', () => {
    withFolders({
      always: [folder('/Users/ren/src', { write: true }), folder('/Users/ren/k9s', { refused: 'not a folder' })],
    });
    render(<SandboxFolderGroups path={pathOf()} />);

    const mine = group(/^Custom · 2$/);
    const rows = within(mine).getAllByRole('listitem');
    expect(within(rows[0]).getByTestId('folder').textContent).toBe('/Users/ren/src');
    expect(within(rows[0]).getByRole('combobox')).toHaveTextContent('Read and write');
    expect(within(rows[1]).getByRole('combobox')).toHaveTextContent('Read');
    expect(within(rows[1]).getByText('not a folder')).toBeInTheDocument();
  });

  it("spells a folder's trailing whitespace", () => {
    withFolders({ always: [folder('/Users/ren/my src ')] });
    render(<SandboxFolderGroups path={pathOf()} />);
    expect(within(group(/^Custom · /)).getByTestId('folder').textContent).toBe('/Users/ren/my src\\u{20}');
  });

  it("changes a folder's access by granting it again, always", async () => {
    withFolders({ always: [folder('/Users/ren/k9s')] });
    render(<SandboxFolderGroups path={pathOf()} />);
    await userEvent.click(screen.getByRole('combobox'));
    await userEvent.click(await screen.findByRole('option', { name: 'Read and write' }));
    expect(folders.current.grant).toHaveBeenCalledWith('/Users/ren/k9s', true, 'Always');
  });

  it('removes a folder, and draws a refused remove', async () => {
    withFolders({ always: [folder('/Users/ren/k9s')], revokeError: 'held' });
    render(<SandboxFolderGroups path={pathOf()} />);
    await userEvent.click(screen.getByRole('button', { name: 'Remove' }));
    expect(folders.current.revoke).toHaveBeenCalledWith('/Users/ren/k9s');
    expect(screen.getByText('held')).toBeInTheDocument();
  });

  it('holds the controls while the rules are held or the row is being removed', () => {
    withFolders({ always: [folder('/a'), folder('/b')], revoking: new Set(['/b']) });
    const { unmount } = render(<SandboxFolderGroups path={pathOf()} />);
    let rows = within(group(/^Custom · /)).getAllByRole('listitem');
    expect(within(rows[0]).getByRole('button', { name: 'Remove' })).toBeEnabled();
    expect(within(rows[1]).getByRole('button', { name: 'Remove' })).toBeDisabled();
    expect(within(rows[1]).getByRole('combobox')).toBeDisabled();
    unmount();

    withFolders({ always: [folder('/a')], rulesHeld: true });
    render(<SandboxFolderGroups path={pathOf()} />);
    rows = within(group(/^Custom · /)).getAllByRole('listitem');
    expect(within(rows[0]).getByRole('button', { name: 'Remove' })).toBeDisabled();
    expect(within(rows[0]).getByRole('combobox')).toBeDisabled();
  });

  it('says a folder inside a closed one may meet a macOS prompt, on macOS alone', () => {
    withFolders({ always: [folder('/Users/ren/Documents/runbooks')] });
    const { unmount } = render(<SandboxFolderGroups path={pathOf()} />);
    expect(screen.queryByText(/macOS may ask/)).toBeNull();
    unmount();
    mac.current = true;
    render(<SandboxFolderGroups path={pathOf()} />);
    expect(screen.getByText('macOS may ask you to let Kstack reach this folder.')).toBeInTheDocument();
  });

  it('mounts the folder-grant form from Add folder, for always', async () => {
    render(<SandboxFolderGroups path={pathOf()} />);
    expect(screen.queryByRole('textbox')).toBeNull();
    await userEvent.click(screen.getByRole('button', { name: 'Add folder' }));
    await userEvent.type(screen.getByRole('textbox'), '/Users/ren/new');
    await userEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(folders.current.grant).toHaveBeenCalledWith('/Users/ren/new', false, 'Always');
    expect(screen.queryByRole('textbox')).toBeNull();
  });

  it('lists what is never readable, and the folders only a folder inside of can be granted', async () => {
    withFolders({
      never: ['/Users/ren/.ssh', '/Users/ren/.aws '],
      closed: ['/Users/ren/Documents', '/opt/homebrew/var'],
    });
    render(<SandboxFolderGroups path={pathOf()} />);
    await userEvent.click(screen.getByRole('button', { name: 'Closed folders' }));
    const never = await screen.findByRole('list', { name: 'Never, not even a folder inside' });
    expect(
      within(never)
        .getAllByRole('listitem')
        .map((li) => li.textContent),
    ).toEqual(['/Users/ren/.ssh', '/Users/ren/.aws\\u{20}']);
    const inside = screen.getByRole('list', { name: 'Only a folder inside can be granted' });
    expect(
      within(inside)
        .getAllByRole('listitem')
        .map((li) => li.textContent),
    ).toEqual(['/Users/ren/Documents', '/opt/homebrew/var']);
  });

  it('switches each PATH folder: on is included, off is waiting or removed', async () => {
    const path = pathOf({
      entries: [
        entry('/opt/homebrew/bin', 'Adopted'),
        entry('/Users/ren/bin', 'Pending', { shared: true }),
        entry('/Users/ren/shims', 'Gone', { target: '/Users/ren/real' }),
      ],
    });
    render(<SandboxFolderGroups path={path} />);

    const rows = within(group(/^PATH · 3$/)).getAllByRole('listitem');
    expect(within(rows[0]).getByRole('switch')).toBeChecked();
    expect(within(rows[1]).getByRole('switch')).not.toBeChecked();
    expect(within(rows[1]).getByText('shared with its group')).toBeInTheDocument();
    expect(within(rows[2]).getByTestId('target').textContent).toBe('/Users/ren/real');

    await userEvent.click(within(rows[0]).getByRole('switch'));
    expect(path.remove).toHaveBeenCalledWith('/opt/homebrew/bin');
    await userEvent.click(within(rows[2]).getByRole('switch'));
    expect(path.include).toHaveBeenCalledWith('/Users/ren/shims', '/Users/ren/real');
  });

  it('holds a PATH switch while it changes or the list refreshes, and draws a refused change', () => {
    const path = pathOf({
      entries: [entry('/a', 'Adopted'), entry('/b', 'Pending')],
      changing: new Set(['/b']),
      changeError: 'moved',
    });
    const { unmount } = render(<SandboxFolderGroups path={path} />);
    let rows = within(group(/^PATH · /)).getAllByRole('listitem');
    expect(within(rows[0]).getByRole('switch')).not.toHaveAttribute('aria-disabled');
    expect(within(rows[1]).getByRole('switch')).toHaveAttribute('aria-disabled', 'true');
    expect(screen.getByText('moved')).toBeInTheDocument();
    unmount();

    render(<SandboxFolderGroups path={pathOf({ entries: [entry('/a', 'Adopted')], refreshing: true })} />);
    rows = within(group(/^PATH · /)).getAllByRole('listitem');
    expect(within(rows[0]).getByRole('switch')).toHaveAttribute('aria-disabled', 'true');
  });

  // Placeholder groups: their switches change nothing.
  it('draws the toolchain, shell and system groups as disabled placeholders, System collapsed', async () => {
    render(<SandboxFolderGroups path={pathOf()} />);
    for (const name of [/^Tools · /, /^Shell · /, /^System · /]) {
      expect(within(group(name)).getByText('Coming soon')).toBeInTheDocument();
    }
    within(group(/^Tools · /))
      .getAllByRole('switch')
      .forEach((s) => expect(s).toHaveAttribute('aria-disabled', 'true'));
    expect(within(group(/^System · /)).queryAllByRole('listitem')).toHaveLength(0);
    await userEvent.click(screen.getByRole('button', { name: /^System · / }));
    expect(within(group(/^System · /)).getAllByRole('listitem').length).toBeGreaterThan(0);
  });

  it('collapses a group from its header', async () => {
    withFolders({ always: [folder('/a')] });
    render(<SandboxFolderGroups path={pathOf()} />);
    await userEvent.click(screen.getByRole('button', { name: /^Custom · / }));
    expect(within(group(/^Custom · /)).queryAllByRole('listitem')).toHaveLength(0);
  });

  it('draws empty groups before the sidecar answers', async () => {
    withFolders({ always: undefined, never: undefined, closed: undefined });
    render(<SandboxFolderGroups path={pathOf({ entries: undefined })} />);
    expect(screen.getByRole('button', { name: 'Custom · 0' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'PATH · 0' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Closed folders' }));
    const never = await screen.findByRole('list', { name: 'Never, not even a folder inside' });
    expect(within(never).queryAllByRole('listitem')).toHaveLength(0);
    const inside = screen.getByRole('list', { name: 'Only a folder inside can be granted' });
    expect(within(inside).queryAllByRole('listitem')).toHaveLength(0);
  });

  it('closes the folder-grant form on Cancel', async () => {
    render(<SandboxFolderGroups path={pathOf()} />);
    await userEvent.click(screen.getByRole('button', { name: 'Add folder' }));
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('textbox')).toBeNull();
  });

  it('says a closed folder itself may meet a macOS prompt, and nothing before the home is known', () => {
    mac.current = true;
    withFolders({ always: [folder('/Users/ren/Desktop')] });
    const { unmount } = render(<SandboxFolderGroups path={pathOf()} />);
    expect(screen.getByText('macOS may ask you to let Kstack reach this folder.')).toBeInTheDocument();
    unmount();

    withFolders({ always: [folder('/Users/ren/Desktop')], wide: undefined });
    render(<SandboxFolderGroups path={pathOf()} />);
    expect(screen.queryByText(/macOS may ask/)).toBeNull();
  });
});
