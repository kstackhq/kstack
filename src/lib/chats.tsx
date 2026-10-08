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

// What a window reads about chats: the recent list and one chat's transcript, each a
// delta watch folded into an id-keyed map. Stored records with no cache behind them,
// so there is no provenance to guard and no need for `useCacheDeltaWatch`.
import { useMemo } from 'react';

import { graphql } from '@/gql';
import type { ChatMessagesWatchSubscription, ChatMode, ChatsWatchSubscription, ToolActionKind } from '@/gql/graphql';
import type { AppMode } from '@/lib/app-mode';
import { applyChange } from '@/lib/clusters';
import { useWatchSubscription, watchPhase } from '@/lib/graphql/use-watch-subscription';
import type { WatchPhase } from '@/lib/graphql/use-watch-subscription';

const ChatsWatchSubscription = graphql(`
  subscription ChatsWatch {
    chatsWatch {
      type
      chat {
        id
        title
        mode
        clusterID
        createdAt
        updatedAt
        awaitingApproval
        sandboxDisabled
        networkEnabled
      }
    }
  }
`);

const ChatMessagesWatchSubscription = graphql(`
  subscription ChatMessagesWatch($chatID: ChatID!) {
    chatMessagesWatch(chatID: $chatID) {
      type
      message {
        id
        chatID
        seq
        role
        content
        thinking
        status
        awaitingApproval
        error
        model
        provider {
          id
          label
        }
        effort
        finishReason
        toolCalls {
          id
          name
          actionKind
          status
          runsOn
          action {
            description
            command {
              text
              cwd
              background
              sandboxed
              network
            }
            read {
              path
            }
            write {
              path
              content
            }
            edit {
              path
              oldString
              newString
              replaceAll
            }
            search {
              query
            }
            fetch {
              url
              host
            }
            memory {
              op
              name
              body
              scope
            }
            delegate {
              prompt
              agentType
              model
            }
            kubeQuery {
              sql
              limit
            }
            logsView {
              sources {
                namespace
                kind
                name
                containers
                previous
              }
              filters {
                field
                values
              }
              grep
              anchor {
                kind
                at
              }
              pinToEnd
            }
          }
          agentCallID
          approval {
            id
            status
            duration
          }
          network
          clusterWrites {
            approval {
              id
              status
              duration
            }
            action {
              summary
              class
              context
              namespace
              verb
              group
              kind
              grantable
              commandRule
              chatRule
            }
            method
            path
            subresource
            contentType
            body
            dryRun
            diff
            diffCut
            diffError
            reason
          }
          output
          background {
            status
            exitCode
            report
          }
        }
        citations {
          type
          url
          title
          citedText
        }
      }
    }
  }
`);

export type Chat = NonNullable<ChatsWatchSubscription['chatsWatch']['chat']>;
export type ChatMessage = NonNullable<ChatMessagesWatchSubscription['chatMessagesWatch']['message']>;
export type ChatToolCall = ChatMessage['toolCalls'][number];
export type ChatClusterWrite = ChatToolCall['clusterWrites'][number];
export type ChatCitation = ChatMessage['citations'][number];

// Whether the turn behind an answer is still running: streaming, or stopped on
// a command the user has not decided.
export function inFlight(status: ChatMessage['status']): boolean {
  return status === 'Streaming' || status === 'WaitingApproval';
}

// The wire's spelling of the app's mode. The list filter and the send both go
// through here, so they cannot disagree.
export const chatModeOf = (mode: AppMode): ChatMode => (mode === 'dashboard' ? 'Dashboard' : 'Chat');

export type Folded<T> = { items: Map<string, T>; synced: boolean };

// Folds one batch of frames. The Bookmark closes the snapshot and is keyed on `type`,
// never on a missing entity: a nested non-null field erroring nulls its parent too,
// and reading that as the boundary would call a half-listed collection complete.
export function fold<T extends { id: string }>(
  prev: Folded<T> | undefined,
  frames: { type: string; entity: T | null | undefined }[],
): Folded<T> {
  const items = new Map(prev?.items);
  let synced = prev?.synced ?? false;
  frames.forEach(({ type, entity }) => {
    if (type === 'Bookmark') synced = true;
    else if (entity) applyChange(items, type, entity.id, entity);
  });
  return { items, synced };
}

// Both watches are `unshared`: a consumer can mount into a stream already running
// (the sidebar's list when the card reopens, a chat another window is streaming),
// and a shared stream never replays the snapshot that opened it.

/** The recent list, newest first. */
export function useChats(): { chats: Chat[]; phase: WatchPhase } {
  const { data, connected } = useWatchSubscription(
    { query: ChatsWatchSubscription },
    (prev: Folded<Chat> | undefined, frames) =>
      fold(
        prev,
        frames.map(({ chatsWatch: { type, chat } }) => ({ type, entity: chat })),
      ),
    { unshared: true },
  );

  const items = data?.items;
  // `updatedAt` is an ISO string, so it is parsed rather than compared as text. The
  // sidecar stores millis, so two chats can share one; the id (opaque text) breaks the
  // tie, so the order is at least stable.
  const chats = useMemo(
    () =>
      [...(items?.values() ?? [])].sort(
        (a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt) || b.id.localeCompare(a.id),
      ),
    [items],
  );

  return { chats, phase: watchPhase(!!data?.synced, connected) };
}

/** One chat's messages in `seq` order. A streaming answer arrives as `Modified` frames carrying the whole text so far. */
export function useChatMessages(chatID: string): { messages: ChatMessage[]; phase: WatchPhase } {
  const { data, connected } = useWatchSubscription(
    { query: ChatMessagesWatchSubscription, variables: { chatID } },
    (prev: Folded<ChatMessage> | undefined, frames) =>
      fold(
        prev,
        frames.map(({ chatMessagesWatch: { type, message } }) => ({ type, entity: message })),
      ),
    { unshared: true },
  );

  const items = data?.items;
  // `seq` is the order; `createdAt` is not, since two windows can send into one chat.
  const messages = useMemo(() => [...(items?.values() ?? [])].sort((a, b) => a.seq - b.seq), [items]);

  return { messages, phase: watchPhase(!!data?.synced, connected) };
}

type TextBlock = { type: 'text'; text: string };

function isTextBlock(block: unknown): block is TextBlock {
  if (typeof block !== 'object' || block === null) return false;
  const { type, text } = block as { type?: unknown; text?: unknown };
  return type === 'text' && typeof text === 'string';
}

type ContextBlock = { type: 'context'; text: string };

function isContextBlock(block: unknown): block is ContextBlock {
  if (typeof block !== 'object' || block === null) return false;
  const { type, text } = block as { type?: unknown; text?: unknown };
  return type === 'context' && typeof text === 'string';
}

function isToolBlock(block: unknown): boolean {
  if (typeof block !== 'object' || block === null) return false;
  const { type } = block as { type?: unknown };
  return type === 'tool_use' || type === 'tool_result';
}

/** A call the provider ran for the model, kept in the content for the replay alone. */
function isServerUseBlock(block: unknown): boolean {
  if (typeof block !== 'object' || block === null) return false;
  return (block as { type?: unknown }).type === 'server_use';
}

/** One source an answer cites: where it is and what it is called. Both are web text. */
export type Source = { url: string; title: string };

/**
 * The words in a message's content blocks, in order. `content` is `JSON!` on the wire
 * and arrives `unknown`; this is its one reader. Thinking, context, tool and
 * server-use blocks draw nothing here. Two text blocks with a tool round or a
 * server call between them were written at different moments, so a blank line separates
 * them, the way the sidecar's `llm.Text` joins them. Content that is not an array of blocks yields
 * '' rather than throwing.
 */
export function textOf(content: unknown): string {
  if (!Array.isArray(content)) return '';
  let text = '';
  let separate = false;
  content.forEach((block) => {
    if (isToolBlock(block) || isServerUseBlock(block)) {
      separate = text !== '';
    } else if (isTextBlock(block)) {
      text += (separate ? '\n\n' : '') + block.text;
      separate = false;
    }
  });
  return text;
}

/** A call's kind, spelled. Keyed by the generated union, so a new kind is a type error here. */
const ACTION_KIND_LABELS: Record<ToolActionKind, string> = {
  Command: 'Command',
  Read: 'Read',
  Write: 'Write',
  Edit: 'Edit',
  Search: 'Search',
  Fetch: 'Fetch page',
  Stop: 'Stop task',
  Memory: 'Memory',
  Delegate: 'Agent',
  KubeQuery: 'Query',
  LogsView: 'Logs',
};

export function actionKindLabel(kind: ToolActionKind): string {
  return ACTION_KIND_LABELS[kind];
}

/**
 * Whether a call draws Grant a folder… under it: a sandboxed command that
 * failed. Nothing tells a hidden path from any other failure, so every one
 * offers it.
 */
export function grantOffered(call: Pick<ChatToolCall, 'action' | 'status' | 'background'>): boolean {
  if (!call.action?.command?.sandboxed) return false;
  // A background call answers at once, so its end is the task's; a code the
  // sidecar could not read may be a failure.
  if (call.background) return call.background.status === 'Exited' && call.background.exitCode !== 0;
  return call.status === 'Failed';
}

/**
 * Whether a call is a search that ran: the provider's, which has no status, or
 * the sidecar's once it has succeeded. The kind is the tool's, so a search
 * whose arguments did not parse still counts.
 */
export function ranSearch(call: Pick<ChatToolCall, 'actionKind' | 'runsOn' | 'status'>): boolean {
  return call.actionKind === 'Search' && (call.runsOn === 'Provider' || call.status === 'Succeeded');
}

/**
 * An answer's searches: how many of its own ran (ranSearch), and the queries
 * they asked, in order, empty ones left out. A subagent's are drawn inside
 * its Agent call instead. A search whose action is null counts but lists
 * nothing. The queries are the model's words, and they are what left the
 * machine: read as data, drawn as text.
 */
export function searchesOf(
  calls: readonly Pick<ChatToolCall, 'actionKind' | 'runsOn' | 'status' | 'action' | 'agentCallID'>[],
): {
  count: number;
  queries: string[];
} {
  const ran = calls.filter((call) => call.agentCallID === null && ranSearch(call));
  return {
    count: ran.length,
    queries: ran.map((call) => call.action?.search?.query ?? '').filter((query) => query !== ''),
  };
}

/**
 * The sources an answer drew on, one per URL, in the order it cites them, the
 * first title winning. A citation with no URL (a document the model was given)
 * is not a source to list. All of it is web text: read as data, drawn as text.
 */
export function sourcesOf(citations: readonly ChatCitation[]): Source[] {
  const seen = new Set<string>();
  const sources: Source[] = [];
  citations.forEach(({ url, title }) => {
    if (url === '' || seen.has(url)) return;
    seen.add(url);
    sources.push({ url, title });
  });
  return sources;
}

/**
 * The cluster card a question carries — what the app told the model about the
 * cluster on that turn — or '' for a question without one. The one reader of a
 * context block.
 */
export function contextOf(content: unknown): string {
  if (!Array.isArray(content)) return '';
  return content.find(isContextBlock)?.text ?? '';
}

/**
 * One request waiting on the user: a call's own, or a cluster write its
 * sandboxed command sent, which `change` holds (null for a call's own).
 */
export type WaitingRequest = {
  call: ChatToolCall;
  approval: NonNullable<ChatToolCall['approval']>;
  change: ChatClusterWrite | null;
};

/** Whether a cluster write waits on the user: pending, while its call runs. */
export function isWaitingWrite(call: ChatToolCall, write: ChatClusterWrite): boolean {
  return write.approval.status === 'Pending' && call.status === 'Running';
}

/**
 * The requests awaiting approval, in the order they were asked — approval ids
 * are UUIDv7 — so a new one lands below the rest: an answer's own calls and
 * cluster writes, and each of its agents', which wait whether or not the answer
 * is still going.
 */
export function waitingRequestsOf(calls: ChatToolCall[]): WaitingRequest[] {
  return calls
    .flatMap((call): WaitingRequest[] => {
      const own =
        call.status === 'AwaitingApproval' && call.approval ? [{ call, approval: call.approval, change: null }] : [];
      const writes = call.clusterWrites
        .filter((w) => isWaitingWrite(call, w))
        .map((w) => ({ call, approval: w.approval, change: w }));
      return [...own, ...writes];
    })
    .sort((a, b) => (a.approval.id < b.approval.id ? -1 : 1));
}

/**
 * How one background task ended, as a notice told the model: what the
 * transcript draws of the sidecar's `llm.TaskNotice`. A task is a background
 * command or an agent; a notice naming no kind reads as a command's. `stoppedBy` is who stopped a stopped task,
 * '' otherwise. `description` and `command` are the model's text, one line
 * each, cut by the sidecar; `agentDescription` is the description of the Agent
 * call whose subagent started a command, '' for the answer's own. An agent's
 * report and error are the model's copy, never read here: the user reads the
 * report in the Agent call.
 */
export type TaskNotice = {
  id: string;
  kind: 'command' | 'agent';
  status: 'exited' | 'completed' | 'failed' | 'stopped' | 'lost';
  stoppedBy: string;
  exitCode: number | null;
  description: string;
  command: string;
  agentDescription: string;
};

const NOTICE_STATUSES = new Set(['exited', 'completed', 'failed', 'stopped', 'lost']);

const stringOr = (value: unknown): string => (typeof value === 'string' ? value : '');

/**
 * The notices a message carries, in order: each background task whose end rode
 * it to the model. The one reader of a notice block; a block of any other shape
 * is dropped.
 */
export function noticesOf(content: unknown): TaskNotice[] {
  if (!Array.isArray(content)) return [];
  return content.flatMap((block): TaskNotice[] => {
    if (typeof block !== 'object' || block === null) return [];
    const { type, task } = block as { type?: unknown; task?: unknown };
    if (type !== 'task_notification' || typeof task !== 'object' || task === null) return [];
    const {
      id,
      kind,
      status,
      stopped_by: stoppedBy,
      exit_code: code,
      description,
      command,
      agent_description: agentDescription,
    } = task as Record<string, unknown>;
    if (typeof id !== 'string' || typeof status !== 'string' || !NOTICE_STATUSES.has(status)) return [];
    return [
      {
        id,
        kind: kind === 'agent' ? 'agent' : 'command',
        status: status as TaskNotice['status'],
        stoppedBy: stringOr(stoppedBy),
        exitCode: typeof code === 'number' ? code : null,
        description: stringOr(description),
        command: stringOr(command),
        agentDescription: stringOr(agentDescription),
      },
    ];
  });
}
