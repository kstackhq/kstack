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
// cluster is the address. The server icon, carrying the window's cluster's verdict,
// opens its status; the brain opens its memory; a press anywhere else opens the
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
import { ClusterStatusPopover } from '@/components/widgets/cluster-status-popover';
import { MemoryPopover } from '@/components/widgets/memory';
import { UnverifiedTLSBadge } from '@/components/widgets/unverified-tls-badge';

import { useActiveCluster } from '@/lib/active-cluster';
import { useActiveKubeContext } from '@/lib/active-kube-context';
import { contextHealth } from '@/lib/cluster-health';
import type { Health } from '@/lib/cluster-health';
import { clusterForContext, useClusters } from '@/lib/clusters';
import { useMemories } from '@/lib/memories';
import type { Memory } from '@/lib/memories';

// Clears the icon around the dot's corner. A ring in the pill's colour would do
// the same, but that colour is translucent in dark mode.
const DOT_CUTOUT = { maskImage: 'radial-gradient(circle at 100% 100%, transparent 6px, black 6.5px)' };

// The server icon, with the cluster's verdict as a dot in its corner when given one.
function ServerGlyph({ health }: { health?: Health }) {
  return (
    <span data-testid="cluster-glyph" className="relative flex">
      <Server aria-hidden className="size-4" style={health ? DOT_CUTOUT : undefined} />
      {health && <ClusterDot tone={health.tone} title={health.summary} className="absolute -right-1 -bottom-1" />}
    </span>
  );
}

// The server icon as its own control, opening the window's cluster's status. Its
// box is the memory button's size, and its wrapper carries the same padding, so
// the label centres on the pill. Nothing while the kubeconfig has no context: the
// trigger draws the bare icon then.
function StatusButton({ anchorRef }: { anchorRef: RefObject<HTMLElement | null> }) {
  const { context, contexts } = useActiveKubeContext();
  const { clusters } = useClusters();
  if (contexts.length === 0) return null;

  const cluster = clusterForContext(clusters, context);
  const health = contextHealth(clusters, context);
  return (
    <div className="flex items-center pl-1">
      <ClusterStatusPopover anchorRef={anchorRef} context={context} cluster={cluster}>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`Cluster status: ${health.summary}`}
          className={cn('size-8 rounded-full text-muted-foreground', APP_BAR_HOVER_CLASS)}
        >
          <ServerGlyph health={health} />
        </Button>
      </ClusterStatusPopover>
    </div>
  );
}

// Everything between the status and memory buttons, reading the window's cluster
// with its TLS badge, or why there is none. It fills the pill's height, so a press
// anywhere there lands on it. With no cluster it also draws the bare icon, where the
// status button would be.
function ClusterTrigger({ anchorRef }: { anchorRef: RefObject<HTMLElement | null> }) {
  const { context, active, contexts, phase } = useActiveKubeContext();
  const hasCluster = contexts.length > 0;

  let name: string;
  let label: ReactNode;
  if (hasCluster) {
    name = `Cluster: ${context}`;
    label = (
      <>
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
        className={cn(
          'flex h-full min-w-0 flex-1 items-center rounded-full text-sm',
          !hasCluster && 'pl-1',
          APP_BAR_HOVER_CLASS,
        )}
      >
        {!hasCluster && (
          <span className="flex size-8 shrink-0 items-center justify-center text-muted-foreground">
            <ServerGlyph />
          </span>
        )}
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
  // Every popover hangs from the pill itself, not from the control that opens it.
  const pillRef = useRef<HTMLDivElement>(null);

  return (
    <div
      ref={pillRef}
      data-testid="context-omnibox"
      // Opts out of the app bar's drag region: this is a control, and pressing
      // it should no more move the window than a browser's address bar.
      data-tauri-drag-region="false"
      className={cn(
        // No vertical padding, since a press there would land on no control. The
        // outline is an inset ring rather than a border, so it adds nothing.
        'flex w-full max-w-160 items-stretch bg-background shadow-sm ring-1 ring-inset ring-app-bar-input-border dark:bg-input/30',
        APP_BAR_CONTROL_CLASS,
      )}
    >
      <StatusButton anchorRef={pillRef} />
      <ClusterTrigger anchorRef={pillRef} />
      <div className="flex items-center pr-1">
        <MemoryButton anchorRef={pillRef} />
      </div>
    </div>
  );
}
