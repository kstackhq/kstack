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

// The Settings dialog's Permissions section: the approval modes and the rules a
// sandboxed command's cluster writes are decided by. Drawn only on a machine
// with a sandbox, since without one every command asks and nothing here
// applies. Every context, pattern and rule line is user or cluster text, drawn
// through VisibleText.
import { useId, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';

import { Button } from '@kubetail/ui/elements/button';
import { Input } from '@kubetail/ui/elements/input';
import { Tabs, TabsList, TabsTrigger } from '@kubetail/ui/elements/tabs';

import { Dialog } from '@/components/widgets/dialog';
import { VisibleText } from '@/components/widgets/visible-text';
import type { PermissionClass, PermissionEffect, PermissionMode, PermissionModeSource } from '@/gql/graphql';
import { usePermissionSettings } from '@/lib/permission-settings';
import type { PermissionSettings as Permissions, Refusal, RuleInput } from '@/lib/permission-settings';
import { useSandbox } from '@/lib/sandbox';

const SELECT = 'h-9 rounded-md border bg-transparent px-3 text-sm';

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

// Where a mode comes from; an entry's is drawn as its pattern.
const SOURCES: Record<Exclude<PermissionModeSource, 'Entry'>, string> = {
  Default: 'the default mode',
  Refused: 'read-only until the file is fixed',
};

// The classes a rule may name. A destructive write is never allowed by a
// rule, so Allow does not offer it.
const CLASSES: { value: PermissionClass; label: string }[] = [
  { value: 'UpstreamWrite', label: 'Cluster writes' },
  { value: 'Destructive', label: 'Destructive cluster writes' },
  { value: 'SecretRead', label: 'Secret reads' },
];

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
  if (available !== true) return null;
  return <PermissionSection />;
}

function PermissionSection() {
  const permissions = usePermissionSettings();
  const { settings, refused, error, held } = permissions;
  if (!settings) return null;

  return (
    <section aria-label="Permissions" className="flex flex-col gap-4 text-sm">
      <h3 className="font-medium">Permissions</h3>
      <p className="text-muted-foreground">
        Modes and rules decide what sandboxed commands may change. A chat switched outside the sandbox asks for every
        command.
      </p>
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}

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

      {held('rules') && (
        <HeldField field="rules" refused={refused} onDiscard={() => permissions.discardRefused('rules')} />
      )}
      <RuleList title="Rules" empty="No rules yet.">
        {settings.rules.map((rule) => (
          <li key={rule.id} className="flex items-center justify-between gap-2">
            <span className="break-all">
              <VisibleText text={rule.line} />
            </span>
            <Button
              type="button"
              variant="ghost"
              size="xs"
              disabled={held('rules')}
              onClick={() => permissions.removeRule(rule.id)}
            >
              Remove
            </Button>
          </li>
        ))}
      </RuleList>
      <RuleForm disabled={held('rules')} onAdd={permissions.addRule} />
      <RuleList title="Always asks" note="In every mode, and refused in a read-only one.">
        {settings.destructive.map((line) => (
          <li key={line}>{line}</li>
        ))}
      </RuleList>
    </section>
  );
}

// The default mode: a refused one the file holds, the picker, and what each
// mode does.
export function DefaultModePicker({ permissions }: { permissions: Permissions }) {
  const { settings, refused, held, setDefaultMode } = permissions;
  if (!settings) return null;
  const defaultRefusal = held('defaultMode') ? refused.find((r) => r.field === 'defaultMode') : undefined;
  return (
    <div className="flex flex-col gap-2">
      <span className="font-medium">Default mode</span>
      {defaultRefusal && (
        <p className="text-destructive">
          The file&apos;s default mode, <VisibleText text={defaultRefusal.value} />, {defaultRefusal.reason}: Kstack
          reads it as read-only until you pick one.
        </p>
      )}
      {/* While held no tab is selected, so picking read-only still sends it. */}
      <Tabs
        value={held('defaultMode') ? '' : settings.defaultMode}
        onValueChange={(mode) => setDefaultMode(mode as PermissionMode)}
      >
        <TabsList aria-label="Default mode">
          {MODES.map(({ value, label }) => (
            <TabsTrigger key={value} value={value}>
              {label}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      <ul className="text-muted-foreground">
        {MODES.map(({ value, label, line }) => (
          <li key={value}>
            {label}: {line}
          </li>
        ))}
      </ul>
    </div>
  );
}

function RuleList({
  title,
  note,
  empty,
  children,
}: {
  title: string;
  note?: string;
  empty?: string;
  children: ReactNode[];
}) {
  return (
    <section aria-label={title} className="flex flex-col gap-1">
      <h4 className="font-medium">{title}</h4>
      {note && <p className="text-muted-foreground">{note}</p>}
      {children.length > 0 ? (
        <ul className="flex flex-col gap-1">{children}</ul>
      ) : (
        <p className="text-muted-foreground">{empty}</p>
      )}
    </section>
  );
}

type ContextMode = {
  context: string;
  mode: PermissionMode;
  source: PermissionModeSource;
  pattern: string;
  own: boolean;
};

// One row per known context: its mode, where the mode comes from, and a picker
// that sets the context's own. Clear removes the context's own entry, never a
// pattern it matches.
export function ContextModes({
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
    <div className="flex flex-col gap-1">
      <span className="font-medium">Contexts</span>
      <table className="w-full">
        <tbody>
          {contexts.map((c) => (
            <tr key={c.context} aria-label={c.context}>
              <td className="py-1 font-mono break-all">
                <VisibleText text={c.context} />
              </td>
              <td className="py-1">
                <select
                  className={SELECT}
                  aria-label={`Mode of ${c.context}`}
                  value={c.mode}
                  disabled={disabled}
                  onChange={(e) => onSet(c.context, e.target.value as PermissionMode)}
                >
                  {MODES.map(({ value, label }) => (
                    <option key={value} value={value}>
                      {label}
                    </option>
                  ))}
                </select>
              </td>
              <td className="py-1 text-muted-foreground">
                {c.source === 'Entry' ? (
                  <span className="font-mono break-all">
                    <VisibleText text={c.pattern} />
                  </span>
                ) : (
                  SOURCES[c.source]
                )}
              </td>
              <td className="py-1">
                {c.own && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="xs"
                    disabled={disabled}
                    onClick={() => onClear(c.context)}
                  >
                    Clear
                  </Button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// The namespace that is cluster-scoped objects alone: permissions.ClusterScope.
const CLUSTER_SCOPE = '[cluster]';

const EMPTY_RULE: RuleInput = {
  effect: 'Allow',
  class: 'UpstreamWrite',
  context: '',
  namespace: '',
  verb: '',
  group: '',
  kind: '',
};

// The form a cluster rule is added by: its effect and class, then its scope
// and target, each optional and empty for anything.
function RuleForm({ disabled, onAdd }: { disabled: boolean; onAdd: (input: RuleInput) => void }) {
  const id = useId();
  const [draft, setDraft] = useState(EMPTY_RULE);
  const set = (over: Partial<RuleInput>) => setDraft((d) => ({ ...d, ...over }));
  const classes = CLASSES.filter((c) => !(draft.effect === 'Allow' && c.value === 'Destructive'));
  // A class the effect no longer offers falls back to the first.
  const klass = classes.some((c) => c.value === draft.class) ? draft.class : classes[0].value;

  // A Secret read rule names a context and a namespace alone, and every Secret
  // is in a namespace, so picking it clears the fields it draws none of.
  const secretRead = klass === 'SecretRead';
  const pickClass = (value: PermissionClass) =>
    value === 'SecretRead'
      ? set({
          class: value,
          verb: '',
          group: '',
          kind: '',
          namespace: draft.namespace === CLUSTER_SCOPE ? '' : draft.namespace,
        })
      : set({ class: value });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    onAdd({ ...draft, class: klass });
  };

  const field = (name: string, label: string, control: ReactNode) => (
    <div className="flex flex-col gap-1">
      <label htmlFor={`${id}-${name}`}>{label}</label>
      {control}
    </div>
  );
  const text = (name: Exclude<keyof RuleInput, 'effect' | 'class'>, label: string) =>
    field(
      name,
      label,
      <Input id={`${id}-${name}`} value={draft[name]} onChange={(e) => set({ [name]: e.target.value })} />,
    );
  // The namespace is a pattern over namespaced objects, or the one word for
  // cluster-scoped ones; the checkbox holds the word and shuts the pattern.
  const clusterScoped = draft.namespace === CLUSTER_SCOPE;
  const namespace = field(
    'namespace',
    'Namespace',
    <>
      <Input
        id={`${id}-namespace`}
        value={clusterScoped ? '' : draft.namespace}
        disabled={clusterScoped}
        onChange={(e) => set({ namespace: e.target.value })}
      />
      {!secretRead && (
        <label htmlFor={`${id}-cluster-scoped`} className="flex items-center gap-1">
          <input
            id={`${id}-cluster-scoped`}
            type="checkbox"
            checked={clusterScoped}
            onChange={(e) => set({ namespace: e.target.checked ? CLUSTER_SCOPE : '' })}
          />
          Cluster-scoped
        </label>
      )}
    </>,
  );

  return (
    <form aria-label="Add a rule" className="flex flex-col gap-2" onSubmit={submit}>
      <span className="font-medium">Add a rule</span>
      <div className="grid grid-cols-3 gap-2">
        {field(
          'effect',
          'Effect',
          <select
            id={`${id}-effect`}
            className={SELECT}
            value={draft.effect}
            onChange={(e) => set({ effect: e.target.value as PermissionEffect })}
          >
            <option value="Allow">Allow</option>
            <option value="Deny">Deny</option>
            <option value="Ask">Ask</option>
          </select>,
        )}
        {field(
          'class',
          'Class',
          <select
            id={`${id}-class`}
            className={SELECT}
            value={klass}
            onChange={(e) => pickClass(e.target.value as PermissionClass)}
          >
            {classes.map((c) => (
              <option key={c.value} value={c.value}>
                {c.label}
              </option>
            ))}
          </select>,
        )}
        {text('context', 'Context')}
        {namespace}
        {!secretRead && (
          <>
            {text('verb', 'Verb')}
            {text('group', 'API group')}
            {text('kind', 'Resource')}
          </>
        )}
      </div>
      <p className="text-muted-foreground">
        Each field matches anything when empty. Every field but the API group is a pattern: * matches any run of
        characters. A namespace pattern matches objects in a namespace alone; Cluster-scoped matches the rest.
      </p>
      <div>
        <Button type="submit" size="sm" disabled={disabled}>
          Add
        </Button>
      </div>
    </form>
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
    <section aria-label={title} className="flex flex-col gap-1 rounded-md border border-destructive/50 p-2">
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
