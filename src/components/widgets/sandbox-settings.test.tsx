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
import type { SandboxExecutable, SandboxExecutables } from '@/lib/sandbox-executables';

const { sandbox, path, folders, executables, mac, openDialog } = vi.hoisted(() => ({
  openDialog: vi.fn(),
  sandbox: { current: {} as { available: boolean | undefined } },
  path: { current: {} as SandboxPath },
  folders: { current: {} as SandboxFolders },
  executables: { current: {} as SandboxExecutables },
  mac: { current: false },
}));
vi.mock('@/lib/sandbox', () => ({ useSandbox: () => sandbox.current }));
vi.mock('@/lib/sandbox-path', () => ({ useSandboxPath: () => path.current }));
vi.mock('@/lib/sandbox-executables', () => ({ useSandboxExecutables: () => executables.current }));
vi.mock('@/lib/sandbox-folders', () => ({ useSandboxFolders: () => folders.current }));
vi.mock('@/lib/platform', () => ({ isMacOS: () => mac.current }));
vi.mock('@/lib/dialog', () => ({ useDialog: () => ({ openDialog }) }));

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
    refresh: vi.fn(async () => true),
    ...over,
  };
}

function withFolders(over: Partial<SandboxFolders>) {
  folders.current = {
    always: [],
    chat: [],
    never: [],
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

const folder = (dir: string, extra: Partial<SandboxFolder> = {}): SandboxFolder => ({
  id: dir,
  path: dir,
  write: false,
  refused: null,
  ...extra,
});

function withExecutables(over: Partial<SandboxExecutables>) {
  executables.current = {
    report: [],
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

beforeEach(() => {
  openDialog.mockClear();
  sandbox.current = { available: true };
  mac.current = false;
  withPath({});
  withFolders({});
  withExecutables({});
});

describe('SandboxSettings', () => {
  it('opens the onboarding flow again, and writes nothing', async () => {
    const user = userEvent.setup();
    render(<SandboxSettings />);
    await user.click(screen.getByRole('button', { name: 'Set up the sandbox again' }));
    expect(openDialog).toHaveBeenCalledExactlyOnceWith('onboarding');
    expect(path.current.include).not.toHaveBeenCalled();
    expect(path.current.refresh).not.toHaveBeenCalled();
    expect(executables.current.probe).not.toHaveBeenCalled();
  });

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

describe('SandboxSettings: Folders', () => {
  const folderRows = () => within(screen.getByRole('list', { name: 'Folders' })).queryAllByRole('listitem');

  it('draws each always grant with its tag, Remove and a refused reason', async () => {
    withFolders({
      always: [
        folder('/Users/ren/code'),
        folder('/Users/ren/svc', { write: true }),
        folder('/Users/ren/gone', { refused: 'This folder does not exist.' }),
      ],
    });
    render(<SandboxSettings />);
    const rows = folderRows();
    expect(rows.map((r) => within(r).getByTestId('folder').textContent)).toEqual([
      '/Users/ren/code',
      '/Users/ren/svc',
      '/Users/ren/gone',
    ]);
    expect(within(rows[0]).getByText('read')).toBeInTheDocument();
    expect(within(rows[1]).getByText('read and write')).toBeInTheDocument();
    expect(within(rows[2]).getByText('This folder does not exist.')).toBeInTheDocument();
    await userEvent.click(within(rows[1]).getByRole('button', { name: 'Remove' }));
    expect(folders.current.revoke).toHaveBeenCalledWith('/Users/ren/svc');
  });

  it('holds a Remove in flight and draws a refused one', () => {
    withFolders({
      always: [folder('/Users/ren/code')],
      revoking: new Set(['/Users/ren/code']),
      revokeError: 'Record not found',
    });
    render(<SandboxSettings />);
    expect(within(folderRows()[0]).getByRole('button', { name: 'Remove' })).toBeDisabled();
    expect(screen.getByText('Record not found')).toBeInTheDocument();
  });

  it('draws Remove disabled while the rules are held, since the sidecar refuses it', () => {
    withFolders({ always: [folder('/Users/ren/code')], rulesHeld: true });
    render(<SandboxSettings />);
    expect(within(folderRows()[0]).getByRole('button', { name: 'Remove' })).toBeDisabled();
  });

  it('adds a folder, read or read and write, and clears the field once it is taken', async () => {
    render(<SandboxSettings />);
    const field = screen.getByRole('textbox', { name: 'Folder to grant' });
    expect(screen.getByRole('button', { name: 'Add' })).toBeDisabled();
    await userEvent.type(field, '/Users/ren/code');
    await userEvent.click(screen.getByRole('checkbox', { name: 'read and write' }));
    await userEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(folders.current.grant).toHaveBeenCalledWith('/Users/ren/code', true, 'Always');
    expect(field).toHaveValue('');
    expect(screen.getByRole('checkbox', { name: 'read and write' })).not.toBeChecked();
  });

  it('sends the folder as typed, whitespace included', async () => {
    render(<SandboxSettings />);
    await userEvent.type(screen.getByRole('textbox', { name: 'Folder to grant' }), ' /Users/ren/code  ');
    await userEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(folders.current.grant).toHaveBeenCalledWith(' /Users/ren/code  ', false, 'Always');
  });

  it("keeps the field on a refusal, draws its reason, and offers a link's target", async () => {
    withFolders({
      grant: vi.fn(async () => false),
      grantError: {
        message: '/Users/ren/src is a link to /Users/ren/code; grant /Users/ren/code instead.',
        target: '/Users/ren/code',
      },
    });
    render(<SandboxSettings />);
    const field = screen.getByRole('textbox', { name: 'Folder to grant' });
    await userEvent.type(field, '/Users/ren/src');
    await userEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(field).toHaveValue('/Users/ren/src');
    expect(
      screen.getByText('/Users/ren/src is a link to /Users/ren/code; grant /Users/ren/code instead.'),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Grant /Users/ren/code' }));
    expect(field).toHaveValue('/Users/ren/code');
  });

  it('warns of a wide folder before Add is pressed', async () => {
    withFolders({ wide: ['/Users/ren', '/Users', '/Volumes'] });
    render(<SandboxSettings />);
    const field = screen.getByRole('textbox', { name: 'Folder to grant' });
    const home = /This lets commands read everything in your home folder except the credential folders Kstack knows of/;
    const typeIn = async (typed: string) => {
      await userEvent.clear(field);
      await userEvent.type(field, typed);
    };
    await typeIn('/Users/ren/');
    expect(screen.getByText(home)).toBeInTheDocument();
    await typeIn('/Users');
    expect(screen.getByText(home)).toBeInTheDocument();
    await typeIn('/Users/ren/code');
    expect(screen.queryByText(home)).toBeNull();
    await typeIn('/Volumes');
    expect(screen.getByText(/This lets commands read every disk mounted on this Mac\./)).toBeInTheDocument();
  });

  it('says a grant inside a closed folder may meet a macOS prompt, on macOS alone', () => {
    withFolders({ always: [folder('/Users/ren/Documents/project'), folder('/Users/ren/code')] });
    const { unmount } = render(<SandboxSettings />);
    expect(screen.queryByText('macOS may ask you to let Kstack reach this folder.')).toBeNull();
    unmount();

    mac.current = true;
    render(<SandboxSettings />);
    const rows = folderRows();
    expect(within(rows[0]).getByText('macOS may ask you to let Kstack reach this folder.')).toBeInTheDocument();
    expect(within(rows[1]).queryByText('macOS may ask you to let Kstack reach this folder.')).toBeNull();
  });

  it('lists what is never readable', () => {
    withFolders({ never: ['/Users/ren/.ssh', '/Users/ren/Library/Application Support/Kstack'] });
    render(<SandboxSettings />);
    const list = screen.getByRole('list', { name: 'Never readable' });
    expect(
      within(list)
        .getAllByRole('listitem')
        .map((r) => r.textContent),
    ).toEqual(['/Users/ren/.ssh', '/Users/ren/Library/Application Support/Kstack']);
    expect(screen.getByText(/A sandboxed command never reads these, whatever you grant/)).toBeInTheDocument();
  });
});

describe('SandboxSettings: Executables', () => {
  const executableRows = () => within(screen.getByRole('list', { name: 'Executables' })).getAllByRole('listitem');

  it('draws each executable: its path or why it has none, a shim, its version, its tag and its error', () => {
    withExecutables({
      report: [
        probed('kubectl', { shim: true, target: '/Users/ren/.asdf/installs/kubectl/1.31/bin/kubectl' }),
        probed('helm', { ok: false, error: 'Exit code 1', version: 'Error: plugin failed' }),
        probed('kustomize', { resolved: '', ok: false, version: '', error: "not found on the sandbox's PATH" }),
        probed('git', { probed: false, resolved: '', ok: false, version: '', error: 'not probed yet' }),
        probed('jq', {
          probed: false,
          resolved: '',
          ok: false,
          version: '',
          error: 'could not start: the disk did not answer in time',
        }),
      ],
    });
    render(<SandboxSettings />);
    const [kubectl, helm, kustomize, git, jq] = executableRows();

    expect(within(kubectl).getByTestId('resolved').textContent).toBe('/opt/homebrew/bin/kubectl');
    expect(within(kubectl).getByText('shim →')).toBeInTheDocument();
    expect(within(kubectl).getByTestId('target').textContent).toBe(
      '/Users/ren/.asdf/installs/kubectl/1.31/bin/kubectl',
    );
    expect(within(kubectl).getByText('kubectl v1')).toBeInTheDocument();
    expect(within(kubectl).getByText('ok')).toBeInTheDocument();

    expect(within(helm).getByText('failed')).toBeInTheDocument();
    expect(within(helm).getByText('Exit code 1')).toBeInTheDocument();
    expect(within(helm).getByText('Error: plugin failed')).toBeInTheDocument();

    expect(within(kustomize).getByText("not found on the sandbox's PATH")).toBeInTheDocument();
    expect(within(kustomize).queryByText('failed')).toBeNull();

    expect(within(git).getByText('not probed yet')).toBeInTheDocument();
    expect(within(git).queryByText('failed')).toBeNull();

    expect(within(jq).getByText('could not start: the disk did not answer in time')).toBeInTheDocument();
    expect(within(jq).queryByText('failed')).toBeNull();
    expect(
      screen.getByText(
        'Kstack runs each executable once in the sandbox, with no cluster and no network, to see what it needs.',
      ),
    ).toBeInTheDocument();
  });

  it('says plainly when kubectl was not found, and only then', () => {
    const line = "kubectl was not found on the sandbox's PATH. Install it, or include its folder above.";
    withExecutables({
      report: [probed('kubectl', { resolved: '', ok: false, error: "not found on the sandbox's PATH" })],
    });
    const { unmount } = render(<SandboxSettings />);
    expect(screen.getByText(line)).toBeInTheDocument();

    unmount();
    withExecutables({ report: [probed('kubectl')] });
    const found = render(<SandboxSettings />);
    expect(screen.queryByText(line)).toBeNull();

    found.unmount();
    withExecutables({ report: [probed('kubectl', { probed: false, resolved: '', error: 'not probed yet' })] });
    const unprobed = render(<SandboxSettings />);
    expect(screen.queryByText(line)).toBeNull();

    // A probe that could not start says nothing about whether kubectl is there.
    unprobed.unmount();
    withExecutables({
      report: [
        probed('kubectl', { probed: false, resolved: '', error: 'could not start: the disk did not answer in time' }),
      ],
    });
    render(<SandboxSettings />);
    expect(screen.queryByText(line)).toBeNull();
  });

  it('probes again, holding the button while it runs, and draws a refused probe', async () => {
    withExecutables({ report: [probed('kubectl')] });
    const { unmount } = render(<SandboxSettings />);
    await userEvent.click(screen.getByRole('button', { name: 'Probe again' }));
    expect(executables.current.probe).toHaveBeenCalled();

    unmount();
    withExecutables({ report: [probed('kubectl')], probing: true, probeError: 'This machine has no sandbox.' });
    render(<SandboxSettings />);
    expect(screen.getByRole('button', { name: /Probing…/ })).toBeDisabled();
    expect(screen.getByText('This machine has no sandbox.')).toBeInTheDocument();
  });

  it('probes once Refresh PATH answers, and not when it is refused', async () => {
    const { unmount } = render(<SandboxSettings />);
    await userEvent.click(screen.getByRole('button', { name: 'Refresh PATH' }));
    expect(executables.current.probe).toHaveBeenCalledTimes(1);

    unmount();
    withPath({ refresh: vi.fn(async () => false) });
    withExecutables({});
    render(<SandboxSettings />);
    await userEvent.click(screen.getByRole('button', { name: 'Refresh PATH' }));
    expect(executables.current.probe).not.toHaveBeenCalled();
  });

  it('lists the registered executables with Remove, and adds one', async () => {
    withExecutables({ report: [probed('kubectl'), probed('k9s', { registered: true })] });
    render(<SandboxSettings />);
    const registered = within(screen.getByRole('list', { name: 'Registered executables' })).getAllByRole('listitem');
    expect(registered).toHaveLength(1);
    await userEvent.click(within(registered[0]).getByRole('button', { name: 'Remove' }));
    expect(executables.current.remove).toHaveBeenCalledWith('k9s');

    const name = screen.getByRole('textbox', { name: 'Executable name' });
    const invocation = screen.getByRole('textbox', { name: 'Invocation' });
    expect(invocation).toHaveAttribute('placeholder', '<name> --version');
    await userEvent.type(name, 'stern');
    await userEvent.click(screen.getByRole('button', { name: 'Add executable' }));
    expect(executables.current.register).toHaveBeenCalledWith('stern', '');
    expect(name).toHaveValue('');
  });

  it('keeps the form and draws why a register was refused', async () => {
    withExecutables({ register: vi.fn(async () => false), registerError: 'Kstack probes kubectl already.' });
    render(<SandboxSettings />);
    await userEvent.type(screen.getByRole('textbox', { name: 'Executable name' }), 'kubectl');
    await userEvent.click(screen.getByRole('button', { name: 'Add executable' }));
    expect(screen.getByRole('textbox', { name: 'Executable name' })).toHaveValue('kubectl');
    expect(screen.getByText('Kstack probes kubectl already.')).toBeInTheDocument();
  });

  it('holds Add and Remove while a change is in flight', () => {
    withExecutables({ report: [probed('k9s', { registered: true })], changing: true });
    render(<SandboxSettings />);
    expect(screen.getByRole('button', { name: 'Remove' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Add executable' })).toBeDisabled();
  });
});
