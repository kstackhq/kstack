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

// The message box. Sending is a save: `chatSend` returns once the turn is accepted,
// and the answer arrives on the messages watch. The draft and the send are the
// outbox's, so this only draws them — and decides the one thing a send leaves to
// its caller, which is whether the window should follow a chat it created.
import { Button } from '@kubetail/ui/elements/button';
import { ArrowUp, AtSign, Plus, Square, SquareSlash, X } from 'lucide-react';
import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import type { ReactNode } from 'react';
import { useMutation } from 'urql';

import { PROSE_TEXT } from '@/components/primitives/text-entry';
import { graphql } from '@/gql';
import type { AppMode } from '@/lib/app-mode';
import { useAskAgainWhenLive } from '@/lib/ask-again-when-live';
import type { Created } from '@/lib/chat-outbox';
import { useChatOutbox } from '@/lib/chat-outbox';
import { inFlight } from '@/lib/chats';
import type { ChatMessage } from '@/lib/chats';
import type { WatchPhase } from '@/lib/graphql/use-watch-subscription';
import type { LogViewportHandle, SelectedLine } from '@/lib/log-viewport';
import { useLogWindows } from '@/lib/log-windows';
import { MAX_SELECTED, snapshotOf } from '@/lib/logs-snapshot';
import { useLogsView } from '@/lib/logs-view';
import { modelOf, seedPick, useModels } from '@/lib/models';
import { useRightSidebar } from '@/lib/right-sidebar';

import { ChatGrants } from './grants';
import { ModelPicker } from './model-picker';
import { NetworkSwitch } from './network-switch';
import { SandboxPopover } from './sandbox-popover';

const ChatCancelMutation = graphql(`
  mutation ChatCancel($chatID: ChatID!) {
    chatCancel(chatID: $chatID)
  }
`);

type ChatComposerProps = {
  /** Null for a chat that has not started: the first send creates it. */
  chatID: string | null;
  /** Which mode's list a chat created from here joins. */
  mode: AppMode;
  /**
   * The cluster a send is filed under: the window's for a new chat, the chat's own
   * for an open one. Undefined while there is none, when nothing can be sent.
   */
  clusterID: string | undefined;
  /**
   * What the composer waits on before it can send: the messages watch for an open
   * chat, the clusters watch for one that has not started.
   */
  phase: WatchPhase;
  /** The transcript's last message, which says whether a turn is running. */
  last: Pick<ChatMessage, 'seq' | 'status'> | null;
  /**
   * The chat's last answer, which is what its composer starts on. Null for a chat
   * with none, and for one whose transcript has not arrived.
   */
  lastAnswer?: Pick<ChatMessage, 'provider' | 'model' | 'effort'> | null;
  /**
   * Present while a request waits on the user in a message before the last, which
   * the transcript never scrolls to by itself. Show scrolls to it; it never approves.
   */
  onShowWaiting?: () => void;
  /** The first send from a null `chatID` created this chat. */
  onCreated?: (chatID: string) => void;
  /** Whether the machine offers a sandbox. Undefined until the sidecar says. */
  sandboxAvailable?: boolean;
  /** The open chat's switch. Undefined until the list watch delivers the chat. */
  sandboxDisabled?: boolean;
  /** Whether the machine can give a sandboxed command the internet. Undefined until the sidecar says. */
  networkAvailable?: boolean;
  /** Why it cannot, in the sidecar's words. */
  networkReason?: string;
  /** The open chat's network switch. Undefined until the list watch delivers the chat. */
  networkEnabled?: boolean;
  /** A switch is in flight. Send waits for it rather than send a switch about to change. */
  switching?: boolean;
  onSwitchNetwork?: (enabled: boolean) => void;
};

const noop = () => {};

// A toolbar button with no feature behind it yet. `aria-disabled` rather than
// `disabled`, so it still takes the hover that shows its title.
function ComingSoon({
  label,
  variant = 'ghost',
  children,
}: {
  label: string;
  variant?: 'ghost' | 'outline';
  children: ReactNode;
}) {
  return (
    <Button
      type="button"
      variant={variant}
      size="icon"
      aria-label={label}
      aria-disabled
      title={`${label} (coming soon)`}
      className="shrink-0"
    >
      {children}
    </Button>
  );
}

/** Where the view that rides the send is drawn. */
type Place = 'sidebar' | 'window';

/** The view that rides the send: its call, the handle its lines are read from, and where it is. */
type ShownView = { callId: string; viewport: LogViewportHandle; where: Place };

// The chip's words: what rides the send with the view. `count` is every line
// selected, `carried` the lines the send would carry.
function viewLine(where: Place, count: number, carried: number, riding: boolean): string {
  const view = `With the view in ${where === 'sidebar' ? 'the sidebar' : 'a window'}`;
  if (count === 0) return view;
  const lines = `${count} selected ${count === 1 ? 'line' : 'lines'}`;
  if (!riding) return `${view} (${lines} not sent)`;
  if (carried < count) return `${view} and ${lines} (the first ${carried} sent)`;
  return `${view} and ${lines}`;
}

export function ChatComposer({
  chatID,
  mode,
  clusterID,
  phase,
  last,
  lastAnswer,
  onShowWaiting,
  onCreated,
  sandboxAvailable,
  sandboxDisabled,
  networkAvailable,
  networkReason = '',
  networkEnabled,
  switching = false,
  onSwitchNetwork = noop,
}: ChatComposerProps) {
  const { models, loaded, failed, retry: askAgainForModels } = useModels();
  // Without a catalog there is no pick and nothing can be sent.
  useAskAgainWhenLive(failed, phase === 'live', askAgainForModels);
  // Seeded only once both things it depends on have answered: before the messages
  // watch's Bookmark the last answer is whichever row arrived first, and before the
  // catalog there is nothing to check a pick against.
  const seed =
    loaded && phase !== 'connecting'
      ? seedPick(
          models,
          lastAnswer?.provider
            ? { providerID: lastAnswer.provider.id, id: lastAnswer.model, effort: lastAnswer.effort }
            : null,
        )
      : undefined;
  const { draft, send, refusal, pick, setDraft, setPick, submit, retry, discard, settle } = useChatOutbox(
    mode,
    chatID,
    clusterID,
    seed,
  );
  const picked = modelOf(models, pick?.model ?? null);
  // A stored pick the catalog lacks is moved to the seed: a model can leave the
  // catalog, and the outbox outlives the composer. With nothing to seed it leaves
  // the pick alone, and Send stays shut on `picked`.
  useEffect(() => {
    if (pick && !picked && seed) setPick(seed);
  });
  const [, chatCancel] = useMutation(ChatCancelMutation);

  // What the user looks at rides the send, in chat mode on a chat: the focused view
  // while the sidebar beside the chat shows it, else this chat's log window focused
  // last, with the selected lines unless × removed them. The viewer writes a new
  // selection array only when the selection changes, and a window's mirror keeps its
  // array while the push names the same one, so reading it by identity keeps a
  // following tail from drawing the composer again, and a new selection is never
  // `removed`.
  const { view, viewport: sidebarViewport } = useLogsView();
  const { open: sidebarOpen } = useRightSidebar();
  const { shownWindowOf } = useLogWindows();
  let shownView: ShownView | null = null;
  if (mode === 'chat' && chatID !== null) {
    const inWindow = shownWindowOf(chatID);
    if (sidebarOpen && view?.chatId === chatID) {
      shownView = { callId: view.callId, viewport: sidebarViewport, where: 'sidebar' };
    } else if (inWindow) {
      shownView = { callId: inWindow.window.callId, viewport: inWindow.viewport, where: 'window' };
    }
  }
  const viewport = shownView?.viewport ?? sidebarViewport;
  const selection = useSyncExternalStore(viewport.subscribe, () => viewport.get().selection);
  const selectedCount = useSyncExternalStore(viewport.subscribe, () => viewport.get().selectedCount);
  const [removed, setRemoved] = useState<SelectedLine[] | null>(null);
  const riding = selectedCount > 0 && selection !== removed;

  // A send that lands after the window has moved on must not move it again. It moves
  // on by unmounting the composer — and, for a chat that has not started, by
  // switching cluster under one that stays mounted, which would open the created chat
  // under a cluster it does not belong to.
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const scope = useRef(clusterID);
  useEffect(() => {
    scope.current = clusterID;
  }, [clusterID]);

  // An accepted send is closed by the row it is awaiting arriving, which only a
  // composer watching the messages can see. Until then the last message on screen is
  // the one from before the send — and the entry stays busy, which is what the
  // transcript's Ask again and the next submit read.
  useEffect(() => {
    if (last) settle(last.seq);
  });

  // A turn waiting on a command is as busy as one streaming: one turn per chat.
  const streaming = last !== null && inFlight(last.status);
  const settled = send.status === 'idle';
  // What a send says the user saw. A chat that has not started has no row, and
  // starts sandboxed with no network.
  const shownDisabled = chatID === null ? false : sandboxDisabled;
  const shownNetwork = chatID === null ? false : networkEnabled;
  // The network switch changes nothing outside the sandbox.
  const inSandbox = sandboxAvailable === true && shownDisabled !== true;
  // `streaming` is checked here as well as behind the Cancel button, so Enter refuses
  // for the same reason the button is not a Send: one turn per chat.
  // `picked` is the entry's own pick checked against the list, never a fallback
  // computed here: what `submit` sends is the entry's, so a stale one holds Send shut.
  const canSend =
    settled &&
    clusterID !== undefined &&
    picked !== null &&
    phase !== 'connecting' &&
    !streaming &&
    !switching &&
    shownDisabled !== undefined &&
    shownNetwork !== undefined &&
    draft.trim() !== '';
  // All only once the watch they wait on has answered: a window at startup has not
  // failed to pick anything, and a catalog still in flight is not an empty one.
  let placeholder = 'Ask about this cluster, or paste a manifest, log or kubectl output…';
  if (loaded && models.length === 0) placeholder = 'Set an API key, then restart Kstack';
  else if (clusterID === undefined && phase !== 'connecting') placeholder = 'Pick a cluster to start a chat';

  const follow = async (outcome: Promise<Created | null>) => {
    const created = await outcome;
    if (created && mounted.current && created.clusterID === scope.current) onCreated?.(created.chatID);
  };

  const onSend = () => {
    if (canSend && shownDisabled !== undefined && shownNetwork !== undefined) {
      // Read at the press: the sidebar's handle as its viewer wrote it, a window's
      // mirror as its last push left it.
      const shown = viewport.get();
      const snapshot = shownView
        ? snapshotOf(shownView, shown.selection === removed ? { ...shown, selection: [], selectedCount: 0 } : shown)
        : null;
      void follow(submit({ sandboxDisabled: shownDisabled, networkEnabled: shownNetwork }, snapshot));
    }
  };

  let action;
  if (streaming && chatID) {
    action = (
      <Button
        type="button"
        size="icon"
        aria-label="Cancel"
        className="shrink-0 rounded-full"
        onClick={() => chatCancel({ chatID })}
      >
        <Square className="fill-current" aria-hidden />
      </Button>
    );
  } else if (send.status === 'held') {
    action = (
      <>
        <Button type="button" size="sm" onClick={() => follow(retry())}>
          Retry
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={discard}>
          Discard
        </Button>
      </>
    );
  } else {
    action = (
      <Button
        type="submit"
        size="icon"
        aria-label="Send"
        className="shrink-0 rounded-full bg-foreground text-background hover:bg-foreground/90 disabled:opacity-50"
        disabled={!canSend}
      >
        <ArrowUp aria-hidden />
      </Button>
    );
  }

  return (
    <form
      className="flex shrink-0 flex-col gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        onSend();
      }}
    >
      {/* Named for the model that refused, since another may still take the chat: the draft stays and Send stays open. */}
      {refusal?.kind === 'context-full' && (
        <p className="text-xs text-destructive">
          This chat is longer than {modelOf(models, refusal.model)?.label ?? refusal.model.id} can read. Pick a model
          that reads more, or start a new chat.
        </p>
      )}
      {refusal?.kind === 'sandbox-changed' && (
        <p className="text-xs text-destructive">This chat&apos;s sandbox switch changed. Check it, then send again.</p>
      )}
      {refusal?.kind === 'network-changed' && (
        <p className="text-xs text-destructive">This chat&apos;s network switch changed. Check it, then send again.</p>
      )}
      {shownView && (
        <p className="flex w-fit items-center gap-1 rounded-full border px-2 text-xs text-muted-foreground">
          <span>{viewLine(shownView.where, selectedCount, Math.min(selection.length, MAX_SELECTED), riding)}</span>
          {riding && (
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label="Don't send the selected lines"
              onClick={() => setRemoved(selection)}
            >
              <X aria-hidden />
            </Button>
          )}
        </p>
      )}
      {onShowWaiting && (
        <p className="flex items-center gap-2 text-xs text-muted-foreground">
          <span>An agent is waiting on you.</span>
          <Button type="button" size="xs" variant="outline" onClick={onShowWaiting}>
            Show
          </Button>
        </p>
      )}
      <div className="flex flex-col rounded-[10px] border bg-card shadow-xs focus-within:border-ring">
        <textarea
          {...PROSE_TEXT}
          value={draft}
          // What is in flight is what the box shows, so it must not change under it.
          readOnly={send.status === 'sending' || send.status === 'held'}
          placeholder={placeholder}
          rows={1}
          className="max-h-60 min-h-13 w-full resize-none bg-transparent px-3.5 pt-3.5 pb-1 text-sm/5 field-sizing-content placeholder:text-muted-foreground focus:outline-none"
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            // Enter while an IME is composing confirms the character, not the message.
            if (e.key !== 'Enter' || e.shiftKey || e.nativeEvent.isComposing) return;
            e.preventDefault();
            onSend();
          }}
        />
        {/* Wraps rather than clip at the dashboard sidebar's narrowest. */}
        <div className="flex flex-wrap items-center justify-between gap-1 px-2.5 py-2">
          <div className="flex min-w-0 items-center gap-0.5">
            <ComingSoon label="Add attachment" variant="outline">
              <Plus aria-hidden />
            </ComingSoon>
            <ComingSoon label="Mention a resource">
              <AtSign aria-hidden />
            </ComingSoon>
            <ComingSoon label="Slash commands">
              <SquareSlash aria-hidden />
            </ComingSoon>
            {chatID !== null && inSandbox && (
              <NetworkSwitch
                networkEnabled={networkEnabled}
                available={networkAvailable}
                switching={switching}
                onSwitch={onSwitchNetwork}
              />
            )}
            {chatID !== null && <ChatGrants chatID={chatID} />}
          </div>
          <div className="ml-auto flex min-w-0 flex-wrap items-center justify-end gap-1">
            {pick && picked && <ModelPicker models={models} pick={pick} picked={picked} onPick={setPick} />}
            {sandboxAvailable === true && <SandboxPopover sandboxDisabled={shownDisabled} />}
            <div aria-hidden className="mx-1 h-4 w-px bg-border" />
            <div className="flex shrink-0 gap-2">{action}</div>
          </div>
        </div>
      </div>
      {inSandbox && networkAvailable === false && (
        <p className="px-1 text-xs text-muted-foreground">No network on this machine: {networkReason}</p>
      )}
    </form>
  );
}
