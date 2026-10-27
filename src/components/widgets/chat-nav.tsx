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

// The recent chats. Rename edits in place and delete asks first, since deletion is
// instant, permanent, and disappears from every window at once. Both rows and titles
// come off `chatsWatch`, so a refused mutation leaves the list as it was without
// anything local to undo.
//
// A chat belongs to one cluster, so the list is the window's cluster's: switching
// cluster switches the rows.
//
// The mode decides two things at once, which is why it is one prop: which chats are
// listed, and where a row points. In chat mode a chat is a route; on the dashboard it
// is the panel's search param. No row is ever marked active on the dashboard — the
// panel shows the list or the open chat, never both.
import { useState } from 'react';

import { Link } from '@tanstack/react-router';
import { MoreHorizontal, Plus } from 'lucide-react';
import { useMutation } from 'urql';

import { Button } from '@kubetail/ui/elements/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@kubetail/ui/elements/dropdown-menu';
import { Spinner } from '@kubetail/ui/elements/spinner';

import { Dialog } from '@/components/widgets/dialog';
import { PROSE_TEXT } from '@/components/widgets/text-entry';
import { graphql } from '@/gql';
import { useActiveCluster } from '@/lib/active-cluster';
import type { AppMode } from '@/lib/app-mode';
import { chatModeOf, useChats } from '@/lib/chats';
import type { Chat } from '@/lib/chats';

const ChatRenameMutation = graphql(`
  mutation ChatRename($id: ChatID!, $title: String!) {
    chatRename(id: $id, title: $title) {
      id
      title
      updatedAt
    }
  }
`);

const ChatDeleteMutation = graphql(`
  mutation ChatDelete($id: ChatID!) {
    chatDelete(id: $id)
  }
`);

const ROW =
  'flex w-full items-center gap-1 rounded-md py-1.5 pr-1 pl-2 text-left text-sm text-muted-foreground ' +
  'transition-colors hover:bg-sidebar-accent hover:text-foreground ' +
  'data-[status=active]:bg-sidebar-accent data-[status=active]:text-foreground';

// A row's title, and a dot while a request in the chat waits on the user: an
// agent can wait in a chat the user is not looking at.
function RowLabel({ chat }: { chat: Chat }) {
  return (
    <>
      <span className="truncate">{chat.title}</span>
      {chat.awaitingApproval && (
        <span role="img" aria-label="Waiting on you" className="ml-auto size-2 shrink-0 rounded-full bg-primary" />
      )}
    </>
  );
}

function RenameBox({ chat, onDone }: { chat: Chat; onDone: () => void }) {
  const [title, setTitle] = useState(chat.title);
  const [, chatRename] = useMutation(ChatRenameMutation);

  const commit = () => {
    const next = title.trim();
    if (next && next !== chat.title) chatRename({ id: chat.id, title: next });
    onDone();
  };

  return (
    <input
      // eslint-disable-next-line jsx-a11y/no-autofocus -- the row became this input on the user's click
      autoFocus
      {...PROSE_TEXT}
      aria-label="Chat title"
      value={title}
      className="w-full rounded-md border bg-background px-2 py-1 text-sm"
      onChange={(e) => setTitle(e.target.value)}
      onBlur={onDone}
      onKeyDown={(e) => {
        if (e.key === 'Enter') commit();
        if (e.key === 'Escape') onDone();
      }}
    />
  );
}

export function ChatNav({ mode }: { mode: AppMode }) {
  const { chats: all, phase } = useChats();
  const { clusterID, phase: clusterPhase } = useActiveCluster();
  const [renaming, setRenaming] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<Chat | null>(null);
  const [, chatDelete] = useMutation(ChatDeleteMutation);

  const onDashboard = mode === 'dashboard';
  // Both watches: the rows are filtered by the window's cluster, so a chats snapshot
  // that lands first has every row filtered out until the clusters watch names one.
  // The spinner gates on the unfiltered phases; "No chats yet." then covers an empty
  // cluster and no cluster alike.
  const connecting = phase === 'connecting' || clusterPhase === 'connecting';
  const chats = all.filter((chat) => chat.mode === chatModeOf(mode) && chat.clusterID === clusterID);

  const remove = () => {
    if (confirming) chatDelete({ id: confirming.id });
    setConfirming(null);
  };

  return (
    <nav aria-label="Chats" className="flex flex-col gap-1">
      {/* Writes nothing: the first send from `/chat` creates the chat, so a stray
          click leaves no empty row behind. The panel has no such link — typing in its
          composer is how a dashboard chat starts. */}
      {!onDashboard && (
        <Link to="/chat" className={ROW}>
          <Plus className="size-3.5 shrink-0" aria-hidden />
          New chat
        </Link>
      )}

      {connecting && <Spinner size="sm" className="mt-2 self-center" />}

      {!connecting && chats.length === 0 && <p className="px-2 py-1 text-xs text-muted-foreground">No chats yet.</p>}

      {chats.map((chat) =>
        renaming === chat.id ? (
          <RenameBox key={chat.id} chat={chat} onDone={() => setRenaming(null)} />
        ) : (
          <div key={chat.id} className="group/row flex items-center">
            {onDashboard ? (
              <Link to="/dashboard" search={(prev) => ({ ...prev, chat: chat.id })} className={`${ROW} min-w-0 flex-1`}>
                <RowLabel chat={chat} />
              </Link>
            ) : (
              <Link to="/chat/$chatId" params={{ chatId: chat.id }} className={`${ROW} min-w-0 flex-1`}>
                <RowLabel chat={chat} />
              </Link>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger
                aria-label={`Actions for ${chat.title}`}
                className="rounded p-1 text-muted-foreground opacity-0 outline-none group-hover/row:opacity-100 focus-visible:opacity-100"
              >
                <MoreHorizontal className="size-4" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" sideOffset={4}>
                <DropdownMenuItem onClick={() => setRenaming(chat.id)}>Rename</DropdownMenuItem>
                <DropdownMenuItem onClick={() => setConfirming(chat)}>Delete</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        ),
      )}

      <Dialog
        open={!!confirming}
        onOpenChange={(open) => !open && setConfirming(null)}
        title="Delete chat?"
        description={`“${confirming?.title ?? ''}” and its messages go from every window. This cannot be undone.`}
      >
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={() => setConfirming(null)}>
            Keep chat
          </Button>
          <Button type="button" variant="destructive" onClick={remove}>
            Delete chat
          </Button>
        </div>
      </Dialog>
    </nav>
  );
}
