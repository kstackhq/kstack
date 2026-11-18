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

import type { KubeConfig } from '@/lib/kube-config';

const { kube } = vi.hoisted(() => ({ kube: { current: null as KubeConfig | null } }));
vi.mock('@/lib/kube-config', () => ({ useKubeConfig: () => ({ kubeConfig: kube.current, connected: true }) }));

const { SandboxConsole } = await import('./sandbox-console');

function renderConsole(props: Partial<Parameters<typeof SandboxConsole>[0]> = {}) {
  return render(<SandboxConsole open onOpenChange={vi.fn()} networkAvailable {...props} />);
}

beforeEach(() => {
  kube.current = {
    currentContext: 'kind-kstack',
    contexts: [{ name: 'kind-kstack', cluster: 'kind', user: 'kind', clusterEntry: null }],
  };
});

describe('SandboxConsole', () => {
  it('opens on its empty state, and a chip fills the command', async () => {
    renderConsole();
    const dialog = await screen.findByRole('dialog', { name: 'Sandbox console' });
    expect(within(dialog).getByText('Try a command in the sandbox')).toBeInTheDocument();
    expect(within(dialog).getByText(/Nothing here reaches a chat or the model\./)).toBeInTheDocument();
    expect(within(dialog).getByText('Your always grants apply')).toBeInTheDocument();

    await userEvent.click(within(dialog).getByRole('button', { name: 'cat ~/.ssh/config' }));
    expect(within(dialog).getByRole('textbox', { name: 'Command' })).toHaveValue('cat ~/.ssh/config');
  });

  // Placeholder: nothing runs a command yet.
  it('holds Run disabled whatever is typed', async () => {
    renderConsole();
    const box = await screen.findByRole('textbox', { name: 'Command' });
    await userEvent.type(box, 'ls');
    expect(screen.getByRole('button', { name: 'Run' })).toBeDisabled();
  });

  it('offers no cluster or each kubeconfig context', async () => {
    renderConsole();
    const cluster = await screen.findByRole('combobox', { name: 'Cluster' });
    expect(cluster).toHaveTextContent('No cluster');
    await userEvent.click(cluster);
    expect((await screen.findAllByRole('option')).map((o) => o.textContent)).toEqual(['No cluster', 'kind-kstack']);
    await userEvent.click(screen.getByRole('option', { name: 'kind-kstack' }));
    expect(cluster).toHaveTextContent('kind-kstack');
  });

  it('offers the network only where the machine can give it', async () => {
    const { unmount } = renderConsole();
    expect(await screen.findByRole('checkbox', { name: 'Network' })).not.toHaveAttribute('aria-disabled');
    unmount();
    renderConsole({ networkAvailable: false });
    expect(await screen.findByRole('checkbox', { name: 'Network' })).toHaveAttribute('aria-disabled', 'true');
  });

  it('draws each run: its command, its output stripped and spelled, and how it exited', async () => {
    renderConsole({
      history: [
        {
          command: 'cat ~/.aws/credentials',
          output: '\u001b[31mOperation not permitted\u001b[0m ',
          exitCode: 1,
          ms: 8,
        },
        { command: 'kubectl get ns', output: 'default', exitCode: 0, ms: 1500 },
      ],
    });
    const runs = await screen.findAllByRole('listitem');
    expect(runs[0]).toHaveTextContent('$ cat ~/.aws/credentials');
    expect(within(runs[0]).getByTestId('output').textContent).toBe('Operation not permitted\\u{20}');
    expect(within(runs[0]).getByText('exit 1 · 8ms')).toHaveClass('text-destructive');
    expect(within(runs[1]).getByText('exit 0 · 1s')).not.toHaveClass('text-destructive');
    expect(screen.queryByText('Try a command in the sandbox')).toBeNull();
  });
});
