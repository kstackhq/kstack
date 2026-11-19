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
import { type ReactNode, useEffect, useState } from 'react';

import { Dialog } from '@/components/primitives/dialog';
import { VisibleText } from '@/components/primitives/visible-text';
import {
  type ChatGPTActions,
  type ChatGPTConnection,
  type Connector,
  type ConnectorFailure,
  type OpenRouterActions,
  type OpenRouterConnection,
  useConnectors,
} from '@/lib/connectors';
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
          .map((c) => {
            if (c.chatgpt) return <ChatGPTRow key={c.id} connector={c} {...c.chatgpt} onConnect={confirm.sent} />;
            if (c.openrouter) return <OpenRouterRow key={c.id} connector={c} {...c.openrouter} />;
            return <ConnectorRow key={c.id} connector={c} />;
          })}
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

// A string detail may be an account's, so it is spelled out; an element is the pane's own.
function RowText({ name, detail }: { name: string; detail: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-0.5">
      <span className="truncate text-sm text-popover-foreground">
        <VisibleText text={name} />
      </span>
      <span className="truncate text-xs text-muted-foreground">
        {typeof detail === 'string' ? <VisibleText text={detail} /> : detail}
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

// failureText is one line saying why a connector's last attempt, session or key
// ended. name is the service's.
const failureText: Record<ConnectorFailure, (name: string) => string> = {
  Declined: (name) => `You declined on ${name}’s page.`,
  ScopeNotGranted: () => 'The sign-in did not allow use of your plan.',
  AccountMismatch: () => 'That is not the account this computer connected. Use a different account to change it.',
  TimedOut: () => 'The sign-in timed out.',
  Unavailable: (name) => `${name} could not be reached.`,
  Refused: (name) => `${name} refused the sign-in.`,
  NotSaved: () => 'This computer could not save the sign-in.',
  NotDeleted: () => 'Kstack could not remove the key from this machine.',
  SessionEnded: (name) => `${name} ended the session. Connect again.`,
  KeyRejected: (name) => `${name} no longer accepts the key. Connect again.`,
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
  if (connection.phase === 'Failed' && connection.failure) detail = failureText[connection.failure](connector.name);
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

function openRouterDetail({ phase, failure }: OpenRouterConnection, { name, detail: offer }: Connector): ReactNode {
  switch (phase) {
    case 'SignedOut':
      return offer;
    case 'SigningIn':
      return 'Waiting for the browser…';
    case 'Connected':
      return 'Key "Kstack"';
    case 'FromEnvironment':
      return (
        <>
          Using <code>OPENROUTER_API_KEY</code>
        </>
      );
    case 'Failed':
      return failure ? failureText[failure](name) : offer;
  }
}

function OpenRouterRow({
  connector,
  connection,
  actions,
}: {
  connector: Connector;
  connection: OpenRouterConnection;
  actions: OpenRouterActions;
}) {
  const [confirming, setConfirming] = useState(false);
  const { phase } = connection;
  // The key is still in the keyring: only another Disconnect moves on, so no Connect.
  const notDeleted = phase === 'Failed' && connection.failure === 'NotDeleted';
  return (
    <li className="flex flex-col gap-2 px-4 py-2.5">
      <div className="flex items-center gap-3">
        <RowText name={connector.name} detail={openRouterDetail(connection, connector)} />
        {phase === 'SigningIn' && (
          <Button variant="outline" size="sm" onClick={actions.cancel}>
            Cancel
          </Button>
        )}
        {(phase === 'SignedOut' || (phase === 'Failed' && !notDeleted)) && (
          <Button variant="outline" size="sm" onClick={actions.connect}>
            Connect
          </Button>
        )}
        {phase === 'Connected' && <OpenRouterMenu actions={actions} onDisconnect={() => setConfirming(true)} />}
      </div>
      {notDeleted && (
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          <p className="flex-1">
            It will be used again after a restart. Retry, or delete the key labelled &quot;Kstack&quot; on OpenRouter.
          </p>
          <Button variant="outline" size="xs" onClick={actions.openKeys}>
            Open OpenRouter settings
          </Button>
          <Button variant="outline" size="xs" onClick={actions.disconnect}>
            Retry
          </Button>
        </div>
      )}
      <Dialog
        open={confirming}
        onOpenChange={setConfirming}
        title="Disconnect OpenRouter?"
        description='Kstack will forget this key. It stays active on OpenRouter until you delete the key labelled "Kstack" in your OpenRouter settings.'
      >
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" className="mr-auto" onClick={actions.openKeys}>
            Open OpenRouter settings
          </Button>
          <Button type="button" variant="outline" onClick={() => setConfirming(false)}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={() => {
              setConfirming(false);
              actions.disconnect();
            }}
          >
            Disconnect
          </Button>
        </div>
      </Dialog>
    </li>
  );
}

function OpenRouterMenu({ actions, onDisconnect }: { actions: OpenRouterActions; onDisconnect: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="icon" className="size-7 rounded-md" />}
        aria-label="Actions for OpenRouter"
      >
        <Ellipsis className="size-4" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={actions.openKeys}>
          Open OpenRouter settings
          <ExternalLink className="ml-auto" />
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onClick={onDisconnect}>
          Disconnect
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
