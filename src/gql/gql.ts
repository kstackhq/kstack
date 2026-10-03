/* eslint-disable */
import * as types from './graphql';
import type { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';

/**
 * Map of all GraphQL operations in the project.
 *
 * This map has several performance disadvantages:
 * 1. It is not tree-shakeable, so it will include all operations in the project.
 * 2. It is not minifiable, so the string of a GraphQL query will be multiple times inside the bundle.
 * 3. It does not support dead code elimination, so it will add unused operations.
 *
 * Therefore it is highly recommended to use the babel or swc plugin for production.
 * Learn more about it here: https://the-guild.dev/graphql/codegen/plugins/presets/preset-client#reducing-bundle-size
 */
type Documents = {
    "\n  mutation ChatCancel($chatID: ChatID!) {\n    chatCancel(chatID: $chatID)\n  }\n": typeof types.ChatCancelDocument,
    "\n  mutation ChatRename($id: ChatID!, $title: String!) {\n    chatRename(id: $id, title: $title) {\n      id\n      title\n      updatedAt\n    }\n  }\n": typeof types.ChatRenameDocument,
    "\n  mutation ChatDelete($id: ChatID!) {\n    chatDelete(id: $id)\n  }\n": typeof types.ChatDeleteDocument,
    "\n  mutation ApprovalDecide($id: ApprovalID!, $approve: Boolean!) {\n    approvalDecide(id: $id, approve: $approve)\n  }\n": typeof types.ApprovalDecideDocument,
    "\n  mutation BackgroundTaskStop($id: ToolCallID!) {\n    backgroundTaskStop(id: $id)\n  }\n": typeof types.BackgroundTaskStopDocument,
    "\n  mutation ClusterEnabledSet($id: ClusterID!, $enabled: Boolean!) {\n    clusterEnabledSet(id: $id, enabled: $enabled) {\n      id\n      spec {\n        enabled\n      }\n    }\n  }\n": typeof types.ClusterEnabledSetDocument,
    "\n  mutation ClusterSyncEnabledSet($id: ClusterID!, $syncEnabled: Boolean!) {\n    clusterSyncEnabledSet(id: $id, syncEnabled: $syncEnabled) {\n      id\n      spec {\n        syncEnabled\n      }\n    }\n  }\n": typeof types.ClusterSyncEnabledSetDocument,
    "\n  mutation ClusterCacheClear($id: ObjectID!) {\n    clusterCacheClear(id: $id) {\n      id\n    }\n  }\n": typeof types.ClusterCacheClearDocument,
    "\n  mutation ClusterCachedKindSyncEnabledSet($id: ObjectID!, $syncEnabled: Boolean!) {\n    clusterCachedKindSyncEnabledSet(id: $id, syncEnabled: $syncEnabled) {\n      id\n      spec {\n        syncEnabled\n      }\n    }\n  }\n": typeof types.ClusterCachedKindSyncEnabledSetDocument,
    "\n  mutation ClusterDelete($id: ClusterID!) {\n    clusterDelete(id: $id)\n  }\n": typeof types.ClusterDeleteDocument,
    "\n  mutation ClusterConnectionRetry($id: ClusterID!) {\n    clusterConnectionRetry(id: $id)\n  }\n": typeof types.ClusterConnectionRetryDocument,
    "\n  subscription ClusterConnectionEvents($id: ClusterID!) {\n    clusterEventsWatch(id: $id, category: \"connection\") {\n      type\n      event {\n        id\n        type\n        reason\n        message\n        count\n        firstAt\n        lastAt\n      }\n    }\n  }\n": typeof types.ClusterConnectionEventsDocument,
    "\n  subscription ClusterSyncEvents($id: ObjectID!) {\n    eventsWatch(id: $id, category: \"sync\") {\n      type\n      event {\n        id\n        type\n        reason\n        message\n        count\n        firstAt\n        lastAt\n      }\n    }\n  }\n": typeof types.ClusterSyncEventsDocument,
    "\n  subscription ClusterDiscoveryEvents($id: ObjectID!) {\n    eventsWatch(id: $id, category: \"discovery\") {\n      type\n      event {\n        id\n        type\n        reason\n        message\n        count\n        firstAt\n        lastAt\n      }\n    }\n  }\n": typeof types.ClusterDiscoveryEventsDocument,
    "\n  subscription ClusterCacheSyncStatus($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCacheSyncStatusWatch(id: $id, cacheID: $cacheID) {\n      discovery {\n        reason\n        message\n      }\n      kinds {\n        apiVersion\n        resource\n        reason\n        message\n        objectCount\n      }\n    }\n  }\n": typeof types.ClusterCacheSyncStatusDocument,
    "\n  subscription ClusterCacheStats($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCacheStatsWatch(id: $id, cacheID: $cacheID) {\n      exists\n      bytes\n      dbBytes\n      walBytes\n      shmBytes\n      objectCount\n      kindCount\n    }\n  }\n": typeof types.ClusterCacheStatsDocument,
    "\n  subscription ClusterCachedKinds($cacheID: ObjectID!) {\n    clusterCachedKindsWatch(cacheID: $cacheID) {\n      type\n      kind {\n        id\n        spec {\n          apiVersion\n          resource\n        }\n      }\n    }\n  }\n": typeof types.ClusterCachedKindsDocument,
    "\n  subscription ClusterSchedule($id: ClusterID!) {\n    clusterScheduleWatch(id: $id) {\n      nextRequeueAt\n      probing\n    }\n  }\n": typeof types.ClusterScheduleDocument,
    "\n  mutation MemorySave($input: MemorySaveInput!) {\n    memorySave(input: $input) {\n      id\n    }\n  }\n": typeof types.MemorySaveDocument,
    "\n  mutation MemoryDelete($id: MemoryID!) {\n    memoryDelete(id: $id)\n  }\n": typeof types.MemoryDeleteDocument,
    "\n  query PermissionSettings {\n    permissionSettings {\n      defaultMode\n      contexts {\n        context\n        mode\n        source\n        pattern\n        own\n      }\n      rules {\n        id\n        line\n      }\n      destructive\n      held\n    }\n  }\n": typeof types.PermissionSettingsDocument,
    "\n  query SecurityRefused {\n    securityRefused {\n      field\n      value\n      reason\n    }\n  }\n": typeof types.SecurityRefusedDocument,
    "\n  mutation PermissionDefaultModeSet($mode: PermissionMode!) {\n    permissionDefaultModeSet(mode: $mode) {\n      held\n    }\n  }\n": typeof types.PermissionDefaultModeSetDocument,
    "\n  mutation PermissionModeSet($context: String!, $mode: PermissionMode!) {\n    permissionModeSet(context: $context, mode: $mode) {\n      held\n    }\n  }\n": typeof types.PermissionModeSetDocument,
    "\n  mutation PermissionModeClear($context: String!) {\n    permissionModeClear(context: $context) {\n      held\n    }\n  }\n": typeof types.PermissionModeClearDocument,
    "\n  mutation PermissionRuleAdd($input: PermissionRuleInput!) {\n    permissionRuleAdd(input: $input) {\n      held\n    }\n  }\n": typeof types.PermissionRuleAddDocument,
    "\n  mutation PermissionRuleRemove($id: String!) {\n    permissionRuleRemove(id: $id) {\n      held\n    }\n  }\n": typeof types.PermissionRuleRemoveDocument,
    "\n  mutation PermissionDiscardRefused($field: String!) {\n    permissionDiscardRefused(field: $field) {\n      held\n    }\n  }\n": typeof types.PermissionDiscardRefusedDocument,
    "\n  subscription AuthStateWatch {\n    authStateWatch {\n      authenticated\n      identity {\n        sub\n        email\n        name\n      }\n    }\n  }\n": typeof types.AuthStateWatchDocument,
    "\n  mutation AuthLoginStart {\n    authLoginStart\n  }\n": typeof types.AuthLoginStartDocument,
    "\n  mutation AuthLogout {\n    authLogout\n  }\n": typeof types.AuthLogoutDocument,
    "\n  mutation ChatSend(\n    $chatID: ChatID\n    $mode: ChatMode!\n    $clusterID: ClusterID!\n    $sandboxDisabled: Boolean!\n    $providerID: String!\n    $modelID: String!\n    $effort: String!\n    $requestID: String!\n    $content: String!\n  ) {\n    chatSend(\n      chatID: $chatID\n      mode: $mode\n      clusterID: $clusterID\n      sandboxDisabled: $sandboxDisabled\n      providerID: $providerID\n      modelID: $modelID\n      effort: $effort\n      requestID: $requestID\n      content: $content\n    ) {\n      id\n      chatID\n      seq\n      status\n    }\n  }\n": typeof types.ChatSendDocument,
    "\n  subscription ChatsWatch {\n    chatsWatch {\n      type\n      chat {\n        id\n        title\n        mode\n        clusterID\n        createdAt\n        updatedAt\n        awaitingApproval\n        sandboxDisabled\n      }\n    }\n  }\n": typeof types.ChatsWatchDocument,
    "\n  subscription ChatMessagesWatch($chatID: ChatID!) {\n    chatMessagesWatch(chatID: $chatID) {\n      type\n      message {\n        id\n        chatID\n        seq\n        role\n        content\n        thinking\n        status\n        awaitingApproval\n        error\n        model\n        provider {\n          id\n          label\n        }\n        effort\n        finishReason\n        toolCalls {\n          id\n          name\n          actionKind\n          status\n          runsOn\n          action {\n            description\n            command {\n              text\n              cwd\n              background\n              sandboxed\n            }\n            read {\n              path\n            }\n            write {\n              path\n              content\n            }\n            edit {\n              path\n              oldString\n              newString\n              replaceAll\n            }\n            search {\n              query\n            }\n            fetch {\n              url\n              host\n            }\n            memory {\n              op\n              name\n              body\n              scope\n            }\n            delegate {\n              prompt\n              agentType\n              model\n            }\n            kubeQuery {\n              sql\n              limit\n            }\n          }\n          agentCallID\n          approval {\n            id\n            status\n          }\n          clusterWrites {\n            approval {\n              id\n              status\n            }\n            method\n            path\n            subresource\n            contentType\n            body\n            dryRun\n            reason\n          }\n          output\n          background {\n            status\n            exitCode\n            report\n          }\n        }\n        citations {\n          type\n          url\n          title\n          citedText\n        }\n      }\n    }\n  }\n": typeof types.ChatMessagesWatchDocument,
    "\n  subscription ClusterCachedDataEventsWatch($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCachedDataEventsWatch(id: $id, cacheID: $cacheID) {\n      type\n      cacheID\n      event {\n        uid\n        type\n        reason\n        message\n        count\n        firstSeen\n        lastSeen\n        involvedKind\n        involvedNamespace\n        involvedName\n      }\n    }\n  }\n": typeof types.ClusterCachedDataEventsWatchDocument,
    "\n  subscription ClusterCachedDataKindsWatch($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCachedDataKindsWatch(id: $id, cacheID: $cacheID) {\n      type\n      cacheID\n      kind {\n        apiVersion\n        kind\n        resource\n        scope\n        isCRD\n        count\n        printerColumns {\n          name\n          type\n          jsonPath\n          priority\n        }\n      }\n    }\n  }\n": typeof types.ClusterCachedDataKindsWatchDocument,
    "\n  subscription ClusterCachedDataObjectsWatch(\n    $id: ClusterID!\n    $cacheID: ObjectID!\n    $apiVersion: String!\n    $resource: String!\n  ) {\n    clusterCachedDataObjectsWatch(id: $id, cacheID: $cacheID, apiVersion: $apiVersion, resource: $resource) {\n      type\n      cacheID\n      apiVersion\n      resource\n      object {\n        uid\n        apiVersion\n        kind\n        namespace\n        name\n        creationTimestamp\n        rawJSON\n      }\n    }\n  }\n": typeof types.ClusterCachedDataObjectsWatchDocument,
    "\n  subscription ClustersWatch {\n    clustersWatch {\n      type\n      cluster {\n        id\n        deletionRequestedAt\n        spec {\n          name\n          syncEnabled\n          enabled\n          source {\n            kubeconfig {\n              context\n            }\n          }\n        }\n        status {\n          source {\n            kubeconfig {\n              cluster {\n                name\n                entry {\n                  server\n                  insecureSkipTLSVerify\n                }\n              }\n              user {\n                name\n              }\n              isPresent\n              isDefault\n            }\n          }\n          server {\n            uid\n          }\n        }\n        conditions {\n          type\n          status\n          reason\n          message\n          liveness\n          unconfirmed\n          transitionedAt\n        }\n      }\n    }\n  }\n": typeof types.ClustersWatchDocument,
    "\n  subscription ClusterCachesWatch {\n    clusterCachesWatch {\n      type\n      cache {\n        id\n        clusterID\n        spec {\n          serverUid\n        }\n        # On-disk stats ride clusterCacheStatsWatch, subscribed per expanded row.\n      }\n    }\n  }\n": typeof types.ClusterCachesWatchDocument,
    "\n  subscription ClusterCacheHealthWatch {\n    clusterCacheHealthWatch {\n      cacheID\n      status\n      reason\n      unhealthyKindRefs {\n        apiVersion\n        resource\n      }\n      totalKinds\n      unhealthyKinds\n      pausedKinds\n      lastUpdateAt\n      lastLiveAt\n    }\n  }\n": typeof types.ClusterCacheHealthWatchDocument,
    "\n  subscription MemoriesWatch($clusterID: ClusterID!) {\n    memoriesWatch(clusterID: $clusterID) {\n      type\n      memory {\n        id\n        clusterID\n        name\n        body\n        writtenBy\n        updatedAt\n      }\n    }\n  }\n": typeof types.MemoriesWatchDocument,
    "\n  query Models {\n    models {\n      provider {\n        id\n        label\n      }\n      id\n      label\n      efforts\n      defaultEffort\n    }\n  }\n": typeof types.ModelsDocument,
    "\n  query SandboxPath {\n    sandboxPath {\n      dir\n      target\n      state\n      source\n      shared\n    }\n    sandboxPathFault\n    sandboxPathResolved\n  }\n": typeof types.SandboxPathDocument,
    "\n  mutation SandboxPathInclude($dir: String!, $target: String!) {\n    sandboxPathInclude(dir: $dir, target: $target) {\n      dir\n    }\n  }\n": typeof types.SandboxPathIncludeDocument,
    "\n  mutation SandboxPathRemove($dir: String!) {\n    sandboxPathRemove(dir: $dir) {\n      dir\n    }\n  }\n": typeof types.SandboxPathRemoveDocument,
    "\n  mutation SandboxPathRefresh {\n    sandboxPathRefresh {\n      dir\n    }\n  }\n": typeof types.SandboxPathRefreshDocument,
    "\n  mutation ChatSandboxDisabledSet($id: ChatID!, $sandboxDisabled: Boolean!) {\n    chatSandboxDisabledSet(id: $id, sandboxDisabled: $sandboxDisabled) {\n      id\n      sandboxDisabled\n    }\n  }\n": typeof types.ChatSandboxDisabledSetDocument,
    "\n  query Sandbox {\n    sandbox {\n      available\n    }\n  }\n": typeof types.SandboxDocument,
};
const documents: Documents = {
    "\n  mutation ChatCancel($chatID: ChatID!) {\n    chatCancel(chatID: $chatID)\n  }\n": types.ChatCancelDocument,
    "\n  mutation ChatRename($id: ChatID!, $title: String!) {\n    chatRename(id: $id, title: $title) {\n      id\n      title\n      updatedAt\n    }\n  }\n": types.ChatRenameDocument,
    "\n  mutation ChatDelete($id: ChatID!) {\n    chatDelete(id: $id)\n  }\n": types.ChatDeleteDocument,
    "\n  mutation ApprovalDecide($id: ApprovalID!, $approve: Boolean!) {\n    approvalDecide(id: $id, approve: $approve)\n  }\n": types.ApprovalDecideDocument,
    "\n  mutation BackgroundTaskStop($id: ToolCallID!) {\n    backgroundTaskStop(id: $id)\n  }\n": types.BackgroundTaskStopDocument,
    "\n  mutation ClusterEnabledSet($id: ClusterID!, $enabled: Boolean!) {\n    clusterEnabledSet(id: $id, enabled: $enabled) {\n      id\n      spec {\n        enabled\n      }\n    }\n  }\n": types.ClusterEnabledSetDocument,
    "\n  mutation ClusterSyncEnabledSet($id: ClusterID!, $syncEnabled: Boolean!) {\n    clusterSyncEnabledSet(id: $id, syncEnabled: $syncEnabled) {\n      id\n      spec {\n        syncEnabled\n      }\n    }\n  }\n": types.ClusterSyncEnabledSetDocument,
    "\n  mutation ClusterCacheClear($id: ObjectID!) {\n    clusterCacheClear(id: $id) {\n      id\n    }\n  }\n": types.ClusterCacheClearDocument,
    "\n  mutation ClusterCachedKindSyncEnabledSet($id: ObjectID!, $syncEnabled: Boolean!) {\n    clusterCachedKindSyncEnabledSet(id: $id, syncEnabled: $syncEnabled) {\n      id\n      spec {\n        syncEnabled\n      }\n    }\n  }\n": types.ClusterCachedKindSyncEnabledSetDocument,
    "\n  mutation ClusterDelete($id: ClusterID!) {\n    clusterDelete(id: $id)\n  }\n": types.ClusterDeleteDocument,
    "\n  mutation ClusterConnectionRetry($id: ClusterID!) {\n    clusterConnectionRetry(id: $id)\n  }\n": types.ClusterConnectionRetryDocument,
    "\n  subscription ClusterConnectionEvents($id: ClusterID!) {\n    clusterEventsWatch(id: $id, category: \"connection\") {\n      type\n      event {\n        id\n        type\n        reason\n        message\n        count\n        firstAt\n        lastAt\n      }\n    }\n  }\n": types.ClusterConnectionEventsDocument,
    "\n  subscription ClusterSyncEvents($id: ObjectID!) {\n    eventsWatch(id: $id, category: \"sync\") {\n      type\n      event {\n        id\n        type\n        reason\n        message\n        count\n        firstAt\n        lastAt\n      }\n    }\n  }\n": types.ClusterSyncEventsDocument,
    "\n  subscription ClusterDiscoveryEvents($id: ObjectID!) {\n    eventsWatch(id: $id, category: \"discovery\") {\n      type\n      event {\n        id\n        type\n        reason\n        message\n        count\n        firstAt\n        lastAt\n      }\n    }\n  }\n": types.ClusterDiscoveryEventsDocument,
    "\n  subscription ClusterCacheSyncStatus($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCacheSyncStatusWatch(id: $id, cacheID: $cacheID) {\n      discovery {\n        reason\n        message\n      }\n      kinds {\n        apiVersion\n        resource\n        reason\n        message\n        objectCount\n      }\n    }\n  }\n": types.ClusterCacheSyncStatusDocument,
    "\n  subscription ClusterCacheStats($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCacheStatsWatch(id: $id, cacheID: $cacheID) {\n      exists\n      bytes\n      dbBytes\n      walBytes\n      shmBytes\n      objectCount\n      kindCount\n    }\n  }\n": types.ClusterCacheStatsDocument,
    "\n  subscription ClusterCachedKinds($cacheID: ObjectID!) {\n    clusterCachedKindsWatch(cacheID: $cacheID) {\n      type\n      kind {\n        id\n        spec {\n          apiVersion\n          resource\n        }\n      }\n    }\n  }\n": types.ClusterCachedKindsDocument,
    "\n  subscription ClusterSchedule($id: ClusterID!) {\n    clusterScheduleWatch(id: $id) {\n      nextRequeueAt\n      probing\n    }\n  }\n": types.ClusterScheduleDocument,
    "\n  mutation MemorySave($input: MemorySaveInput!) {\n    memorySave(input: $input) {\n      id\n    }\n  }\n": types.MemorySaveDocument,
    "\n  mutation MemoryDelete($id: MemoryID!) {\n    memoryDelete(id: $id)\n  }\n": types.MemoryDeleteDocument,
    "\n  query PermissionSettings {\n    permissionSettings {\n      defaultMode\n      contexts {\n        context\n        mode\n        source\n        pattern\n        own\n      }\n      rules {\n        id\n        line\n      }\n      destructive\n      held\n    }\n  }\n": types.PermissionSettingsDocument,
    "\n  query SecurityRefused {\n    securityRefused {\n      field\n      value\n      reason\n    }\n  }\n": types.SecurityRefusedDocument,
    "\n  mutation PermissionDefaultModeSet($mode: PermissionMode!) {\n    permissionDefaultModeSet(mode: $mode) {\n      held\n    }\n  }\n": types.PermissionDefaultModeSetDocument,
    "\n  mutation PermissionModeSet($context: String!, $mode: PermissionMode!) {\n    permissionModeSet(context: $context, mode: $mode) {\n      held\n    }\n  }\n": types.PermissionModeSetDocument,
    "\n  mutation PermissionModeClear($context: String!) {\n    permissionModeClear(context: $context) {\n      held\n    }\n  }\n": types.PermissionModeClearDocument,
    "\n  mutation PermissionRuleAdd($input: PermissionRuleInput!) {\n    permissionRuleAdd(input: $input) {\n      held\n    }\n  }\n": types.PermissionRuleAddDocument,
    "\n  mutation PermissionRuleRemove($id: String!) {\n    permissionRuleRemove(id: $id) {\n      held\n    }\n  }\n": types.PermissionRuleRemoveDocument,
    "\n  mutation PermissionDiscardRefused($field: String!) {\n    permissionDiscardRefused(field: $field) {\n      held\n    }\n  }\n": types.PermissionDiscardRefusedDocument,
    "\n  subscription AuthStateWatch {\n    authStateWatch {\n      authenticated\n      identity {\n        sub\n        email\n        name\n      }\n    }\n  }\n": types.AuthStateWatchDocument,
    "\n  mutation AuthLoginStart {\n    authLoginStart\n  }\n": types.AuthLoginStartDocument,
    "\n  mutation AuthLogout {\n    authLogout\n  }\n": types.AuthLogoutDocument,
    "\n  mutation ChatSend(\n    $chatID: ChatID\n    $mode: ChatMode!\n    $clusterID: ClusterID!\n    $sandboxDisabled: Boolean!\n    $providerID: String!\n    $modelID: String!\n    $effort: String!\n    $requestID: String!\n    $content: String!\n  ) {\n    chatSend(\n      chatID: $chatID\n      mode: $mode\n      clusterID: $clusterID\n      sandboxDisabled: $sandboxDisabled\n      providerID: $providerID\n      modelID: $modelID\n      effort: $effort\n      requestID: $requestID\n      content: $content\n    ) {\n      id\n      chatID\n      seq\n      status\n    }\n  }\n": types.ChatSendDocument,
    "\n  subscription ChatsWatch {\n    chatsWatch {\n      type\n      chat {\n        id\n        title\n        mode\n        clusterID\n        createdAt\n        updatedAt\n        awaitingApproval\n        sandboxDisabled\n      }\n    }\n  }\n": types.ChatsWatchDocument,
    "\n  subscription ChatMessagesWatch($chatID: ChatID!) {\n    chatMessagesWatch(chatID: $chatID) {\n      type\n      message {\n        id\n        chatID\n        seq\n        role\n        content\n        thinking\n        status\n        awaitingApproval\n        error\n        model\n        provider {\n          id\n          label\n        }\n        effort\n        finishReason\n        toolCalls {\n          id\n          name\n          actionKind\n          status\n          runsOn\n          action {\n            description\n            command {\n              text\n              cwd\n              background\n              sandboxed\n            }\n            read {\n              path\n            }\n            write {\n              path\n              content\n            }\n            edit {\n              path\n              oldString\n              newString\n              replaceAll\n            }\n            search {\n              query\n            }\n            fetch {\n              url\n              host\n            }\n            memory {\n              op\n              name\n              body\n              scope\n            }\n            delegate {\n              prompt\n              agentType\n              model\n            }\n            kubeQuery {\n              sql\n              limit\n            }\n          }\n          agentCallID\n          approval {\n            id\n            status\n          }\n          clusterWrites {\n            approval {\n              id\n              status\n            }\n            method\n            path\n            subresource\n            contentType\n            body\n            dryRun\n            reason\n          }\n          output\n          background {\n            status\n            exitCode\n            report\n          }\n        }\n        citations {\n          type\n          url\n          title\n          citedText\n        }\n      }\n    }\n  }\n": types.ChatMessagesWatchDocument,
    "\n  subscription ClusterCachedDataEventsWatch($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCachedDataEventsWatch(id: $id, cacheID: $cacheID) {\n      type\n      cacheID\n      event {\n        uid\n        type\n        reason\n        message\n        count\n        firstSeen\n        lastSeen\n        involvedKind\n        involvedNamespace\n        involvedName\n      }\n    }\n  }\n": types.ClusterCachedDataEventsWatchDocument,
    "\n  subscription ClusterCachedDataKindsWatch($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCachedDataKindsWatch(id: $id, cacheID: $cacheID) {\n      type\n      cacheID\n      kind {\n        apiVersion\n        kind\n        resource\n        scope\n        isCRD\n        count\n        printerColumns {\n          name\n          type\n          jsonPath\n          priority\n        }\n      }\n    }\n  }\n": types.ClusterCachedDataKindsWatchDocument,
    "\n  subscription ClusterCachedDataObjectsWatch(\n    $id: ClusterID!\n    $cacheID: ObjectID!\n    $apiVersion: String!\n    $resource: String!\n  ) {\n    clusterCachedDataObjectsWatch(id: $id, cacheID: $cacheID, apiVersion: $apiVersion, resource: $resource) {\n      type\n      cacheID\n      apiVersion\n      resource\n      object {\n        uid\n        apiVersion\n        kind\n        namespace\n        name\n        creationTimestamp\n        rawJSON\n      }\n    }\n  }\n": types.ClusterCachedDataObjectsWatchDocument,
    "\n  subscription ClustersWatch {\n    clustersWatch {\n      type\n      cluster {\n        id\n        deletionRequestedAt\n        spec {\n          name\n          syncEnabled\n          enabled\n          source {\n            kubeconfig {\n              context\n            }\n          }\n        }\n        status {\n          source {\n            kubeconfig {\n              cluster {\n                name\n                entry {\n                  server\n                  insecureSkipTLSVerify\n                }\n              }\n              user {\n                name\n              }\n              isPresent\n              isDefault\n            }\n          }\n          server {\n            uid\n          }\n        }\n        conditions {\n          type\n          status\n          reason\n          message\n          liveness\n          unconfirmed\n          transitionedAt\n        }\n      }\n    }\n  }\n": types.ClustersWatchDocument,
    "\n  subscription ClusterCachesWatch {\n    clusterCachesWatch {\n      type\n      cache {\n        id\n        clusterID\n        spec {\n          serverUid\n        }\n        # On-disk stats ride clusterCacheStatsWatch, subscribed per expanded row.\n      }\n    }\n  }\n": types.ClusterCachesWatchDocument,
    "\n  subscription ClusterCacheHealthWatch {\n    clusterCacheHealthWatch {\n      cacheID\n      status\n      reason\n      unhealthyKindRefs {\n        apiVersion\n        resource\n      }\n      totalKinds\n      unhealthyKinds\n      pausedKinds\n      lastUpdateAt\n      lastLiveAt\n    }\n  }\n": types.ClusterCacheHealthWatchDocument,
    "\n  subscription MemoriesWatch($clusterID: ClusterID!) {\n    memoriesWatch(clusterID: $clusterID) {\n      type\n      memory {\n        id\n        clusterID\n        name\n        body\n        writtenBy\n        updatedAt\n      }\n    }\n  }\n": types.MemoriesWatchDocument,
    "\n  query Models {\n    models {\n      provider {\n        id\n        label\n      }\n      id\n      label\n      efforts\n      defaultEffort\n    }\n  }\n": types.ModelsDocument,
    "\n  query SandboxPath {\n    sandboxPath {\n      dir\n      target\n      state\n      source\n      shared\n    }\n    sandboxPathFault\n    sandboxPathResolved\n  }\n": types.SandboxPathDocument,
    "\n  mutation SandboxPathInclude($dir: String!, $target: String!) {\n    sandboxPathInclude(dir: $dir, target: $target) {\n      dir\n    }\n  }\n": types.SandboxPathIncludeDocument,
    "\n  mutation SandboxPathRemove($dir: String!) {\n    sandboxPathRemove(dir: $dir) {\n      dir\n    }\n  }\n": types.SandboxPathRemoveDocument,
    "\n  mutation SandboxPathRefresh {\n    sandboxPathRefresh {\n      dir\n    }\n  }\n": types.SandboxPathRefreshDocument,
    "\n  mutation ChatSandboxDisabledSet($id: ChatID!, $sandboxDisabled: Boolean!) {\n    chatSandboxDisabledSet(id: $id, sandboxDisabled: $sandboxDisabled) {\n      id\n      sandboxDisabled\n    }\n  }\n": types.ChatSandboxDisabledSetDocument,
    "\n  query Sandbox {\n    sandbox {\n      available\n    }\n  }\n": types.SandboxDocument,
};

/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 *
 *
 * @example
 * ```ts
 * const query = graphql(`query GetUser($id: ID!) { user(id: $id) { name } }`);
 * ```
 *
 * The query argument is unknown!
 * Please regenerate the types.
 */
export function graphql(source: string): unknown;

/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation ChatCancel($chatID: ChatID!) {\n    chatCancel(chatID: $chatID)\n  }\n"): (typeof documents)["\n  mutation ChatCancel($chatID: ChatID!) {\n    chatCancel(chatID: $chatID)\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation ChatRename($id: ChatID!, $title: String!) {\n    chatRename(id: $id, title: $title) {\n      id\n      title\n      updatedAt\n    }\n  }\n"): (typeof documents)["\n  mutation ChatRename($id: ChatID!, $title: String!) {\n    chatRename(id: $id, title: $title) {\n      id\n      title\n      updatedAt\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation ChatDelete($id: ChatID!) {\n    chatDelete(id: $id)\n  }\n"): (typeof documents)["\n  mutation ChatDelete($id: ChatID!) {\n    chatDelete(id: $id)\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation ApprovalDecide($id: ApprovalID!, $approve: Boolean!) {\n    approvalDecide(id: $id, approve: $approve)\n  }\n"): (typeof documents)["\n  mutation ApprovalDecide($id: ApprovalID!, $approve: Boolean!) {\n    approvalDecide(id: $id, approve: $approve)\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation BackgroundTaskStop($id: ToolCallID!) {\n    backgroundTaskStop(id: $id)\n  }\n"): (typeof documents)["\n  mutation BackgroundTaskStop($id: ToolCallID!) {\n    backgroundTaskStop(id: $id)\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation ClusterEnabledSet($id: ClusterID!, $enabled: Boolean!) {\n    clusterEnabledSet(id: $id, enabled: $enabled) {\n      id\n      spec {\n        enabled\n      }\n    }\n  }\n"): (typeof documents)["\n  mutation ClusterEnabledSet($id: ClusterID!, $enabled: Boolean!) {\n    clusterEnabledSet(id: $id, enabled: $enabled) {\n      id\n      spec {\n        enabled\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation ClusterSyncEnabledSet($id: ClusterID!, $syncEnabled: Boolean!) {\n    clusterSyncEnabledSet(id: $id, syncEnabled: $syncEnabled) {\n      id\n      spec {\n        syncEnabled\n      }\n    }\n  }\n"): (typeof documents)["\n  mutation ClusterSyncEnabledSet($id: ClusterID!, $syncEnabled: Boolean!) {\n    clusterSyncEnabledSet(id: $id, syncEnabled: $syncEnabled) {\n      id\n      spec {\n        syncEnabled\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation ClusterCacheClear($id: ObjectID!) {\n    clusterCacheClear(id: $id) {\n      id\n    }\n  }\n"): (typeof documents)["\n  mutation ClusterCacheClear($id: ObjectID!) {\n    clusterCacheClear(id: $id) {\n      id\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation ClusterCachedKindSyncEnabledSet($id: ObjectID!, $syncEnabled: Boolean!) {\n    clusterCachedKindSyncEnabledSet(id: $id, syncEnabled: $syncEnabled) {\n      id\n      spec {\n        syncEnabled\n      }\n    }\n  }\n"): (typeof documents)["\n  mutation ClusterCachedKindSyncEnabledSet($id: ObjectID!, $syncEnabled: Boolean!) {\n    clusterCachedKindSyncEnabledSet(id: $id, syncEnabled: $syncEnabled) {\n      id\n      spec {\n        syncEnabled\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation ClusterDelete($id: ClusterID!) {\n    clusterDelete(id: $id)\n  }\n"): (typeof documents)["\n  mutation ClusterDelete($id: ClusterID!) {\n    clusterDelete(id: $id)\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation ClusterConnectionRetry($id: ClusterID!) {\n    clusterConnectionRetry(id: $id)\n  }\n"): (typeof documents)["\n  mutation ClusterConnectionRetry($id: ClusterID!) {\n    clusterConnectionRetry(id: $id)\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClusterConnectionEvents($id: ClusterID!) {\n    clusterEventsWatch(id: $id, category: \"connection\") {\n      type\n      event {\n        id\n        type\n        reason\n        message\n        count\n        firstAt\n        lastAt\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription ClusterConnectionEvents($id: ClusterID!) {\n    clusterEventsWatch(id: $id, category: \"connection\") {\n      type\n      event {\n        id\n        type\n        reason\n        message\n        count\n        firstAt\n        lastAt\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClusterSyncEvents($id: ObjectID!) {\n    eventsWatch(id: $id, category: \"sync\") {\n      type\n      event {\n        id\n        type\n        reason\n        message\n        count\n        firstAt\n        lastAt\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription ClusterSyncEvents($id: ObjectID!) {\n    eventsWatch(id: $id, category: \"sync\") {\n      type\n      event {\n        id\n        type\n        reason\n        message\n        count\n        firstAt\n        lastAt\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClusterDiscoveryEvents($id: ObjectID!) {\n    eventsWatch(id: $id, category: \"discovery\") {\n      type\n      event {\n        id\n        type\n        reason\n        message\n        count\n        firstAt\n        lastAt\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription ClusterDiscoveryEvents($id: ObjectID!) {\n    eventsWatch(id: $id, category: \"discovery\") {\n      type\n      event {\n        id\n        type\n        reason\n        message\n        count\n        firstAt\n        lastAt\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClusterCacheSyncStatus($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCacheSyncStatusWatch(id: $id, cacheID: $cacheID) {\n      discovery {\n        reason\n        message\n      }\n      kinds {\n        apiVersion\n        resource\n        reason\n        message\n        objectCount\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription ClusterCacheSyncStatus($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCacheSyncStatusWatch(id: $id, cacheID: $cacheID) {\n      discovery {\n        reason\n        message\n      }\n      kinds {\n        apiVersion\n        resource\n        reason\n        message\n        objectCount\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClusterCacheStats($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCacheStatsWatch(id: $id, cacheID: $cacheID) {\n      exists\n      bytes\n      dbBytes\n      walBytes\n      shmBytes\n      objectCount\n      kindCount\n    }\n  }\n"): (typeof documents)["\n  subscription ClusterCacheStats($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCacheStatsWatch(id: $id, cacheID: $cacheID) {\n      exists\n      bytes\n      dbBytes\n      walBytes\n      shmBytes\n      objectCount\n      kindCount\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClusterCachedKinds($cacheID: ObjectID!) {\n    clusterCachedKindsWatch(cacheID: $cacheID) {\n      type\n      kind {\n        id\n        spec {\n          apiVersion\n          resource\n        }\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription ClusterCachedKinds($cacheID: ObjectID!) {\n    clusterCachedKindsWatch(cacheID: $cacheID) {\n      type\n      kind {\n        id\n        spec {\n          apiVersion\n          resource\n        }\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClusterSchedule($id: ClusterID!) {\n    clusterScheduleWatch(id: $id) {\n      nextRequeueAt\n      probing\n    }\n  }\n"): (typeof documents)["\n  subscription ClusterSchedule($id: ClusterID!) {\n    clusterScheduleWatch(id: $id) {\n      nextRequeueAt\n      probing\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation MemorySave($input: MemorySaveInput!) {\n    memorySave(input: $input) {\n      id\n    }\n  }\n"): (typeof documents)["\n  mutation MemorySave($input: MemorySaveInput!) {\n    memorySave(input: $input) {\n      id\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation MemoryDelete($id: MemoryID!) {\n    memoryDelete(id: $id)\n  }\n"): (typeof documents)["\n  mutation MemoryDelete($id: MemoryID!) {\n    memoryDelete(id: $id)\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  query PermissionSettings {\n    permissionSettings {\n      defaultMode\n      contexts {\n        context\n        mode\n        source\n        pattern\n        own\n      }\n      rules {\n        id\n        line\n      }\n      destructive\n      held\n    }\n  }\n"): (typeof documents)["\n  query PermissionSettings {\n    permissionSettings {\n      defaultMode\n      contexts {\n        context\n        mode\n        source\n        pattern\n        own\n      }\n      rules {\n        id\n        line\n      }\n      destructive\n      held\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  query SecurityRefused {\n    securityRefused {\n      field\n      value\n      reason\n    }\n  }\n"): (typeof documents)["\n  query SecurityRefused {\n    securityRefused {\n      field\n      value\n      reason\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation PermissionDefaultModeSet($mode: PermissionMode!) {\n    permissionDefaultModeSet(mode: $mode) {\n      held\n    }\n  }\n"): (typeof documents)["\n  mutation PermissionDefaultModeSet($mode: PermissionMode!) {\n    permissionDefaultModeSet(mode: $mode) {\n      held\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation PermissionModeSet($context: String!, $mode: PermissionMode!) {\n    permissionModeSet(context: $context, mode: $mode) {\n      held\n    }\n  }\n"): (typeof documents)["\n  mutation PermissionModeSet($context: String!, $mode: PermissionMode!) {\n    permissionModeSet(context: $context, mode: $mode) {\n      held\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation PermissionModeClear($context: String!) {\n    permissionModeClear(context: $context) {\n      held\n    }\n  }\n"): (typeof documents)["\n  mutation PermissionModeClear($context: String!) {\n    permissionModeClear(context: $context) {\n      held\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation PermissionRuleAdd($input: PermissionRuleInput!) {\n    permissionRuleAdd(input: $input) {\n      held\n    }\n  }\n"): (typeof documents)["\n  mutation PermissionRuleAdd($input: PermissionRuleInput!) {\n    permissionRuleAdd(input: $input) {\n      held\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation PermissionRuleRemove($id: String!) {\n    permissionRuleRemove(id: $id) {\n      held\n    }\n  }\n"): (typeof documents)["\n  mutation PermissionRuleRemove($id: String!) {\n    permissionRuleRemove(id: $id) {\n      held\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation PermissionDiscardRefused($field: String!) {\n    permissionDiscardRefused(field: $field) {\n      held\n    }\n  }\n"): (typeof documents)["\n  mutation PermissionDiscardRefused($field: String!) {\n    permissionDiscardRefused(field: $field) {\n      held\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription AuthStateWatch {\n    authStateWatch {\n      authenticated\n      identity {\n        sub\n        email\n        name\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription AuthStateWatch {\n    authStateWatch {\n      authenticated\n      identity {\n        sub\n        email\n        name\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation AuthLoginStart {\n    authLoginStart\n  }\n"): (typeof documents)["\n  mutation AuthLoginStart {\n    authLoginStart\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation AuthLogout {\n    authLogout\n  }\n"): (typeof documents)["\n  mutation AuthLogout {\n    authLogout\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation ChatSend(\n    $chatID: ChatID\n    $mode: ChatMode!\n    $clusterID: ClusterID!\n    $sandboxDisabled: Boolean!\n    $providerID: String!\n    $modelID: String!\n    $effort: String!\n    $requestID: String!\n    $content: String!\n  ) {\n    chatSend(\n      chatID: $chatID\n      mode: $mode\n      clusterID: $clusterID\n      sandboxDisabled: $sandboxDisabled\n      providerID: $providerID\n      modelID: $modelID\n      effort: $effort\n      requestID: $requestID\n      content: $content\n    ) {\n      id\n      chatID\n      seq\n      status\n    }\n  }\n"): (typeof documents)["\n  mutation ChatSend(\n    $chatID: ChatID\n    $mode: ChatMode!\n    $clusterID: ClusterID!\n    $sandboxDisabled: Boolean!\n    $providerID: String!\n    $modelID: String!\n    $effort: String!\n    $requestID: String!\n    $content: String!\n  ) {\n    chatSend(\n      chatID: $chatID\n      mode: $mode\n      clusterID: $clusterID\n      sandboxDisabled: $sandboxDisabled\n      providerID: $providerID\n      modelID: $modelID\n      effort: $effort\n      requestID: $requestID\n      content: $content\n    ) {\n      id\n      chatID\n      seq\n      status\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ChatsWatch {\n    chatsWatch {\n      type\n      chat {\n        id\n        title\n        mode\n        clusterID\n        createdAt\n        updatedAt\n        awaitingApproval\n        sandboxDisabled\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription ChatsWatch {\n    chatsWatch {\n      type\n      chat {\n        id\n        title\n        mode\n        clusterID\n        createdAt\n        updatedAt\n        awaitingApproval\n        sandboxDisabled\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ChatMessagesWatch($chatID: ChatID!) {\n    chatMessagesWatch(chatID: $chatID) {\n      type\n      message {\n        id\n        chatID\n        seq\n        role\n        content\n        thinking\n        status\n        awaitingApproval\n        error\n        model\n        provider {\n          id\n          label\n        }\n        effort\n        finishReason\n        toolCalls {\n          id\n          name\n          actionKind\n          status\n          runsOn\n          action {\n            description\n            command {\n              text\n              cwd\n              background\n              sandboxed\n            }\n            read {\n              path\n            }\n            write {\n              path\n              content\n            }\n            edit {\n              path\n              oldString\n              newString\n              replaceAll\n            }\n            search {\n              query\n            }\n            fetch {\n              url\n              host\n            }\n            memory {\n              op\n              name\n              body\n              scope\n            }\n            delegate {\n              prompt\n              agentType\n              model\n            }\n            kubeQuery {\n              sql\n              limit\n            }\n          }\n          agentCallID\n          approval {\n            id\n            status\n          }\n          clusterWrites {\n            approval {\n              id\n              status\n            }\n            method\n            path\n            subresource\n            contentType\n            body\n            dryRun\n            reason\n          }\n          output\n          background {\n            status\n            exitCode\n            report\n          }\n        }\n        citations {\n          type\n          url\n          title\n          citedText\n        }\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription ChatMessagesWatch($chatID: ChatID!) {\n    chatMessagesWatch(chatID: $chatID) {\n      type\n      message {\n        id\n        chatID\n        seq\n        role\n        content\n        thinking\n        status\n        awaitingApproval\n        error\n        model\n        provider {\n          id\n          label\n        }\n        effort\n        finishReason\n        toolCalls {\n          id\n          name\n          actionKind\n          status\n          runsOn\n          action {\n            description\n            command {\n              text\n              cwd\n              background\n              sandboxed\n            }\n            read {\n              path\n            }\n            write {\n              path\n              content\n            }\n            edit {\n              path\n              oldString\n              newString\n              replaceAll\n            }\n            search {\n              query\n            }\n            fetch {\n              url\n              host\n            }\n            memory {\n              op\n              name\n              body\n              scope\n            }\n            delegate {\n              prompt\n              agentType\n              model\n            }\n            kubeQuery {\n              sql\n              limit\n            }\n          }\n          agentCallID\n          approval {\n            id\n            status\n          }\n          clusterWrites {\n            approval {\n              id\n              status\n            }\n            method\n            path\n            subresource\n            contentType\n            body\n            dryRun\n            reason\n          }\n          output\n          background {\n            status\n            exitCode\n            report\n          }\n        }\n        citations {\n          type\n          url\n          title\n          citedText\n        }\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClusterCachedDataEventsWatch($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCachedDataEventsWatch(id: $id, cacheID: $cacheID) {\n      type\n      cacheID\n      event {\n        uid\n        type\n        reason\n        message\n        count\n        firstSeen\n        lastSeen\n        involvedKind\n        involvedNamespace\n        involvedName\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription ClusterCachedDataEventsWatch($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCachedDataEventsWatch(id: $id, cacheID: $cacheID) {\n      type\n      cacheID\n      event {\n        uid\n        type\n        reason\n        message\n        count\n        firstSeen\n        lastSeen\n        involvedKind\n        involvedNamespace\n        involvedName\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClusterCachedDataKindsWatch($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCachedDataKindsWatch(id: $id, cacheID: $cacheID) {\n      type\n      cacheID\n      kind {\n        apiVersion\n        kind\n        resource\n        scope\n        isCRD\n        count\n        printerColumns {\n          name\n          type\n          jsonPath\n          priority\n        }\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription ClusterCachedDataKindsWatch($id: ClusterID!, $cacheID: ObjectID!) {\n    clusterCachedDataKindsWatch(id: $id, cacheID: $cacheID) {\n      type\n      cacheID\n      kind {\n        apiVersion\n        kind\n        resource\n        scope\n        isCRD\n        count\n        printerColumns {\n          name\n          type\n          jsonPath\n          priority\n        }\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClusterCachedDataObjectsWatch(\n    $id: ClusterID!\n    $cacheID: ObjectID!\n    $apiVersion: String!\n    $resource: String!\n  ) {\n    clusterCachedDataObjectsWatch(id: $id, cacheID: $cacheID, apiVersion: $apiVersion, resource: $resource) {\n      type\n      cacheID\n      apiVersion\n      resource\n      object {\n        uid\n        apiVersion\n        kind\n        namespace\n        name\n        creationTimestamp\n        rawJSON\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription ClusterCachedDataObjectsWatch(\n    $id: ClusterID!\n    $cacheID: ObjectID!\n    $apiVersion: String!\n    $resource: String!\n  ) {\n    clusterCachedDataObjectsWatch(id: $id, cacheID: $cacheID, apiVersion: $apiVersion, resource: $resource) {\n      type\n      cacheID\n      apiVersion\n      resource\n      object {\n        uid\n        apiVersion\n        kind\n        namespace\n        name\n        creationTimestamp\n        rawJSON\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClustersWatch {\n    clustersWatch {\n      type\n      cluster {\n        id\n        deletionRequestedAt\n        spec {\n          name\n          syncEnabled\n          enabled\n          source {\n            kubeconfig {\n              context\n            }\n          }\n        }\n        status {\n          source {\n            kubeconfig {\n              cluster {\n                name\n                entry {\n                  server\n                  insecureSkipTLSVerify\n                }\n              }\n              user {\n                name\n              }\n              isPresent\n              isDefault\n            }\n          }\n          server {\n            uid\n          }\n        }\n        conditions {\n          type\n          status\n          reason\n          message\n          liveness\n          unconfirmed\n          transitionedAt\n        }\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription ClustersWatch {\n    clustersWatch {\n      type\n      cluster {\n        id\n        deletionRequestedAt\n        spec {\n          name\n          syncEnabled\n          enabled\n          source {\n            kubeconfig {\n              context\n            }\n          }\n        }\n        status {\n          source {\n            kubeconfig {\n              cluster {\n                name\n                entry {\n                  server\n                  insecureSkipTLSVerify\n                }\n              }\n              user {\n                name\n              }\n              isPresent\n              isDefault\n            }\n          }\n          server {\n            uid\n          }\n        }\n        conditions {\n          type\n          status\n          reason\n          message\n          liveness\n          unconfirmed\n          transitionedAt\n        }\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClusterCachesWatch {\n    clusterCachesWatch {\n      type\n      cache {\n        id\n        clusterID\n        spec {\n          serverUid\n        }\n        # On-disk stats ride clusterCacheStatsWatch, subscribed per expanded row.\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription ClusterCachesWatch {\n    clusterCachesWatch {\n      type\n      cache {\n        id\n        clusterID\n        spec {\n          serverUid\n        }\n        # On-disk stats ride clusterCacheStatsWatch, subscribed per expanded row.\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription ClusterCacheHealthWatch {\n    clusterCacheHealthWatch {\n      cacheID\n      status\n      reason\n      unhealthyKindRefs {\n        apiVersion\n        resource\n      }\n      totalKinds\n      unhealthyKinds\n      pausedKinds\n      lastUpdateAt\n      lastLiveAt\n    }\n  }\n"): (typeof documents)["\n  subscription ClusterCacheHealthWatch {\n    clusterCacheHealthWatch {\n      cacheID\n      status\n      reason\n      unhealthyKindRefs {\n        apiVersion\n        resource\n      }\n      totalKinds\n      unhealthyKinds\n      pausedKinds\n      lastUpdateAt\n      lastLiveAt\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  subscription MemoriesWatch($clusterID: ClusterID!) {\n    memoriesWatch(clusterID: $clusterID) {\n      type\n      memory {\n        id\n        clusterID\n        name\n        body\n        writtenBy\n        updatedAt\n      }\n    }\n  }\n"): (typeof documents)["\n  subscription MemoriesWatch($clusterID: ClusterID!) {\n    memoriesWatch(clusterID: $clusterID) {\n      type\n      memory {\n        id\n        clusterID\n        name\n        body\n        writtenBy\n        updatedAt\n      }\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  query Models {\n    models {\n      provider {\n        id\n        label\n      }\n      id\n      label\n      efforts\n      defaultEffort\n    }\n  }\n"): (typeof documents)["\n  query Models {\n    models {\n      provider {\n        id\n        label\n      }\n      id\n      label\n      efforts\n      defaultEffort\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  query SandboxPath {\n    sandboxPath {\n      dir\n      target\n      state\n      source\n      shared\n    }\n    sandboxPathFault\n    sandboxPathResolved\n  }\n"): (typeof documents)["\n  query SandboxPath {\n    sandboxPath {\n      dir\n      target\n      state\n      source\n      shared\n    }\n    sandboxPathFault\n    sandboxPathResolved\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation SandboxPathInclude($dir: String!, $target: String!) {\n    sandboxPathInclude(dir: $dir, target: $target) {\n      dir\n    }\n  }\n"): (typeof documents)["\n  mutation SandboxPathInclude($dir: String!, $target: String!) {\n    sandboxPathInclude(dir: $dir, target: $target) {\n      dir\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation SandboxPathRemove($dir: String!) {\n    sandboxPathRemove(dir: $dir) {\n      dir\n    }\n  }\n"): (typeof documents)["\n  mutation SandboxPathRemove($dir: String!) {\n    sandboxPathRemove(dir: $dir) {\n      dir\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation SandboxPathRefresh {\n    sandboxPathRefresh {\n      dir\n    }\n  }\n"): (typeof documents)["\n  mutation SandboxPathRefresh {\n    sandboxPathRefresh {\n      dir\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  mutation ChatSandboxDisabledSet($id: ChatID!, $sandboxDisabled: Boolean!) {\n    chatSandboxDisabledSet(id: $id, sandboxDisabled: $sandboxDisabled) {\n      id\n      sandboxDisabled\n    }\n  }\n"): (typeof documents)["\n  mutation ChatSandboxDisabledSet($id: ChatID!, $sandboxDisabled: Boolean!) {\n    chatSandboxDisabledSet(id: $id, sandboxDisabled: $sandboxDisabled) {\n      id\n      sandboxDisabled\n    }\n  }\n"];
/**
 * The graphql function is used to parse GraphQL queries into a document that can be used by GraphQL clients.
 */
export function graphql(source: "\n  query Sandbox {\n    sandbox {\n      available\n    }\n  }\n"): (typeof documents)["\n  query Sandbox {\n    sandbox {\n      available\n    }\n  }\n"];

export function graphql(source: string) {
  return (documents as any)[source] ?? {};
}

export type DocumentType<TDocumentNode extends DocumentNode<any, any>> = TDocumentNode extends DocumentNode<  infer TType,  any>  ? TType  : never;