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

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { SandboxPath, SandboxPathEntry } from '@/lib/sandbox-path';

import { SandboxPathSection } from './sandbox-path';

const entry = (dir: string, state: SandboxPathEntry['state'] = 'Adopted'): SandboxPathEntry => ({
  dir,
  target: dir,
  state,
  source: 'Shell',
  shared: false,
});

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

describe('SandboxPathSection', () => {
  it("draws every folder in the shell's order, joined as PATH", () => {
    render(
      <SandboxPathSection
        path={pathOf({ entries: [entry('/opt/homebrew/bin'), entry('/usr/bin', 'Pending'), entry('/bin ', 'Gone')] })}
        onRefreshed={vi.fn()}
      />,
    );
    expect(screen.getByRole('group', { name: 'PATH' })).toHaveTextContent("Your shell's PATH, as last imported.");
    expect(screen.getByTestId('path').textContent).toBe('/opt/homebrew/bin:/usr/bin:/bin\\u{20}');
  });

  it('shows the last imported shell and date, and updates after another import', () => {
    const path = pathOf({ shell: 'zsh\u202e', importedAt: '2026-11-22T12:00:00Z' });
    const { rerender } = render(<SandboxPathSection path={path} onRefreshed={vi.fn()} />);
    expect(screen.getByText(/Imported from/)).toHaveTextContent('zsh');
    expect(screen.getByText(/Imported from/).querySelector('mark')).toBeInTheDocument();
    expect(screen.getByText(/Imported from/)).toHaveTextContent('2026');

    rerender(
      <SandboxPathSection path={pathOf({ shell: 'bash', importedAt: '2026-11-23T12:00:00Z' })} onRefreshed={vi.fn()} />,
    );
    expect(screen.getByText(/Imported from/)).toHaveTextContent('bash');
    expect(screen.getByText(/Imported from/)).toHaveTextContent('23');
  });

  it('imports from the shell, then probes the executables, but not on a refusal', async () => {
    const onRefreshed = vi.fn();
    const path = pathOf();
    const { unmount } = render(<SandboxPathSection path={path} onRefreshed={onRefreshed} />);
    await userEvent.click(screen.getByRole('button', { name: 'Import from shell' }));
    expect(path.refresh).toHaveBeenCalled();
    expect(onRefreshed).toHaveBeenCalled();
    unmount();

    onRefreshed.mockClear();
    render(
      <SandboxPathSection
        path={pathOf({ refresh: vi.fn(async () => false), refreshError: 'shell timed out' })}
        onRefreshed={onRefreshed}
      />,
    );
    await userEvent.click(screen.getByRole('button', { name: 'Import from shell' }));
    expect(onRefreshed).not.toHaveBeenCalled();
    expect(screen.getByText('shell timed out')).toBeInTheDocument();
  });

  it('holds Import while a refresh runs', () => {
    render(<SandboxPathSection path={pathOf({ refreshing: true })} onRefreshed={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Import from shell' })).toBeDisabled();
  });

  it('says when the shell could not be read, the list or the default', () => {
    const cases: [Partial<SandboxPath>, string][] = [
      [{ entries: [entry('/bin')] }, 'The list is from the last time it could.'],
      [{ entries: [], resolved: true }, 'Sandboxed commands search no folder.'],
      [{ entries: [], resolved: false }, "Sandboxed commands use the system's default PATH."],
    ];
    cases.forEach(([over, tail]) => {
      const { unmount } = render(
        <SandboxPathSection path={pathOf({ fault: 'zsh exited 1', ...over })} onRefreshed={vi.fn()} />,
      );
      expect(screen.getByText(`Kstack could not read your shell's PATH: zsh exited 1. ${tail}`)).toBeInTheDocument();
      unmount();
    });
  });

  it('draws an empty PATH before the sidecar answers, and the default while the shell cannot be read', () => {
    render(
      <SandboxPathSection
        path={pathOf({ entries: undefined, resolved: undefined, fault: 'zsh exited 1' })}
        onRefreshed={vi.fn()}
      />,
    );
    expect(screen.getByTestId('path').textContent).toBe('');
    expect(screen.getByText(/Sandboxed commands use the system's default PATH\./)).toBeInTheDocument();
  });
});
