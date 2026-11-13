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
import { Button, buttonVariants } from '@kubetail/ui/elements/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@kubetail/ui/elements/dropdown-menu';
import { Spinner } from '@kubetail/ui/elements/spinner';
import { Link } from '@tanstack/react-router';
import { MessageCircle, MoreHorizontal, SquarePen } from 'lucide-react';
import { useState } from 'react';
import { useMutation } from 'urql';

import { Dialog } from '@/components/primitives/dialog';
import { SectionHeading } from '@/components/primitives/section-heading';
import { PROSE_TEXT, SearchBox } from '@/components/primitives/text-entry';
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

// The highlight sits on the row, not the link, so it runs under the actions button.
// Hover is half the active row's tint, and leaves the active row alone.
const ROW =
  'group/row flex items-center rounded-md text-muted-foreground transition-colors ' +
  'hover:text-foreground not-has-data-[status=active]:hover:bg-sidebar-accent/50 ' +
  'has-data-[status=active]:bg-sidebar-accent has-data-[status=active]:text-foreground';
const ROW_LINK = 'flex min-w-0 flex-1 items-center gap-1 py-1.5 pr-1 pl-2 text-left text-sm';
const NOTE = 'px-2 py-1 text-xs text-muted-foreground';

// Fills the sidebar below New chat, lifted above center so it reads under the button.
function NoChats() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-2 px-4 pb-26 text-center">
      <div className="mb-3 flex size-10 items-center justify-center rounded-lg bg-muted">
        <MessageCircle className="size-5 text-muted-foreground" aria-hidden />
      </div>
      <p className="text-sm font-medium text-sidebar-foreground">No chats yet</p>
      <p className="max-w-50 text-xs text-muted-foreground">
        Ask about your cluster with New chat. Your chats will show up here.
      </p>
    </div>
  );
}

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
    if (next && next !== chat.title) void chatRename({ id: chat.id, title: next });
    onDone();
  };

  return (
    <input
      // oxlint-disable-next-line jsx-a11y/no-autofocus -- the row became this input on the user's click
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
  const [query, setQuery] = useState('');
  const [, chatDelete] = useMutation(ChatDeleteMutation);

  const onDashboard = mode === 'dashboard';
  // Both watches: the rows are filtered by the window's cluster, so a chats snapshot
  // that lands first has every row filtered out until the clusters watch names one.
  // The spinner gates on the unfiltered phases; the empty state then covers an empty
  // cluster and no cluster alike.
  const connecting = phase === 'connecting' || clusterPhase === 'connecting';
  const chats = all.filter((chat) => chat.mode === chatModeOf(mode) && chat.clusterID === clusterID);
  const needle = query.trim().toLowerCase();
  const shown = chats.filter((chat) => chat.title.toLowerCase().includes(needle));

  const remove = () => {
    /* v8 ignore else -- the dialog that calls this opens only with a chat to confirm */
    if (confirming) void chatDelete({ id: confirming.id });
    setConfirming(null);
  };

  return (
    <nav aria-label="Chats" className="flex flex-1 flex-col gap-1">
      {/* Writes nothing: the first send from `/chat` creates the chat, so a stray
          click leaves no empty row behind. The panel has no such link — typing in its
          composer is how a dashboard chat starts. */}
      {!onDashboard && (
        <Link to="/chat" className={buttonVariants({ variant: 'outline', className: 'mb-2 w-full border-input' })}>
          <SquarePen aria-hidden />
          New chat
        </Link>
      )}

      {/* Sticky, so the rows it filters scroll under it. */}
      {!onDashboard && chats.length > 0 && (
        <div className="sticky top-0 z-10 bg-sidebar pb-2">
          <SearchBox
            aria-label="Search chats"
            placeholder="Search"
            containerClassName="max-w-none"
            className="bg-background shadow-xs"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') setQuery('');
            }}
          />
        </div>
      )}

      {connecting && <Spinner size="sm" className="mt-2 self-center" />}

      {!connecting && chats.length === 0 && (onDashboard ? <p className={NOTE}>No chats yet.</p> : <NoChats />)}

      {chats.length > 0 &&
        (shown.length > 0 ? <SectionHeading label="Recents" /> : <p className={NOTE}>No chats match.</p>)}

      {shown.map((chat) =>
        renaming === chat.id ? (
          <RenameBox key={chat.id} chat={chat} onDone={() => setRenaming(null)} />
        ) : (
          <div key={chat.id} className={ROW}>
            {onDashboard ? (
              <Link to="/dashboard" search={(prev) => ({ ...prev, chat: chat.id })} className={ROW_LINK}>
                <RowLabel chat={chat} />
              </Link>
            ) : (
              <Link to="/chat/$chatId" params={{ chatId: chat.id }} className={ROW_LINK}>
                <RowLabel chat={chat} />
              </Link>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger
                aria-label={`Actions for ${chat.title}`}
                className="mr-1 rounded p-1 text-muted-foreground opacity-0 outline-none hover:bg-foreground/10 group-hover/row:opacity-100 group-has-data-[status=active]/row:opacity-100 focus-visible:opacity-100"
              >
                <MoreHorizontal className="size-4" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" sideOffset={4}>
                <DropdownMenuItem onClick={() => setRenaming(chat.id)}>Rename</DropdownMenuItem>
                <DropdownMenuItem variant="destructive" onClick={() => setConfirming(chat)}>
                  Delete
                </DropdownMenuItem>
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
