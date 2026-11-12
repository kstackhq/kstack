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

// A picker's menu: a search box over a list of options under labels. The caller decides
// what a query matches; the menu owns the box, the list and picking.
import { Popover, PopoverContent, PopoverTrigger } from '@kubetail/ui/elements/popover';
import { cn } from '@kubetail/ui/lib/utils';
import { ChevronsUpDown } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { useCallback, useId, useRef, useState } from 'react';
import type { ComponentProps, KeyboardEvent, ReactElement } from 'react';

import { CODE_TEXT } from '@/components/widgets/text-entry';
import { VisibleText } from '@/components/widgets/visible-text';

export type ScopeMenuEntry =
  | { type: 'label'; key: string; label: string }
  | {
      type: 'option';
      id: string;
      label: string;
      // A muted word after the label, such as *Not found*.
      note?: string;
    };

type ScopeTriggerProps = ComponentProps<'button'> & { icon?: LucideIcon };

// The button that opens a menu, as wide as the nav: an icon, what is chosen, and the
// up-down chevron. Opaque on the sidebar's tint, like the search box.
export function ScopeTrigger({ icon: Icon, className, children, ...props }: ScopeTriggerProps) {
  return (
    <button
      type="button"
      className={cn(
        'flex h-9 w-full min-w-0 items-center gap-2 rounded-md border bg-background px-2.5 text-sm font-medium hover:bg-accent',
        className,
      )}
      {...props}
    >
      {Icon && <Icon aria-hidden className="size-4 shrink-0 text-muted-foreground" />}
      <span className="flex-1 truncate text-left">{children}</span>
      <ChevronsUpDown aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
    </button>
  );
}

type ScopeMenuProps = {
  trigger: ReactElement;
  searchLabel: string;
  placeholder: string;
  entries: (query: string) => readonly ScopeMenuEntry[];
  // The chosen option's id.
  current: string | undefined;
  onPick: (id: string) => void;
};

export function ScopeMenu({ trigger, searchLabel, placeholder, entries, current, onPick }: ScopeMenuProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  const listId = useId();
  // The option Enter picks: the one set by opening, an arrow or the pointer while it is
  // still shown, else the first shown.
  const [highlight, setHighlight] = useState<string>();
  const shown = entries(query);
  const optionIds = shown.flatMap((e) => (e.type === 'option' ? [e.id] : []));
  const active = highlight !== undefined && optionIds.includes(highlight) ? highlight : optionIds[0];
  const optionId = (id: string) => `${listId}-${id}`;

  // The list mounts on each open, so this is the scroll it opens at. Stable, or React
  // would call it again on every render.
  const scrollToCurrent = useCallback((list: HTMLElement | null) => {
    list?.querySelector('[data-current]')?.scrollIntoView({ block: 'center' });
  }, []);

  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (next) {
      setQuery('');
      setHighlight(current);
    }
  };

  const pick = (id: string) => {
    setOpen(false);
    onPick(id);
  };

  const onKeyDown = (e: KeyboardEvent) => {
    // No option is shown: nothing to move to or pick.
    if (active === undefined) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const i = optionIds.indexOf(active) + (e.key === 'ArrowDown' ? 1 : -1);
      const next = optionIds[Math.max(0, Math.min(i, optionIds.length - 1))];
      setHighlight(next);
      document.getElementById(optionId(next))?.scrollIntoView({ block: 'nearest' });
    } else if (e.key === 'Enter') {
      e.preventDefault();
      pick(active);
    }
  };

  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger render={trigger} />
      {/* Capped to the room the window leaves: the list shrinks and scrolls, and the search
      box and footer stay on screen. */}
      <PopoverContent
        align="start"
        initialFocus={inputRef}
        className="flex w-75 flex-col p-0"
        style={{ maxHeight: 'var(--available-height)' }}
      >
        <div className="shrink-0 p-2">
          <input
            {...CODE_TEXT}
            ref={inputRef}
            role="combobox"
            aria-label={searchLabel}
            aria-expanded
            aria-controls={listId}
            placeholder={placeholder}
            value={query}
            aria-activedescendant={active === undefined ? undefined : optionId(active)}
            onChange={(e) => {
              setQuery(e.target.value);
              setHighlight(undefined);
            }}
            onKeyDown={onKeyDown}
            className="h-9 w-full rounded-md border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
          />
        </div>
        <div
          ref={scrollToCurrent}
          id={listId}
          role="listbox"
          aria-label={searchLabel}
          className="max-h-118 min-h-0 overflow-y-auto border-t p-1.5"
        >
          {shown.length === 0 && <p className="px-2 py-1.5 text-sm text-muted-foreground">No matches</p>}
          {shown.map((e) =>
            e.type === 'label' ? (
              <div key={e.key} role="presentation" className="px-2 pt-2 pb-1 text-xs text-muted-foreground">
                <VisibleText text={e.label} />
              </div>
            ) : (
              // oxlint-disable-next-line jsx-a11y/click-events-have-key-events -- the combobox above takes the keys for every option
              <div
                key={e.id}
                id={optionId(e.id)}
                role="option"
                aria-selected={e.id === active}
                data-current={e.id === current || undefined}
                tabIndex={-1}
                onClick={() => pick(e.id)}
                onPointerMove={() => setHighlight(e.id)}
                className={cn(
                  'flex h-8 items-center gap-2 rounded-md px-2 text-sm aria-selected:bg-muted',
                  e.id === current && 'bg-sidebar-accent font-medium aria-selected:bg-sidebar-accent',
                )}
              >
                <span className="truncate">
                  <VisibleText text={e.label} />
                </span>
                {e.note && <span className="text-xs text-muted-foreground">{e.note}</span>}
              </div>
            ),
          )}
        </div>
        <div aria-hidden className="flex shrink-0 gap-3 border-t px-3 py-2 text-xs text-muted-foreground">
          <span>↑↓ navigate</span>
          <span>↵ open</span>
          <span>esc close</span>
        </div>
      </PopoverContent>
    </Popover>
  );
}
