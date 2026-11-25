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
import { describe, expect, it, vi } from 'vitest';

import type { RuleInput } from '@/lib/permission-settings';
import { expectCodeText } from '@/test-utils';

import { RuleForm } from './permission-rule-form';

const rule: RuleInput = {
  effect: 'Allow',
  class: 'UpstreamWrite',
  clusterID: 'c2',
  namespace: 'payments-*',
  verb: '',
  group: '',
  kind: '',
};

const CLUSTERS = [
  { value: 'c1', label: 'dev-eks' },
  { value: 'c2', label: 'Staging (staging)' },
];

function renderForm(props: Partial<Parameters<typeof RuleForm>[0]> = {}) {
  const onSubmit = vi.fn();
  const onCancel = vi.fn();
  render(<RuleForm clusters={CLUSTERS} onSubmit={onSubmit} onCancel={onCancel} {...props} />);
  return { onSubmit, onCancel, form: screen.getByRole('form') };
}

const CLUSTER_SCOPED = 'Cluster-scoped objects only, instead of a namespace';

describe('RuleForm', () => {
  it("turns off the webview's suggestions in every field, each empty one reading Any", () => {
    const { form } = renderForm();
    const boxes = within(form).getAllByRole('textbox');
    expect(boxes).toHaveLength(4);
    boxes.forEach((box) => {
      expectCodeText(box);
      expect(box).toHaveAttribute('placeholder', 'Any');
    });
  });

  it('adds a cluster rule with its fields', async () => {
    const user = userEvent.setup();
    const { form, onSubmit } = renderForm();
    expect(form).toHaveAccessibleName('Add a rule');

    await user.selectOptions(within(form).getByLabelText('Effect'), 'Deny');
    await user.selectOptions(within(form).getByLabelText('Cluster'), 'Staging (staging)');
    await user.type(within(form).getByLabelText('Group'), 'apps');
    await user.type(within(form).getByLabelText('Resource'), 'deployments');
    await user.click(within(form).getByRole('button', { name: 'Add' }));
    expect(onSubmit).toHaveBeenCalledWith({
      effect: 'Deny',
      class: 'UpstreamWrite',
      clusterID: 'c2',
      namespace: '',
      verb: '',
      group: 'apps',
      kind: 'deployments',
    });
  });

  it('cancels', async () => {
    const user = userEvent.setup();
    const { form, onCancel } = renderForm();
    await user.click(within(form).getByRole('button', { name: 'Cancel' }));
    expect(onCancel).toHaveBeenCalled();
  });

  it('sends the cluster-scope word for a cluster-scoped rule and shuts the pattern', async () => {
    const user = userEvent.setup();
    const { form, onSubmit } = renderForm();

    await user.type(within(form).getByLabelText('Namespace'), 'team-*');
    await user.click(within(form).getByRole('checkbox', { name: CLUSTER_SCOPED }));
    expect(within(form).getByLabelText('Namespace')).toBeDisabled();
    expect(within(form).getByLabelText('Namespace')).toHaveValue('');
    await user.click(within(form).getByRole('button', { name: 'Add' }));
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ namespace: '[cluster]' }));

    await user.click(within(form).getByRole('checkbox', { name: CLUSTER_SCOPED }));
    expect(within(form).getByLabelText('Namespace')).toBeEnabled();
    expect(within(form).getByLabelText('Namespace')).toHaveValue('');
  });

  // A Secret read rule names a cluster and a namespace and nothing else, so
  // the form draws those alone and sends the rest empty, whatever was typed.
  it('adds a Secret read rule by its cluster and namespace alone', async () => {
    const user = userEvent.setup();
    const { form, onSubmit } = renderForm();

    await user.type(within(form).getByLabelText('Verb'), 'delete');
    await user.click(within(form).getByRole('checkbox', { name: CLUSTER_SCOPED }));
    await user.selectOptions(within(form).getByLabelText('Applies to'), 'Secret reads');
    ['Verb', 'Group', 'Resource'].forEach((label) => expect(within(form).queryByLabelText(label)).toBeNull());
    expect(within(form).queryByRole('checkbox')).toBeNull();
    await user.type(within(form).getByLabelText('Namespace'), 'team-a');
    await user.click(within(form).getByRole('button', { name: 'Add' }));
    expect(onSubmit).toHaveBeenCalledWith({
      effect: 'Allow',
      class: 'SecretRead',
      clusterID: 'c1',
      namespace: 'team-a',
      verb: '',
      group: '',
      kind: '',
    });
  });

  it.each(['Allow', 'Deny', 'Ask'])('offers Secret reads to %s', async (effect) => {
    const user = userEvent.setup();
    const { form } = renderForm();
    await user.selectOptions(within(form).getByLabelText('Effect'), effect);
    expect(within(form).getByRole('option', { name: 'Secret reads' })).toBeInTheDocument();
  });

  it('offers no destructive class to an Allow', async () => {
    const user = userEvent.setup();
    const { form } = renderForm();
    const destructive = () => within(form).queryByRole('option', { name: 'Destructive cluster writes' });

    expect(destructive()).toBeNull();
    await user.selectOptions(within(form).getByLabelText('Effect'), 'Deny');
    expect(destructive()).toBeInTheDocument();
  });

  it('falls back to cluster writes when an Allow no longer offers the class picked', async () => {
    const user = userEvent.setup();
    const { form, onSubmit } = renderForm();
    await user.selectOptions(within(form).getByLabelText('Effect'), 'Deny');
    await user.selectOptions(within(form).getByLabelText('Applies to'), 'Destructive cluster writes');
    await user.selectOptions(within(form).getByLabelText('Effect'), 'Allow');
    expect(within(form).getByLabelText('Applies to')).toHaveValue('UpstreamWrite');
    await user.click(within(form).getByRole('button', { name: 'Add' }));
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ effect: 'Allow', class: 'UpstreamWrite' }));
  });

  it('draws the target fields again when Secret reads is put back', async () => {
    const user = userEvent.setup();
    const { form } = renderForm();
    await user.selectOptions(within(form).getByLabelText('Applies to'), 'Secret reads');
    await user.selectOptions(within(form).getByLabelText('Applies to'), 'Cluster writes');
    ['Verb', 'Group', 'Resource'].forEach((label) => expect(within(form).getByLabelText(label)).toHaveValue(''));
  });

  // Adding a rule the always rules already hold writes nothing, so saving it
  // unchanged would leave the remove to delete the original.
  it('edits a rule from its fields, saving only once the draft differs', async () => {
    const user = userEvent.setup();
    const { form, onSubmit } = renderForm({ initial: rule });
    expect(form).toHaveAccessibleName('Edit rule');
    expect(within(form).getByLabelText('Cluster')).toHaveValue('c2');
    expect(within(form).getByLabelText('Namespace')).toHaveValue('payments-*');
    const save = within(form).getByRole('button', { name: 'Save' });
    expect(save).toBeDisabled();

    await user.type(within(form).getByLabelText('Verb'), 'patch');
    expect(save).toBeEnabled();
    await user.clear(within(form).getByLabelText('Verb'));
    expect(save).toBeDisabled();

    await user.selectOptions(within(form).getByLabelText('Effect'), 'Ask');
    await user.click(save);
    expect(onSubmit).toHaveBeenCalledWith({ ...rule, effect: 'Ask' });
  });

  // An Allow on every cluster would reach a cluster added later, so only Deny
  // and Ask offer it, and an Allow falls back to the first cluster.
  it('offers every cluster to Deny and Ask alone', async () => {
    const user = userEvent.setup();
    const { form, onSubmit } = renderForm();
    const every = () => within(form).queryByRole('option', { name: 'Every cluster' });

    expect(every()).toBeNull();
    expect(within(form).getByLabelText('Cluster')).toHaveValue('c1');
    await user.selectOptions(within(form).getByLabelText('Effect'), 'Ask');
    await user.selectOptions(within(form).getByLabelText('Cluster'), 'Every cluster');
    await user.click(within(form).getByRole('button', { name: 'Add' }));
    expect(onSubmit).toHaveBeenLastCalledWith(expect.objectContaining({ effect: 'Ask', clusterID: null }));

    await user.selectOptions(within(form).getByLabelText('Effect'), 'Allow');
    expect(every()).toBeNull();
    expect(within(form).getByLabelText('Cluster')).toHaveValue('c1');
  });

  it('adds no Allow while there is no cluster to name', () => {
    const { form } = renderForm({ clusters: [] });
    expect(within(form).getByRole('button', { name: 'Add' })).toBeDisabled();
  });

  it('opens a cluster-scoped rule with its box checked', () => {
    const { form } = renderForm({ initial: { ...rule, namespace: '[cluster]' } });
    expect(within(form).getByRole('checkbox', { name: CLUSTER_SCOPED })).toBeChecked();
  });
});
