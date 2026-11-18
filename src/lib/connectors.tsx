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
// ChatGPT is real: its entry folds `chatGPTConnectionWatch`, and the sidecar runs
// its sign-in and opens the browser itself. The rest are placeholders.
import { useCallback, useMemo } from 'react';
import { useMutation } from 'urql';

import { graphql } from '@/gql';
import type { ChatGptConnectionWatchSubscription } from '@/gql/graphql';
import { perFrame, useWatchSubscription } from '@/lib/graphql/use-watch-subscription';

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

export type Connector = {
  id: string;
  name: string;
  // The second line: the account, profile or what the service offers.
  detail: string;
  connected: boolean;
  // The label of the item that opens the service's own settings, if it has one.
  manageLabel?: string;
  // ChatGPT's own: its connection and actions. Absent on a placeholder.
  chatgpt?: { connection: ChatGPTConnection; actions: ChatGPTActions };
};

export type Connectors = {
  connectors: Connector[];
  // The ChatGPT watch's first frame has landed; nothing draws before it.
  loaded: boolean;
};

// The connectors Kstack plans to ship, in the tab's order. Each but ChatGPT is a
// placeholder until its backend exists.
const PLANNED: Connector[] = [
  { id: 'github', name: 'GitHub', detail: 'Code resources and Copilot models', connected: false },
  { id: 'aws', name: 'AWS', detail: 'Cloud resources and Bedrock models', connected: false },
  { id: 'google', name: 'Google', detail: 'Cloud resources and Gemini models', connected: false },
  { id: 'azure', name: 'Azure', detail: 'Cloud resources and Foundry models', connected: false },
  { id: CHATGPT_ID, name: 'ChatGPT', detail: 'OpenAI models on your ChatGPT plan', connected: false },
  { id: 'openrouter', name: 'OpenRouter', detail: 'Many providers on one account', connected: false },
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

/** Opens ChatGPT's usage settings in the browser, at the sidecar's own address. */
export function useOpenChatGPTUsage(): () => void {
  const [, run] = useMutation(ChatGPTUsageOpenDocument);
  return useCallback(() => void run({}), [run]);
}

export function useConnectors(): Connectors {
  // Unshared: a gauge's first frame is its snapshot, and the tab remounts each time
  // it is shown.
  const { data } = useWatchSubscription<ChatGptConnectionWatchSubscription, ChatGPTConnection>(
    { query: ChatGPTConnectionWatchDocument },
    perFrame((_prev, next) => next.chatGPTConnectionWatch),
    { unshared: true },
  );
  const [, runConnect] = useMutation(ChatGPTConnectDocument);
  const [, runCancel] = useMutation(ChatGPTConnectCancelDocument);
  const [, runDisconnect] = useMutation(ChatGPTDisconnectDocument);
  const openUsage = useOpenChatGPTUsage();
  // A mutation's error reaches the error bus; its outcome is the watch's.
  const actions = useMemo<ChatGPTActions>(
    () => ({
      connect: (fresh) => void runConnect({ fresh }),
      cancel: () => void runCancel({}),
      disconnect: () => void runDisconnect({}),
      openUsage,
    }),
    [runConnect, runCancel, runDisconnect, openUsage],
  );
  return useMemo(() => {
    if (!data) return { connectors: PLANNED, loaded: false };
    const connectors = PLANNED.map((c) => {
      if (c.id !== CHATGPT_ID) return c;
      const connected = data.phase === 'Connected';
      return {
        ...c,
        connected,
        detail: (connected && data.email) || c.detail,
        chatgpt: { connection: data, actions },
      };
    });
    return { connectors, loaded: true };
  }, [data, actions]);
}
