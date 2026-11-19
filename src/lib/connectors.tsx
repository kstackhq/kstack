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
// ChatGPT, OpenRouter, AWS and GitHub are real: each entry folds its connection's
// watch, and the sidecar runs the sign-in and opens the browser itself. The rest
// are placeholders.
import { useCallback, useMemo } from 'react';
import { useMutation } from 'urql';

import { graphql } from '@/gql';
import type {
  AwsConnectionWatchSubscription,
  ChatGptConnectionWatchSubscription,
  GitHubConnectionWatchSubscription,
  OpenRouterConnectionWatchSubscription,
} from '@/gql/graphql';
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

/** The AWS connector's id. */
export const AWS_ID = 'aws';

export type AWSConnection = AwsConnectionWatchSubscription['awsConnectionWatch'];

/** What the AWS entry can do. Each outcome arrives on the watch. */
export type AWSActions = {
  connect: () => void;
  cancel: () => void;
  disconnect: () => void;
};

/** The GitHub connector's id: the sidecar's `github.ID`. */
export const GITHUB_ID = 'github';

export type GitHubConnection = GitHubConnectionWatchSubscription['gitHubConnectionWatch'];

/** What the GitHub entry can do. Each outcome arrives on the watch. */
export type GitHubActions = {
  connect: () => void;
  cancel: () => void;
  disconnect: () => void;
  openSettings: () => void;
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
  aws?: { connection: AWSConnection; actions: AWSActions };
  github?: { connection: GitHubConnection; actions: GitHubActions };
};

export type Connectors = {
  connectors: Connector[];
  // Every watch's first frame has landed; nothing draws before them.
  loaded: boolean;
};

// The connectors Kstack plans to ship; the tab sorts them by name. Each but ChatGPT,
// OpenRouter, AWS and GitHub is a placeholder until its backend exists.
const PLANNED: Connector[] = [
  { id: GITHUB_ID, name: 'GitHub', detail: 'Repositories for git and gh in the sandbox', connected: false },
  { id: AWS_ID, name: 'AWS', detail: 'Cloud resources and Bedrock models', connected: false },
  { id: 'google', name: 'Google', detail: 'Cloud resources and Gemini models', connected: false },
  { id: 'azure', name: 'Azure', detail: 'Cloud resources and Foundry models', connected: false },
  { id: CHATGPT_ID, name: 'ChatGPT', detail: 'OpenAI models on your ChatGPT plan', connected: false },
  { id: OPENROUTER_ID, name: 'OpenRouter', detail: 'Many providers on one account', connected: false },
  { id: 'gitlab', name: 'GitLab', detail: 'Code, pipelines and the container registry', connected: false },
  { id: 'atlassian', name: 'Atlassian', detail: 'Jira issues, Confluence pages and Opsgenie alerts', connected: false },
  { id: 'linear', name: 'Linear', detail: 'Issues and projects', connected: false },
  { id: 'datadog', name: 'Datadog', detail: 'Metrics, logs, traces and monitors', connected: false },
  { id: 'grafana', name: 'Grafana', detail: 'Dashboards, alerts, Loki logs and Mimir metrics', connected: false },
  { id: 'victoriametrics', name: 'VictoriaMetrics', detail: 'Metrics and logs', connected: false },
  { id: 'clickhouse', name: 'ClickHouse', detail: 'Queries over your ClickHouse Cloud data', connected: false },
  { id: 'newrelic', name: 'New Relic', detail: 'Metrics, logs, traces and alerts', connected: false },
  { id: 'honeycomb', name: 'Honeycomb', detail: 'Traces and events', connected: false },
  { id: 'elastic', name: 'Elastic', detail: 'Logs and search over Elastic Cloud', connected: false },
  { id: 'splunk', name: 'Splunk', detail: 'Logs and Observability Cloud', connected: false },
  { id: 'sentry', name: 'Sentry', detail: 'Errors and releases', connected: false },
  { id: 'pagerduty', name: 'PagerDuty', detail: 'Incidents and on-call schedules', connected: false },
  { id: 'slack', name: 'Slack', detail: 'Channels and incident threads', connected: false },
  { id: 'cloudflare', name: 'Cloudflare', detail: 'DNS, tunnels and edge config', connected: false },
  { id: 'digitalocean', name: 'DigitalOcean', detail: 'Cloud resources and DOKS clusters', connected: false },
  { id: 'dockerhub', name: 'Docker Hub', detail: 'Images and tags', connected: false },
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

const AWSConnectionWatchDocument = graphql(`
  subscription AWSConnectionWatch {
    awsConnectionWatch {
      phase
      arn
      failure
    }
  }
`);

const AWSConnectDocument = graphql(`
  mutation AWSConnect {
    awsConnect
  }
`);

const AWSConnectCancelDocument = graphql(`
  mutation AWSConnectCancel {
    awsConnectCancel
  }
`);

const AWSDisconnectDocument = graphql(`
  mutation AWSDisconnect {
    awsDisconnect
  }
`);

const GitHubConnectionWatchDocument = graphql(`
  subscription GitHubConnectionWatch {
    gitHubConnectionWatch {
      phase
      login
      userCode
      failure
    }
  }
`);

const GitHubConnectDocument = graphql(`
  mutation GitHubConnect {
    gitHubConnect
  }
`);

const GitHubConnectCancelDocument = graphql(`
  mutation GitHubConnectCancel {
    gitHubConnectCancel
  }
`);

const GitHubDisconnectDocument = graphql(`
  mutation GitHubDisconnect {
    gitHubDisconnect
  }
`);

const GitHubSettingsOpenDocument = graphql(`
  mutation GitHubSettingsOpen {
    gitHubSettingsOpen
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
  const { data: aws } = useWatchSubscription<AwsConnectionWatchSubscription, AWSConnection>(
    { query: AWSConnectionWatchDocument },
    perFrame((_prev, next) => next.awsConnectionWatch),
    { unshared: true },
  );
  const { data: gitHub } = useWatchSubscription<GitHubConnectionWatchSubscription, GitHubConnection>(
    { query: GitHubConnectionWatchDocument },
    perFrame((_prev, next) => next.gitHubConnectionWatch),
    { unshared: true },
  );
  const chatGPTActions = useChatGPTActions();
  const openRouterActions = useOpenRouterActions();
  const awsActions = useAWSActions();
  const gitHubActions = useGitHubActions();
  return useMemo(() => {
    if (!chatGPT || !openRouter || !aws || !gitHub) return { connectors: PLANNED, loaded: false };
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
        case AWS_ID: {
          const connected = aws.phase === 'Connected';
          return {
            ...c,
            connected,
            detail: (connected && aws.arn) || c.detail,
            aws: { connection: aws, actions: awsActions },
          };
        }
        case GITHUB_ID: {
          const connected = gitHub.phase === 'Connected';
          return {
            ...c,
            connected,
            detail: (connected && gitHub.login) || c.detail,
            github: { connection: gitHub, actions: gitHubActions },
          };
        }
        default:
          return c;
      }
    });
    return { connectors, loaded: true };
  }, [chatGPT, chatGPTActions, openRouter, openRouterActions, aws, awsActions, gitHub, gitHubActions]);
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

function useAWSActions(): AWSActions {
  const [, connect] = useMutation(AWSConnectDocument);
  const [, cancel] = useMutation(AWSConnectCancelDocument);
  const [, disconnect] = useMutation(AWSDisconnectDocument);
  return useMemo(
    () => ({
      connect: () => void connect({}),
      cancel: () => void cancel({}),
      disconnect: () => void disconnect({}),
    }),
    [connect, cancel, disconnect],
  );
}

function useGitHubActions(): GitHubActions {
  const [, connect] = useMutation(GitHubConnectDocument);
  const [, cancel] = useMutation(GitHubConnectCancelDocument);
  const [, disconnect] = useMutation(GitHubDisconnectDocument);
  const [, openSettings] = useMutation(GitHubSettingsOpenDocument);
  return useMemo(
    () => ({
      connect: () => void connect({}),
      cancel: () => void cancel({}),
      disconnect: () => void disconnect({}),
      openSettings: () => void openSettings({}),
    }),
    [connect, cancel, disconnect, openSettings],
  );
}
