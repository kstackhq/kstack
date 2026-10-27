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

// The memory popover's left rail: a collapsible Cluster group over the General
// row, as wide as the user last dragged it. Selection is the popover's; the
// rail only draws it.
import { useState } from 'react';

import { ChevronDown } from 'lucide-react';

import { Button } from '@kubetail/ui/elements/button';
import { cn } from '@kubetail/ui/lib/utils';

import { ResizeHandle } from '@/components/widgets/resize-handle';
import { usePersistedWidth } from '@/lib/persisted-width';

// What the rail selects: one kubeconfig context, or General, the memories for
// every cluster. Tagged, so a context named "General" is still a cluster.
export type Scope = { kind: 'cluster'; name: string } | { kind: 'general' };
export const GENERAL: Scope = { kind: 'general' };

const WIDTH = { min: 100, max: 320, initial: 140 };

// A selected row keeps the accent the others take on hover.
const ROW_CLASS =
  'h-7 w-full justify-start px-2 font-normal hover:bg-accent hover:text-accent-foreground aria-pressed:bg-accent aria-pressed:font-medium aria-pressed:text-accent-foreground dark:hover:bg-accent';

function Row({ name, selected, onSelect }: { name: string; selected: boolean; onSelect: () => void }) {
  return (
    <Button variant="ghost" size="sm" aria-pressed={selected} onClick={onSelect} className={ROW_CLASS} title={name}>
      <span className="truncate">{name}</span>
    </Button>
  );
}

export function MemoryRail({
  clusters,
  selected,
  onSelect,
}: {
  clusters: string[];
  selected: Scope;
  onSelect: (scope: Scope) => void;
}) {
  const [clustersOpen, setClustersOpen] = useState(true);
  const [width, setWidth] = usePersistedWidth('memory-popover-rail-width', WIDTH);

  return (
    // `pr-1` is the gap to the detail pane; the handle straddles its border.
    <div data-testid="memory-rail" className="relative flex h-full shrink-0 flex-col gap-2 pr-1" style={{ width }}>
      {/* The cluster list scrolls inside the rail, so General stays in reach. */}
      <div className="flex min-h-0 flex-col">
        <Button
          variant="ghost"
          size="sm"
          aria-expanded={clustersOpen}
          onClick={() => setClustersOpen((open) => !open)}
          className="w-full justify-between px-2 hover:bg-transparent dark:hover:bg-transparent"
        >
          Cluster
          <ChevronDown className={cn('transition-transform', !clustersOpen && '-rotate-90')} aria-hidden />
        </Button>
        {clustersOpen && (
          <div className="flex min-h-0 flex-col gap-1 overflow-y-auto px-1.5 py-0.5">
            {clusters.map((name) => (
              <Row
                key={name}
                name={name}
                selected={selected.kind === 'cluster' && selected.name === name}
                onSelect={() => onSelect({ kind: 'cluster', name })}
              />
            ))}
          </div>
        )}
      </div>
      {/* General reads as a group header, in its weight, until it is selected. */}
      <div className="shrink-0 pr-1.5">
        <Row name="General" selected={selected.kind === 'general'} onSelect={() => onSelect(GENERAL)} />
      </div>
      <ResizeHandle edge="right" onResize={setWidth} label="Resize memory sidebar" />
    </div>
  );
}
