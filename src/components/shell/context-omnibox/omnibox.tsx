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
import { Button } from '@kubetail/ui/elements/button';
import { Spinner } from '@kubetail/ui/elements/spinner';
import { cn } from '@kubetail/ui/lib/utils';
import { Brain, Server } from 'lucide-react';
import type { ReactNode, RefObject } from 'react';
import { useRef, useState } from 'react';

import { ClusterDot } from '@/components/cluster/cluster-dot';
import { ClusterPicker } from '@/components/cluster/cluster-picker';
import { ClusterStatusPopover } from '@/components/cluster/cluster-status';
import { MemoryPopover } from '@/components/cluster/memory';
import { UnverifiedTLSBadge } from '@/components/cluster/unverified-tls-badge';
import { useActiveCluster } from '@/lib/active-cluster';
import { useActiveKubeContext } from '@/lib/active-kube-context';
import { contextHealth } from '@/lib/cluster-health';
import type { Health } from '@/lib/cluster-health';
import { clusterForContext, useClusters } from '@/lib/clusters';
import { APP_BAR_BOX_CLASS, APP_BAR_HOVER_CLASS } from '@/lib/control-style';
import { tlsUnverifiedReason } from '@/lib/kube-config';
import { useMemories } from '@/lib/memories';
import type { Memory } from '@/lib/memories';

// Each control's `after` box stretches its hit area over the box's 1px border, so every
// press in the omnibox, to its drawn edge, lands on a control. It sits behind the control's
// content, so a tooltip there still shows; `isolate` keeps it within the control rather
// than behind the omnibox's fill.
const HIT_AREA_CLASS = 'relative isolate after:absolute after:-z-10';

// The status and memory buttons: `icon-sm`'s 28px, as circles, their icons gray. Their
// margins set them off the trigger and the box's ends, and their hit areas cover those
// margins too. Under 14rem the margins go, leaving the trigger room. The insets count from
// inside the button's own 1px transparent border (the library's, for its focus ring), so
// each is a pixel more than the margin and the box's border.
const BUTTON_CLASS = `${HIT_AREA_CLASS} mx-1 rounded-full text-muted-foreground after:-inset-x-1.5 after:-inset-y-0.5 @max-[14rem]:mx-0 @max-[14rem]:after:-inset-0.5 ${APP_BAR_HOVER_CLASS}`;

// Clears the icon around the dot's corner. A ring in the omnibox's colour would do
// the same, but that colour is translucent in dark mode.
const DOT_CUTOUT = { maskImage: 'radial-gradient(circle at 100% 100%, transparent 6px, black 6.5px)' };

// A narrow omnibox has room for no words but the cluster's name.
const COMPACT_HIDDEN = '@max-[14rem]:hidden';

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
// box is the memory button's size, so the label centres on the omnibox. Nothing while
// the kubeconfig has no context: the trigger draws the bare icon then.
function StatusButton({ anchorRef }: { anchorRef: RefObject<HTMLElement | null> }) {
  const { context, contexts } = useActiveKubeContext();
  const { clusters } = useClusters();
  if (contexts.length === 0) return null;

  const cluster = clusterForContext(clusters, context);
  const health = contextHealth(clusters, context);
  return (
    <ClusterStatusPopover anchorRef={anchorRef} context={context} cluster={cluster}>
      <Button variant="ghost" size="icon-sm" aria-label={`Cluster status: ${health.summary}`} className={BUTTON_CLASS}>
        <ServerGlyph health={health} />
      </Button>
    </ClusterStatusPopover>
  );
}

// Everything between the status and memory buttons, reading the window's cluster
// with its TLS badge, or why there is none. It fills the omnibox's height, and its hit area
// reaches over the border, so a press anywhere there lands on it. In a narrow omnibox
// the name truncates first and the badge shrinks to its icon, so the warning stays; the
// connecting and empty states draw their icon alone, their words left to the button's
// name. With no cluster it also draws the bare icon, where the
// status button would be.
function ClusterTrigger({ anchorRef }: { anchorRef: RefObject<HTMLElement | null> }) {
  const { context, active, contexts, phase } = useActiveKubeContext();
  const hasCluster = contexts.length > 0;

  let name: string;
  let label: ReactNode;
  if (hasCluster) {
    const entry = active?.clusterEntry ?? null;
    // The button's name stands in for everything inside it, the badge's words included.
    name = tlsUnverifiedReason(entry) ? `Cluster: ${context}, unverified TLS` : `Cluster: ${context}`;
    label = (
      <>
        <span data-testid="cluster-trigger-name" className="truncate">
          {context}
        </span>
        <UnverifiedTLSBadge entry={entry} collapsible />
      </>
    );
  } else if (phase === 'connecting') {
    // Said apart from "No kubeconfig", so a stalled first dial does not read as
    // a permanent absence.
    name = 'Cluster: connecting';
    label = (
      <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Spinner size="xs" className="mr-0" />
        <span className={COMPACT_HIDDEN}>Connecting…</span>
      </span>
    );
  } else {
    name = 'Cluster: no kubeconfig';
    label = <span className={cn('text-xs text-muted-foreground', COMPACT_HIDDEN)}>No kubeconfig</span>;
  }

  return (
    <ClusterPicker anchorRef={anchorRef}>
      <button
        type="button"
        aria-label={name}
        className={cn(
          HIT_AREA_CLASS,
          'flex h-full min-w-0 flex-1 items-center rounded-md after:-inset-px',
          APP_BAR_HOVER_CLASS,
        )}
      >
        {!hasCluster && (
          <span className="flex size-7 shrink-0 items-center justify-center text-muted-foreground">
            <ServerGlyph />
          </span>
        )}
        <span className="flex min-w-0 flex-1 items-center justify-center gap-2 text-[13px] font-medium text-muted-foreground @max-[14rem]:gap-1">
          {label}
        </span>
      </button>
    </ClusterPicker>
  );
}

// Opens what the window's cluster remembers. The brain rests gray and pulses
// once per save: a primary copy drawn over the base for one run of
// `--animate-memory-save-pulse` (index.css). Saves that land during a pulse
// join it rather than restarting it.
function MemoryButton({ anchorRef }: { anchorRef: RefObject<HTMLElement | null> }) {
  const { clusterID } = useActiveCluster();
  const { lastSaved } = useMemories(clusterID);
  const [pulsed, setPulsed] = useState<Memory>();
  const pulsing = lastSaved !== undefined && lastSaved !== pulsed;

  return (
    <MemoryPopover anchorRef={anchorRef}>
      <Button variant="ghost" size="icon-sm" aria-label="Memory" className={BUTTON_CLASS}>
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
  // Every popover hangs from the omnibox itself, not from the control that opens it.
  const boxRef = useRef<HTMLDivElement>(null);

  return (
    <div
      ref={boxRef}
      data-testid="context-omnibox"
      // Opts out of the app bar's drag region: this is a control, and pressing
      // it should no more move the window than a browser's address bar.
      data-tauri-drag-region="false"
      // No padding or gap, since a press there would land on no control. The bar's
      // middle column is at most 352px and shrinks first, so the box fills it; at the
      // window's 600px minimum it is about 80px. A container, so its controls compact
      // to its width.
      className={cn('@container flex w-full max-w-88 items-stretch bg-white dark:bg-input/30', APP_BAR_BOX_CLASS)}
    >
      <StatusButton anchorRef={boxRef} />
      <ClusterTrigger anchorRef={boxRef} />
      <MemoryButton anchorRef={boxRef} />
    </div>
  );
}
