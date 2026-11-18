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

// The memory popover: the window's cluster's instructions and notes, read and
// written through the sidecar. A note is a memory; the instructions are local
// state, since nothing stores them yet.
import { Popover as PopoverPrimitive } from '@base-ui/react/popover';
import { Button } from '@kubetail/ui/elements/button';
import { Spinner } from '@kubetail/ui/elements/spinner';
import { ArrowRight, Plus } from 'lucide-react';
import { useRef, useState } from 'react';
import type { ReactElement, RefObject } from 'react';

import { useActiveCluster } from '@/lib/active-cluster';
import { useActiveKubeContext } from '@/lib/active-kube-context';
import { useMemories } from '@/lib/memories';
import type { Memory } from '@/lib/memories';
import { useMemoryWrites } from '@/lib/memory-writes';
import { UnsavedDrafts } from '@/lib/unsaved-drafts';

import { MemoryInstructions } from '../memory-instructions';
import { MemoryList } from '../memory-list';
import { OmniboxPopover } from '../omnibox-popover';

// The cluster's own notes. Its watch also carries the memories for every
// cluster; the popover leaves those out.
function Notes({
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

  let body;
  if (!clusterID) {
    body = <p className="text-xs text-muted-foreground">Kstack has not reached this cluster yet.</p>;
  } else if (phase === 'connecting') {
    body = <Spinner />;
  } else {
    const own = memories.filter((m) => m.clusterID === clusterID);
    const onSave = (memory: Memory | undefined, name: string, text: string) =>
      save({ id: memory?.id ?? null, clusterID, name, body: text });
    body = (
      <MemoryList
        scope={scope}
        memories={own}
        adding={adding}
        onSave={onSave}
        onAddDone={onAddDone}
        onDelete={remove}
      />
    );
  }

  return (
    <section className="flex flex-col gap-2.5">
      <div className="flex flex-col gap-0.5">
        <h3 className="text-sm font-medium">Notes</h3>
        <p className="text-xs text-muted-foreground">Facts Kstack knows about this cluster.</p>
      </div>
      {body}
    </section>
  );
}

export function MemoryPopoverContent() {
  const { context } = useActiveKubeContext();
  const { clusterID } = useActiveCluster();
  const [adding, setAdding] = useState(false);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex shrink-0 flex-col gap-0.5 border-b px-5 pt-3.5 pb-3">
        <p className="text-base font-medium">Memory</p>
        <p className="text-xs text-muted-foreground">Chats on {context || 'this cluster'} read this.</p>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {/* The bottom padding is the fade's room once scrolled to the end. */}
        <div className="flex flex-col gap-6 px-5 pt-4 pb-8">
          {context ? (
            <>
              <MemoryInstructions />
              <Notes scope={context} clusterID={clusterID} adding={adding} onAddDone={() => setAdding(false)} />
            </>
          ) : (
            <p className="text-xs text-muted-foreground">Pick a cluster to see its memory.</p>
          )}
        </div>
        {/* Says more lies below. */}
        <div aria-hidden className="pointer-events-none sticky bottom-0 z-20 -mt-8 h-8 bg-linear-to-t from-popover" />
      </div>
      {/* Settings cannot open on a tab yet, so the link leads nowhere. */}
      <footer className="flex h-12 shrink-0 items-center justify-between border-t bg-muted/50 px-5 py-2">
        <Button variant="link" size="sm" className="px-0 text-xs text-foreground">
          Open memory settings
          <ArrowRight aria-hidden />
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="text-xs"
          disabled={!clusterID || adding}
          onClick={() => setAdding(true)}
        >
          <Plus aria-hidden />
          Add note
        </Button>
      </footer>
    </div>
  );
}

// Hangs from the omnibox, at its width.
export function MemoryPopover({
  anchorRef,
  children,
}: {
  anchorRef: RefObject<HTMLElement | null>;
  children: ReactElement;
}) {
  const { context } = useActiveKubeContext();
  // Opening focuses the popup itself, so no field inside looks active until
  // the user picks one, and Tab still starts inside it.
  const popupRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [drafts] = useState(() => new Set<string>());

  // A close waits while an editor holds unsaved work. The press that asked for
  // it also blurs the editor, which saves; once that lands, the next press
  // closes.
  const onOpenChange = (next: boolean) => {
    if (next || drafts.size === 0) setOpen(next);
  };

  const popup = (
    <PopoverPrimitive.Popup
      ref={popupRef}
      initialFocus={popupRef}
      data-testid="memory-popup"
      className="overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-lg outline-hidden"
      style={{ width: 'var(--anchor-width)', height: 'min(476px, var(--available-height))' }}
    >
      {/* Keyed on the window's cluster, so no instructions or draft carry over to another. */}
      <UnsavedDrafts value={drafts}>
        <MemoryPopoverContent key={context} />
      </UnsavedDrafts>
    </PopoverPrimitive.Popup>
  );

  return (
    <OmniboxPopover anchorRef={anchorRef} popup={popup} open={open} onOpenChange={onOpenChange}>
      {children}
    </OmniboxPopover>
  );
}
