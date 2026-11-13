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

// The dashboard's resource nav (sidebar, dashboard mode only): Overview and Events, then
// the Cluster and Namespace sections of grouped kinds.
// See docs/adr/2026-11-12-the-dashboard-nav-groups-kinds-by-scope-and-api-group.md
import { Separator } from '@kubetail/ui/elements/separator';
import { Spinner } from '@kubetail/ui/elements/spinner';
import { useSearch } from '@tanstack/react-router';
import { Compass, List } from 'lucide-react';
import { Fragment, useEffect, useState } from 'react';

import { SearchBox } from '@/components/primitives/text-entry';
import { useDashboardNav } from '@/lib/dashboard-nav';
import { filterNav, groupCount, PINNED, resolveDashboardResource } from '@/lib/dashboard-resources';
import type { DashboardResource, NavGroup as Group, NavSection } from '@/lib/dashboard-resources';
import { usePersistedFlag } from '@/lib/persisted-flag';

import { NamespacePicker } from './namespace-picker';
import { NavGroup } from './nav-group';
import { NavItem } from './nav-item';
import { SectionHeading } from './section-heading';

// A group's open state, kept across mounts; `initial` until the user toggles it. Moving to
// a kind inside the group opens it; closing it again sticks until the next move.
function useGroupOpen(name: string, initial: boolean, active: DashboardResource, holdsActive: boolean) {
  const [open, setOpen] = usePersistedFlag(`dashboard-nav-open:${name}`, initial);
  useEffect(() => {
    if (holdsActive) setOpen(true);
  }, [active, holdsActive, setOpen]);
  return [open, () => setOpen(!open)] as const;
}

type GroupBlockProps = {
  group: Group;
  sectionId: string;
  // A section's first group starts open, the rest closed.
  first: boolean;
  active: DashboardResource;
  // A search holds every group open, whatever is stored.
  searching: boolean;
};

function GroupBlock({ group, sectionId, first, active, searching }: GroupBlockProps) {
  const holdsActive = group.kinds.some((k) => k.id === active);
  const [stored, toggle] = useGroupOpen(`${sectionId}/${group.id}`, first, active, holdsActive);
  const open = searching || stored;
  return (
    <>
      <NavGroup
        label={group.label}
        open={open}
        count={groupCount(group)}
        active={holdsActive && !open}
        onToggle={toggle}
      />
      {open &&
        group.kinds.map((k) => (
          <NavItem key={k.id} id={k.id} label={k.label} level="kind" count={k.count} active={k.id === active} />
        ))}
    </>
  );
}

// A section with no match says so while searching.
function sectionLabel(section: NavSection, searching: boolean): string {
  return searching && section.groups.length === 0 ? `${section.label} · no matches` : section.label;
}

const PINNED_ICON = { overview: Compass, events: List };

export function DashboardResourceNav() {
  const { nav, active: watchActive, phase } = useDashboardNav();
  const { resource } = useSearch({ strict: false });
  const active = resolveDashboardResource(resource);
  const [query, setQuery] = useState('');
  const view = filterNav(nav, { active, query });
  const searching = query.trim() !== '';

  // Only a live watch can be loading or reconnecting; a paused one (no synced cache)
  // leaves the curated kinds standing, which is not a fault.
  let status: string | null = null;
  if (watchActive && phase === 'reconnecting') status = 'Reconnecting…';
  else if (watchActive && phase === 'connecting') status = 'Loading resources…';

  return (
    <nav aria-label="Resources" className="flex flex-col gap-0.5">
      {/* The scope first: it decides what the rows below count and show. */}
      <div className="pb-2">
        <NamespacePicker />
      </div>
      {PINNED.map((p) => (
        <NavItem
          key={p.id}
          id={p.id}
          label={p.label}
          level="top"
          icon={PINNED_ICON[p.id]}
          count={p.id === 'events' ? nav.eventsCount : undefined}
          active={active === p.id}
        />
      ))}
      {/* The pinned rows are pages; a rule sets them apart from the kinds below. */}
      <Separator className="my-2 bg-sidebar-border" />
      {/* Above the kinds it filters, and sticky, so they scroll under it. */}
      <div className="sticky top-0 z-10 bg-sidebar pb-1">
        <SearchBox
          aria-label="Search kinds"
          placeholder="Search"
          containerClassName="max-w-none"
          // Opaque on the sidebar's tint, as the frame draws it; dark mode keeps the library's.
          className="bg-background"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Escape') setQuery('');
          }}
        />
        {searching && (
          <div className="flex items-center justify-between px-1 pt-1.5">
            <span className="text-xs text-primary">
              {view.matches} of {view.total} kinds
            </span>
            <button
              type="button"
              className="text-xs font-medium text-primary hover:underline"
              onClick={() => setQuery('')}
            >
              Clear
            </button>
          </div>
        )}
      </div>
      {view.sections.map((s) => (
        <Fragment key={s.id}>
          <SectionHeading label={sectionLabel(s, searching)} />
          {s.groups.map((g, i) => (
            <GroupBlock key={g.id} group={g} sectionId={s.id} first={i === 0} active={active} searching={searching} />
          ))}
        </Fragment>
      ))}
      {/* Transport hint below the tree so it never shifts the resource rows. */}
      {status !== null && (
        <div className="flex items-center gap-1.5 px-3 py-1.5 text-xs text-muted-foreground" data-testid="nav-status">
          <Spinner size="xs" className="mr-0" />
          {status}
        </div>
      )}
    </nav>
  );
}
