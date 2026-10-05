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

// Two seams: the sandbox query and urql, whose queries answer from state and
// whose mutations are one mock, told apart by the operation they send.
const { sandboxState, settingsState, refusedState, mutateMock, queryArgs, reexecuteMock } = vi.hoisted(() => {
  const sandbox: { current: { available: boolean | undefined } } = { current: { available: true } };
  const settingsHeld: { current: unknown } = { current: undefined };
  const refused: { current: unknown[] } = { current: [] };
  const args: unknown[] = [];
  return {
    sandboxState: sandbox,
    settingsState: settingsHeld,
    refusedState: refused,
    mutateMock: vi.fn(),
    queryArgs: args,
    reexecuteMock: vi.fn(),
  };
});
vi.mock('@/lib/sandbox', () => ({ useSandbox: () => ({ ...sandboxState.current, failed: false, retry: () => {} }) }));
vi.mock('urql', () => ({
  useQuery: (args: { query: unknown }) => {
    queryArgs.push(args);
    return [
      {
        data: JSON.stringify(args.query).includes('securityRefused')
          ? { securityRefused: refusedState.current }
          : { permissionSettings: settingsState.current },
      },
      reexecuteMock,
    ];
  },
  useMutation: (doc: unknown) => {
    const name = /permission[A-Za-z]+/.exec(JSON.stringify(doc))![0];
    return [{}, (vars: unknown) => mutateMock(name, vars)];
  },
}));

const { PermissionSettings } = await import('./permission-settings');

const allow = { id: 'r1', line: 'Allow cluster writes in dev-eks / team-a' };

function settings(over: Record<string, unknown> = {}) {
  return {
    defaultMode: 'Ask',
    contexts: [
      { context: 'dev-eks', mode: 'Ask', source: 'Default', pattern: '', own: false },
      { context: 'prod-eu', mode: 'ReadOnly', source: 'Refused', pattern: '', own: false },
      { context: 'qa', mode: 'Auto', source: 'Entry', pattern: 'qa', own: true },
      { context: 'staging', mode: 'Auto', source: 'Entry', pattern: 'stag*', own: false },
    ],
    rules: [allow],
    destructive: ['Deleting a namespace', 'Any change to roles'],
    held: [],
    ...over,
  };
}

const row = (context: string) => screen.getByRole('row', { name: context });

beforeEach(() => {
  vi.clearAllMocks();
  queryArgs.length = 0;
  sandboxState.current = { available: true };
  settingsState.current = settings();
  refusedState.current = [];
  mutateMock.mockResolvedValue({ data: {} });
});

describe('PermissionSettings', () => {
  it('draws nothing on a machine with no sandbox', () => {
    sandboxState.current = { available: false };
    const { container } = render(<PermissionSettings />);
    expect(container).toBeEmptyDOMElement();
  });

  // Another window can change the settings, and the clusters watch can learn a
  // context, so what was read when Settings last opened is not trusted.
  it('asks the sidecar again on opening, and when its window regains focus', () => {
    render(<PermissionSettings />);
    const settingsQuery = queryArgs.find((a) => JSON.stringify(a).includes('permissionSettings'));
    expect(settingsQuery).toMatchObject({ requestPolicy: 'cache-and-network' });

    window.dispatchEvent(new Event('focus'));
    expect(reexecuteMock).toHaveBeenCalledWith({ requestPolicy: 'network-only' });
  });

  it('sets the default mode, and says what a mode governs', async () => {
    const user = userEvent.setup();
    render(<PermissionSettings />);

    expect(
      screen.getByText(
        'Modes and rules decide what sandboxed commands may change. A chat switched outside the sandbox asks for every command.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Ask', selected: true })).toBeInTheDocument();
    await user.click(screen.getByRole('tab', { name: 'Auto' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionDefaultModeSet', { mode: 'Auto' });
  });

  it("draws each context's mode and where it comes from", () => {
    render(<PermissionSettings />);
    expect(row('dev-eks')).toHaveTextContent('the default mode');
    expect(row('prod-eu')).toHaveTextContent('read-only until the file is fixed');
    expect(row('staging')).toHaveTextContent('stag*');
    expect(within(row('staging')).getByRole('combobox')).toHaveValue('Auto');
  });

  it("sets and clears a context's mode", async () => {
    const user = userEvent.setup();
    render(<PermissionSettings />);

    await user.selectOptions(within(row('dev-eks')).getByRole('combobox'), 'Auto');
    expect(mutateMock).toHaveBeenCalledWith('permissionModeSet', { context: 'dev-eks', mode: 'Auto' });
    await user.click(within(row('qa')).getByRole('button', { name: 'Clear' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionModeClear', { context: 'qa' });
  });

  // Clear removes a context's own entry; a pattern it matches is not its own.
  it('offers Clear for an own entry alone', () => {
    render(<PermissionSettings />);
    expect(within(row('staging')).queryByRole('button', { name: 'Clear' })).toBeNull();
    expect(within(row('dev-eks')).queryByRole('button', { name: 'Clear' })).toBeNull();
  });

  it('spells the context out', () => {
    settingsState.current = settings({
      contexts: [{ context: 'prod\u202Eue', mode: 'Ask', source: 'Default', pattern: '', own: false }],
    });
    render(<PermissionSettings />);

    expect(screen.getByRole('row').querySelector('mark')).toHaveTextContent('\\u{202E}');
  });

  it('draws the rules, and the class 5 list without Remove', async () => {
    const user = userEvent.setup();
    render(<PermissionSettings />);

    const mine = screen.getByRole('region', { name: 'Rules' });
    expect(mine).toHaveTextContent('Allow cluster writes in dev-eks / team-a');
    const asks = screen.getByRole('region', { name: 'Always asks' });
    expect(asks).toHaveTextContent('Deleting a namespace');
    expect(within(asks).queryByRole('button')).toBeNull();

    await user.click(within(mine).getByRole('button', { name: 'Remove' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionRuleRemove', { id: 'r1' });
  });

  it('adds a cluster rule with its fields', async () => {
    const user = userEvent.setup();
    render(<PermissionSettings />);
    const form = screen.getByRole('form', { name: 'Add a rule' });

    expect(within(form).getByLabelText('Verb')).toBeInTheDocument();
    expect(within(form).getByLabelText('API group')).toBeInTheDocument();
    expect(within(form).getByLabelText('Resource')).toBeInTheDocument();

    await user.selectOptions(within(form).getByLabelText('Effect'), 'Deny');
    await user.type(within(form).getByLabelText('Context'), 'prod-*');
    await user.type(within(form).getByLabelText('Resource'), 'deployments');
    await user.click(within(form).getByRole('button', { name: 'Add' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionRuleAdd', {
      input: {
        effect: 'Deny',
        class: 'UpstreamWrite',
        context: 'prod-*',
        namespace: '',
        verb: '',
        group: '',
        kind: 'deployments',
      },
    });
  });

  it('sends the cluster-scope word for a Cluster-scoped rule and shuts the pattern', async () => {
    const user = userEvent.setup();
    render(<PermissionSettings />);
    const form = screen.getByRole('form', { name: 'Add a rule' });

    await user.type(within(form).getByLabelText('Namespace'), 'team-*');
    await user.click(within(form).getByLabelText('Cluster-scoped'));
    expect(within(form).getByLabelText('Namespace')).toBeDisabled();
    expect(within(form).getByLabelText('Namespace')).toHaveValue('');
    await user.click(within(form).getByRole('button', { name: 'Add' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionRuleAdd', {
      input: expect.objectContaining({ namespace: '[cluster]' }),
    });

    await user.click(within(form).getByLabelText('Cluster-scoped'));
    expect(within(form).getByLabelText('Namespace')).toBeEnabled();
    expect(within(form).getByLabelText('Namespace')).toHaveValue('');
  });

  // A Secret read rule names a context and a namespace and nothing else, so
  // the form draws those alone and sends the rest empty, whatever was typed.
  it('adds a Secret read rule by its context and namespace alone', async () => {
    const user = userEvent.setup();
    render(<PermissionSettings />);
    const form = screen.getByRole('form', { name: 'Add a rule' });

    await user.type(within(form).getByLabelText('Verb'), 'delete');
    await user.click(within(form).getByLabelText('Cluster-scoped'));
    await user.selectOptions(within(form).getByLabelText('Class'), 'Secret reads');
    ['Verb', 'API group', 'Resource', 'Cluster-scoped'].forEach((label) => {
      expect(within(form).queryByLabelText(label)).toBeNull();
    });
    await user.type(within(form).getByLabelText('Context'), 'dev-*');
    await user.type(within(form).getByLabelText('Namespace'), 'team-a');
    await user.click(within(form).getByRole('button', { name: 'Add' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionRuleAdd', {
      input: {
        effect: 'Allow',
        class: 'SecretRead',
        context: 'dev-*',
        namespace: 'team-a',
        verb: '',
        group: '',
        kind: '',
      },
    });
  });

  it.each(['Allow', 'Deny', 'Ask'])('offers Secret reads to %s', async (effect) => {
    const user = userEvent.setup();
    render(<PermissionSettings />);
    const form = screen.getByRole('form', { name: 'Add a rule' });
    await user.selectOptions(within(form).getByLabelText('Effect'), effect);
    expect(within(form).getByRole('option', { name: 'Secret reads' })).toBeInTheDocument();
  });

  it('says what each mode does with Secret data', () => {
    render(<PermissionSettings />);
    [
      'Read-only: Every change to the cluster is refused. Showing Secret data asks you first.',
      'Ask: Every change to the cluster, and showing Secret data, asks you first.',
      'Auto: Changes run and Secret data is shown without asking, except what always asks.',
    ].forEach((line) => expect(screen.getByText(line)).toBeInTheDocument());
  });

  it('offers no destructive class to an Allow', async () => {
    const user = userEvent.setup();
    render(<PermissionSettings />);
    const form = screen.getByRole('form', { name: 'Add a rule' });
    const destructive = () => within(form).queryByRole('option', { name: 'Destructive cluster writes' });

    expect(destructive()).toBeNull();
    await user.selectOptions(within(form).getByLabelText('Effect'), 'Deny');
    expect(destructive()).toBeInTheDocument();
  });

  it('says why a mutation was refused', async () => {
    const user = userEvent.setup();
    mutateMock.mockResolvedValue({
      error: { graphQLErrors: [{ message: 'rules: names a class its provider does not have' }] },
    });
    render(<PermissionSettings />);

    await user.click(within(screen.getByRole('form', { name: 'Add a rule' })).getByRole('button', { name: 'Add' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('rules: names a class its provider does not have');
  });

  it("draws a held field's refused values, holds its edits, and discards behind a confirm", async () => {
    const user = userEvent.setup();
    settingsState.current = settings({ held: ['rules'] });
    refusedState.current = [
      { field: 'rules', value: '{"id":"b","class":9}', reason: 'names a class its provider does not have' },
      { field: 'modes', value: 'x', reason: 'not held, so not drawn' },
    ];
    render(<PermissionSettings />);

    const held = screen.getByRole('region', { name: 'Rules Kstack cannot read' });
    expect(held).toHaveTextContent('{"id":"b","class":9}');
    expect(held).toHaveTextContent('names a class its provider does not have');
    expect(held).toHaveTextContent(
      'Every cluster write is refused, and Secret data stays redacted, until the file is fixed.',
    );
    expect(screen.queryByText('not held, so not drawn')).toBeNull();
    expect(
      within(screen.getByRole('region', { name: 'Rules' })).getByRole('button', { name: 'Remove' }),
    ).toBeDisabled();
    expect(
      within(screen.getByRole('form', { name: 'Add a rule' })).getByRole('button', { name: 'Add' }),
    ).toBeDisabled();

    await user.click(within(held).getByRole('button', { name: 'Discard what Kstack cannot read' }));
    expect(mutateMock).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog')).toHaveTextContent('{"id":"b","class":9}');
    await user.click(screen.getByRole('button', { name: 'Discard' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionDiscardRefused', { field: 'rules' });
  });

  it('holds the context pickers while modes is held, and says why', () => {
    settingsState.current = settings({ held: ['modes'] });
    render(<PermissionSettings />);
    expect(within(row('dev-eks')).getByRole('combobox')).toBeDisabled();
    expect(screen.getByRole('region', { name: 'Modes Kstack cannot read' })).toHaveTextContent(
      'Every context is read-only',
    );
  });

  it('says why a default mode was refused, above its picker, while it is held', () => {
    refusedState.current = [{ field: 'defaultMode', value: '"readonly"', reason: 'is not read-only, ask or auto' }];
    settingsState.current = settings({ held: ['defaultMode'] });
    const { unmount } = render(<PermissionSettings />);
    expect(screen.getByText(/is not read-only, ask or auto/)).toBeInTheDocument();
    unmount();

    // Setting the default mode fixes it; the refusal at open is history.
    settingsState.current = settings();
    render(<PermissionSettings />);
    expect(screen.queryByText(/is not read-only, ask or auto/)).toBeNull();
  });

  it('selects no default mode while it is held, so picking read-only sends it', async () => {
    const user = userEvent.setup();
    settingsState.current = settings({ defaultMode: 'ReadOnly', held: ['defaultMode'] });
    render(<PermissionSettings />);

    expect(screen.queryByRole('tab', { selected: true })).toBeNull();
    await user.click(screen.getByRole('tab', { name: 'Read-only' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionDefaultModeSet', { mode: 'ReadOnly' });
  });
});
