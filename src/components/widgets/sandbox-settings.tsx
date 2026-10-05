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
// folders granted always, with Add and Remove; then what no grant opens. A
// folder name is text the user's shell or hand produced, so it is drawn
// through VisibleText with its whitespace kept and the trailing spelled: a
// change approves the folder drawn, and two that differ only in whitespace
// must not look alike.
import { Button } from '@kubetail/ui/elements/button';
import { Field, FieldContent, FieldDescription, FieldLabel } from '@kubetail/ui/elements/field';

import { FolderGrantForm } from '@/components/widgets/folder-grant-form';
import { VisibleText } from '@/components/widgets/visible-text';
import { isMacOS } from '@/lib/platform';
import { useSandbox } from '@/lib/sandbox';
import { useSandboxFolders } from '@/lib/sandbox-folders';
import type { SandboxFolder } from '@/lib/sandbox-folders';
import { useSandboxPath } from '@/lib/sandbox-path';
import type { SandboxPathEntry } from '@/lib/sandbox-path';

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

function SandboxPathList() {
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
        <Button size="sm" variant="outline" disabled={refreshing} onClick={() => refresh()}>
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

export function SandboxSettings() {
  const { available } = useSandbox();
  if (available !== true) return null;
  return (
    <>
      <SandboxPathList />
      <SandboxFolderList />
    </>
  );
}
