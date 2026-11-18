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

// The Settings dialog's Connectors tab: the services Kstack signs in to, then
// the ones it can.
import { Button } from '@kubetail/ui/elements/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@kubetail/ui/elements/dropdown-menu';
import { Ellipsis, ExternalLink } from 'lucide-react';

import { VisibleText } from '@/components/primitives/visible-text';
import { type Connector, useConnectors } from '@/lib/connectors';

import { Group } from './group';

export function ConnectorsSettings() {
  const connectors = useConnectors();
  return (
    <>
      <ConnectorGroup
        title="Connected"
        lede="Services Kstack signs in to. Providers and tools use their credentials."
        connectors={connectors.filter((c) => c.connected)}
      />
      <ConnectorGroup
        title="Available"
        lede="Connect a service to use its credentials in Kstack."
        connectors={connectors.filter((c) => !c.connected)}
      />
    </>
  );
}

function ConnectorGroup({ title, lede, connectors }: { title: string; lede: string; connectors: Connector[] }) {
  if (connectors.length === 0) return null;
  return (
    <Group title={title} lede={lede}>
      <ul className="divide-y rounded-lg border">
        {connectors.map((c) => (
          <ConnectorRow key={c.id} connector={c} />
        ))}
      </ul>
    </Group>
  );
}

function ConnectorRow({ connector }: { connector: Connector }) {
  return (
    <li className="flex items-center gap-3 px-4 py-2.5">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-sm text-popover-foreground">
          <VisibleText text={connector.name} />
        </span>
        <span className="truncate text-xs text-muted-foreground">
          <VisibleText text={connector.detail} />
        </span>
      </div>
      {/* Every action is disabled: no connector backend yet. */}
      {connector.connected ? (
        <RowMenu connector={connector} />
      ) : (
        <Button variant="outline" size="sm" disabled>
          Connect
        </Button>
      )}
    </li>
  );
}

function RowMenu({ connector }: { connector: Connector }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="icon" className="size-7 rounded-md" />}
        aria-label={`Actions for ${connector.name}`}
      >
        <Ellipsis className="size-4" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {connector.manageLabel && (
          <DropdownMenuItem disabled>
            {connector.manageLabel}
            <ExternalLink className="ml-auto" />
          </DropdownMenuItem>
        )}
        <DropdownMenuItem disabled>Reconnect</DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" disabled>
          Disconnect
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
