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
        data: JSON.stringify(args.query).includes('settingsRefused')
          ? { settingsRefused: refusedState.current }
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

const { PermissionSettings } = await import('./permissions');

const allow = {
  id: 'r1',
  line: 'Allow cluster writes in dev-eks / team-a',
  effect: 'Allow',
  class: 'UpstreamWrite',
  context: 'dev-eks',
  namespace: 'team-a',
  verb: '',
  group: '',
  kind: '',
};

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

const row = (context: string) => screen.getByRole('listitem', { name: context });
const rules = () => screen.getByRole('group', { name: 'Rules' });
const actions = () => within(rules()).getByRole('button', { name: `Actions for ${allow.line}` });

beforeEach(() => {
  vi.clearAllMocks();
  queryArgs.length = 0;
  sandboxState.current = { available: true };
  settingsState.current = settings();
  refusedState.current = [];
  mutateMock.mockResolvedValue({ data: {} });
});

describe('PermissionSettings', () => {
  it('says why nothing applies on a machine with no sandbox', () => {
    sandboxState.current = { available: false };
    render(<PermissionSettings />);
    expect(
      screen.getByText(
        'This machine has no sandbox, so every command asks before it runs. Modes and rules apply only to sandboxed commands.',
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole('group')).toBeNull();
  });

  it('draws nothing until the sandbox query answers', () => {
    sandboxState.current = { available: undefined };
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
    const card = screen.getByRole('group', { name: 'Default mode' });
    expect(card).toHaveTextContent('Every change to the cluster, and showing Secret data, asks you first.');
    expect(card).not.toHaveTextContent('Every change to the cluster is refused.');
    await user.click(screen.getByRole('tab', { name: 'Auto' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionDefaultModeSet', { mode: 'Auto' });
  });

  it("draws each context's mode and where it comes from", () => {
    render(<PermissionSettings />);
    expect(screen.getByRole('group', { name: 'Contexts' })).toHaveTextContent(
      'A context follows the default mode unless a pattern or its own entry sets one.',
    );
    expect(row('dev-eks')).toHaveTextContent('Default mode');
    expect(row('prod-eu')).toHaveTextContent('read-only until the file is fixed');
    expect(row('qa')).toHaveTextContent('Set for this context');
    expect(row('staging')).toHaveTextContent('From pattern stag*');
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

  it('spells the context and its pattern out', () => {
    settingsState.current = settings({
      contexts: [{ context: 'prod\u202Eue', mode: 'Ask', source: 'Entry', pattern: 'prod\u202E*', own: false }],
    });
    render(<PermissionSettings />);

    const marks = row('prod\u202Eue').querySelectorAll('mark');
    expect(marks).toHaveLength(2);
    marks.forEach((mark) => expect(mark).toHaveTextContent('\\u{202E}'));
  });

  // Placeholder: no query yet says what applies to one context.
  it('draws the Showing filter held on All clusters, listing each context', () => {
    render(<PermissionSettings />);
    const showing = screen.getByRole('combobox', { name: 'Showing' });
    expect(showing).toBeDisabled();
    expect(showing).toHaveValue('');
    expect(
      within(showing)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['All clusters', 'dev-eks', 'prod-eu', 'qa', 'staging']);
  });

  it('lists what always asks behind the Default mode info', async () => {
    const user = userEvent.setup();
    render(<PermissionSettings />);
    await user.click(screen.getByRole('button', { name: 'What modes and rules cannot change' }));
    const popup = await screen.findByRole('dialog');
    expect(popup).toHaveTextContent('Deleting a namespace');
    expect(popup).toHaveTextContent('Any change to roles');
  });

  it('draws the chat grants and what ran without asking as coming soon', () => {
    render(<PermissionSettings />);
    expect(screen.getByRole('group', { name: 'Chat grants' })).toHaveTextContent('Coming soon');
    expect(screen.getByRole('group', { name: 'Ran without asking' })).toHaveTextContent('Coming soon');
  });

  it('draws the rules, and removes one from its menu', async () => {
    const user = userEvent.setup();
    render(<PermissionSettings />);

    expect(rules()).toHaveTextContent('Allow cluster writes in dev-eks / team-a');
    await user.click(actions());
    await user.click(await screen.findByRole('menuitem', { name: 'Remove' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionRuleRemove', { id: 'r1' });
  });

  it('adds a rule', async () => {
    const user = userEvent.setup();
    render(<PermissionSettings />);

    await user.click(screen.getByRole('button', { name: 'Add rule' }));
    await user.type(screen.getByLabelText('Resource'), 'deployments');
    await user.click(screen.getByRole('button', { name: 'Add' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionRuleAdd', {
      input: expect.objectContaining({ effect: 'Allow', class: 'UpstreamWrite', kind: 'deployments' }),
    });
  });

  // urql's cache gives each rule a __typename, which the rule input refuses.
  it('edits a rule by adding the edit, then removing the original', async () => {
    const user = userEvent.setup();
    settingsState.current = settings({ rules: [{ __typename: 'PermissionRule', ...allow }] });
    render(<PermissionSettings />);

    await user.click(actions());
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }));
    await user.type(screen.getByLabelText('Verb'), 'patch');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(mutateMock.mock.calls).toEqual([
      [
        'permissionRuleAdd',
        {
          input: {
            effect: 'Allow',
            class: 'UpstreamWrite',
            context: 'dev-eks',
            namespace: 'team-a',
            verb: 'patch',
            group: '',
            kind: '',
          },
        },
      ],
      ['permissionRuleRemove', { id: 'r1' }],
    ]);
  });

  it('removes nothing when an edit is refused, and says why', async () => {
    const user = userEvent.setup();
    mutateMock.mockResolvedValue({ error: { graphQLErrors: [{ message: 'rules: a destructive rule cannot allow' }] } });
    render(<PermissionSettings />);

    await user.click(actions());
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }));
    await user.type(screen.getByLabelText('Verb'), 'patch');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('rules: a destructive rule cannot allow');
    expect(mutateMock).not.toHaveBeenCalledWith('permissionRuleRemove', expect.anything());
  });

  it('says why a mutation was refused', async () => {
    const user = userEvent.setup();
    mutateMock.mockResolvedValue({
      error: { graphQLErrors: [{ message: 'rules: names a class its provider does not have' }] },
    });
    render(<PermissionSettings />);

    await user.click(screen.getByRole('button', { name: 'Add rule' }));
    await user.click(screen.getByRole('button', { name: 'Add' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('rules: names a class its provider does not have');
  });

  it("draws a held field's refused values, holds its edits, and discards behind a confirm", async () => {
    const user = userEvent.setup();
    settingsState.current = settings({ held: ['permissions.rules'] });
    refusedState.current = [
      { field: 'permissions.rules', value: '{"id":"b","class":9}', reason: 'names a class its provider does not have' },
      { field: 'permissions.modes', value: 'x', reason: 'not held, so not drawn' },
    ];
    render(<PermissionSettings />);

    const held = screen.getByRole('region', { name: 'Rules Kstack cannot read' });
    expect(held).toHaveTextContent('{"id":"b","class":9}');
    expect(held).toHaveTextContent('names a class its provider does not have');
    expect(held).toHaveTextContent(
      'Every cluster write is refused, and Secret data stays redacted, until the file is fixed.',
    );
    expect(screen.queryByText('not held, so not drawn')).toBeNull();
    expect(actions()).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Add rule' })).toBeDisabled();

    await user.click(within(held).getByRole('button', { name: 'Discard what Kstack cannot read' }));
    expect(mutateMock).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog')).toHaveTextContent('{"id":"b","class":9}');
    await user.click(screen.getByRole('button', { name: 'Discard' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionDiscardRefused', { field: 'permissions.rules' });
  });

  it('discards held modes behind a confirm', async () => {
    const user = userEvent.setup();
    settingsState.current = settings({ held: ['permissions.modes'] });
    refusedState.current = [{ field: 'permissions.modes', value: 'x', reason: 'names a mode there is not' }];
    render(<PermissionSettings />);

    const held = screen.getByRole('region', { name: 'Modes Kstack cannot read' });
    await user.click(within(held).getByRole('button', { name: 'Discard what Kstack cannot read' }));
    await user.click(screen.getByRole('button', { name: 'Discard' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionDiscardRefused', { field: 'permissions.modes' });
  });

  it('discards nothing when the confirm is cancelled or dismissed', async () => {
    const user = userEvent.setup();
    settingsState.current = settings({ held: ['permissions.modes'] });
    refusedState.current = [{ field: 'permissions.modes', value: 'x', reason: 'names a mode there is not' }];
    render(<PermissionSettings />);

    const held = screen.getByRole('region', { name: 'Modes Kstack cannot read' });
    const discard = within(held).getByRole('button', { name: 'Discard what Kstack cannot read' });
    await user.click(discard);
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByText('Discard what Kstack cannot read?')).not.toBeInTheDocument();

    await user.click(discard);
    await user.keyboard('{Escape}');
    expect(screen.queryByText('Discard what Kstack cannot read?')).not.toBeInTheDocument();
    expect(mutateMock).not.toHaveBeenCalledWith('permissionDiscardRefused', expect.anything());
  });

  it('holds the context pickers while modes is held, and says why', () => {
    settingsState.current = settings({ held: ['permissions.modes'] });
    render(<PermissionSettings />);
    expect(within(row('dev-eks')).getByRole('combobox')).toBeDisabled();
    expect(screen.getByRole('region', { name: 'Modes Kstack cannot read' })).toHaveTextContent(
      'Every context is read-only',
    );
  });

  it('says why a default mode was refused, above its picker, while it is held', () => {
    refusedState.current = [
      { field: 'permissions.defaultMode', value: '"readonly"', reason: 'is not read-only, ask or auto' },
    ];
    settingsState.current = settings({ held: ['permissions.defaultMode'] });
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
    settingsState.current = settings({ defaultMode: 'ReadOnly', held: ['permissions.defaultMode'] });
    render(<PermissionSettings />);

    expect(screen.queryByRole('tab', { selected: true })).toBeNull();
    await user.click(screen.getByRole('tab', { name: 'Read-only' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionDefaultModeSet', { mode: 'ReadOnly' });
  });
});
