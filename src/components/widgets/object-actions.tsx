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

// The object table's last column: a menu of the row's actions behind a ⋯ on every row. YAML
// and Events are every kind's; Logs is a kind's whose objects a log view can follow.
import { Button } from '@kubetail/ui/elements/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@kubetail/ui/elements/dropdown-menu';
import { FileCode, List, MoreHorizontal, ScrollText } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';

import type { VirtualColumn } from '@/components/widgets/virtual-table';
import type { LogsViewSourceKind } from '@/gql/graphql';
import type { ClusterCachedDataObject } from '@/lib/cluster-cached-data-objects';
import { gvrKey } from '@/lib/gvr';
import type { GVR } from '@/lib/gvr';
import type { ObjectTab } from '@/lib/object-view';

// Each action opens the object panel on its tab.
export type ObjectAction = ObjectTab;

// The kinds a log view reads, by gvrKey: matched on API group and plural, so a CRD reusing
// a built-in's kind name is not one.
const LOGS_KINDS = new Map<string, LogsViewSourceKind>([
  ['v1/pods', 'Pod'],
  ['apps/v1/deployments', 'Deployment'],
  ['apps/v1/statefulsets', 'StatefulSet'],
  ['apps/v1/daemonsets', 'DaemonSet'],
  ['apps/v1/replicasets', 'ReplicaSet'],
  ['batch/v1/jobs', 'Job'],
  ['batch/v1/cronjobs', 'CronJob'],
]);

export function logsSourceKind(gvr: GVR): LogsViewSourceKind | undefined {
  return LOGS_KINDS.get(gvrKey(gvr));
}

const ACTION: Record<ObjectAction, { label: string; icon: LucideIcon }> = {
  yaml: { label: 'View YAML', icon: FileCode },
  events: { label: 'View events', icon: List },
  logs: { label: 'View logs', icon: ScrollText },
};

export function actionsForKind(gvr: GVR): ObjectAction[] {
  return logsSourceKind(gvr) ? ['yaml', 'events', 'logs'] : ['yaml', 'events'];
}

export function actionsColumn(
  gvr: GVR,
  onAction: (action: ObjectAction, object: ClusterCachedDataObject) => void,
): VirtualColumn<ClusterCachedDataObject> {
  const actions = actionsForKind(gvr);
  return {
    key: 'actions',
    // The trigger names itself; a header would only crowd the narrow column.
    header: '',
    className: 'text-right',
    // The `icon-sm` trigger (28px) and the last cell's padding (6px left, 12px right).
    width: 46,
    cell: (o) => (
      <DropdownMenu>
        <DropdownMenuTrigger
          render={<Button type="button" variant="ghost" size="icon-sm" />}
          aria-label={`Actions for ${o.name}`}
        >
          <MoreHorizontal className="size-4" aria-hidden />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" sideOffset={4}>
          {actions.map((action) => {
            const { label, icon: Icon } = ACTION[action];
            return (
              <DropdownMenuItem key={action} onClick={() => onAction(action, o)}>
                <Icon className="size-4" aria-hidden />
                {label}
              </DropdownMenuItem>
            );
          })}
        </DropdownMenuContent>
      </DropdownMenu>
    ),
  };
}
