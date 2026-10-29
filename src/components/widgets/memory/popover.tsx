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

// The memory popover: a rail of clusters beside the selected one's instructions
// and saved memories. A cluster row lists that cluster's own memories, General
// the ones for every cluster, all read and written through the sidecar. The
// instructions are local state; nothing stores them yet.
import { useState } from 'react';
import type { ReactElement, RefObject } from 'react';

import { Popover as PopoverPrimitive } from '@base-ui/react/popover';
import { X } from 'lucide-react';

import { Button } from '@kubetail/ui/elements/button';
import { PopoverClose } from '@kubetail/ui/elements/popover';
import { Spinner } from '@kubetail/ui/elements/spinner';

import { OmniboxPopover } from '@/components/widgets/omnibox-popover';

import { useActiveKubeContext } from '@/lib/active-kube-context';
import { clusterForContext, useClusters } from '@/lib/clusters';
import { useMemories } from '@/lib/memories';
import type { Memory } from '@/lib/memories';
import { usePersistedWidth } from '@/lib/persisted-width';
import { MemoryInstructions } from './instructions';
import { MemoryList } from './list';
import { GENERAL, MemoryRail } from './rail';
import type { Scope } from './rail';
import { PopupResizeHandles } from './resize-handles';
import { useMemoryWrites } from './writes';

function Banner({ cluster, defaultCluster, onBack }: { cluster: string; defaultCluster: string; onBack: () => void }) {
  return (
    <div className="flex h-7 shrink-0 items-center gap-2 rounded-lg bg-accent px-3">
      <p className="min-w-0 flex-1 truncate text-xs text-accent-foreground" title={`Viewing ${cluster}’s memory.`}>
        Viewing {cluster}’s memory.
      </p>
      <Button variant="link" size="xs" className="h-auto px-0" onClick={onBack}>
        Back to {defaultCluster}
      </Button>
    </div>
  );
}

// One rail row's memories. `scopeID` is the cluster the row names, null for
// General; `watchID` is the cluster whose watch carries them, since every
// cluster's watch also carries the memories for every cluster.
function MemoryDetail({
  scope,
  scopeID,
  watchID,
}: {
  scope: string;
  scopeID: string | null | undefined;
  watchID: string | undefined;
}) {
  const { memories, phase } = useMemories(watchID);
  const { save, remove } = useMemoryWrites();
  const [adding, setAdding] = useState(false);

  if (!watchID || scopeID === undefined) {
    return <p className="py-2 text-xs text-muted-foreground">Kstack has not reached this cluster yet.</p>;
  }
  if (phase === 'connecting') return <Spinner />;

  const own = memories.filter((m) => m.clusterID === scopeID);
  const onSave = (memory: Memory | undefined, name: string, body: string) =>
    save({ id: memory?.id ?? null, clusterID: memory ? memory.clusterID : scopeID, name, body });

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-[18px]">
      <MemoryList
        scope={scope}
        memories={own}
        adding={adding}
        onSave={onSave}
        onAddDone={() => setAdding(false)}
        onDelete={remove}
      />
      <Button variant="outline" className="w-full" disabled={adding} onClick={() => setAdding(true)}>
        Add memory
      </Button>
    </div>
  );
}

export function MemoryPopoverContent() {
  // The window's cluster leads the rail, then the kubeconfig's other contexts.
  // The selection tracks the window's cluster until a row is picked; General
  // stands in while the window has none.
  const { context, contexts } = useActiveKubeContext();
  const { clusters: records } = useClusters();
  const idOf = (name: string) => clusterForContext(records, name)?.id;
  const clusters = contexts.map((c) => c.name).filter((name) => name !== context);
  if (context) clusters.unshift(context);
  const [picked, setPicked] = useState<Scope | null>(null);
  const selected: Scope = picked ?? (context ? { kind: 'cluster', name: context } : GENERAL);
  const general = selected.kind === 'general';
  const name = general ? 'General' : selected.name;
  // Keys the scope's draft and instructions apart from a context named General.
  const key = general ? 'general' : `cluster:${selected.name}`;

  const [instructions, setInstructions] = useState<Record<string, string>>({});

  // General is a bucket of its own, not another cluster's memory.
  const viewingOther = !general && context !== '' && selected.name !== context;
  // General's memories ride any cluster's watch: the window's, else the first known.
  const watchID = general ? (idOf(context) ?? records?.[0]?.id) : idOf(selected.name);

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 p-[18px]">
      <div className="flex flex-col gap-0.5">
        <div className="flex items-center gap-2">
          <p className="flex-1 text-base font-medium">Memory</p>
          <PopoverClose
            render={<Button variant="ghost" size="icon-xs" className="text-muted-foreground" />}
            aria-label="Close"
          >
            <X aria-hidden />
          </PopoverClose>
        </div>
        <p className="text-xs text-muted-foreground">
          Notes the chats on this cluster read. Kstack saves some as you talk; you can add, edit and delete them.
        </p>
      </div>
      <div className="flex min-h-0 flex-1 items-start">
        <MemoryRail clusters={clusters} selected={selected} onSelect={setPicked} />
        {/* Keyed on the selection so a draft in progress never carries over.
            `min-w-70` keeps the instructions editor's buttons on one line; the
            rail gives way to it. */}
        <div
          key={key}
          data-testid="memory-detail"
          className="flex h-full min-w-70 flex-1 flex-col gap-[18px] rounded-xl border bg-background p-3"
        >
          {viewingOther && (
            <Banner
              cluster={name}
              defaultCluster={context}
              onBack={() => setPicked({ kind: 'cluster', name: context })}
            />
          )}
          <div className="flex min-h-0 flex-1 flex-col gap-6">
            <MemoryInstructions
              text={instructions[key] ?? ''}
              onSave={(text) => setInstructions((prev) => ({ ...prev, [key]: text }))}
            />
            <MemoryDetail scope={name} scopeID={general ? null : idOf(selected.name)} watchID={watchID} />
          </div>
        </div>
      </div>
    </div>
  );
}

// A width of 0 is one the user never dragged: the popup takes the omnibox's,
// but no less than `WIDTH.min`, which holds the rail's and the detail pane's
// minimums. The window bounds what is drawn, never what is stored. The width's bound is
// the viewport's, not `--available-width`: that moves with the popup's own
// position, so a popup wider than the window would chase it every frame.
const WIDTH = { min: 480, max: 1600, initial: 0 };
const HEIGHT = { min: 320, max: 1200, initial: 490 };

// `usePersistedWidth` is a clamped, remembered number; the height is one too.
function MemoryPopup() {
  const [width, setWidth] = usePersistedWidth('memory-popover-width', WIDTH);
  const [height, setHeight] = usePersistedWidth('memory-popover-height', HEIGHT);

  return (
    <PopoverPrimitive.Popup
      data-testid="memory-popup"
      className="relative max-w-[calc(100vw-16px)] rounded-xl border bg-popover text-popover-foreground shadow-lg outline-hidden"
      style={{
        width: width ? `${width}px` : `max(var(--anchor-width), ${WIDTH.min}px)`,
        height: `min(${height}px, var(--available-height))`,
      }}
    >
      <MemoryPopoverContent />
      <PopupResizeHandles onWidth={setWidth} onHeight={setHeight} label="Resize memory" />
    </PopoverPrimitive.Popup>
  );
}

// Hung from the omnibox, not the brain button that opens it.
export function MemoryPopover({
  anchorRef,
  children,
}: {
  anchorRef: RefObject<HTMLElement | null>;
  children: ReactElement;
}) {
  return (
    <OmniboxPopover anchorRef={anchorRef} popup={<MemoryPopup />}>
      {children}
    </OmniboxPopover>
  );
}
