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

// A log window's page: its bar over the view the host paired it with. The page asks
// the host which chat and call it shows (`log_window_view`) and reads the rest from
// the watches, so the URL carries nothing and a reload draws the same view.
import { invoke } from '@tauri-apps/api/core';
import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';

import { Button } from '@kubetail/ui/elements/button';

import { LogViewer } from '@/components/widgets/log-viewer';
import { LogsViewHeader } from '@/components/widgets/logs-view-panel';
import { LogsWindowBar } from '@/components/widgets/logs-window-bar';
import { useChatMessages, useChats } from '@/lib/chats';
import { displayName, useClusters } from '@/lib/clusters';
import type { Cluster } from '@/lib/clusters';
import { reportHostError } from '@/lib/error-bus';
import { createLogViewportHandle } from '@/lib/log-viewport';
import { viewOfCall } from '@/lib/logs-view';
import type { LogsView } from '@/lib/logs-view';

/** What the page reads of `log_window_view`'s answer. `grep` is null when the open carried none. */
type Answer = { chatId: string; callId: string; grep: string | null };

function Notice({ children }: { children: ReactNode }) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-2 text-sm text-muted-foreground">
      {children}
    </div>
  );
}

export function LogsWindow() {
  const [answer, setAnswer] = useState<Answer | null>(null);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);

  // The host only reads here, so a second mount's call answers the same.
  useEffect(() => {
    let cancelled = false;
    invoke<Answer>('log_window_view').then(
      (next) => {
        if (!cancelled) setAnswer(next);
      },
      (e: unknown) => {
        if (cancelled) return;
        reportHostError(e);
        setFailed(true);
      },
    );
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  if (answer) return <Paired answer={answer} />;
  return (
    <>
      <LogsWindowBar cluster={undefined} chatTitle="" />
      {failed ? (
        <Notice>
          <p>This log window could not load.</p>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setFailed(false);
              setAttempt((n) => n + 1);
            }}
          >
            Try again
          </Button>
        </Notice>
      ) : (
        <Notice>Loading…</Notice>
      )}
    </>
  );
}

// What the view was last drawn from.
type Shown = { view: LogsView; chatTitle: string; cluster: Cluster | undefined };

function sameShown(a: Shown | null, b: Shown | null) {
  if (a === null || b === null) return a === b;
  return (
    a.view.action === b.view.action &&
    a.view.clusterId === b.view.clusterId &&
    a.chatTitle === b.chatTitle &&
    a.cluster === b.cluster
  );
}

function Paired({ answer }: { answer: Answer }) {
  const { chats, phase: chatsPhase } = useChats();
  const { messages, phase: messagesPhase } = useChatMessages(answer.chatId);
  const { clusters } = useClusters();

  const chat = chats.find((c) => c.id === answer.chatId);
  const cluster = clusters?.find((c) => c.id === chat?.clusterID);
  const call = messages.flatMap((m) => m.toolCalls).find((c) => c.id === answer.callId);
  const action = call && viewOfCall(call);
  const loading = chatsPhase === 'connecting' || messagesPhase === 'connecting' || clusters === null;
  const next =
    !loading && chat && action
      ? {
          view: { clusterId: chat.clusterID, chatId: chat.id, callId: answer.callId, action },
          chatTitle: chat.title,
          cluster,
        }
      : null;

  // A reconnect masks a watch's data until its new snapshot lands. The view drawn
  // before it stays up through that, so the viewer keeps its grep, scroll, pause and
  // selection; a settled snapshot that draws a notice lets it go.
  const [shown, setShown] = useState<Shown | null>(null);
  if (!loading && !sameShown(shown, next)) setShown(next);
  const drawn = loading ? shown : next;

  let body: ReactNode;
  if (drawn) {
    body = (
      <View
        view={drawn.view}
        openGrep={answer.grep}
        chatTitle={drawn.chatTitle}
        clusterName={drawn.cluster ? displayName(drawn.cluster) : ''}
      />
    );
  } else if (loading) {
    body = <Notice>Loading…</Notice>;
  } else if (!chat) {
    body = <Notice>This chat was deleted.</Notice>;
  } else {
    body = <Notice>This log view is not available.</Notice>;
  }
  return (
    <>
      <LogsWindowBar
        cluster={loading ? shown?.cluster : cluster}
        chatTitle={loading ? (shown?.chatTitle ?? '') : (chat?.title ?? '')}
      />
      {body}
    </>
  );
}

function View({
  view,
  openGrep,
  chatTitle,
  clusterName,
}: {
  view: LogsView;
  /** The grep the open carried, if any. */
  openGrep: string | null;
  chatTitle: string;
  clusterName: string;
}) {
  const [viewport] = useState(createLogViewportHandle);
  // The grep the user applied here, over the open's, over the action's.
  const [applied, setApplied] = useState<string | null>(null);
  const grep = applied ?? openGrep ?? view.action.grep;

  // The host builds the native title from these parts; an empty one keeps its place.
  const [first] = view.action.sources;
  const source = `${first.kind} ${first.name} in ${first.namespace}`;
  useEffect(() => {
    invoke('log_window_title', { parts: [source, chatTitle, clusterName] }).catch(reportHostError);
  }, [source, chatTitle, clusterName]);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <LogsViewHeader view={view} grep={grep} />
      {/* Keyed on the call, as the panel's: a new call lands at its anchor. */}
      <LogViewer
        key={view.callId}
        clusterId={view.clusterId}
        query={{ ...view.action, grep }}
        openAt={view.action.anchor}
        pinToEnd={view.action.pinToEnd}
        viewport={viewport}
        onGrepApplied={setApplied}
      />
    </div>
  );
}
