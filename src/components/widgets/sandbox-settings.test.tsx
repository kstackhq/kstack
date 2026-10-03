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

import type { SandboxPath, SandboxPathEntry } from '@/lib/sandbox-path';

const { sandbox, path } = vi.hoisted(() => ({
  sandbox: { current: {} as { available: boolean | undefined } },
  path: { current: {} as SandboxPath },
}));
vi.mock('@/lib/sandbox', () => ({ useSandbox: () => sandbox.current }));
vi.mock('@/lib/sandbox-path', () => ({ useSandboxPath: () => path.current }));

const { SandboxSettings } = await import('./sandbox-settings');

const entry = (
  dir: string,
  state: SandboxPathEntry['state'],
  extra: Partial<SandboxPathEntry> = {},
): SandboxPathEntry => ({
  dir,
  target: dir,
  state,
  source: 'Shell',
  shared: false,
  ...extra,
});

function withPath(over: Partial<SandboxPath>) {
  path.current = {
    entries: [],
    fault: null,
    resolved: true,
    changing: new Set(),
    refreshing: false,
    changeError: null,
    refreshError: null,
    include: vi.fn(async () => {}),
    remove: vi.fn(async () => {}),
    refresh: vi.fn(async () => {}),
    ...over,
  };
}

beforeEach(() => {
  sandbox.current = { available: true };
  withPath({});
});

describe('SandboxSettings', () => {
  it('draws the entries in order with their tags and buttons', () => {
    withPath({
      entries: [
        entry('/opt/homebrew/bin', 'Adopted'),
        entry('/Users/ren/bin2', 'Pending', { shared: true }),
        entry('/Users/ren/.local/share/mise/shims', 'Gone', { target: '/Users/ren/.local/share/mise/v1' }),
      ],
    });
    render(<SandboxSettings />);

    const rows = screen.getAllByRole('listitem');
    expect(rows.map((r) => within(r).getByTestId('dir').textContent)).toEqual([
      '/opt/homebrew/bin',
      '/Users/ren/bin2',
      '/Users/ren/.local/share/mise/shims',
    ]);
    expect(within(rows[0]).getByText('included')).toBeInTheDocument();
    expect(within(rows[0]).getByRole('button', { name: 'Remove' })).toBeInTheDocument();
    expect(within(rows[0]).queryByRole('button', { name: 'Include' })).toBeNull();
    expect(within(rows[0]).queryByTestId('target')).toBeNull();

    expect(within(rows[1]).getByText('waiting for you')).toBeInTheDocument();
    expect(within(rows[1]).getByText('shared with its group')).toBeInTheDocument();
    expect(within(rows[1]).getByRole('button', { name: 'Include' })).toBeInTheDocument();
    expect(within(rows[1]).getByRole('button', { name: 'Remove' })).toBeInTheDocument();

    expect(within(rows[2]).getByText('removed')).toBeInTheDocument();
    expect(within(rows[2]).getByTestId('target').textContent).toBe('/Users/ren/.local/share/mise/v1');
    expect(within(rows[2]).getByRole('button', { name: 'Include' })).toBeInTheDocument();
    expect(within(rows[2]).queryByRole('button', { name: 'Remove' })).toBeNull();

    expect(screen.getByText(/Sandboxed commands find programs in these folders, in this order\./)).toBeInTheDocument();
  });

  // Two folders differing only in whitespace must not look alike where the
  // user includes one.
  it("keeps a folder name's whitespace and spells the trailing", () => {
    withPath({ entries: [entry('/opt/my  tools ', 'Pending', { target: '/opt/real\tbin ' })] });
    render(<SandboxSettings />);
    const row = screen.getByRole('listitem');
    const dir = within(row).getByTestId('dir');
    expect(dir).toHaveClass('whitespace-pre-wrap');
    expect(dir.textContent).toBe('/opt/my  tools\\u{20}');
    expect(within(row).getByTestId('target').textContent).toBe('/opt/real\tbin\\u{20}');
  });

  it('draws nothing on a machine with no sandbox, or before it answers', () => {
    [false, undefined].forEach((available) => {
      sandbox.current = { available };
      const { container, unmount } = render(<SandboxSettings />);
      expect(container).toBeEmptyDOMElement();
      unmount();
    });
  });

  it('calls each change, and holds its button while it is in flight', async () => {
    withPath({ entries: [entry('/opt/homebrew/bin', 'Adopted'), entry('/Users/ren/bin2', 'Pending')] });
    const { unmount } = render(<SandboxSettings />);
    const rows = screen.getAllByRole('listitem');
    await userEvent.click(within(rows[1]).getByRole('button', { name: 'Include' }));
    expect(path.current.include).toHaveBeenCalledWith('/Users/ren/bin2', '/Users/ren/bin2');
    await userEvent.click(within(rows[0]).getByRole('button', { name: 'Remove' }));
    expect(path.current.remove).toHaveBeenCalledWith('/opt/homebrew/bin');
    await userEvent.click(screen.getByRole('button', { name: 'Refresh PATH' }));
    expect(path.current.refresh).toHaveBeenCalled();

    // A fresh render each time: the mock's answer changes without the state
    // that would re-render the section in the app.
    unmount();
    withPath({ ...path.current, changing: new Set(['/Users/ren/bin2']) });
    const second = render(<SandboxSettings />);
    const pending = screen.getAllByRole('listitem')[1];
    expect(within(pending).getByRole('button', { name: 'Include' })).toBeDisabled();
    expect(within(screen.getAllByRole('listitem')[0]).getByRole('button', { name: 'Remove' })).toBeEnabled();

    second.unmount();
    withPath({ ...path.current, refreshing: true });
    render(<SandboxSettings />);
    expect(screen.getByRole('button', { name: 'Refresh PATH' })).toBeDisabled();
    // A refresh can move what an entry leads to, so nothing is included or
    // removed until the list it answers is drawn.
    const refreshingRows = screen.getAllByRole('listitem');
    expect(within(refreshingRows[1]).getByRole('button', { name: 'Include' })).toBeDisabled();
    expect(within(refreshingRows[0]).getByRole('button', { name: 'Remove' })).toBeDisabled();
  });

  it('draws a refused refresh and a refused change', () => {
    withPath({
      entries: [entry('/opt/homebrew/bin', 'Adopted')],
      refreshError: 'Your shell did not answer: timeout.',
      changeError: 'Your PATH settings hold an entry Kstack cannot read. Refresh PATH first.',
    });
    render(<SandboxSettings />);
    expect(screen.getByText('Your shell did not answer: timeout.')).toBeInTheDocument();
    expect(
      screen.getByText('Your PATH settings hold an entry Kstack cannot read. Refresh PATH first.'),
    ).toBeInTheDocument();
  });

  it('says when the shell could not be read, the list or the default', () => {
    withPath({ entries: [entry('/opt/homebrew/bin', 'Adopted')], fault: 'bad output' });
    const { unmount } = render(<SandboxSettings />);
    expect(
      screen.getByText("Kstack could not read your shell's PATH: bad output. The list is from the last time it could."),
    ).toBeInTheDocument();

    unmount();
    withPath({ entries: [], fault: 'bad output', resolved: true });
    const resolved = render(<SandboxSettings />);
    expect(
      screen.getByText("Kstack could not read your shell's PATH: bad output. Sandboxed commands search no folder."),
    ).toBeInTheDocument();

    // Only a list never read falls back to the system's default.
    resolved.unmount();
    withPath({ entries: [], fault: 'bad output', resolved: false });
    render(<SandboxSettings />);
    expect(
      screen.getByText(
        "Kstack could not read your shell's PATH: bad output. Sandboxed commands use the system's default PATH.",
      ),
    ).toBeInTheDocument();
  });
});
