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

// The Settings dialog's Connectors tab: every service Kstack can sign in to,
// in one alphabetical list, each connected or not.
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

import { Intro } from './group';

export function ConnectorsSettings() {
  const connectors = useConnectors().toSorted((a, b) => a.name.localeCompare(b.name));
  return (
    <Intro lede="Accounts Kstack signs in to. Model providers and tools use their credentials.">
      <ul className="divide-y rounded-lg border">
        {connectors.map((c) => (
          <ConnectorRow key={c.id} connector={c} />
        ))}
      </ul>
    </Intro>
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
          Coming soon
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
