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

// The app bar's context omnibox: the address bar of the cluster surface, where the
// cluster is the address. A press anywhere on it but the memory button opens the
// cluster picker, which switches the window's cluster and leads to the Clusters
// dialog.
import type { ReactNode, RefObject } from 'react';
import { useRef, useState } from 'react';

import { Brain, Server } from 'lucide-react';

import { Button } from '@kubetail/ui/elements/button';
import { Spinner } from '@kubetail/ui/elements/spinner';
import { cn } from '@kubetail/ui/lib/utils';

import { APP_BAR_CONTROL_CLASS, APP_BAR_HOVER_CLASS } from '@/components/widgets/app-bar-button';
import { ClusterDot } from '@/components/widgets/cluster-dot';
import { ClusterPicker } from '@/components/widgets/cluster-picker';
import { MemoryPopover } from '@/components/widgets/memory';
import { UnverifiedTLSBadge } from '@/components/widgets/unverified-tls-badge';

import { useActiveCluster } from '@/lib/active-cluster';
import { useActiveKubeContext } from '@/lib/active-kube-context';
import { contextHealth } from '@/lib/cluster-health';
import { useClusters } from '@/lib/clusters';
import { useMemories } from '@/lib/memories';
import type { Memory } from '@/lib/memories';

// Everything left of the memory button, reading the window's cluster with its
// verdict and TLS badge, or why there is none. It carries the pill's left and
// vertical padding itself, so a press anywhere there lands on it. The glyph's box
// is the memory button's size, so the label centres on the pill.
function ClusterTrigger({ anchorRef }: { anchorRef: RefObject<HTMLElement | null> }) {
  const { context, active, contexts, phase } = useActiveKubeContext();
  const { clusters } = useClusters();

  let name: string;
  let label: ReactNode;
  if (contexts.length > 0) {
    const health = contextHealth(clusters, context);
    name = `Cluster: ${context}`;
    label = (
      <>
        <ClusterDot tone={health.tone} title={health.summary} />
        <span data-testid="cluster-trigger-name" className="truncate">
          {context}
        </span>
        <UnverifiedTLSBadge entry={active?.clusterEntry ?? null} />
      </>
    );
  } else if (phase === 'connecting') {
    // Said apart from "No kubeconfig", so a stalled first dial does not read as
    // a permanent absence.
    name = 'Cluster: connecting';
    label = (
      <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Spinner size="xs" className="mr-0" />
        Connecting…
      </span>
    );
  } else {
    name = 'Cluster: no kubeconfig';
    label = <span className="text-xs text-muted-foreground">No kubeconfig</span>;
  }

  return (
    <ClusterPicker anchorRef={anchorRef}>
      <button
        type="button"
        aria-label={name}
        className={cn('flex h-full min-w-0 flex-1 items-center rounded-full py-1.5 pl-1 text-sm', APP_BAR_HOVER_CLASS)}
      >
        <span
          data-testid="cluster-trigger-glyph"
          className="flex size-8 shrink-0 items-center justify-center text-muted-foreground"
        >
          <Server aria-hidden className="size-4" />
        </span>
        <span className="flex min-w-0 flex-1 items-center justify-center gap-2">{label}</span>
      </button>
    </ClusterPicker>
  );
}

// Opens what the window's cluster remembers, lit while it remembers anything, and
// pulsing once per save: a primary copy of the brain drawn over the base for one
// run of `--animate-memory-save-pulse` (index.css). Saves that land during a
// pulse join it rather than restarting it.
function MemoryButton({ anchorRef }: { anchorRef: RefObject<HTMLElement | null> }) {
  const { clusterID } = useActiveCluster();
  const { memories, lastSaved } = useMemories(clusterID);
  const lit = memories.length > 0;
  const [pulsed, setPulsed] = useState<Memory>();
  const pulsing = lastSaved !== undefined && lastSaved !== pulsed;

  return (
    <MemoryPopover anchorRef={anchorRef}>
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label="Memory"
        data-lit={lit}
        className={cn(
          'relative size-8 rounded-full',
          lit ? 'text-primary' : 'text-muted-foreground',
          APP_BAR_HOVER_CLASS,
        )}
      >
        <Brain aria-hidden />
        {pulsing && (
          <Brain
            aria-hidden
            data-testid="memory-save-pulse"
            className="absolute inset-0 m-auto animate-memory-save-pulse text-primary motion-reduce:animate-memory-save-fade"
            onAnimationEnd={() => setPulsed(lastSaved)}
          />
        )}
      </Button>
    </MemoryPopover>
  );
}

export function ContextOmnibox() {
  // Both popovers hang from the pill itself, not from the control that opens them.
  const pillRef = useRef<HTMLDivElement>(null);

  return (
    <div
      ref={pillRef}
      data-testid="context-omnibox"
      // Opts out of the app bar's drag region: this is a control, and pressing
      // it should no more move the window than a browser's address bar.
      data-tauri-drag-region="false"
      className={cn(
        // 44px tall by content: the 32px buttons plus the trigger's padding. No
        // padding of its own, since a press there would land on no control. The
        // outline is an inset ring rather than a border, so it adds nothing.
        'flex w-full max-w-160 items-stretch bg-background shadow-sm ring-1 ring-inset ring-app-bar-input-border dark:bg-input/30',
        APP_BAR_CONTROL_CLASS,
      )}
    >
      <ClusterTrigger anchorRef={pillRef} />
      <div className="flex items-center pr-1">
        <MemoryButton anchorRef={pillRef} />
      </div>
    </div>
  );
}
