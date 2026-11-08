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

// The memory popover: the window's cluster's instructions and saved memories,
// read and written through the sidecar. The instructions are local state;
// nothing stores them yet.
import { Popover as PopoverPrimitive } from '@base-ui/react/popover';
import { Button } from '@kubetail/ui/elements/button';
import { Popover, PopoverClose, PopoverTrigger } from '@kubetail/ui/elements/popover';
import { Spinner } from '@kubetail/ui/elements/spinner';
import { ArrowRight, X } from 'lucide-react';
import { useState } from 'react';
import type { ReactElement } from 'react';

import { useActiveCluster } from '@/lib/active-cluster';
import { useActiveKubeContext } from '@/lib/active-kube-context';
import { useMemories } from '@/lib/memories';
import type { Memory } from '@/lib/memories';

import { MemoryInstructions } from './instructions';
import { MemoryList } from './list';
import { useMemoryWrites } from './writes';

// The cluster's own memories. Its watch also carries the memories for every
// cluster; the popover leaves those out.
function MemoryDetail({
  scope,
  clusterID,
  adding,
  onAddDone,
}: {
  scope: string;
  clusterID: string | undefined;
  adding: boolean;
  onAddDone: () => void;
}) {
  const { memories, phase } = useMemories(clusterID);
  const { save, remove } = useMemoryWrites();

  if (!clusterID) {
    return <p className="px-6 py-2 text-xs text-muted-foreground">Kstack has not reached this cluster yet.</p>;
  }
  if (phase === 'connecting') return <Spinner />;

  const own = memories.filter((m) => m.clusterID === clusterID);
  const onSave = (memory: Memory | undefined, name: string, body: string) =>
    save({ id: memory?.id ?? null, clusterID, name, body });

  return (
    <MemoryList scope={scope} memories={own} adding={adding} onSave={onSave} onAddDone={onAddDone} onDelete={remove} />
  );
}

export function MemoryPopoverContent() {
  const { context } = useActiveKubeContext();
  const { clusterID } = useActiveCluster();
  const [instructions, setInstructions] = useState('');
  const [adding, setAdding] = useState(false);

  return (
    <div className="flex h-full min-h-0 flex-col gap-[18px]">
      <header className="flex shrink-0 items-start">
        <div className="flex min-w-0 flex-1 flex-col gap-1 pt-3 pl-6">
          <p className="text-base font-medium">{context ? `${context} memory` : 'Memory'}</p>
          <p className="text-xs text-muted-foreground">
            Notes the chats on {context || 'this cluster'} read. Kstack saves some as you talk; you can add, edit and
            delete them.
          </p>
        </div>
        {/* In the popup's corner, 4px inside its border. */}
        <div className="shrink-0 p-1">
          <PopoverClose
            render={<Button variant="ghost" size="icon-sm" className="size-8 text-muted-foreground" />}
            aria-label="Close"
          >
            <X aria-hidden />
          </PopoverClose>
        </div>
      </header>
      <div className="flex min-h-0 flex-1 flex-col px-[18px]">
        <div
          data-testid="memory-detail"
          className="flex min-h-0 flex-1 flex-col gap-3 rounded-xl border bg-background px-2 pt-[18px] pb-3"
        >
          {context ? (
            <>
              <MemoryInstructions text={instructions} onSave={setInstructions} />
              <MemoryDetail scope={context} clusterID={clusterID} adding={adding} onAddDone={() => setAdding(false)} />
            </>
          ) : (
            <p className="px-6 py-2 text-xs text-muted-foreground">Pick a cluster to see its memory.</p>
          )}
        </div>
      </div>
      {/* Nothing manages every memory yet, so the link leads nowhere. */}
      <footer className="flex h-12 shrink-0 items-center justify-between border-t bg-muted/50 px-[18px] py-2">
        <Button variant="link" size="sm" className="gap-2 text-xs text-foreground">
          Manage all memories
          <ArrowRight aria-hidden className="size-4" />
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="gap-2 border-input text-xs shadow-2xs"
          disabled={!clusterID || adding}
          onClick={() => setAdding(true)}
        >
          Add memory
        </Button>
      </footer>
    </div>
  );
}

// Hangs below the brain button, its right edge on the button's. z-50, as the
// library's popover content has: above the sidebar drawer and its scrim. It
// never flips above: its height is capped at the room below, which would
// otherwise trip the flip.
export function MemoryPopover({ children }: { children: ReactElement }) {
  // Keyed on the window's cluster, so its instructions and any draft never
  // carry over to another.
  const { context } = useActiveKubeContext();

  return (
    <Popover>
      <PopoverTrigger render={children} />
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Positioner
          align="end"
          side="bottom"
          sideOffset={8}
          collisionAvoidance={{ side: 'none' }}
          className="z-50"
        >
          <PopoverPrimitive.Popup
            data-testid="memory-popup"
            className="w-155 max-w-[calc(100vw-16px)] overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-lg outline-hidden"
            style={{ height: 'min(476px, var(--available-height))' }}
          >
            <MemoryPopoverContent key={context} />
          </PopoverPrimitive.Popup>
        </PopoverPrimitive.Positioner>
      </PopoverPrimitive.Portal>
    </Popover>
  );
}
