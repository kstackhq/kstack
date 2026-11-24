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
// applies. A mode and a rule belong to a cluster, joined onto the clusters
// watch by its id. Every cluster name, context and rule line is user or
// cluster text, drawn through VisibleText.
import { Button } from '@kubetail/ui/elements/button';
import { Tabs, TabsList, TabsTrigger } from '@kubetail/ui/elements/tabs';
import { useId } from 'react';

import { VisibleText } from '@/components/primitives/visible-text';
import type { PermissionMode, PermissionModeSource } from '@/gql/graphql';
import { displayName, useClusters } from '@/lib/clusters';
import type { Cluster } from '@/lib/clusters';
import { usePermissionSettings } from '@/lib/permission-settings';
import type { ClusterMode, PermissionSettings as Permissions } from '@/lib/permission-settings';
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

// Where a cluster's mode comes from.
const SOURCES: Record<PermissionModeSource, string> = {
  Entry: 'Set for this cluster',
  Default: 'Default mode',
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
  const { clusters } = useClusters();
  const { settings, error, readError, retry } = permissions;
  if (!settings) {
    if (!readError) return null;
    return (
      <div role="alert" className="flex items-center gap-3 text-sm text-destructive">
        <span>
          Kstack could not read the permissions: <VisibleText text={readError} />
        </span>
        <Button type="button" variant="outline" size="sm" onClick={retry}>
          Try again
        </Button>
      </div>
    );
  }
  // A mode waits for the clusters watch to deliver its cluster; the default
  // mode and the rules do not.
  const byID = new Map(clusters?.map((c) => [c.id, c]));
  const rows = settings.clusters.flatMap((mode) => {
    const cluster = byID.get(mode.clusterID);
    return cluster ? [{ cluster, mode }] : [];
  });
  // Two clusters can name one context, from two kubeconfig files.
  const contexts = [...new Set(rows.flatMap(({ cluster }) => cluster.spec.source.kubeconfig?.context ?? []))];
  const ruleClusters = rows.map(({ cluster }) => ({ value: cluster.id, label: clusterLabel(cluster) }));

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

      <ClusterModes rows={rows} onSet={permissions.setMode} onClear={permissions.clearMode} />

      <ShowingFilter contexts={contexts} />

      <PermissionRules
        rules={settings.kubeRules}
        clusters={ruleClusters}
        contexts={contexts}
        onAdd={permissions.addRule}
        onRemove={permissions.removeRule}
      />

      <ChatGrants />
      <RanWithoutAsking />
    </>
  );
}

// The default mode and its picker.
function DefaultModePicker({ permissions }: { permissions: Permissions }) {
  const { settings, setDefaultMode } = permissions;
  if (!settings) return null;
  const current = MODES.find((m) => m.value === settings.defaultMode);
  return (
    <Group title="Default mode" info={<PermissionModeInfo destructive={settings.destructive} />}>
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

// distinctContext is a cluster's kube-context where it differs from its name,
// else ''.
function distinctContext(cluster: Cluster): string {
  const context = cluster.spec.source.kubeconfig?.context ?? '';
  return context === displayName(cluster) ? '' : context;
}

// clusterLabel is a cluster's name, and its context where that differs, since
// two clusters can share a name.
function clusterLabel(cluster: Cluster): string {
  const context = distinctContext(cluster);
  return context ? `${displayName(cluster)} (${context})` : displayName(cluster);
}

// One row per cluster with a kube-context: its name, its mode and where the
// mode comes from, and a picker that sets the cluster's own. Clear returns it
// to the default.
function ClusterModes({
  rows,
  onSet,
  onClear,
}: {
  rows: { cluster: Cluster; mode: ClusterMode }[];
  onSet: (clusterID: string, mode: PermissionMode) => void;
  onClear: (clusterID: string) => void;
}) {
  return (
    <Group title="Clusters" lede="A cluster follows the default mode unless you set one for it.">
      <ul className="divide-y rounded-lg border text-sm">
        {rows.map(({ cluster, mode }) => {
          const context = distinctContext(cluster);
          const label = clusterLabel(cluster);
          return (
            <li key={cluster.id} aria-label={label} className="flex items-center gap-3 px-4 py-3">
              <div className="flex min-w-0 flex-1 flex-col gap-0.5 break-all">
                <span className="font-mono">
                  <VisibleText text={displayName(cluster)} />
                </span>
                <span className="text-xs text-muted-foreground">
                  {context && (
                    <>
                      <span className="font-mono">
                        <VisibleText text={context} />
                      </span>
                      {' · '}
                    </>
                  )}
                  {SOURCES[mode.source]}
                </span>
              </div>
              {mode.source === 'Entry' && (
                <Button type="button" variant="ghost" size="sm" onClick={() => onClear(cluster.id)}>
                  Clear
                </Button>
              )}
              <Select
                className="w-36"
                aria-label={`Mode of ${label}`}
                options={MODES}
                value={mode.mode}
                onChange={(value) => onSet(cluster.id, value)}
              />
            </li>
          );
        })}
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
