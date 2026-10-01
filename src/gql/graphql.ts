/* eslint-disable */
/** Internal type. DO NOT USE DIRECTLY. */
type Exact<T extends { [key: string]: unknown }> = { [K in keyof T]: T[K] };
/** Internal type. DO NOT USE DIRECTLY. */
export type Incremental<T> = T | { [P in keyof T]?: P extends ' $fragmentName' | '__typename' ? T[P] : never };
import type { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';
/**
 * Where the user's decision is. `Pending` until it commits, and after, when the call's turn ended
 * before anyone answered: the record of a question nobody answered. An `Approved` call whose status
 * is `NotRun` was approved and never started. `Abandoned` is a cluster write whose wait ended with no
 * decision while its call ran on; a call's own approval never takes it.
 */
export type ApprovalStatus =
  | 'Abandoned'
  | 'Approved'
  | 'Denied'
  | 'Pending';

/**
 * Where a background task is. A command ends `Exited`; an agent `Completed` (it answered) or
 * `Failed` (it could not). `Stopped` was ended by the model, the user, the app's quit, or an agent's
 * request that went unanswered; `Lost` was running when the sidecar stopped unexpectedly, and a
 * command may still be running.
 */
export type BackgroundTaskStatus =
  | 'Completed'
  | 'Exited'
  | 'Failed'
  | 'Lost'
  | 'Running'
  | 'Stopped';

/**
 * Who produced a message, matching the Messages API wire vocabulary. Not a display hint:
 * a `User` message carrying only tool results is machinery, not something a person typed.
 * What to draw comes from the content blocks.
 */
export type ChatMessageRole =
  | 'Assistant'
  | 'User';

/** How far along an assistant's turn is. A user message is always `Complete`. */
export type ChatMessageStatus =
  /** Stopped by `chatCancel` or by shutdown. The partial text is kept. */
  | 'Cancelled'
  | 'Complete'
  /** Ended badly; `error` says how. Never retried automatically. */
  | 'Failed'
  /** Still being written. Reconciled to `Failed` if the sidecar restarts. */
  | 'Streaming'
  /** Stopped on a command the user has not decided: in flight, drawn with the approval request. */
  | 'WaitingApproval';

/**
 * Which of the app's two modes a chat belongs to. Set when the chat is created and never
 * changed. Each mode lists only its own chats.
 */
export type ChatMode =
  | 'Chat'
  | 'Dashboard';

/** A condition's three-valued verdict, Kubernetes-style. */
export type ConditionStatus =
  /** The condition does not hold. */
  | 'False'
  /** The condition holds. */
  | 'True'
  /** The condition cannot currently be assessed. */
  | 'Unknown';

/**
 * Classifies one frame on a delta watch, mirroring a Kubernetes watch event. On
 * subscribe a watch replays the current set as `Added` frames (the snapshot), closes it
 * with one `Bookmark`, then streams live changes. A `Deleted` carries the object's
 * last-known state; the client keys on its `id`.
 */
export type DeltaFrameType =
  | 'Added'
  | 'Bookmark'
  | 'Deleted'
  | 'Modified';

/**
 * Classifies one frame on an event-timeline watch. Two values where `DeltaFrameType` has
 * four: an event log is a positioned log, not a mirrored set. The server delivers the
 * snapshot plus what grows above it and never reports a prune, so a run is only ever
 * upserted and there is no `Deleted`.
 */
export type EventFrameType =
  /** Closes the on-subscribe snapshot; sent once, carrying no run. */
  | 'Bookmark'
  /** A run to upsert by `Event.id`. */
  | 'Run';

/** An event's severity, mirroring the control plane's event type: Normal (✓) or Warning (✗). */
export type EventType =
  | 'Normal'
  | 'Warning';

/** Who last wrote a memory. */
export type MemoryAuthor =
  | 'Model'
  | 'User';

export type MemorySaveInput = {
  body: string;
  /** The cluster it is for. Null is every cluster. */
  clusterID?: string | null | undefined;
  /** The memory to rewrite. Null creates one. */
  id?: string | null | undefined;
  name: string;
};

/** What a call does, from the tool that made it. Never read from the call's arguments. */
export type ToolActionKind =
  | 'Command'
  | 'Delegate'
  | 'Edit'
  | 'Fetch'
  | 'KubeQuery'
  | 'Memory'
  | 'Read'
  | 'Search'
  | 'Stop'
  | 'Write';

/** Who ran a tool call: the sidecar, on the user's machine, or the model's provider, on its own servers. */
export type ToolCallRunsOn =
  | 'Provider'
  | 'Sidecar';

/**
 * Where a tool call is. `NotRun` never started; `Interrupted` started and was cut off with no
 * answer from its tool (a cancel, the loop's deadline, a crash), so it may have run; `Failed` is a
 * tool's own error.
 */
export type ToolCallStatus =
  | 'AwaitingApproval'
  | 'Denied'
  | 'Failed'
  | 'Interrupted'
  | 'NotRun'
  | 'Running'
  | 'Succeeded';

export type ChatCancelMutationVariables = Exact<{
  chatID: string;
}>;


export type ChatCancelMutation = { chatCancel: boolean };

export type ChatRenameMutationVariables = Exact<{
  id: string;
  title: string;
}>;


export type ChatRenameMutation = { chatRename: { id: string, title: string, updatedAt: string } };

export type ChatDeleteMutationVariables = Exact<{
  id: string;
}>;


export type ChatDeleteMutation = { chatDelete: boolean };

export type ApprovalDecideMutationVariables = Exact<{
  id: string;
  approve: boolean;
}>;


export type ApprovalDecideMutation = { approvalDecide: boolean };

export type BackgroundTaskStopMutationVariables = Exact<{
  id: string;
}>;


export type BackgroundTaskStopMutation = { backgroundTaskStop: boolean };

export type ClusterEnabledSetMutationVariables = Exact<{
  id: string;
  enabled: boolean;
}>;


export type ClusterEnabledSetMutation = { clusterEnabledSet: { id: string, spec: { enabled: boolean } } };

export type ClusterSyncEnabledSetMutationVariables = Exact<{
  id: string;
  syncEnabled: boolean;
}>;


export type ClusterSyncEnabledSetMutation = { clusterSyncEnabledSet: { id: string, spec: { syncEnabled: boolean } } };

export type ClusterCacheClearMutationVariables = Exact<{
  id: string;
}>;


export type ClusterCacheClearMutation = { clusterCacheClear: { id: string } };

export type ClusterCachedKindSyncEnabledSetMutationVariables = Exact<{
  id: string;
  syncEnabled: boolean;
}>;


export type ClusterCachedKindSyncEnabledSetMutation = { clusterCachedKindSyncEnabledSet: { id: string, spec: { syncEnabled: boolean } } };

export type ClusterDeleteMutationVariables = Exact<{
  id: string;
}>;


export type ClusterDeleteMutation = { clusterDelete: boolean };

export type ClusterConnectionRetryMutationVariables = Exact<{
  id: string;
}>;


export type ClusterConnectionRetryMutation = { clusterConnectionRetry: boolean };

export type ClusterConnectionEventsSubscriptionVariables = Exact<{
  id: string;
}>;


export type ClusterConnectionEventsSubscription = { clusterEventsWatch: { type: EventFrameType, event: { id: string, type: EventType, reason: string, message: string, count: number, firstAt: string, lastAt: string } | null } };

export type ClusterSyncEventsSubscriptionVariables = Exact<{
  id: string;
}>;


export type ClusterSyncEventsSubscription = { eventsWatch: { type: EventFrameType, event: { id: string, type: EventType, reason: string, message: string, count: number, firstAt: string, lastAt: string } | null } };

export type ClusterDiscoveryEventsSubscriptionVariables = Exact<{
  id: string;
}>;


export type ClusterDiscoveryEventsSubscription = { eventsWatch: { type: EventFrameType, event: { id: string, type: EventType, reason: string, message: string, count: number, firstAt: string, lastAt: string } | null } };

export type ClusterCacheSyncStatusSubscriptionVariables = Exact<{
  id: string;
  cacheID: string;
}>;


export type ClusterCacheSyncStatusSubscription = { clusterCacheSyncStatusWatch: { discovery: { reason: string, message: string }, kinds: Array<{ apiVersion: string, resource: string, reason: string, message: string, objectCount: number }> } };

export type ClusterCacheStatsSubscriptionVariables = Exact<{
  id: string;
  cacheID: string;
}>;


export type ClusterCacheStatsSubscription = { clusterCacheStatsWatch: { exists: boolean, bytes: number, dbBytes: number, walBytes: number, shmBytes: number, objectCount: number, kindCount: number } };

export type ClusterCachedKindsSubscriptionVariables = Exact<{
  cacheID: string;
}>;


export type ClusterCachedKindsSubscription = { clusterCachedKindsWatch: { type: DeltaFrameType, kind: { id: string, spec: { apiVersion: string, resource: string } } | null } };

export type ClusterScheduleSubscriptionVariables = Exact<{
  id: string;
}>;


export type ClusterScheduleSubscription = { clusterScheduleWatch: { nextRequeueAt: string | null, probing: boolean } };

export type MemorySaveMutationVariables = Exact<{
  input: MemorySaveInput;
}>;


export type MemorySaveMutation = { memorySave: { id: string } };

export type MemoryDeleteMutationVariables = Exact<{
  id: string;
}>;


export type MemoryDeleteMutation = { memoryDelete: boolean };

export type AuthStateWatchSubscriptionVariables = Exact<{ [key: string]: never; }>;


export type AuthStateWatchSubscription = { authStateWatch: { authenticated: boolean, identity: { sub: string, email: string, name: string } | null } };

export type AuthLoginStartMutationVariables = Exact<{ [key: string]: never; }>;


export type AuthLoginStartMutation = { authLoginStart: boolean };

export type AuthLogoutMutationVariables = Exact<{ [key: string]: never; }>;


export type AuthLogoutMutation = { authLogout: boolean };

export type ChatSendMutationVariables = Exact<{
  chatID?: string | null | undefined;
  mode: ChatMode;
  clusterID: string;
  providerID: string;
  modelID: string;
  effort: string;
  requestID: string;
  content: string;
}>;


export type ChatSendMutation = { chatSend: { id: string, chatID: string, seq: number, status: ChatMessageStatus } };

export type ChatsWatchSubscriptionVariables = Exact<{ [key: string]: never; }>;


export type ChatsWatchSubscription = { chatsWatch: { type: DeltaFrameType, chat: { id: string, title: string, mode: ChatMode, clusterID: string, createdAt: string, updatedAt: string, awaitingApproval: boolean, sandboxDisabled: boolean } | null } };

export type ChatMessagesWatchSubscriptionVariables = Exact<{
  chatID: string;
}>;


export type ChatMessagesWatchSubscription = { chatMessagesWatch: { type: DeltaFrameType, message: { id: string, chatID: string, seq: number, role: ChatMessageRole, content: unknown, thinking: string, status: ChatMessageStatus, awaitingApproval: boolean, error: string, model: string, effort: string, finishReason: string, provider: { id: string, label: string } | null, toolCalls: Array<{ id: string, name: string, actionKind: ToolActionKind | null, status: ToolCallStatus | null, runsOn: ToolCallRunsOn, agentCallID: string | null, output: string, action: { description: string, command: { text: string, cwd: string, background: boolean, sandboxed: boolean, outsideSandbox: boolean } | null, read: { path: string } | null, write: { path: string, content: string } | null, edit: { path: string, oldString: string, newString: string, replaceAll: boolean } | null, search: { query: string } | null, fetch: { url: string, host: string } | null, memory: { op: string, name: string, body: string, scope: string } | null, delegate: { prompt: string, agentType: string, model: string } | null, kubeQuery: { sql: string, limit: number } | null } | null, approval: { id: string, status: ApprovalStatus } | null, clusterWrites: Array<{ method: string, path: string, subresource: string, contentType: string, body: string, dryRun: boolean, approval: { id: string, status: ApprovalStatus } }>, background: { status: BackgroundTaskStatus, exitCode: number | null, report: string } | null }>, citations: Array<{ type: string, url: string, title: string, citedText: string }> } | null } };

export type ClusterCachedDataEventsWatchSubscriptionVariables = Exact<{
  id: string;
  cacheID: string;
}>;


export type ClusterCachedDataEventsWatchSubscription = { clusterCachedDataEventsWatch: { type: DeltaFrameType, cacheID: string, event: { uid: string, type: string, reason: string, message: string, count: number, firstSeen: string | null, lastSeen: string | null, involvedKind: string, involvedNamespace: string, involvedName: string } | null } };

export type ClusterCachedDataKindsWatchSubscriptionVariables = Exact<{
  id: string;
  cacheID: string;
}>;


export type ClusterCachedDataKindsWatchSubscription = { clusterCachedDataKindsWatch: { type: DeltaFrameType, cacheID: string, kind: { apiVersion: string, kind: string, resource: string, scope: string, isCRD: boolean, count: number, printerColumns: Array<{ name: string, type: string, jsonPath: string, priority: number }> } | null } };

export type ClusterCachedDataObjectsWatchSubscriptionVariables = Exact<{
  id: string;
  cacheID: string;
  apiVersion: string;
  resource: string;
}>;


export type ClusterCachedDataObjectsWatchSubscription = { clusterCachedDataObjectsWatch: { type: DeltaFrameType, cacheID: string, apiVersion: string, resource: string, object: { uid: string, apiVersion: string, kind: string, namespace: string, name: string, creationTimestamp: string | null, rawJSON: unknown } | null } };

export type ClustersWatchSubscriptionVariables = Exact<{ [key: string]: never; }>;


export type ClustersWatchSubscription = { clustersWatch: { type: DeltaFrameType, cluster: { id: string, deletionRequestedAt: string | null, spec: { name: string | null, syncEnabled: boolean, enabled: boolean, source: { kubeconfig: { context: string } | null } }, status: { source: { kubeconfig: { isPresent: boolean, isDefault: boolean, cluster: { name: string, entry: { server: string, insecureSkipTLSVerify: boolean } | null }, user: { name: string } } | null }, server: { uid: string | null } }, conditions: Array<{ type: string, status: ConditionStatus, reason: string, message: string, liveness: boolean, unconfirmed: boolean, transitionedAt: string }> } | null } };

export type ClusterCachesWatchSubscriptionVariables = Exact<{ [key: string]: never; }>;


export type ClusterCachesWatchSubscription = { clusterCachesWatch: { type: DeltaFrameType, cache: { id: string, clusterID: string, spec: { serverUid: string } } | null } };

export type ClusterCacheHealthWatchSubscriptionVariables = Exact<{ [key: string]: never; }>;


export type ClusterCacheHealthWatchSubscription = { clusterCacheHealthWatch: { cacheID: string, status: ConditionStatus, reason: string, totalKinds: number, unhealthyKinds: number, pausedKinds: number, lastUpdateAt: string | null, lastLiveAt: string | null, unhealthyKindRefs: Array<{ apiVersion: string, resource: string }> } };

export type MemoriesWatchSubscriptionVariables = Exact<{
  clusterID: string;
}>;


export type MemoriesWatchSubscription = { memoriesWatch: { type: DeltaFrameType, memory: { id: string, clusterID: string | null, name: string, body: string, writtenBy: MemoryAuthor, updatedAt: string } | null } };

export type ModelsQueryVariables = Exact<{ [key: string]: never; }>;


export type ModelsQuery = { models: Array<{ id: string, label: string, efforts: Array<string>, defaultEffort: string, provider: { id: string, label: string } }> };

export type SandboxQueryVariables = Exact<{ [key: string]: never; }>;


export type SandboxQuery = { sandbox: { available: boolean } };


export const ChatCancelDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ChatCancel"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"chatID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ChatID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"chatCancel"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"chatID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"chatID"}}}]}]}}]} as unknown as DocumentNode<ChatCancelMutation, ChatCancelMutationVariables>;
export const ChatRenameDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ChatRename"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ChatID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"title"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"chatRename"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"title"},"value":{"kind":"Variable","name":{"kind":"Name","value":"title"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"title"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]}}]} as unknown as DocumentNode<ChatRenameMutation, ChatRenameMutationVariables>;
export const ChatDeleteDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ChatDelete"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ChatID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"chatDelete"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<ChatDeleteMutation, ChatDeleteMutationVariables>;
export const ApprovalDecideDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ApprovalDecide"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ApprovalID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"approve"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Boolean"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"approvalDecide"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"approve"},"value":{"kind":"Variable","name":{"kind":"Name","value":"approve"}}}]}]}}]} as unknown as DocumentNode<ApprovalDecideMutation, ApprovalDecideMutationVariables>;
export const BackgroundTaskStopDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"BackgroundTaskStop"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ToolCallID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"backgroundTaskStop"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<BackgroundTaskStopMutation, BackgroundTaskStopMutationVariables>;
export const ClusterEnabledSetDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ClusterEnabledSet"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"enabled"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Boolean"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterEnabledSet"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"enabled"},"value":{"kind":"Variable","name":{"kind":"Name","value":"enabled"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"spec"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"enabled"}}]}}]}}]}}]} as unknown as DocumentNode<ClusterEnabledSetMutation, ClusterEnabledSetMutationVariables>;
export const ClusterSyncEnabledSetDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ClusterSyncEnabledSet"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"syncEnabled"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Boolean"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterSyncEnabledSet"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"syncEnabled"},"value":{"kind":"Variable","name":{"kind":"Name","value":"syncEnabled"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"spec"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"syncEnabled"}}]}}]}}]}}]} as unknown as DocumentNode<ClusterSyncEnabledSetMutation, ClusterSyncEnabledSetMutationVariables>;
export const ClusterCacheClearDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ClusterCacheClear"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ObjectID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterCacheClear"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}}]}}]}}]} as unknown as DocumentNode<ClusterCacheClearMutation, ClusterCacheClearMutationVariables>;
export const ClusterCachedKindSyncEnabledSetDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ClusterCachedKindSyncEnabledSet"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ObjectID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"syncEnabled"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"Boolean"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterCachedKindSyncEnabledSet"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"syncEnabled"},"value":{"kind":"Variable","name":{"kind":"Name","value":"syncEnabled"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"spec"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"syncEnabled"}}]}}]}}]}}]} as unknown as DocumentNode<ClusterCachedKindSyncEnabledSetMutation, ClusterCachedKindSyncEnabledSetMutationVariables>;
export const ClusterDeleteDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ClusterDelete"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterDelete"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<ClusterDeleteMutation, ClusterDeleteMutationVariables>;
export const ClusterConnectionRetryDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ClusterConnectionRetry"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterConnectionRetry"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<ClusterConnectionRetryMutation, ClusterConnectionRetryMutationVariables>;
export const ClusterConnectionEventsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClusterConnectionEvents"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterEventsWatch"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"category"},"value":{"kind":"StringValue","value":"connection","block":false}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"event"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"count"}},{"kind":"Field","name":{"kind":"Name","value":"firstAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastAt"}}]}}]}}]}}]} as unknown as DocumentNode<ClusterConnectionEventsSubscription, ClusterConnectionEventsSubscriptionVariables>;
export const ClusterSyncEventsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClusterSyncEvents"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ObjectID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"eventsWatch"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"category"},"value":{"kind":"StringValue","value":"sync","block":false}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"event"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"count"}},{"kind":"Field","name":{"kind":"Name","value":"firstAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastAt"}}]}}]}}]}}]} as unknown as DocumentNode<ClusterSyncEventsSubscription, ClusterSyncEventsSubscriptionVariables>;
export const ClusterDiscoveryEventsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClusterDiscoveryEvents"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ObjectID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"eventsWatch"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"category"},"value":{"kind":"StringValue","value":"discovery","block":false}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"event"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"count"}},{"kind":"Field","name":{"kind":"Name","value":"firstAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastAt"}}]}}]}}]}}]} as unknown as DocumentNode<ClusterDiscoveryEventsSubscription, ClusterDiscoveryEventsSubscriptionVariables>;
export const ClusterCacheSyncStatusDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClusterCacheSyncStatus"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"cacheID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ObjectID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterCacheSyncStatusWatch"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"cacheID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"cacheID"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"discovery"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"message"}}]}},{"kind":"Field","name":{"kind":"Name","value":"kinds"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"apiVersion"}},{"kind":"Field","name":{"kind":"Name","value":"resource"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"objectCount"}}]}}]}}]}}]} as unknown as DocumentNode<ClusterCacheSyncStatusSubscription, ClusterCacheSyncStatusSubscriptionVariables>;
export const ClusterCacheStatsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClusterCacheStats"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"cacheID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ObjectID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterCacheStatsWatch"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"cacheID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"cacheID"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"exists"}},{"kind":"Field","name":{"kind":"Name","value":"bytes"}},{"kind":"Field","name":{"kind":"Name","value":"dbBytes"}},{"kind":"Field","name":{"kind":"Name","value":"walBytes"}},{"kind":"Field","name":{"kind":"Name","value":"shmBytes"}},{"kind":"Field","name":{"kind":"Name","value":"objectCount"}},{"kind":"Field","name":{"kind":"Name","value":"kindCount"}}]}}]}}]} as unknown as DocumentNode<ClusterCacheStatsSubscription, ClusterCacheStatsSubscriptionVariables>;
export const ClusterCachedKindsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClusterCachedKinds"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"cacheID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ObjectID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterCachedKindsWatch"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"cacheID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"cacheID"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"kind"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"spec"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"apiVersion"}},{"kind":"Field","name":{"kind":"Name","value":"resource"}}]}}]}}]}}]}}]} as unknown as DocumentNode<ClusterCachedKindsSubscription, ClusterCachedKindsSubscriptionVariables>;
export const ClusterScheduleDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClusterSchedule"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterScheduleWatch"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"nextRequeueAt"}},{"kind":"Field","name":{"kind":"Name","value":"probing"}}]}}]}}]} as unknown as DocumentNode<ClusterScheduleSubscription, ClusterScheduleSubscriptionVariables>;
export const MemorySaveDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"MemorySave"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"input"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"MemorySaveInput"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"memorySave"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"input"},"value":{"kind":"Variable","name":{"kind":"Name","value":"input"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}}]}}]}}]} as unknown as DocumentNode<MemorySaveMutation, MemorySaveMutationVariables>;
export const MemoryDeleteDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"MemoryDelete"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"MemoryID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"memoryDelete"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}}]}]}}]} as unknown as DocumentNode<MemoryDeleteMutation, MemoryDeleteMutationVariables>;
export const AuthStateWatchDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"AuthStateWatch"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"authStateWatch"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"authenticated"}},{"kind":"Field","name":{"kind":"Name","value":"identity"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"sub"}},{"kind":"Field","name":{"kind":"Name","value":"email"}},{"kind":"Field","name":{"kind":"Name","value":"name"}}]}}]}}]}}]} as unknown as DocumentNode<AuthStateWatchSubscription, AuthStateWatchSubscriptionVariables>;
export const AuthLoginStartDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AuthLoginStart"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"authLoginStart"}}]}}]} as unknown as DocumentNode<AuthLoginStartMutation, AuthLoginStartMutationVariables>;
export const AuthLogoutDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"AuthLogout"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"authLogout"}}]}}]} as unknown as DocumentNode<AuthLogoutMutation, AuthLogoutMutationVariables>;
export const ChatSendDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"mutation","name":{"kind":"Name","value":"ChatSend"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"chatID"}},"type":{"kind":"NamedType","name":{"kind":"Name","value":"ChatID"}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"mode"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ChatMode"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"clusterID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"providerID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"modelID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"effort"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"requestID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"content"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"chatSend"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"chatID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"chatID"}}},{"kind":"Argument","name":{"kind":"Name","value":"mode"},"value":{"kind":"Variable","name":{"kind":"Name","value":"mode"}}},{"kind":"Argument","name":{"kind":"Name","value":"clusterID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"clusterID"}}},{"kind":"Argument","name":{"kind":"Name","value":"providerID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"providerID"}}},{"kind":"Argument","name":{"kind":"Name","value":"modelID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"modelID"}}},{"kind":"Argument","name":{"kind":"Name","value":"effort"},"value":{"kind":"Variable","name":{"kind":"Name","value":"effort"}}},{"kind":"Argument","name":{"kind":"Name","value":"requestID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"requestID"}}},{"kind":"Argument","name":{"kind":"Name","value":"content"},"value":{"kind":"Variable","name":{"kind":"Name","value":"content"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"chatID"}},{"kind":"Field","name":{"kind":"Name","value":"seq"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}}]}}]} as unknown as DocumentNode<ChatSendMutation, ChatSendMutationVariables>;
export const ChatsWatchDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ChatsWatch"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"chatsWatch"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"chat"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"title"}},{"kind":"Field","name":{"kind":"Name","value":"mode"}},{"kind":"Field","name":{"kind":"Name","value":"clusterID"}},{"kind":"Field","name":{"kind":"Name","value":"createdAt"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}},{"kind":"Field","name":{"kind":"Name","value":"awaitingApproval"}},{"kind":"Field","name":{"kind":"Name","value":"sandboxDisabled"}}]}}]}}]}}]} as unknown as DocumentNode<ChatsWatchSubscription, ChatsWatchSubscriptionVariables>;
export const ChatMessagesWatchDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ChatMessagesWatch"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"chatID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ChatID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"chatMessagesWatch"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"chatID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"chatID"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"message"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"chatID"}},{"kind":"Field","name":{"kind":"Name","value":"seq"}},{"kind":"Field","name":{"kind":"Name","value":"role"}},{"kind":"Field","name":{"kind":"Name","value":"content"}},{"kind":"Field","name":{"kind":"Name","value":"thinking"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"awaitingApproval"}},{"kind":"Field","name":{"kind":"Name","value":"error"}},{"kind":"Field","name":{"kind":"Name","value":"model"}},{"kind":"Field","name":{"kind":"Name","value":"provider"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"label"}}]}},{"kind":"Field","name":{"kind":"Name","value":"effort"}},{"kind":"Field","name":{"kind":"Name","value":"finishReason"}},{"kind":"Field","name":{"kind":"Name","value":"toolCalls"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"actionKind"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"runsOn"}},{"kind":"Field","name":{"kind":"Name","value":"action"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"description"}},{"kind":"Field","name":{"kind":"Name","value":"command"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"text"}},{"kind":"Field","name":{"kind":"Name","value":"cwd"}},{"kind":"Field","name":{"kind":"Name","value":"background"}},{"kind":"Field","name":{"kind":"Name","value":"sandboxed"}},{"kind":"Field","name":{"kind":"Name","value":"outsideSandbox"}}]}},{"kind":"Field","name":{"kind":"Name","value":"read"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"path"}}]}},{"kind":"Field","name":{"kind":"Name","value":"write"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"path"}},{"kind":"Field","name":{"kind":"Name","value":"content"}}]}},{"kind":"Field","name":{"kind":"Name","value":"edit"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"path"}},{"kind":"Field","name":{"kind":"Name","value":"oldString"}},{"kind":"Field","name":{"kind":"Name","value":"newString"}},{"kind":"Field","name":{"kind":"Name","value":"replaceAll"}}]}},{"kind":"Field","name":{"kind":"Name","value":"search"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"query"}}]}},{"kind":"Field","name":{"kind":"Name","value":"fetch"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"url"}},{"kind":"Field","name":{"kind":"Name","value":"host"}}]}},{"kind":"Field","name":{"kind":"Name","value":"memory"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"op"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"body"}},{"kind":"Field","name":{"kind":"Name","value":"scope"}}]}},{"kind":"Field","name":{"kind":"Name","value":"delegate"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"prompt"}},{"kind":"Field","name":{"kind":"Name","value":"agentType"}},{"kind":"Field","name":{"kind":"Name","value":"model"}}]}},{"kind":"Field","name":{"kind":"Name","value":"kubeQuery"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"sql"}},{"kind":"Field","name":{"kind":"Name","value":"limit"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"agentCallID"}},{"kind":"Field","name":{"kind":"Name","value":"approval"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}},{"kind":"Field","name":{"kind":"Name","value":"clusterWrites"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"approval"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"status"}}]}},{"kind":"Field","name":{"kind":"Name","value":"method"}},{"kind":"Field","name":{"kind":"Name","value":"path"}},{"kind":"Field","name":{"kind":"Name","value":"subresource"}},{"kind":"Field","name":{"kind":"Name","value":"contentType"}},{"kind":"Field","name":{"kind":"Name","value":"body"}},{"kind":"Field","name":{"kind":"Name","value":"dryRun"}}]}},{"kind":"Field","name":{"kind":"Name","value":"output"}},{"kind":"Field","name":{"kind":"Name","value":"background"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"exitCode"}},{"kind":"Field","name":{"kind":"Name","value":"report"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"citations"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"url"}},{"kind":"Field","name":{"kind":"Name","value":"title"}},{"kind":"Field","name":{"kind":"Name","value":"citedText"}}]}}]}}]}}]}}]} as unknown as DocumentNode<ChatMessagesWatchSubscription, ChatMessagesWatchSubscriptionVariables>;
export const ClusterCachedDataEventsWatchDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClusterCachedDataEventsWatch"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"cacheID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ObjectID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterCachedDataEventsWatch"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"cacheID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"cacheID"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"cacheID"}},{"kind":"Field","name":{"kind":"Name","value":"event"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"uid"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"count"}},{"kind":"Field","name":{"kind":"Name","value":"firstSeen"}},{"kind":"Field","name":{"kind":"Name","value":"lastSeen"}},{"kind":"Field","name":{"kind":"Name","value":"involvedKind"}},{"kind":"Field","name":{"kind":"Name","value":"involvedNamespace"}},{"kind":"Field","name":{"kind":"Name","value":"involvedName"}}]}}]}}]}}]} as unknown as DocumentNode<ClusterCachedDataEventsWatchSubscription, ClusterCachedDataEventsWatchSubscriptionVariables>;
export const ClusterCachedDataKindsWatchDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClusterCachedDataKindsWatch"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"cacheID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ObjectID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterCachedDataKindsWatch"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"cacheID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"cacheID"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"cacheID"}},{"kind":"Field","name":{"kind":"Name","value":"kind"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"apiVersion"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"resource"}},{"kind":"Field","name":{"kind":"Name","value":"scope"}},{"kind":"Field","name":{"kind":"Name","value":"isCRD"}},{"kind":"Field","name":{"kind":"Name","value":"count"}},{"kind":"Field","name":{"kind":"Name","value":"printerColumns"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"jsonPath"}},{"kind":"Field","name":{"kind":"Name","value":"priority"}}]}}]}}]}}]}}]} as unknown as DocumentNode<ClusterCachedDataKindsWatchSubscription, ClusterCachedDataKindsWatchSubscriptionVariables>;
export const ClusterCachedDataObjectsWatchDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClusterCachedDataObjectsWatch"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"id"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"cacheID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ObjectID"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"apiVersion"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}},{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"resource"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"String"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterCachedDataObjectsWatch"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"id"},"value":{"kind":"Variable","name":{"kind":"Name","value":"id"}}},{"kind":"Argument","name":{"kind":"Name","value":"cacheID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"cacheID"}}},{"kind":"Argument","name":{"kind":"Name","value":"apiVersion"},"value":{"kind":"Variable","name":{"kind":"Name","value":"apiVersion"}}},{"kind":"Argument","name":{"kind":"Name","value":"resource"},"value":{"kind":"Variable","name":{"kind":"Name","value":"resource"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"cacheID"}},{"kind":"Field","name":{"kind":"Name","value":"apiVersion"}},{"kind":"Field","name":{"kind":"Name","value":"resource"}},{"kind":"Field","name":{"kind":"Name","value":"object"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"uid"}},{"kind":"Field","name":{"kind":"Name","value":"apiVersion"}},{"kind":"Field","name":{"kind":"Name","value":"kind"}},{"kind":"Field","name":{"kind":"Name","value":"namespace"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"creationTimestamp"}},{"kind":"Field","name":{"kind":"Name","value":"rawJSON"}}]}}]}}]}}]} as unknown as DocumentNode<ClusterCachedDataObjectsWatchSubscription, ClusterCachedDataObjectsWatchSubscriptionVariables>;
export const ClustersWatchDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClustersWatch"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clustersWatch"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"cluster"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"deletionRequestedAt"}},{"kind":"Field","name":{"kind":"Name","value":"spec"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"syncEnabled"}},{"kind":"Field","name":{"kind":"Name","value":"enabled"}},{"kind":"Field","name":{"kind":"Name","value":"source"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kubeconfig"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"context"}}]}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"status"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"source"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kubeconfig"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cluster"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"entry"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"server"}},{"kind":"Field","name":{"kind":"Name","value":"insecureSkipTLSVerify"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"user"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"name"}}]}},{"kind":"Field","name":{"kind":"Name","value":"isPresent"}},{"kind":"Field","name":{"kind":"Name","value":"isDefault"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"server"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"uid"}}]}}]}},{"kind":"Field","name":{"kind":"Name","value":"conditions"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"message"}},{"kind":"Field","name":{"kind":"Name","value":"liveness"}},{"kind":"Field","name":{"kind":"Name","value":"unconfirmed"}},{"kind":"Field","name":{"kind":"Name","value":"transitionedAt"}}]}}]}}]}}]}}]} as unknown as DocumentNode<ClustersWatchSubscription, ClustersWatchSubscriptionVariables>;
export const ClusterCachesWatchDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClusterCachesWatch"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterCachesWatch"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"cache"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"clusterID"}},{"kind":"Field","name":{"kind":"Name","value":"spec"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"serverUid"}}]}}]}}]}}]}}]} as unknown as DocumentNode<ClusterCachesWatchSubscription, ClusterCachesWatchSubscriptionVariables>;
export const ClusterCacheHealthWatchDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"ClusterCacheHealthWatch"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"clusterCacheHealthWatch"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"cacheID"}},{"kind":"Field","name":{"kind":"Name","value":"status"}},{"kind":"Field","name":{"kind":"Name","value":"reason"}},{"kind":"Field","name":{"kind":"Name","value":"unhealthyKindRefs"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"apiVersion"}},{"kind":"Field","name":{"kind":"Name","value":"resource"}}]}},{"kind":"Field","name":{"kind":"Name","value":"totalKinds"}},{"kind":"Field","name":{"kind":"Name","value":"unhealthyKinds"}},{"kind":"Field","name":{"kind":"Name","value":"pausedKinds"}},{"kind":"Field","name":{"kind":"Name","value":"lastUpdateAt"}},{"kind":"Field","name":{"kind":"Name","value":"lastLiveAt"}}]}}]}}]} as unknown as DocumentNode<ClusterCacheHealthWatchSubscription, ClusterCacheHealthWatchSubscriptionVariables>;
export const MemoriesWatchDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"MemoriesWatch"},"variableDefinitions":[{"kind":"VariableDefinition","variable":{"kind":"Variable","name":{"kind":"Name","value":"clusterID"}},"type":{"kind":"NonNullType","type":{"kind":"NamedType","name":{"kind":"Name","value":"ClusterID"}}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"memoriesWatch"},"arguments":[{"kind":"Argument","name":{"kind":"Name","value":"clusterID"},"value":{"kind":"Variable","name":{"kind":"Name","value":"clusterID"}}}],"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"type"}},{"kind":"Field","name":{"kind":"Name","value":"memory"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"clusterID"}},{"kind":"Field","name":{"kind":"Name","value":"name"}},{"kind":"Field","name":{"kind":"Name","value":"body"}},{"kind":"Field","name":{"kind":"Name","value":"writtenBy"}},{"kind":"Field","name":{"kind":"Name","value":"updatedAt"}}]}}]}}]}}]} as unknown as DocumentNode<MemoriesWatchSubscription, MemoriesWatchSubscriptionVariables>;
export const ModelsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"Models"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"models"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"provider"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"label"}}]}},{"kind":"Field","name":{"kind":"Name","value":"id"}},{"kind":"Field","name":{"kind":"Name","value":"label"}},{"kind":"Field","name":{"kind":"Name","value":"efforts"}},{"kind":"Field","name":{"kind":"Name","value":"defaultEffort"}}]}}]}}]} as unknown as DocumentNode<ModelsQuery, ModelsQueryVariables>;
export const SandboxDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"Sandbox"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"sandbox"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"available"}}]}}]}}]} as unknown as DocumentNode<SandboxQuery, SandboxQueryVariables>;