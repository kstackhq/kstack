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

// Three seams: the sandbox query, the clusters watch, and urql, whose query
// answers from state and whose mutations are one mock, told apart by the
// operation they send.
const { sandboxState, clustersState, settingsState, queryErrorState, mutateMock, queryArgs, reexecuteMock } =
  vi.hoisted(() => {
    const sandbox: { current: { available: boolean | undefined } } = { current: { available: true } };
    const queryError: { current: unknown } = { current: undefined };
    const clusters: { current: unknown[] | null } = { current: null };
    const settingsValue: { current: unknown } = { current: undefined };
    const args: unknown[] = [];
    return {
      sandboxState: sandbox,
      clustersState: clusters,
      settingsState: settingsValue,
      queryErrorState: queryError,
      mutateMock: vi.fn(),
      queryArgs: args,
      reexecuteMock: vi.fn(),
    };
  });
vi.mock('@/lib/sandbox', () => ({ useSandbox: () => ({ ...sandboxState.current, failed: false, retry: () => {} }) }));
vi.mock('@/lib/clusters', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/clusters')>()),
  useClusters: () => ({ clusters: clustersState.current, connected: true }),
}));
vi.mock('urql', () => ({
  useQuery: (args: { query: unknown }) => {
    queryArgs.push(args);
    const data = settingsState.current ? { permissionSettings: settingsState.current } : undefined;
    return [{ data, error: queryErrorState.current }, reexecuteMock];
  },
  useMutation: (doc: unknown) => {
    const name = /(?:permission|kubeRule)[A-Za-z]*/.exec(JSON.stringify(doc))![0];
    return [{}, (vars: unknown) => mutateMock(name, vars)];
  },
}));

const { PermissionSettings } = await import('./permissions');

const allow = {
  id: 'r1',
  line: 'Allow cluster writes in dev-eks / team-a',
  effect: 'Allow',
  class: 'UpstreamWrite',
  clusterID: 'c1',
  namespace: 'team-a',
  verb: '',
  group: '',
  kind: '',
};

function settings(over: Record<string, unknown> = {}) {
  return {
    defaultMode: 'Ask',
    clusters: [
      { clusterID: 'c1', mode: 'Ask', source: 'Default' },
      { clusterID: 'c2', mode: 'ReadOnly', source: 'Entry' },
    ],
    kubeRules: [allow],
    destructive: ['Deleting a namespace', 'Any change to roles'],
    ...over,
  };
}

// A record of the clusters watch, named name or, with none, by its context.
const cluster = (id: string, context: string, name: string | null = null) => ({
  id,
  spec: { name, source: { kubeconfig: { context } } },
});

const row = (context: string) => screen.getByRole('listitem', { name: context });
const rules = () => screen.getByRole('group', { name: 'Rules' });
const actions = () => within(rules()).getByRole('button', { name: `Actions for ${allow.line}` });

beforeEach(() => {
  vi.clearAllMocks();
  queryArgs.length = 0;
  sandboxState.current = { available: true };
  settingsState.current = settings();
  queryErrorState.current = undefined;
  clustersState.current = [cluster('c1', 'dev-eks'), cluster('c2', 'prod-eu', 'Production')];
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

  it("draws each cluster's mode by its name off the clusters watch, and where it comes from", () => {
    render(<PermissionSettings />);
    expect(screen.getByRole('group', { name: 'Clusters' })).toHaveTextContent(
      'A cluster follows the default mode unless you set one for it.',
    );
    expect(row('dev-eks')).toHaveTextContent('Default mode');
    expect(row('Production (prod-eu)')).toHaveTextContent('Set for this cluster');
    expect(within(row('Production (prod-eu)')).getByRole('combobox')).toHaveValue('ReadOnly');
  });

  // The default mode and the rules need no cluster's name, so they draw while
  // the clusters watch reconnects.
  it('draws a mode only once the clusters watch delivers its cluster', () => {
    clustersState.current = null;
    const { unmount } = render(<PermissionSettings />);
    expect(screen.getByRole('tab', { name: 'Ask', selected: true })).toBeInTheDocument();
    expect(rules()).toHaveTextContent(allow.line);
    expect(within(screen.getByRole('group', { name: 'Clusters' })).queryAllByRole('listitem')).toHaveLength(0);
    unmount();

    clustersState.current = [cluster('c2', 'prod-eu', 'Production')];
    render(<PermissionSettings />);
    expect(within(screen.getByRole('group', { name: 'Clusters' })).getAllByRole('listitem')).toHaveLength(1);
  });

  // Display names are the user's and need not be unique.
  it('names each cluster by its context too, so two of one name are told apart', async () => {
    const user = userEvent.setup();
    clustersState.current = [cluster('c1', 'prod-a', 'Prod'), cluster('c2', 'prod-b', 'Prod')];
    render(<PermissionSettings />);
    expect(row('Prod (prod-a)')).toHaveTextContent('prod-a · Default mode');
    await user.selectOptions(screen.getByRole('combobox', { name: 'Mode of Prod (prod-b)' }), 'Auto');
    expect(mutateMock).toHaveBeenCalledWith('permissionModeSet', { clusterID: 'c2', mode: 'Auto' });
  });

  it('says why the permissions could not be read, and asks again', async () => {
    const user = userEvent.setup();
    settingsState.current = undefined;
    queryErrorState.current = { graphQLErrors: [{ message: 'no such table: kube_rules' }] };
    render(<PermissionSettings />);
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Kstack could not read the permissions: no such table: kube_rules',
    );
    await user.click(screen.getByRole('button', { name: 'Try again' }));
    expect(reexecuteMock).toHaveBeenCalledWith({ requestPolicy: 'network-only' });
  });

  it('draws nothing while the permissions are read', () => {
    settingsState.current = undefined;
    const { container } = render(<PermissionSettings />);
    expect(container).toBeEmptyDOMElement();
  });

  // An add that took, whose answer could not be read, is taken: the edit
  // removes the rule it replaces, and the pane says what happened.
  it('removes the original when an edit took though its answer could not be read', async () => {
    const user = userEvent.setup();
    mutateMock.mockResolvedValue({
      error: {
        graphQLErrors: [
          {
            message: 'The change was saved, but Kstack could not read the settings back.',
            extensions: { code: 'KSTACK_SAVED_UNREAD' },
          },
        ],
      },
    });
    render(<PermissionSettings />);

    await user.click(actions());
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }));
    await user.type(screen.getByLabelText('Verb'), 'patch');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(mutateMock).toHaveBeenCalledWith('kubeRuleRemove', { id: 'r1' });
    expect(await screen.findByRole('alert')).toHaveTextContent('The change was saved');
  });

  it("sets and clears a cluster's mode by its id", async () => {
    const user = userEvent.setup();
    render(<PermissionSettings />);

    await user.selectOptions(within(row('dev-eks')).getByRole('combobox'), 'Auto');
    expect(mutateMock).toHaveBeenCalledWith('permissionModeSet', { clusterID: 'c1', mode: 'Auto' });
    await user.click(within(row('Production (prod-eu)')).getByRole('button', { name: 'Clear' }));
    expect(mutateMock).toHaveBeenCalledWith('permissionModeClear', { clusterID: 'c2' });
  });

  it('offers Clear for a mode of its own alone', () => {
    render(<PermissionSettings />);
    expect(within(row('dev-eks')).queryByRole('button', { name: 'Clear' })).toBeNull();
  });

  it("spells the cluster's name out", () => {
    clustersState.current = [cluster('c1', 'prod\u202Eue')];
    settingsState.current = settings({ clusters: [{ clusterID: 'c1', mode: 'Ask', source: 'Default' }] });
    render(<PermissionSettings />);

    const marks = row('prod\u202Eue').querySelectorAll('mark');
    expect(marks).toHaveLength(1);
    expect(marks[0]).toHaveTextContent('\\u{202E}');
  });

  // Placeholder: no query yet says what applies to one context.
  it('draws the Showing filter held on All clusters, listing each context', () => {
    // A record naming no context, as the watch can hold, lists none, and two
    // clusters of one context, from two kubeconfig files, list it once.
    clustersState.current = [
      ...clustersState.current!,
      { id: 'c3', spec: { name: 'Cloud', source: {} } },
      cluster('c4', 'dev-eks', 'Dev again'),
    ];
    settingsState.current = settings({
      clusters: [
        ...settings().clusters,
        { clusterID: 'c3', mode: 'Ask', source: 'Default' },
        { clusterID: 'c4', mode: 'Ask', source: 'Default' },
      ],
    });
    render(<PermissionSettings />);
    const showing = screen.getByRole('combobox', { name: 'Showing' });
    expect(showing).toBeDisabled();
    expect(showing).toHaveValue('');
    expect(
      within(showing)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['All clusters', 'dev-eks', 'prod-eu']);
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
    expect(mutateMock).toHaveBeenCalledWith('kubeRuleRemove', { id: 'r1' });
  });

  it('adds a rule on a cluster it names as the Clusters card does', async () => {
    const user = userEvent.setup();
    render(<PermissionSettings />);

    await user.click(screen.getByRole('button', { name: 'Add rule' }));
    await user.selectOptions(screen.getByLabelText('Cluster'), 'Production (prod-eu)');
    await user.type(screen.getByLabelText('Resource'), 'deployments');
    await user.click(screen.getByRole('button', { name: 'Add' }));
    expect(mutateMock).toHaveBeenCalledWith('kubeRuleAdd', {
      input: expect.objectContaining({ effect: 'Allow', class: 'UpstreamWrite', clusterID: 'c2', kind: 'deployments' }),
    });
  });

  // urql's cache gives each rule a __typename, which the rule input refuses.
  it('edits a rule by adding the edit, then removing the original', async () => {
    const user = userEvent.setup();
    settingsState.current = settings({ kubeRules: [{ __typename: 'KubeRule', ...allow }] });
    render(<PermissionSettings />);

    await user.click(actions());
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }));
    await user.type(screen.getByLabelText('Verb'), 'patch');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(mutateMock.mock.calls).toEqual([
      [
        'kubeRuleAdd',
        {
          input: {
            effect: 'Allow',
            class: 'UpstreamWrite',
            clusterID: 'c1',
            namespace: 'team-a',
            verb: 'patch',
            group: '',
            kind: '',
          },
        },
      ],
      ['kubeRuleRemove', { id: 'r1' }],
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
    expect(mutateMock).not.toHaveBeenCalledWith('kubeRuleRemove', expect.anything());
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
});
