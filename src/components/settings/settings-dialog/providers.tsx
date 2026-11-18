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

// The Settings dialog's Providers tab: the providers chats can run on, then the
// ones that could be set up.
import { Button } from '@kubetail/ui/elements/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@kubetail/ui/elements/dropdown-menu';
import { MoreHorizontal, RotateCw } from 'lucide-react';
import { type ReactNode, useState } from 'react';

import { VisibleText } from '@/components/primitives/visible-text';
import { type Catalog, providersOf, useModels } from '@/lib/models';

import { Group } from './group';
import { type CredentialSource, ProviderSetupForm } from './provider-setup-form';

// Placeholder: awaits a query listing every provider that can be set up, and a
// Bedrock provider in the sidecar's catalog.
const AVAILABLE: { id: string; label: string; detail: string; sources: CredentialSource[] }[] = [
  {
    id: 'bedrock',
    label: 'Amazon Bedrock',
    detail: 'Can use the AWS connector',
    sources: ['aws-connector', 'access-key'],
  },
  {
    id: 'gemini',
    label: 'Google Gemini',
    detail: 'Connect Google, or add an API key',
    sources: ['google-connector', 'api-key'],
  },
];

export function ProvidersSettings() {
  const catalog = useModels();
  // The one row under Available whose setup form is open.
  const [open, setOpen] = useState<string | null>(null);
  const served = new Set(catalog.models.map((m) => m.provider.id));
  return (
    <>
      <Group
        title="Set up"
        lede="The models chats can run on. Each takes its credentials from a connector, an API key or your environment."
      >
        <SetUpList catalog={catalog} />
      </Group>
      <Group title="Available" lede="Set up a provider with a connector or an API key.">
        <ul className="divide-y rounded-lg border">
          {AVAILABLE.filter((p) => !served.has(p.id)).map(({ id, label, detail, sources }) => (
            <Row
              key={id}
              name={label}
              detail={detail}
              action={
                id !== open && (
                  <Button variant="outline" size="sm" onClick={() => setOpen(id)}>
                    Set up
                  </Button>
                )
              }
            >
              {id === open && <ProviderSetupForm sources={sources} onCancel={() => setOpen(null)} />}
            </Row>
          ))}
        </ul>
      </Group>
    </>
  );
}

function SetUpList({ catalog: { models, loaded, failed, retry } }: { catalog: Catalog }) {
  if (failed) {
    return (
      <div>
        <Button type="button" variant="outline" size="sm" onClick={retry}>
          <RotateCw className="size-3.5" aria-hidden />
          Retry
        </Button>
      </div>
    );
  }
  // Nothing before the query answers: a catalog in flight is not an empty one.
  if (!loaded) return null;
  if (models.length === 0) return <p className="text-sm text-muted-foreground">No provider is set up.</p>;
  return (
    <ul className="divide-y rounded-lg border">
      {providersOf(models).map(({ id, label }) => (
        // Placeholder detail: awaits a Provider field saying how its credentials resolve.
        <Row key={id} name={label} detail="From your environment" action={<RowMenu label={label} />} />
      ))}
    </ul>
  );
}

// A provider's row: its name over one detail line, its action on the right,
// and its children under the name.
function Row({
  name,
  detail,
  action,
  children,
}: {
  name: string;
  detail: string;
  action: ReactNode;
  children?: ReactNode;
}) {
  return (
    <li className="flex items-center gap-3 px-4 py-2.5">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span data-slot="name" className="truncate text-sm text-popover-foreground">
          <VisibleText text={name} />
        </span>
        <span className="truncate text-xs text-muted-foreground">{detail}</span>
        {children && <div className="pt-3">{children}</div>}
      </div>
      {action}
    </li>
  );
}

// Placeholder: both items await their mutations; Disable then gets a confirm Dialog.
function RowMenu({ label }: { label: string }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="icon" className="size-7 rounded-md" />}
        aria-label={`${label} actions`}
      >
        <MoreHorizontal className="size-4" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" side="bottom">
        <DropdownMenuItem disabled>Change credentials</DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" disabled>
          Disable
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
