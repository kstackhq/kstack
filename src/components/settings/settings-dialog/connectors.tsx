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

// The Settings dialog's Connectors tab: every service Kstack can sign in to,
// in one alphabetical list, each connected or not.
import { Button } from '@kubetail/ui/elements/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@kubetail/ui/elements/dropdown-menu';
import { Ellipsis, ExternalLink } from 'lucide-react';
import { useEffect, useState } from 'react';

import { Dialog } from '@/components/primitives/dialog';
import { VisibleText } from '@/components/primitives/visible-text';
import { type ChatGPTActions, type ChatGPTConnection, type Connector, useConnectors } from '@/lib/connectors';
import { storageKey } from '@/lib/storage-key';

import { Intro } from './group';

export function ConnectorsSettings() {
  const { connectors, loaded } = useConnectors();
  const confirm = useFirstConnectionConfirm(connectors.find((c) => c.chatgpt)?.chatgpt?.connection);
  if (!loaded) return null;
  return (
    <Intro lede="Accounts Kstack signs in to. Model providers and tools use their credentials.">
      <ul className="divide-y rounded-lg border">
        {connectors
          .toSorted((a, b) => a.name.localeCompare(b.name))
          .map((c) =>
            c.chatgpt ? (
              <ChatGPTRow key={c.id} connector={c} {...c.chatgpt} onConnect={confirm.sent} />
            ) : (
              <ConnectorRow key={c.id} connector={c} />
            ),
          )}
      </ul>
      <Dialog
        open={confirm.open}
        onOpenChange={(open) => !open && confirm.dismiss()}
        title="You're using your ChatGPT plan"
      >
        <div className="flex flex-col gap-4">
          <p className="text-sm text-muted-foreground">
            Requests Kstack sends on ChatGPT models use your plan. You can manage usage in ChatGPT&apos;s settings.
          </p>
          <Button className="self-end" onClick={confirm.dismiss}>
            Got it
          </Button>
        </div>
      </Dialog>
    </Intro>
  );
}

// planConfirmedKey marks the plan's confirmation as seen, once for every window.
// planPendingKey marks a sign-in this window started and has not confirmed: the
// window's own, kept while the tab is hidden and its pane unmounted.
const planConfirmedKey = storageKey('chatgpt-plan-confirmed');
const planPendingKey = storageKey('chatgpt-plan-pending');

// Storage that refuses reads as unset, so a refused confirmed key shows the
// confirmation again; a refused write is dropped.
function isSet(storage: () => Storage, key: string): boolean {
  try {
    return storage().getItem(key) !== null;
  } catch {
    return false;
  }
}

function setFlag(storage: () => Storage, key: string, on: boolean) {
  try {
    if (on) storage().setItem(key, '1');
    else storage().removeItem(key);
  } catch {
    // Refused: the flag lasts as long as this pane's state.
  }
}

const local = () => localStorage;
const session = () => sessionStorage;

/**
 * The first connection's confirmation, shown only in the window that started it,
 * until the user dismisses it once. The confirmed key is read as the phase
 * reaches Connected, never at mount: another window may have set it since.
 */
function useFirstConnectionConfirm(connection: ChatGPTConnection | undefined) {
  const [sentHere, setSentHere] = useState(() => isSet(session, planPendingKey));
  // Each frame is a new object and a click sends none, so a frame saying the
  // attempt is over, or the one the pane mounts onto, ends what this window sent.
  const [seen, setSeen] = useState<ChatGPTConnection>();
  if (connection !== seen) {
    setSeen(connection);
    if (connection?.phase === 'SignedOut' || connection?.phase === 'Failed') setSentHere(false);
  }
  useEffect(() => setFlag(session, planPendingKey, sentHere), [sentHere]);
  return {
    open: sentHere && connection?.phase === 'Connected' && !isSet(local, planConfirmedKey),
    dismiss: () => {
      setFlag(local, planConfirmedKey, true);
      setSentHere(false);
    },
    sent: () => setSentHere(true),
  };
}

function RowText({ name, detail }: { name: string; detail: string }) {
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-0.5">
      <span className="truncate text-sm text-popover-foreground">
        <VisibleText text={name} />
      </span>
      <span className="truncate text-xs text-muted-foreground">
        <VisibleText text={detail} />
      </span>
    </div>
  );
}

// A placeholder: no backend yet, so every action is disabled.
function ConnectorRow({ connector }: { connector: Connector }) {
  return (
    <li className="flex items-center gap-3 px-4 py-2.5">
      <RowText name={connector.name} detail={connector.detail} />
      {connector.connected ? (
        <RowMenu connector={connector} />
      ) : (
        <Button variant="outline" size="sm" disabled>
          Coming soon
        </Button>
      )}
    </li>
  );
}

function RowMenu({ connector }: { connector: Connector }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="icon" className="size-7 rounded-md" />}
        aria-label={`Actions for ${connector.name}`}
      >
        <Ellipsis className="size-4" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {connector.manageLabel && (
          <DropdownMenuItem disabled>
            {connector.manageLabel}
            <ExternalLink className="ml-auto" />
          </DropdownMenuItem>
        )}
        <DropdownMenuItem disabled>Reconnect</DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" disabled>
          Disconnect
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// failureText is one line saying why the last attempt or session ended.
const failureText: Record<NonNullable<ChatGPTConnection['failure']>, string> = {
  Declined: 'You declined on ChatGPT’s page.',
  PlanUsageNotGranted: 'The sign-in did not allow use of your plan.',
  AccountMismatch: 'That is not the account this computer connected. Use a different account to change it.',
  TimedOut: 'The sign-in timed out.',
  Unavailable: 'ChatGPT could not be reached.',
  NotSaved: 'This computer could not save the sign-in.',
  SessionEnded: 'ChatGPT ended the session. Connect again.',
  Refused: 'ChatGPT refused the sign-in.',
};

function ChatGPTRow({
  connector,
  connection,
  actions,
  onConnect,
}: {
  connector: Connector;
  connection: ChatGPTConnection;
  actions: ChatGPTActions;
  onConnect: () => void;
}) {
  const connect = (fresh: boolean) => {
    onConnect();
    actions.connect(fresh);
  };
  let detail = connector.detail;
  if (connection.phase === 'SigningIn') detail = 'Waiting for your browser…';
  if (connection.phase === 'Failed' && connection.failure) detail = failureText[connection.failure];
  return (
    <li className="flex flex-col gap-2 px-4 py-2.5">
      <div className="flex items-center gap-3">
        <RowText name={connector.name} detail={detail} />
        {connection.phase === 'SigningIn' && (
          <Button variant="outline" size="sm" onClick={actions.cancel}>
            Cancel
          </Button>
        )}
        {(connection.phase === 'SignedOut' || connection.phase === 'Failed') && (
          <Button variant="outline" size="sm" onClick={() => connect(false)}>
            Continue with ChatGPT
          </Button>
        )}
        {(connection.phase === 'Connected' || (connection.registered && connection.phase !== 'SigningIn')) && (
          <ChatGPTMenu connected={connection.phase === 'Connected'} actions={actions} onFresh={() => connect(true)} />
        )}
      </div>
      {connection.revocationUnconfirmed && (
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          <p className="flex-1">
            Signed out on this computer. ChatGPT did not confirm it. To be sure, disconnect Kstack in ChatGPT&apos;s
            settings.
          </p>
          <Button variant="outline" size="xs" onClick={actions.openUsage}>
            Open ChatGPT settings
          </Button>
        </div>
      )}
    </li>
  );
}

function ChatGPTMenu({
  connected,
  actions,
  onFresh,
}: {
  connected: boolean;
  actions: ChatGPTActions;
  onFresh: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="icon" className="size-7 rounded-md" />}
        aria-label="Actions for ChatGPT"
      >
        <Ellipsis className="size-4" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {connected ? (
          <>
            <DropdownMenuItem onClick={actions.openUsage}>
              Manage usage
              <ExternalLink className="ml-auto" />
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onClick={actions.disconnect}>
              Disconnect
            </DropdownMenuItem>
          </>
        ) : (
          <DropdownMenuItem onClick={onFresh}>Use a different account</DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
