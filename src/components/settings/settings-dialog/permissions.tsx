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

// The Settings dialog's Permissions pane: the approval modes and the rules a
// sandboxed command's cluster writes are decided by. Drawn only on a machine
// with a sandbox, since without one every command asks and nothing here
// applies. Every context, pattern and rule line is user or cluster text, drawn
// through VisibleText.
import { Button } from '@kubetail/ui/elements/button';
import { Tabs, TabsList, TabsTrigger } from '@kubetail/ui/elements/tabs';
import { useId, useState } from 'react';

import { Dialog } from '@/components/primitives/dialog';
import { VisibleText } from '@/components/primitives/visible-text';
import type { PermissionMode, PermissionModeSource } from '@/gql/graphql';
import { usePermissionSettings } from '@/lib/permission-settings';
import type { PermissionSettings as Permissions, Refusal } from '@/lib/permission-settings';
import { useSandbox } from '@/lib/sandbox';

import { Select } from './field';
import { Group } from './group';
import { ChatGrants, RanWithoutAsking } from './permission-activity';
import { PermissionModeInfo } from './permission-mode-info';
import { PermissionRules } from './permission-rules';

// Each mode with what it asks, in the picker's order.
const MODES: { value: PermissionMode; label: string; line: string }[] = [
  {
    value: 'ReadOnly',
    label: 'Read-only',
    line: 'Every change to the cluster is refused. Showing Secret data asks you first.',
  },
  { value: 'Ask', label: 'Ask', line: 'Every change to the cluster, and showing Secret data, asks you first.' },
  {
    value: 'Auto',
    label: 'Auto',
    line: 'Changes run and Secret data is shown without asking, except what always asks.',
  },
];

// Where a mode comes from; an entry's is drawn as its pattern, or as the
// context's own.
const SOURCES: Record<Exclude<PermissionModeSource, 'Entry'>, string> = {
  Default: 'Default mode',
  Refused: 'read-only until the file is fixed',
};

// What Kstack does while a field it cannot read is held.
const HELD: Record<'modes' | 'rules', { title: string; meanwhile: string }> = {
  modes: {
    title: 'Modes Kstack cannot read',
    meanwhile: 'Every context is read-only until the file is fixed.',
  },
  rules: {
    title: 'Rules Kstack cannot read',
    meanwhile: 'Every cluster write is refused, and Secret data stays redacted, until the file is fixed.',
  },
};

export function PermissionSettings() {
  const { available } = useSandbox();
  if (available === false) {
    return (
      <p className="text-sm text-muted-foreground">
        This machine has no sandbox, so every command asks before it runs. Modes and rules apply only to sandboxed
        commands.
      </p>
    );
  }
  if (available !== true) return null;
  return <PermissionPane />;
}

function PermissionPane() {
  const permissions = usePermissionSettings();
  const { settings, refused, error, held } = permissions;
  if (!settings) return null;
  const contexts = settings.contexts.map((c) => c.context);

  return (
    <>
      <div className="flex flex-col gap-1 text-sm">
        <p className="text-muted-foreground">
          Modes and rules decide what sandboxed commands may change. A chat switched outside the sandbox asks for every
          command.
        </p>
        {error && (
          <p role="alert" className="text-destructive">
            {error}
          </p>
        )}
      </div>

      <DefaultModePicker permissions={permissions} />

      {held('modes') && (
        <HeldField field="modes" refused={refused} onDiscard={() => permissions.discardRefused('modes')} />
      )}
      <ContextModes
        contexts={settings.contexts}
        disabled={held('modes')}
        onSet={permissions.setMode}
        onClear={permissions.clearMode}
      />

      <ShowingFilter contexts={contexts} />

      {held('rules') && (
        <HeldField field="rules" refused={refused} onDiscard={() => permissions.discardRefused('rules')} />
      )}
      <PermissionRules
        rules={settings.rules}
        contexts={contexts}
        disabled={held('rules')}
        onAdd={permissions.addRule}
        onRemove={permissions.removeRule}
      />

      <ChatGrants />
      <RanWithoutAsking />
    </>
  );
}

// The default mode: a refused one the file holds, then the current mode and
// its picker.
function DefaultModePicker({ permissions }: { permissions: Permissions }) {
  const { settings, refused, held, setDefaultMode } = permissions;
  if (!settings) return null;
  const defaultHeld = held('defaultMode');
  const defaultRefusal = defaultHeld ? refused.find((r) => r.field === 'defaultMode') : undefined;
  // While held no mode is current, so picking read-only still sends it.
  const current = defaultHeld ? undefined : MODES.find((m) => m.value === settings.defaultMode);
  return (
    <Group title="Default mode" info={<PermissionModeInfo destructive={settings.destructive} />}>
      {defaultRefusal && (
        <p className="text-sm text-destructive">
          The file&apos;s default mode, <VisibleText text={defaultRefusal.value} />, {defaultRefusal.reason}: Kstack
          reads it as read-only until you pick one.
        </p>
      )}
      <div className="flex items-center gap-3 rounded-lg border px-4 py-3 text-sm">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          {current && (
            <>
              <span>{current.label}</span>
              <span className="text-xs text-muted-foreground">{current.line}</span>
            </>
          )}
        </div>
        <Tabs value={current?.value ?? ''} onValueChange={(mode) => setDefaultMode(mode as PermissionMode)}>
          <TabsList aria-label="Default mode">
            {MODES.map(({ value, label }) => (
              <TabsTrigger key={value} value={value}>
                {label}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </div>
    </Group>
  );
}

type ContextMode = {
  context: string;
  mode: PermissionMode;
  source: PermissionModeSource;
  pattern: string;
  own: boolean;
};

function sourceOf(c: ContextMode) {
  if (c.source !== 'Entry') return SOURCES[c.source];
  if (c.own) return 'Set for this context';
  return (
    <>
      From pattern <VisibleText text={c.pattern} />
    </>
  );
}

// One row per known context: its mode, where the mode comes from, and a picker
// that sets the context's own. Clear removes the context's own entry, never a
// pattern it matches.
function ContextModes({
  contexts,
  disabled,
  onSet,
  onClear,
}: {
  contexts: ContextMode[];
  disabled: boolean;
  onSet: (context: string, mode: PermissionMode) => void;
  onClear: (context: string) => void;
}) {
  return (
    <Group title="Contexts" lede="A context follows the default mode unless a pattern or its own entry sets one.">
      <ul className="divide-y rounded-lg border text-sm">
        {contexts.map((c) => (
          <li key={c.context} aria-label={c.context} className="flex items-center gap-3 px-4 py-3">
            <div className="flex min-w-0 flex-1 flex-col gap-0.5 break-all">
              <span className="font-mono">
                <VisibleText text={c.context} />
              </span>
              <span className="text-xs text-muted-foreground">{sourceOf(c)}</span>
            </div>
            {c.own && (
              <Button type="button" variant="ghost" size="sm" disabled={disabled} onClick={() => onClear(c.context)}>
                Clear
              </Button>
            )}
            <Select
              className="w-36"
              aria-label={`Mode of ${c.context}`}
              options={MODES}
              value={c.mode}
              disabled={disabled}
              onChange={(mode) => onSet(c.context, mode)}
            />
          </li>
        ))}
      </ul>
    </Group>
  );
}

// Placeholder: awaits a sidecar query for what applies to one context;
// filtering by pattern here would reimplement the engine. Held on All clusters.
function ShowingFilter({ contexts }: { contexts: string[] }) {
  const id = useId();
  return (
    <div className="flex items-center gap-2 text-sm">
      <label htmlFor={id} className="text-muted-foreground">
        Showing
      </label>
      <Select
        id={id}
        className="w-52"
        options={[{ value: '', label: 'All clusters' }, ...contexts.map((c) => ({ value: c, label: c }))]}
        disabled
      />
    </div>
  );
}

// A field of the file Kstack could not read: each refused value with its
// reason, what Kstack does meanwhile, and the discard, confirmed first.
function HeldField({
  field,
  refused,
  onDiscard,
}: {
  field: 'modes' | 'rules';
  refused: Refusal[];
  onDiscard: () => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const { title, meanwhile } = HELD[field];
  const values = refused.filter((r) => r.field === field);
  return (
    <section aria-label={title} className="flex flex-col gap-1 rounded-md border border-destructive/50 p-2 text-sm">
      <h4 className="font-medium">{title}</h4>
      <ul>
        {values.map((r) => (
          <li key={r.value}>
            <span className="font-mono break-all">
              <VisibleText text={r.value} />
            </span>{' '}
            {r.reason}
          </li>
        ))}
      </ul>
      <p className="text-muted-foreground">{meanwhile}</p>
      <div>
        <Button type="button" variant="outline" size="sm" onClick={() => setConfirming(true)}>
          Discard what Kstack cannot read
        </Button>
      </div>
      <Dialog
        open={confirming}
        onOpenChange={(open) => !open && setConfirming(false)}
        title="Discard what Kstack cannot read?"
        description="Kstack rewrites the file without these values, and keeps the rest."
      >
        <ul className="mb-2 font-mono text-sm break-all">
          {values.map((r) => (
            <li key={r.value}>
              <VisibleText text={r.value} />
            </li>
          ))}
        </ul>
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={() => setConfirming(false)}>
            Cancel
          </Button>
          <Button
            type="button"
            onClick={() => {
              setConfirming(false);
              onDiscard();
            }}
          >
            Discard
          </Button>
        </div>
      </Dialog>
    </section>
  );
}
