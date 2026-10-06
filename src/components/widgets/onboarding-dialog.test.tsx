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
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { OnboardingFinish } from '@/lib/onboarding';
import type { PermissionSettings } from '@/lib/permission-settings';
import type { Sandbox } from '@/lib/sandbox';
import type { SandboxExecutable, SandboxExecutables } from '@/lib/sandbox-executables';
import type { SandboxPath, SandboxPathEntry } from '@/lib/sandbox-path';

const { sandbox, path, executables, permissions, finishing, windows } = vi.hoisted(() => ({
  sandbox: { current: {} as Sandbox },
  path: { current: {} as SandboxPath },
  executables: { current: {} as SandboxExecutables },
  permissions: { current: {} as PermissionSettings },
  finishing: { current: {} as OnboardingFinish },
  windows: { current: false },
}));
vi.mock('@/lib/sandbox', () => ({ useSandbox: () => sandbox.current }));
vi.mock('@/lib/sandbox-path', () => ({ useSandboxPath: () => path.current }));
vi.mock('@/lib/sandbox-executables', () => ({ useSandboxExecutables: () => executables.current }));
vi.mock('@/lib/permission-settings', () => ({ usePermissionSettings: () => permissions.current }));
vi.mock('@/lib/onboarding', () => ({ useOnboardingFinish: () => finishing.current }));
vi.mock('@/lib/platform', () => ({ isMacOS: () => false, isWindows: () => windows.current }));

const { OnboardingDialog } = await import('./onboarding-dialog');

const entry = (dir: string, state: SandboxPathEntry['state']): SandboxPathEntry => ({
  dir,
  target: dir,
  state,
  source: 'Shell',
  shared: false,
});

const probed = (name: string, extra: Partial<SandboxExecutable> = {}): SandboxExecutable => ({
  name,
  invocation: `${name} --version`,
  registered: false,
  probed: true,
  resolved: `/opt/homebrew/bin/${name}`,
  shim: false,
  target: '',
  ok: true,
  version: `${name} v1`,
  error: '',
  ...extra,
});

const unprobed = (name: string) =>
  probed(name, { probed: false, resolved: '', ok: false, version: '', error: 'not probed yet' });

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
    refresh: vi.fn(async () => true),
    ...over,
  };
}

function withExecutables(over: Partial<SandboxExecutables>) {
  executables.current = {
    report: [probed('kubectl')],
    probing: false,
    changing: false,
    probeError: null,
    registerError: null,
    removeError: null,
    probe: vi.fn(async () => {}),
    register: vi.fn(async () => true),
    remove: vi.fn(async () => {}),
    ...over,
  };
}

function withPermissions(over: Partial<PermissionSettings>) {
  permissions.current = {
    settings: {
      defaultMode: 'Ask',
      contexts: [
        { context: 'dev-eks', mode: 'Ask', source: 'Default', pattern: '', own: false },
        { context: 'prod-eu', mode: 'ReadOnly', source: 'Entry', pattern: 'prod-eu', own: true },
      ],
      rules: [],
      destructive: [],
      held: [],
    },
    refused: [],
    error: null,
    held: () => false,
    setDefaultMode: vi.fn(),
    setMode: vi.fn(),
    clearMode: vi.fn(),
    addRule: vi.fn(),
    removeRule: vi.fn(),
    discardRefused: vi.fn(),
    ...over,
  };
}

function withFinish(over: Partial<OnboardingFinish>) {
  finishing.current = { finishing: false, finishError: null, finish: vi.fn(async () => true), ...over };
}

beforeEach(() => {
  sandbox.current = {
    available: true,
    reason: 'Seatbelt',
    networkAvailable: true,
    networkReason: '',
    failed: false,
    retry: () => {},
  };
  windows.current = false;
  withPath({});
  withExecutables({});
  withPermissions({});
  withFinish({});
});

function renderDialog() {
  const onOpenChange = vi.fn();
  render(<OnboardingDialog open onOpenChange={onOpenChange} />);
  return onOpenChange;
}

const current = () => screen.getByRole('listitem', { current: 'step' });

describe('OnboardingDialog', () => {
  it('walks the three steps with Back and Next, and calls nothing', async () => {
    const user = userEvent.setup();
    renderDialog();

    expect(screen.getByRole('heading', { name: 'Set up the sandbox' })).toBeInTheDocument();
    expect(current()).toHaveTextContent('Programs');
    expect(screen.getByRole('button', { name: 'Back' })).toBeDisabled();

    await user.click(screen.getByRole('button', { name: 'Next' }));
    expect(current()).toHaveTextContent('Executables');
    await user.click(screen.getByRole('button', { name: 'Next' }));
    expect(current()).toHaveTextContent('Permissions');
    expect(screen.queryByRole('button', { name: 'Next' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Finish' })).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Back' }));
    expect(current()).toHaveTextContent('Executables');
    await user.click(screen.getByRole('button', { name: 'Back' }));
    expect(current()).toHaveTextContent('Programs');

    expect(executables.current.probe).not.toHaveBeenCalled();
    expect(finishing.current.finish).not.toHaveBeenCalled();
  });

  it('finishes, then closes', async () => {
    const user = userEvent.setup();
    const onOpenChange = renderDialog();
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Finish' }));

    expect(finishing.current.finish).toHaveBeenCalledOnce();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('stays open with the error when the finish is refused', async () => {
    const user = userEvent.setup();
    withFinish({ finish: vi.fn(async () => false), finishError: 'disk full' });
    const onOpenChange = renderDialog();
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Finish' }));

    expect(onOpenChange).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent('disk full');
    expect(screen.getByRole('button', { name: 'Finish' })).toBeEnabled();
  });

  it('holds Finish while the finish is in flight', async () => {
    const user = userEvent.setup();
    withFinish({ finishing: true });
    renderDialog();
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    expect(screen.getByRole('button', { name: 'Finish' })).toBeDisabled();
  });

  it('closes on Escape without finishing', async () => {
    const user = userEvent.setup();
    const onOpenChange = renderDialog();
    await user.keyboard('{Escape}');
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(finishing.current.finish).not.toHaveBeenCalled();
  });
});

describe('Programs', () => {
  it('draws the PATH list, and Include on a waiting entry includes it', async () => {
    const user = userEvent.setup();
    withPath({ entries: [entry('/opt/homebrew/bin', 'Adopted'), entry('/Users/ren/bin', 'Pending')] });
    renderDialog();

    expect(screen.getByText('Sandboxed commands find programs in these folders.')).toBeInTheDocument();
    expect(screen.getByText('/opt/homebrew/bin')).toBeInTheDocument();
    expect(screen.queryByText('Nothing is waiting for you.')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Include' }));
    expect(path.current.include).toHaveBeenCalledWith('/Users/ren/bin', '/Users/ren/bin');
  });

  it('says when nothing is waiting', () => {
    withPath({ entries: [entry('/opt/homebrew/bin', 'Adopted')] });
    renderDialog();
    expect(screen.getByText('Nothing is waiting for you.')).toBeInTheDocument();
  });

  // A new PATH can change which binary each executable resolves to.
  it('probes the executables after a Refresh PATH that answers', async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.click(screen.getByRole('button', { name: 'Refresh PATH' }));
    expect(executables.current.probe).toHaveBeenCalledOnce();
  });
});

describe('Executables', () => {
  function open() {
    const view = render(<OnboardingDialog open onOpenChange={vi.fn()} />);
    return { rerender: () => view.rerender(<OnboardingDialog open onOpenChange={vi.fn()} />) };
  }
  const next = (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByRole('button', { name: 'Next' }));
  const back = (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByRole('button', { name: 'Back' }));

  it('probes once when an executable is not probed and none runs, and not on a second visit', async () => {
    const user = userEvent.setup();
    withExecutables({ report: [probed('kubectl'), unprobed('helm')] });
    open();
    expect(executables.current.probe).not.toHaveBeenCalled();

    await next(user);
    expect(executables.current.probe).toHaveBeenCalledOnce();
    await back(user);
    await next(user);
    expect(executables.current.probe).toHaveBeenCalledOnce();
  });

  it('starts no probe when every executable is probed', async () => {
    const user = userEvent.setup();
    open();
    await next(user);
    expect(executables.current.probe).not.toHaveBeenCalled();
  });

  it('starts no probe while one runs', async () => {
    const user = userEvent.setup();
    withExecutables({ report: [unprobed('kubectl')], probing: true });
    open();
    await next(user);
    expect(executables.current.probe).not.toHaveBeenCalled();
  });

  // The launch probe can finish between the dialog opening and the step.
  it('decides on the report when the step is shown, not when the dialog opened', async () => {
    const user = userEvent.setup();
    withExecutables({ report: [unprobed('kubectl')] });
    const { rerender } = open();
    const { probe } = executables.current;
    executables.current = { ...executables.current, report: [probed('kubectl')] };
    rerender();

    await next(user);
    expect(probe).not.toHaveBeenCalled();
  });

  it("waits for the watch's first frame, then decides", async () => {
    const user = userEvent.setup();
    withExecutables({ report: undefined });
    const { rerender } = open();
    await next(user);
    expect(executables.current.probe).not.toHaveBeenCalled();

    executables.current = { ...executables.current, report: [unprobed('kubectl')] };
    rerender();
    expect(executables.current.probe).toHaveBeenCalledOnce();
  });

  it('draws the resolved binaries, and the spinner while probing', async () => {
    const user = userEvent.setup();
    withExecutables({ report: [probed('kubectl'), probed('helm')], probing: true });
    open();
    await next(user);
    expect(screen.getByText('/opt/homebrew/bin/kubectl')).toBeInTheDocument();
    expect(screen.getByText('/opt/homebrew/bin/helm')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Probing…/ })).toBeDisabled();
  });

  it('says plainly when kubectl was probed and not found', async () => {
    const user = userEvent.setup();
    withExecutables({
      report: [probed('kubectl', { resolved: '', ok: false, version: '', error: "not found on the sandbox's PATH" })],
    });
    open();
    await next(user);
    expect(screen.getByRole('alert')).toHaveTextContent(
      "kubectl was not found on the sandbox's PATH. Install it, or go Back to Programs and include the folder it is in, then Probe again.",
    );
  });

  it('says nothing of kubectl before it was probed', async () => {
    const user = userEvent.setup();
    withExecutables({ report: [unprobed('kubectl')], probing: true });
    open();
    await next(user);
    expect(screen.queryByRole('alert')).toBeNull();
  });
});

describe('Permissions', () => {
  async function toPermissions() {
    const user = userEvent.setup();
    renderDialog();
    await user.click(screen.getByRole('button', { name: 'Next' }));
    await user.click(screen.getByRole('button', { name: 'Next' }));
    return user;
  }

  it("draws the default mode and each context's mode, and sets a context's", async () => {
    const user = await toPermissions();
    expect(screen.getByRole('tab', { name: 'Ask', selected: true })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Mode of dev-eks' })).toHaveValue('Ask');
    expect(screen.getByRole('combobox', { name: 'Mode of prod-eu' })).toHaveValue('ReadOnly');
    expect(
      screen.getByText(
        'Set a production context to read-only here. Sandboxed commands have no network until you turn it on in a chat.',
      ),
    ).toBeInTheDocument();

    await user.selectOptions(screen.getByRole('combobox', { name: 'Mode of dev-eks' }), 'ReadOnly');
    expect(permissions.current.setMode).toHaveBeenCalledWith('dev-eks', 'ReadOnly');
  });

  it('says why a mode change was refused', async () => {
    withPermissions({ error: 'security.json is read-only' });
    await toPermissions();
    expect(screen.getByRole('alert')).toHaveTextContent('security.json is read-only');
  });

  it('holds the contexts while modes is held, and sends the user to Settings', async () => {
    withPermissions({ held: (field) => field === 'modes' });
    await toPermissions();
    expect(screen.getByRole('combobox', { name: 'Mode of dev-eks' })).toBeDisabled();
    expect(
      screen.getByText('The file holds context modes Kstack cannot read. Fix them in Settings.'),
    ).toBeInTheDocument();
  });
});

describe('with no sandbox', () => {
  beforeEach(() => {
    sandbox.current = { ...sandbox.current, available: false, reason: 'user namespaces are disabled' };
  });

  it('draws one screen with the reason, and OK finishes', async () => {
    const user = userEvent.setup();
    const onOpenChange = renderDialog();
    expect(
      screen.getByText('Kstack has no sandbox on this machine, so every command the model runs waits for you first.'),
    ).toBeInTheDocument();
    expect(screen.getByText('user namespaces are disabled')).toBeInTheDocument();
    expect(screen.queryByRole('list', { name: 'Steps' })).toBeNull();

    await user.click(screen.getByRole('button', { name: 'OK' }));
    expect(finishing.current.finish).toHaveBeenCalledOnce();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('draws no reason on Windows', () => {
    windows.current = true;
    sandbox.current = { ...sandbox.current, reason: 'Windows has no sandbox' };
    renderDialog();
    expect(screen.queryByText('Windows has no sandbox')).toBeNull();
  });

  it('stays open with the error when the finish is refused', async () => {
    const user = userEvent.setup();
    withFinish({ finish: vi.fn(async () => false), finishError: 'disk full' });
    const onOpenChange = renderDialog();
    await user.click(screen.getByRole('button', { name: 'OK' }));
    expect(onOpenChange).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent('disk full');
  });
});
