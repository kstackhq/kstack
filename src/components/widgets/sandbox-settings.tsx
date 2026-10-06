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

// The Settings dialog's Sandbox section: the frozen PATH sandboxed commands
// search, each folder's state, and Include, Remove and Refresh PATH; then the
// executables the probe checks and the user's own executables;
// then the folders granted always, with Add and Remove; then what no grant
// opens. A folder name is text the user's shell or hand produced, so it is
// drawn through VisibleText with its whitespace kept and the trailing
// spelled: a change approves the folder drawn, and two that differ only in
// whitespace must not look alike. A executable's path, version and error are its
// own text, drawn the same way.
import { useState } from 'react';

import { Button } from '@kubetail/ui/elements/button';
import { Field, FieldContent, FieldDescription, FieldLabel } from '@kubetail/ui/elements/field';
import { Input } from '@kubetail/ui/elements/input';
import { Spinner } from '@kubetail/ui/elements/spinner';

import { FolderGrantForm } from '@/components/widgets/folder-grant-form';
import { VisibleText } from '@/components/widgets/visible-text';
import { isMacOS } from '@/lib/platform';
import { useSandbox } from '@/lib/sandbox';
import { useSandboxFolders } from '@/lib/sandbox-folders';
import type { SandboxFolder } from '@/lib/sandbox-folders';
import { useSandboxPath } from '@/lib/sandbox-path';
import type { SandboxPathEntry } from '@/lib/sandbox-path';
import { useSandboxExecutables } from '@/lib/sandbox-executables';
import type { SandboxExecutable, SandboxExecutables } from '@/lib/sandbox-executables';

const TAGS: Record<SandboxPathEntry['state'], string> = {
  Adopted: 'included',
  Pending: 'waiting for you',
  Gone: 'removed',
};

function PathEntry({
  entry,
  busy,
  onInclude,
  onRemove,
}: {
  entry: SandboxPathEntry;
  busy: boolean;
  onInclude: () => void;
  onRemove: () => void;
}) {
  return (
    <li className="flex items-start gap-2 py-1.5">
      <div className="min-w-0 flex-1 text-xs">
        <p className="font-mono break-all whitespace-pre-wrap" data-testid="dir">
          <VisibleText text={entry.dir} trailing="end" />
        </p>
        {entry.target !== entry.dir && (
          <p className="font-mono break-all whitespace-pre-wrap text-muted-foreground" data-testid="target">
            <VisibleText text={entry.target} trailing="end" />
          </p>
        )}
        <p className="text-muted-foreground">
          <span>{TAGS[entry.state]}</span>
          {entry.shared && (
            <>
              <span> · </span>
              <span>shared with its group</span>
            </>
          )}
        </p>
      </div>
      {entry.state !== 'Adopted' && (
        <Button size="sm" variant="outline" disabled={busy} onClick={onInclude}>
          Include
        </Button>
      )}
      {entry.state !== 'Gone' && (
        <Button size="sm" variant="ghost" disabled={busy} onClick={onRemove}>
          Remove
        </Button>
      )}
    </li>
  );
}

// What commands search while the shell cannot be read: the stored list, or
// with none, the default only before the shell was ever read.
function inEffect(entries: number, resolved: boolean | undefined): string {
  if (entries > 0) return 'The list is from the last time it could.';
  if (resolved) return 'Sandboxed commands search no folder.';
  return "Sandboxed commands use the system's default PATH.";
}

function SandboxPathList({ onRefreshed }: { onRefreshed: () => void }) {
  const { entries, fault, resolved, changing, refreshing, changeError, refreshError, include, remove, refresh } =
    useSandboxPath();
  return (
    <Field>
      <FieldContent>
        <FieldLabel>Sandbox</FieldLabel>
        <FieldDescription>
          Sandboxed commands find programs in these folders, in this order. Kstack reads them from your shell at each
          launch; a new folder that would open more to commands waits for you.
        </FieldDescription>
      </FieldContent>
      <div>
        <Button
          size="sm"
          variant="outline"
          disabled={refreshing}
          onClick={async () => {
            // A new PATH can change which binary each executable resolves to.
            if (await refresh()) onRefreshed();
          }}
        >
          Refresh PATH
        </Button>
        {refreshError && <p className="mt-1 text-xs text-destructive">{refreshError}</p>}
      </div>
      {fault && (
        <p className="text-xs text-muted-foreground">
          {`Kstack could not read your shell's PATH: ${fault}. ${inEffect(entries?.length ?? 0, resolved)}`}
        </p>
      )}
      <ul className="divide-y">
        {entries?.map((entry) => (
          <PathEntry
            key={entry.dir}
            entry={entry}
            busy={changing.has(entry.dir) || refreshing}
            onInclude={() => include(entry.dir, entry.target)}
            onRemove={() => remove(entry.dir)}
          />
        ))}
      </ul>
      {changeError && <p className="text-xs text-destructive">{changeError}</p>}
    </Field>
  );
}

function ExecutableRow({ executable }: { executable: SandboxExecutable }) {
  const { name, probed, resolved, shim, target, ok, version, error } = executable;
  return (
    <li className="py-1.5 text-xs">
      <p className="flex gap-2">
        <span className="font-mono font-medium break-all whitespace-pre-wrap">
          <VisibleText text={name} trailing="end" />
        </span>
        {probed && resolved && <span className="text-muted-foreground">{ok ? 'ok' : 'failed'}</span>}
      </p>
      {(!probed || !resolved) && <p className="text-muted-foreground">{error}</p>}
      {resolved && (
        <p className="font-mono break-all whitespace-pre-wrap text-muted-foreground" data-testid="resolved">
          <VisibleText text={resolved} trailing="end" />
        </p>
      )}
      {shim && (
        <p className="text-muted-foreground">
          <span>shim →</span>{' '}
          <span className="font-mono break-all whitespace-pre-wrap" data-testid="target">
            <VisibleText text={target} trailing="end" />
          </span>
        </p>
      )}
      {version && (
        <p className="break-all whitespace-pre-wrap">
          <VisibleText text={version} trailing="end" />
        </p>
      )}
      {probed && resolved && error && (
        <p className="break-all whitespace-pre-wrap text-destructive">
          <VisibleText text={error} trailing="end" />
        </p>
      )}
    </li>
  );
}

// The user's own executables, each with Remove, and the form that adds one.
function RegisteredExecutables({ executables }: { executables: SandboxExecutables }) {
  const { report, changing, registerError, removeError, register, remove } = executables;
  const [name, setName] = useState('');
  const [invocation, setInvocation] = useState('');
  const registered = report?.filter((executable) => executable.registered) ?? [];

  const add = async () => {
    if (await register(name, invocation)) {
      setName('');
      setInvocation('');
    }
  };

  return (
    <div className="flex flex-col gap-1">
      <p className="text-xs font-medium">Your executables</p>
      <ul className="divide-y" aria-label="Registered executables">
        {registered.map((executable) => (
          <li key={executable.name} className="flex items-center gap-2 py-1.5 text-xs">
            <span className="min-w-0 flex-1 font-mono break-all whitespace-pre-wrap">
              <VisibleText text={executable.invocation} trailing="end" />
            </span>
            <Button size="sm" variant="ghost" disabled={changing} onClick={() => remove(executable.name)}>
              Remove
            </Button>
          </li>
        ))}
      </ul>
      {removeError && <p className="text-xs text-destructive">{removeError}</p>}
      <div className="flex flex-wrap items-center gap-2">
        <Input
          aria-label="Executable name"
          className="w-40 font-mono text-xs"
          placeholder="name"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <Input
          aria-label="Invocation"
          className="min-w-48 flex-1 font-mono text-xs"
          placeholder="<name> --version"
          value={invocation}
          onChange={(e) => setInvocation(e.target.value)}
        />
        <Button size="sm" variant="outline" disabled={changing || name === ''} onClick={add}>
          Add executable
        </Button>
      </div>
      {registerError && <p className="text-xs text-destructive">{registerError}</p>}
    </div>
  );
}

function SandboxExecutableList({ executables }: { executables: SandboxExecutables }) {
  const { report, probing, probeError, probe } = executables;
  const kubectl = report?.find((executable) => executable.name === 'kubectl');
  return (
    <Field>
      <FieldContent>
        <FieldLabel>Executables</FieldLabel>
        <FieldDescription>
          Kstack runs each executable once in the sandbox, with no cluster and no network, to see what it needs.
        </FieldDescription>
      </FieldContent>
      {kubectl?.probed && !kubectl.resolved && (
        <p className="text-xs text-destructive">
          kubectl was not found on the sandbox&apos;s PATH. Install it, or include its folder above.
        </p>
      )}
      <div>
        <Button size="sm" variant="outline" disabled={probing} onClick={() => probe()}>
          {probing ? (
            <>
              <Spinner size="xs" className="mr-0" />
              Probing…
            </>
          ) : (
            'Probe again'
          )}
        </Button>
        {probeError && <p className="mt-1 text-xs text-destructive">{probeError}</p>}
      </div>
      <ul className="divide-y" aria-label="Executables">
        {report?.map((executable) => (
          <ExecutableRow key={executable.name} executable={executable} />
        ))}
      </ul>
      <RegisteredExecutables executables={executables} />
    </Field>
  );
}

// The folders macOS's privacy control guards for the app, under the home.
const PRIVATE_FOLDERS = ['Documents', 'Desktop', 'Downloads'];

// guardedByMacOS reports whether path lies inside one of the home's private
// folders, where macOS may ask the user before Kstack reaches it.
function guardedByMacOS(path: string, home: string | undefined): boolean {
  return (
    home !== undefined &&
    PRIVATE_FOLDERS.some((name) => path.startsWith(`${home}/${name}/`) || path === `${home}/${name}`)
  );
}

function FolderRow({
  folder,
  home,
  busy,
  onRemove,
}: {
  folder: SandboxFolder;
  home: string | undefined;
  busy: boolean;
  onRemove: () => void;
}) {
  // Destructured: the HTML-sink lint refuses any `.write`.
  const { path, write, refused } = folder;
  return (
    <li className="flex items-start gap-2 py-1.5">
      <div className="min-w-0 flex-1 text-xs">
        <p className="font-mono break-all whitespace-pre-wrap" data-testid="folder">
          <VisibleText text={path} trailing="end" />
        </p>
        <p className="text-muted-foreground">{write ? 'read and write' : 'read'}</p>
        {refused && <p className="text-muted-foreground">{refused}</p>}
        {isMacOS() && guardedByMacOS(path, home) && (
          <p className="text-muted-foreground">macOS may ask you to let Kstack reach this folder.</p>
        )}
      </div>
      <Button size="sm" variant="ghost" disabled={busy} onClick={onRemove}>
        Remove
      </Button>
    </li>
  );
}

function SandboxFolderList() {
  const { always, never, wide, rulesHeld, revoking, revokeError, revoke } = useSandboxFolders();
  const home = wide?.[0];

  return (
    <>
      <Field>
        <FieldContent>
          <FieldLabel>Folders</FieldLabel>
          <FieldDescription>
            Sandboxed commands, and Read, Write and Edit, reach these folders in every chat without asking: read, or
            read and write.
          </FieldDescription>
        </FieldContent>
        <ul className="divide-y" aria-label="Folders">
          {always?.map((folder) => (
            <FolderRow
              key={folder.id}
              folder={folder}
              home={home}
              busy={rulesHeld || revoking.has(folder.id)}
              onRemove={() => revoke(folder.id)}
            />
          ))}
        </ul>
        {revokeError && <p className="text-xs text-destructive">{revokeError}</p>}
        <FolderGrantForm durations={['Always']} submitLabel="Add" />
      </Field>
      <Field>
        <FieldContent>
          <FieldLabel>Never readable</FieldLabel>
          <FieldDescription>
            A sandboxed command never reads these, whatever you grant: the credential and private folders Kstack knows
            of, and Kstack&apos;s own data. A secret kept anywhere else is readable once its folder is granted.
          </FieldDescription>
        </FieldContent>
        <ul aria-label="Never readable" className="text-xs">
          {never?.map((p) => (
            <li key={p} className="font-mono break-all whitespace-pre-wrap text-muted-foreground">
              <VisibleText text={p} trailing="end" />
            </li>
          ))}
        </ul>
      </Field>
    </>
  );
}

// One reader of the executables for the section, since a Refresh PATH probes them.
function SandboxSections() {
  const executables = useSandboxExecutables();
  return (
    <>
      <SandboxPathList onRefreshed={executables.probe} />
      <SandboxExecutableList executables={executables} />
      <SandboxFolderList />
    </>
  );
}

export function SandboxSettings() {
  const { available } = useSandbox();
  if (available !== true) return null;
  return <SandboxSections />;
}
