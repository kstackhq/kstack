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

// The services Kstack signs in to, whose credentials providers and tools use.
// ChatGPT and OpenRouter are real: each entry folds its connection's watch, and
// the sidecar runs the sign-in and opens the browser itself. The rest are
// placeholders.
import { useCallback, useMemo } from 'react';
import { useMutation } from 'urql';

import { graphql } from '@/gql';
import type { ChatGptConnectionWatchSubscription, OpenRouterConnectionWatchSubscription } from '@/gql/graphql';
import { perFrame, useWatchSubscription } from '@/lib/graphql/use-watch-subscription';

/** Why a connector's last attempt, session or key ended: one enum for every connector. */
export type { ConnectorFailure } from '@/gql/graphql';

/** The ChatGPT plan's provider id, and its connector's: the sidecar's `catalog.ChatGPTID`. */
export const CHATGPT_ID = 'chatgpt';

export type ChatGPTConnection = ChatGptConnectionWatchSubscription['chatGPTConnectionWatch'];

/** What a connected or failed ChatGPT entry can do. Each outcome arrives on the watch. */
export type ChatGPTActions = {
  connect: (fresh: boolean) => void;
  cancel: () => void;
  disconnect: () => void;
  openUsage: () => void;
};

/** The OpenRouter connector's id, and its provider's: the sidecar's `openrouter` row. */
export const OPENROUTER_ID = 'openrouter';

export type OpenRouterConnection = OpenRouterConnectionWatchSubscription['openRouterConnectionWatch'];

/** What the OpenRouter entry can do. Each outcome arrives on the watch. */
export type OpenRouterActions = {
  connect: () => void;
  cancel: () => void;
  disconnect: () => void;
  openKeys: () => void;
};

export type Connector = {
  id: string;
  name: string;
  // The second line: the account, profile or what the service offers.
  detail: string;
  connected: boolean;
  // The label of the item that opens the service's own settings, if it has one.
  manageLabel?: string;
  // A real connector's own: its connection and actions. Absent on every other entry.
  chatgpt?: { connection: ChatGPTConnection; actions: ChatGPTActions };
  openrouter?: { connection: OpenRouterConnection; actions: OpenRouterActions };
};

export type Connectors = {
  connectors: Connector[];
  // Both watches' first frames have landed; nothing draws before them.
  loaded: boolean;
};

// The connectors Kstack plans to ship, in the tab's order. Each but ChatGPT and
// OpenRouter is a placeholder until its backend exists.
const PLANNED: Connector[] = [
  { id: 'github', name: 'GitHub', detail: 'Code resources and Copilot models', connected: false },
  { id: 'aws', name: 'AWS', detail: 'Cloud resources and Bedrock models', connected: false },
  { id: 'google', name: 'Google', detail: 'Cloud resources and Gemini models', connected: false },
  { id: 'azure', name: 'Azure', detail: 'Cloud resources and Foundry models', connected: false },
  { id: CHATGPT_ID, name: 'ChatGPT', detail: 'OpenAI models on your ChatGPT plan', connected: false },
  { id: OPENROUTER_ID, name: 'OpenRouter', detail: 'Many providers on one account', connected: false },
];

const ChatGPTConnectionWatchDocument = graphql(`
  subscription ChatGPTConnectionWatch {
    chatGPTConnectionWatch {
      phase
      email
      failure
      registered
      revocationUnconfirmed
    }
  }
`);

const ChatGPTConnectDocument = graphql(`
  mutation ChatGPTConnect($fresh: Boolean!) {
    chatGPTConnect(fresh: $fresh)
  }
`);

const ChatGPTConnectCancelDocument = graphql(`
  mutation ChatGPTConnectCancel {
    chatGPTConnectCancel
  }
`);

const ChatGPTDisconnectDocument = graphql(`
  mutation ChatGPTDisconnect {
    chatGPTDisconnect
  }
`);

const ChatGPTUsageOpenDocument = graphql(`
  mutation ChatGPTUsageOpen {
    chatGPTUsageOpen
  }
`);

const OpenRouterConnectionWatchDocument = graphql(`
  subscription OpenRouterConnectionWatch {
    openRouterConnectionWatch {
      phase
      failure
    }
  }
`);

const OpenRouterConnectDocument = graphql(`
  mutation OpenRouterConnect {
    openRouterConnect
  }
`);

const OpenRouterConnectCancelDocument = graphql(`
  mutation OpenRouterConnectCancel {
    openRouterConnectCancel
  }
`);

const OpenRouterDisconnectDocument = graphql(`
  mutation OpenRouterDisconnect {
    openRouterDisconnect
  }
`);

const OpenRouterKeysOpenDocument = graphql(`
  mutation OpenRouterKeysOpen {
    openRouterKeysOpen
  }
`);

/** Opens ChatGPT's usage settings in the browser, at the sidecar's own address. */
export function useOpenChatGPTUsage(): () => void {
  const [, run] = useMutation(ChatGPTUsageOpenDocument);
  return useCallback(() => void run({}), [run]);
}

export function useConnectors(): Connectors {
  // Unshared: a gauge's first frame is its snapshot, and the tab remounts each time
  // it is shown.
  const { data: chatGPT } = useWatchSubscription<ChatGptConnectionWatchSubscription, ChatGPTConnection>(
    { query: ChatGPTConnectionWatchDocument },
    perFrame((_prev, next) => next.chatGPTConnectionWatch),
    { unshared: true },
  );
  const { data: openRouter } = useWatchSubscription<OpenRouterConnectionWatchSubscription, OpenRouterConnection>(
    { query: OpenRouterConnectionWatchDocument },
    perFrame((_prev, next) => next.openRouterConnectionWatch),
    { unshared: true },
  );
  const chatGPTActions = useChatGPTActions();
  const openRouterActions = useOpenRouterActions();
  return useMemo(() => {
    if (!chatGPT || !openRouter) return { connectors: PLANNED, loaded: false };
    const connectors = PLANNED.map((c): Connector => {
      switch (c.id) {
        case CHATGPT_ID: {
          const connected = chatGPT.phase === 'Connected';
          return {
            ...c,
            connected,
            detail: (connected && chatGPT.email) || c.detail,
            chatgpt: { connection: chatGPT, actions: chatGPTActions },
          };
        }
        case OPENROUTER_ID:
          return {
            ...c,
            connected: openRouter.phase === 'Connected',
            openrouter: { connection: openRouter, actions: openRouterActions },
          };
        default:
          return c;
      }
    });
    return { connectors, loaded: true };
  }, [chatGPT, chatGPTActions, openRouter, openRouterActions]);
}

// A mutation's error reaches the error bus; its outcome is the watch's.
function useChatGPTActions(): ChatGPTActions {
  const [, connect] = useMutation(ChatGPTConnectDocument);
  const [, cancel] = useMutation(ChatGPTConnectCancelDocument);
  const [, disconnect] = useMutation(ChatGPTDisconnectDocument);
  const openUsage = useOpenChatGPTUsage();
  return useMemo(
    () => ({
      connect: (fresh) => void connect({ fresh }),
      cancel: () => void cancel({}),
      disconnect: () => void disconnect({}),
      openUsage,
    }),
    [connect, cancel, disconnect, openUsage],
  );
}

function useOpenRouterActions(): OpenRouterActions {
  const [, connect] = useMutation(OpenRouterConnectDocument);
  const [, cancel] = useMutation(OpenRouterConnectCancelDocument);
  const [, disconnect] = useMutation(OpenRouterDisconnectDocument);
  const [, openKeys] = useMutation(OpenRouterKeysOpenDocument);
  return useMemo(
    () => ({
      connect: () => void connect({}),
      cancel: () => void cancel({}),
      disconnect: () => void disconnect({}),
      openKeys: () => void openKeys({}),
    }),
    [connect, cancel, disconnect, openKeys],
  );
}
