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

// The app bar's context omnibox — the address bar of the cluster surface: the
// cluster dialog and the cluster in view. The cluster is the window's one scope, and
// the segment is a quick switch over the same state the clusters dialog writes.
import type { ReactNode } from 'react';

import { useLocation, useNavigate } from '@tanstack/react-router';
import { Boxes, Brain, ChevronDown } from 'lucide-react';

import { Button } from '@kubetail/ui/elements/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@kubetail/ui/elements/dropdown-menu';
import { Spinner } from '@kubetail/ui/elements/spinner';
import { cn } from '@kubetail/ui/lib/utils';

import { APP_BAR_CONTROL_CLASS, APP_BAR_HOVER_CLASS } from '@/components/widgets/app-bar-button';

import { useActiveCluster } from '@/lib/active-cluster';
import { useActiveKubeContext } from '@/lib/active-kube-context';
import { useDialog } from '@/lib/dialog';
import { tlsUnverifiedReason } from '@/lib/kube-config';
import { useMemories } from '@/lib/memories';

const UNVERIFIED_TLS_TITLE: Record<'skip-verify' | 'plain-http', string> = {
  'skip-verify': "The server's certificate is not checked: this context's cluster sets insecure-skip-tls-verify.",
  'plain-http': "The server's certificate is not checked: this context's server URL is plain http.",
};

// Rides beside the cluster it is about: the one place the app says a connection
// goes unverified, so it must not be conditioned on anything but the verdict.
function UnverifiedTLSBadge({ reason }: { reason: 'skip-verify' | 'plain-http' }) {
  return (
    <span
      className="inline-block shrink-0 rounded bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-950 dark:text-amber-300"
      title={UNVERIFIED_TLS_TITLE[reason]}
    >
      Unverified TLS
    </span>
  );
}

// One segment of the omnibox: a ghost trigger reading the current value, with the
// choices under it. Kept to the truncating label the bar's width allows.
function Segment({
  label,
  value,
  icon,
  onSelect,
  children,
}: {
  label: string;
  value: string;
  icon?: ReactNode;
  onSelect: (next: string) => void;
  children: ReactNode;
}) {
  return (
    <DropdownMenu>
      {/* `shrink` overrides the button base's `shrink-0`: without it the trigger
          holds its content's full width and the value spills past the omnibox
          instead of the inner `truncate` engaging. */}
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="xs"
            className={cn('min-w-0 shrink rounded-full text-sm font-normal', APP_BAR_HOVER_CLASS)}
          />
        }
        aria-label={`${label}: ${value}`}
      >
        {icon}
        <span className="truncate">{value}</span>
        <ChevronDown className="shrink-0 opacity-60" aria-hidden />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" sideOffset={6} className="max-h-80 min-w-52 overflow-y-auto">
        {/* Called with the value alone: base-ui also passes event details, which
            the setters would take as a stray second argument. */}
        <DropdownMenuRadioGroup value={value} onValueChange={(next) => onSelect(next)}>
          {children}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// Presentational only, matching the Figma mockup's dot — there is no live
// connection-health source wired to the omnibox yet (see `cluster-sync-panel.tsx`
// for the real per-cluster verdict, computed from data this segment doesn't have).
function ClusterDot() {
  return <span aria-hidden className="size-2 shrink-0 rounded-full bg-emerald-500" />;
}

// The cluster half: the kube-contexts the kubeconfig defines. Selection is
// view-scope only — it never rewrites the kubeconfig's current-context.
function ClusterSegment() {
  const { context, active, contexts, phase } = useActiveKubeContext();
  const navigate = useNavigate();
  // Subscribed via `useLocation` (re-renders per navigation): the bar stays mounted
  // across every navigation, and `useMatchRoute` would read stale until a reload.
  const pathname = useLocation({ select: (location) => location.pathname });
  const unverified = tlsUnverifiedReason(active?.clusterEntry ?? null);

  // Not `setContext`: a chat belongs to a cluster, so a switch leaves whichever chat
  // the window is on — the dashboard panel's `chat` param, and chat mode's open chat,
  // which is the route itself. The out-of-scope notice is for arriving at such a chat
  // (a deep link, Back), never for stepping off one.
  const openChat = pathname.startsWith('/chat/');
  const select = (name: string) =>
    navigate({ to: openChat ? '/chat' : '.', search: (prev) => ({ ...prev, kubeContext: name, chat: undefined }) });

  if (contexts.length === 0) {
    // Split the reason so a stalled initial dial doesn't read as a permanent
    // "No kubeconfig".
    if (phase === 'connecting') {
      return (
        <span
          className="flex items-center gap-1.5 px-2 text-xs text-muted-foreground"
          data-testid="kube-context-connecting"
        >
          <Spinner size="xs" className="mr-0" />
          Connecting…
        </span>
      );
    }
    return (
      <span className="px-2 text-xs text-muted-foreground" data-testid="kube-context-empty">
        No kubeconfig
      </span>
    );
  }

  return (
    <>
      <Segment label="Cluster" value={context} icon={<ClusterDot />} onSelect={select}>
        {contexts.map((c) => (
          <DropdownMenuRadioItem key={c.name} value={c.name}>
            {c.name}
          </DropdownMenuRadioItem>
        ))}
      </Segment>
      {unverified && <UnverifiedTLSBadge reason={unverified} />}
    </>
  );
}

// Opens what the window's cluster remembers, lit while it remembers anything.
function MemoryButton() {
  const { openDialog } = useDialog();
  const { clusterID } = useActiveCluster();
  const { memories } = useMemories(clusterID);
  const lit = memories.length > 0;
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label="Memory"
      data-lit={lit}
      onClick={() => openDialog('memories')}
      className={cn('size-8 rounded-full', lit ? 'text-primary' : 'text-muted-foreground', APP_BAR_HOVER_CLASS)}
    >
      <Brain aria-hidden />
    </Button>
  );
}

export function ContextOmnibox() {
  const { openDialog } = useDialog();

  return (
    <div
      data-testid="context-omnibox"
      // Opts out of the app bar's drag region: this is a control, and pressing
      // its padding should no more move the window than a browser's address bar.
      data-tauri-drag-region="false"
      className={cn(
        // 44px "hug": no fixed height, so the 32px buttons plus this padding size
        // it, matching the Figma frame's own hug-contents sizing. The outline is a
        // `ring`, not a `border`: a real border adds to an auto-sized box on top of
        // the padding, which is what pushed this to 46px — a ring is a box-shadow,
        // so it draws inside without touching the layout.
        'flex w-full max-w-160 items-center bg-background px-1 py-1.5 shadow-sm ring-1 ring-inset ring-topbar-input-border dark:bg-input/30',
        APP_BAR_CONTROL_CLASS,
      )}
    >
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label="Clusters"
        onClick={() => openDialog('clusters')}
        className={cn('size-8 rounded-full text-muted-foreground', APP_BAR_HOVER_CLASS)}
      >
        <Boxes aria-hidden />
      </Button>
      {/* `min-w-0` keeps the trigger free to truncate. */}
      <div className="flex min-w-0 flex-1 items-center justify-center">
        <ClusterSegment />
      </div>
      {/* The same size as the clusters button, so the segment centers on the
          omnibox rather than on the space left beside it. */}
      <MemoryButton />
    </div>
  );
}
