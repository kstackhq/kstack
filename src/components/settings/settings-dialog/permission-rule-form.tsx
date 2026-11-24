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

// The form a cluster rule is added or edited by: its effect and class, its
// cluster, then its scope and target, each optional and empty for anything.
import { Button } from '@kubetail/ui/elements/button';
import { Checkbox } from '@kubetail/ui/elements/checkbox';
import { useState } from 'react';
import type { SubmitEvent } from 'react';

import { Input } from '@/components/primitives/text-entry';
import type { PermissionClass, PermissionEffect } from '@/gql/graphql';
import type { RuleInput } from '@/lib/permission-settings';

import { Field, Select } from './field';

const EFFECTS = (['Allow', 'Deny', 'Ask'] satisfies PermissionEffect[]).map((value) => ({ value, label: value }));

// The classes a rule may name. A destructive write is never allowed by a
// rule, so Allow does not offer it.
export const CLASSES: { value: PermissionClass; label: string }[] = [
  { value: 'UpstreamWrite', label: 'Cluster writes' },
  { value: 'Destructive', label: 'Destructive cluster writes' },
  { value: 'SecretRead', label: 'Secret reads' },
];

// The namespace that is cluster-scoped objects alone: permissions.ClusterScope.
const CLUSTER_SCOPE = '[cluster]';

// The cluster select's value for a rule naming none.
const EVERY_CLUSTER = '';

export type ClusterOption = { value: string; label: string };

const EMPTY_RULE: RuleInput = {
  effect: 'Allow',
  class: 'UpstreamWrite',
  clusterID: null,
  namespace: '',
  verb: '',
  group: '',
  kind: '',
};

const sameRule = (a: RuleInput, b: RuleInput) =>
  (Object.keys(EMPTY_RULE) as (keyof RuleInput)[]).every((key) => a[key] === b[key]);

type Props = {
  /** The clusters a rule may name, by id and label. */
  clusters: ClusterOption[];
  /** The rule being edited; the form adds a new one when unset. */
  initial?: RuleInput;
  onSubmit: (input: RuleInput) => void;
  onCancel: () => void;
};

export function RuleForm({ clusters, initial, onSubmit, onCancel }: Props) {
  const [draft, setDraft] = useState(initial ?? EMPTY_RULE);
  const set = (over: Partial<RuleInput>) => setDraft((d) => ({ ...d, ...over }));
  const allow = draft.effect === 'Allow';
  const classes = CLASSES.filter((c) => !(allow && c.value === 'Destructive'));
  // A class the effect no longer offers falls back to the first.
  const klass = classes.some((c) => c.value === draft.class) ? draft.class : classes[0].value;
  // An Allow names one cluster, so it does not offer every cluster and falls
  // back to the first.
  const clusterOptions = allow ? clusters : [{ value: EVERY_CLUSTER, label: 'Every cluster' }, ...clusters];
  const clusterID = allow && draft.clusterID === null ? (clusters[0]?.value ?? null) : draft.clusterID;
  const rule = { ...draft, class: klass, clusterID };

  // A Secret read rule names a cluster and a namespace alone, and every Secret
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

  const submit = (e: SubmitEvent<HTMLFormElement>) => {
    e.preventDefault();
    onSubmit(rule);
  };

  // The namespace is a pattern over namespaced objects, or the one word for
  // cluster-scoped ones; the checkbox holds the word and shuts the pattern.
  const clusterScoped = draft.namespace === CLUSTER_SCOPE;
  const text = (name: 'namespace' | 'verb' | 'group' | 'kind', label: string, shut = false) => (
    <Field label={label}>
      {(id) => (
        <Input
          id={id}
          placeholder="Any"
          value={shut ? '' : draft[name]}
          disabled={shut}
          onChange={(e) => set({ [name]: e.target.value })}
        />
      )}
    </Field>
  );

  return (
    <form aria-label={initial ? 'Edit rule' : 'Add a rule'} className="flex flex-col gap-3 p-4" onSubmit={submit}>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Effect">
          {(id) => <Select id={id} options={EFFECTS} value={draft.effect} onChange={(effect) => set({ effect })} />}
        </Field>
        <Field label="Applies to">
          {(id) => <Select id={id} options={classes} value={klass} onChange={pickClass} />}
        </Field>
        <Field label="Cluster">
          {(id) => (
            <Select
              id={id}
              options={clusterOptions}
              value={clusterID ?? EVERY_CLUSTER}
              onChange={(value) => set({ clusterID: value === EVERY_CLUSTER ? null : value })}
            />
          )}
        </Field>
        {text('namespace', 'Namespace', clusterScoped)}
      </div>
      {!secretRead && (
        <>
          <div className="grid grid-cols-3 gap-3">
            {text('verb', 'Verb')}
            {text('group', 'Group')}
            {text('kind', 'Resource')}
          </div>
          {/* oxlint-disable-next-line jsx-a11y/label-has-associated-control -- Checkbox renders its control inside */}
          <label className="flex items-center gap-2">
            <Checkbox
              checked={clusterScoped}
              onCheckedChange={(checked) => set({ namespace: checked ? CLUSTER_SCOPE : '' })}
            />
            Cluster-scoped objects only, instead of a namespace
          </label>
        </>
      )}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
          Cancel
        </Button>
        {/* Adding a rule the always rules hold writes nothing, so an unchanged edit would only remove the original. */}
        <Button
          type="submit"
          size="sm"
          disabled={(initial && sameRule(rule, initial)) || (allow && clusterID === null)}
        >
          {initial ? 'Save' : 'Add'}
        </Button>
      </div>
    </form>
  );
}
