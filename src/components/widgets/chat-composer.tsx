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
import { useEffect, useRef } from 'react';

import { useMutation } from 'urql';

import { ChevronDown } from 'lucide-react';

import { Button } from '@kubetail/ui/elements/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@kubetail/ui/elements/dropdown-menu';

import { ChatGrants } from '@/components/widgets/chat-grants';
import { NetworkSwitch } from '@/components/widgets/network-switch';
import { SandboxSwitch } from '@/components/widgets/sandbox-switch';
import { graphql } from '@/gql';
import type { AppMode } from '@/lib/app-mode';
import { useAskAgainWhenLive } from '@/lib/ask-again-when-live';
import type { Created } from '@/lib/chat-outbox';
import { useChatOutbox } from '@/lib/chat-outbox';
import { inFlight } from '@/lib/chats';
import type { ChatMessage } from '@/lib/chats';
import type { Model } from '@/lib/models';
import { modelOf, seedPick, useModels } from '@/lib/models';
import type { WatchPhase } from '@/lib/graphql/use-watch-subscription';

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
  onSwitchSandbox?: (disabled: boolean) => void;
  onSwitchNetwork?: (enabled: boolean) => void;
};

type Option = { value: string; label: string };
type OptionGroup = { label?: string; options: Option[] };

// One of the picker's two selects: what is picked now, with the choices under it,
// in groups. A heading is drawn only when there is more than one group: one
// provider needs none.
function Segment({
  label,
  value,
  groups,
  onSelect,
}: {
  label: string;
  value: string;
  groups: OptionGroup[];
  onSelect: (next: string) => void;
}) {
  const current = groups.flatMap((g) => g.options).find((o) => o.value === value);
  const headed = groups.length > 1;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="xs" className="min-w-0 shrink rounded-full text-xs font-normal" />}
        aria-label={`${label}: ${current?.label ?? value}`}
      >
        <span className="truncate">{current?.label ?? value}</span>
        <ChevronDown className="shrink-0 opacity-60" aria-hidden />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" sideOffset={6} className="max-h-80 min-w-40 overflow-y-auto">
        {/* Called with the value alone: base-ui also passes event details, which the
            setter would take as a stray second argument. */}
        <DropdownMenuRadioGroup value={value} onValueChange={(next) => onSelect(next)}>
          {groups.map((g, i) => (
            <DropdownMenuGroup key={g.label ?? i}>
              {headed && <DropdownMenuLabel>{g.label}</DropdownMenuLabel>}
              {g.options.map((o) => (
                <DropdownMenuRadioItem key={o.value} value={o.value}>
                  {o.label}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuGroup>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// A model's key in the select: the id alone is not one, two providers being free to
// name a model the same.
const keyOf = (providerID: string, id: string) => `${providerID}/${id}`;

// The catalog as one group per provider, in catalog order, headed by its label.
function groupByProvider(models: Model[]): OptionGroup[] {
  const byProvider = new Map<string, OptionGroup>();
  models.forEach((m) => {
    const group = byProvider.get(m.provider.id) ?? { label: m.provider.label, options: [] };
    byProvider.set(m.provider.id, group);
    group.options.push({ value: keyOf(m.provider.id, m.id), label: m.label });
  });
  return [...byProvider.values()];
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
  onSwitchSandbox = () => {},
  onSwitchNetwork = () => {},
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
  const {
    draft,
    send,
    refusal,
    pick,
    networkThisTurn,
    setDraft,
    setPick,
    setNetworkThisTurn,
    submit,
    retry,
    discard,
    settle,
  } = useChatOutbox(mode, chatID, clusterID, seed);
  const picked = modelOf(models, pick?.model ?? null);
  // A stored pick the catalog lacks is moved to the seed: a model can leave the
  // catalog, and the outbox outlives the composer. With nothing to seed it leaves
  // the pick alone, and Send stays shut on `picked`.
  useEffect(() => {
    if (pick && !picked && seed) setPick(seed);
  });
  const [, chatCancel] = useMutation(ChatCancelMutation);

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
  // The network switch and the toggle change nothing outside the sandbox, and the
  // toggle adds nothing to a chat whose switch is on.
  const inSandbox = sandboxAvailable === true && shownDisabled !== true;
  const showsToggle = inSandbox && shownNetwork !== true;
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
  let placeholder = 'Message…';
  if (loaded && models.length === 0) placeholder = 'Set an API key, then restart Kstack';
  else if (clusterID === undefined && phase !== 'connecting') placeholder = 'Pick a cluster to start a chat';

  // Another model may not have the level this one is at, so the pick moves to the
  // new model's default rather than to a name it happens to share.
  const selectModel = (key: string) => {
    const next = models.find((m: Model) => keyOf(m.provider.id, m.id) === key);
    if (next) setPick({ model: { providerID: next.provider.id, id: next.id }, effort: next.defaultEffort });
  };

  const follow = async (outcome: Promise<Created | null>) => {
    const created = await outcome;
    if (created && mounted.current && created.clusterID === scope.current) onCreated?.(created.chatID);
  };

  const onSend = () => {
    if (canSend && shownDisabled !== undefined && shownNetwork !== undefined) {
      follow(submit({ sandboxDisabled: shownDisabled, networkEnabled: shownNetwork }));
    }
  };

  let action;
  if (streaming && chatID) {
    action = (
      <Button type="button" variant="outline" onClick={() => chatCancel({ chatID })}>
        Cancel
      </Button>
    );
  } else if (send.status === 'held') {
    action = (
      <>
        <Button type="button" onClick={() => follow(retry())}>
          Retry
        </Button>
        <Button type="button" variant="ghost" onClick={discard}>
          Discard
        </Button>
      </>
    );
  } else {
    action = (
      <Button type="submit" disabled={!canSend}>
        Send
      </Button>
    );
  }

  // A column, so the actions keep their width at the dashboard sidebar's narrowest:
  // beside the box they would be pushed past the panel's edge and clipped.
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
      {onShowWaiting && (
        <p className="flex items-center gap-2 text-xs text-muted-foreground">
          <span>An agent is waiting on you.</span>
          <Button type="button" size="xs" variant="outline" onClick={onShowWaiting}>
            Show
          </Button>
        </p>
      )}
      <textarea
        value={draft}
        // What is in flight is what the box shows, so it must not change under it.
        readOnly={send.status === 'sending' || send.status === 'held'}
        placeholder={placeholder}
        rows={2}
        className="min-h-16 w-full resize-none rounded-md border bg-sidebar px-3 py-2 text-sm"
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          // Enter while an IME is composing confirms the character, not the message.
          if (e.key !== 'Enter' || e.shiftKey || e.nativeEvent.isComposing) return;
          e.preventDefault();
          onSend();
        }}
      />
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-1">
          {/* A chat that has not started has no row to switch: it starts sandboxed. */}
          {chatID !== null && sandboxAvailable === true && (
            <SandboxSwitch sandboxDisabled={sandboxDisabled} switching={switching} onSwitch={onSwitchSandbox} />
          )}
          {chatID !== null && inSandbox && (
            <NetworkSwitch
              networkEnabled={networkEnabled}
              available={networkAvailable}
              switching={switching}
              onSwitch={onSwitchNetwork}
            />
          )}
          {chatID !== null && <ChatGrants chatID={chatID} />}
          {showsToggle && (
            <Button
              type="button"
              variant={networkThisTurn ? 'secondary' : 'ghost'}
              size="xs"
              className="shrink-0 rounded-full text-xs font-normal"
              aria-pressed={networkThisTurn}
              disabled={networkAvailable !== true}
              onClick={() => setNetworkThisTurn(!networkThisTurn)}
            >
              Network for this message
            </Button>
          )}
          {pick && picked && (
            <>
              <Segment
                label="Model"
                value={keyOf(pick.model.providerID, pick.model.id)}
                groups={groupByProvider(models)}
                onSelect={selectModel}
              />
              {/* A model with no such knob has nothing to show here. */}
              {picked.efforts.length > 0 && (
                <Segment
                  label="Effort"
                  value={pick.effort}
                  groups={[{ options: picked.efforts.map((e: string) => ({ value: e, label: e })) }]}
                  onSelect={(effort) => setPick({ model: pick.model, effort })}
                />
              )}
            </>
          )}
        </div>
        <div className="flex shrink-0 gap-2">{action}</div>
      </div>
      {inSandbox && networkAvailable === false && (
        <p className="text-xs text-muted-foreground">No network on this machine: {networkReason}</p>
      )}
    </form>
  );
}
