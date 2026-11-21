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

// The app bar's title: what the page shows, after the cluster chip. In chat mode the
// open chat's title, and nothing at all on `/chat`; on the dashboard, breadcrumbs from
// the nav's own tree: the section's scope, the group, the kind. Every string is drawn
// through `VisibleText`: a chat's title is model-written and a kind's label is the
// cluster's.
import { useParams, useSearch } from '@tanstack/react-router';
import { ChevronRight, Globe, Layers } from 'lucide-react';
import type { ReactNode } from 'react';

import { VisibleText } from '@/components/primitives/visible-text';
import type { AppMode } from '@/lib/app-mode';
import { useChats } from '@/lib/chats';
import { useClusterCachedDataKinds } from '@/lib/cluster-cached-data-kinds';
import { useDashboardNav } from '@/lib/dashboard-nav';
import { dashboardResourceLabel, resolveDashboardResource } from '@/lib/dashboard-resources';
import type { DashboardNav, DashboardResource, NavSection } from '@/lib/dashboard-resources';

import { AppBarDivider } from './button';

const SCOPE_ICON: Record<NavSection['id'], typeof Globe> = { cluster: Globe, namespace: Layers };

// The divider, then the text: absent together.
function Slot({ children }: { children: ReactNode }) {
  return (
    <div data-testid="app-bar-title" className="flex min-w-0 items-center gap-2 pl-1.5">
      <AppBarDivider />
      {children}
    </div>
  );
}

function ChatTitle({ chatID }: { chatID: string }) {
  const { chats } = useChats();
  const chat = chats.find((c) => c.id === chatID);
  if (!chat) return null;
  return (
    <Slot>
      <span className="min-w-0 truncate text-xs/4 text-foreground">
        <VisibleText text={chat.title} />
      </span>
    </Slot>
  );
}

// Where the nav places a kind: its section and group. A pinned row, or a kind the tree
// does not hold, has neither.
function locate(nav: DashboardNav, id: DashboardResource) {
  for (const section of nav.sections) {
    for (const group of section.groups) {
      const kind = group.kinds.find((k) => k.id === id);
      if (kind) return { section, group, kind };
    }
  }
  return undefined;
}

function ScopeIcon({ section }: { section: NavSection['id'] }) {
  const Icon = SCOPE_ICON[section];
  return (
    <Icon
      aria-hidden
      data-testid="breadcrumb-scope"
      data-scope={section}
      className="size-4 shrink-0 text-muted-foreground"
    />
  );
}

function Breadcrumbs() {
  const { resource } = useSearch({ strict: false });
  const { nav } = useDashboardNav();
  const { kinds } = useClusterCachedDataKinds();
  const id = resolveDashboardResource(resource);
  const place = locate(nav, id);
  const label = place?.kind.label ?? dashboardResourceLabel(kinds, id);

  return (
    <Slot>
      <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-1.5 pl-1 text-sm font-medium">
        {place && (
          <>
            <ScopeIcon section={place.section.id} />
            <span className="shrink-0 text-muted-foreground">
              <VisibleText text={place.group.label} />
            </span>
            <ChevronRight aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
          </>
        )}
        <span aria-current="page" className="min-w-0 truncate text-foreground">
          <VisibleText text={label} />
        </span>
      </nav>
    </Slot>
  );
}

export function AppBarTitle({ mode }: { mode: AppMode }) {
  const { chatId } = useParams({ strict: false });
  if (mode === 'dashboard') return <Breadcrumbs />;
  return chatId ? <ChatTitle chatID={chatId} /> : null;
}
