# Name every subscription for its family and kind

Tracked in [#1203](https://github.com/kstackhq/kstack/issues/1203); answers [#1103](https://github.com/kstackhq/kstack/issues/1103).

## Goal

Every subscription in Kstack is named by the rules in [`docs/subscriptions.md`](../subscriptions.md):
the family's verb, the noun, the kind's qualifier. This spec lists each subscription that exists
today, its kind, and its new name.

It renames. It changes no delivery behaviour; the behaviour that does not match a kind's contract
is listed under [Behaviour that does not match](#behaviour-that-does-not-match) and is fixed
elsewhere.

## Vocabulary

One concept, one name. The old terms leave code, comments and every `CLAUDE.md`. ADRs keep
theirs: they are history.

| Today | Becomes |
| --- | --- |
| delta watch, record watch | collection watch |
| gauge | state watch (keyed state watch for `clusterCacheHealth`) |
| series | time series feed |
| event-timeline watch | event feed |
| follow (the subscription) | cursor tail |
| news, change feed, ping, nudge (a `conflate` receiver) | change notification |
| signal (`poke`) | broadcast notification |
| "watch" for a subscription of another family, or of any family | the family's noun, or subscription |

## What breaks

- **The GraphQL schema.** Every subscription field but `authStateWatch` is renamed, and most
  frame types. The webview and the sidecar ship together and the host forwards operations
  unexamined, so no deployed client reads the old names.
- **The failure marker.** `extensions.watchFailed` becomes `subscriptionFailed`.
- **gRPC.** Nothing: `AuthStateWatch` already fits.
- **Persisted data.** Nothing: no subscription name is stored. The agent tool `LogsWatch` is a
  tool name kept in chat transcripts and is not a subscription; it stays.

## Inventory and renames

### GraphQL subscription fields

Collection watches. `DeltaFrameType` becomes `CollectionWatchFrameType`.

| Today | Becomes | Frame type becomes |
| --- | --- | --- |
| `clustersWatch` | `clusterCollectionWatch` | `ClusterCollectionWatchFrame` |
| `clusterCachesWatch` | `clusterCacheCollectionWatch` | `ClusterCacheCollectionWatchFrame` |
| `clusterCachedKindsWatch` | `clusterCachedKindCollectionWatch` | `ClusterCachedKindCollectionWatchFrame` |
| `clusterCachedDataKindsWatch` | `clusterCachedDataKindCollectionWatch` | `ClusterCachedDataKindCollectionWatchFrame` |
| `clusterCachedDataEventsWatch` | `clusterCachedDataEventCollectionWatch` | `ClusterCachedDataEventCollectionWatchFrame` |
| `clusterCachedDataObjectsWatch` | `clusterCachedDataObjectCollectionWatch` | `ClusterCachedDataObjectCollectionWatchFrame` |
| `chatsWatch` | `chatCollectionWatch` | `ChatCollectionWatchFrame` |
| `chatMessagesWatch` | `chatMessageCollectionWatch` | `ChatMessageCollectionWatchFrame` |
| `memoriesWatch` | `memoryCollectionWatch` | `MemoryCollectionWatchFrame` |

State watches. None has a frame type. The type `ClusterCacheSyncStatus` becomes
`ClusterCacheSyncState`, in GraphQL and in Go; its fields are unchanged.

| Today | Becomes |
| --- | --- |
| `authStateWatch` | unchanged |
| `clusterScheduleWatch` | `clusterScheduleStateWatch` |
| `clusterCacheStatsWatch` | `clusterCacheStatsStateWatch` |
| `clusterCacheSyncStatusWatch` | `clusterCacheSyncStateWatch` |
| `sandboxExecutablesWatch` | `sandboxExecutablesStateWatch` |
| `sandboxFoldersWatch` | `sandboxFoldersStateWatch` |
| `sandboxConsoleWatch` | `sandboxConsoleStateWatch` |
| `chatGPTConnectionWatch` | `chatGPTConnectionStateWatch` |
| `openRouterConnectionWatch` | `openRouterConnectionStateWatch` |
| `gitHubConnectionWatch` | `gitHubConnectionStateWatch` |
| `googleConnectionWatch` | `googleConnectionStateWatch` |
| `awsConnectionWatch` | `awsConnectionStateWatch` |
| `modelsWatch` | `modelsStateWatch` |
| `providerCredentialsWatch` | `providerCredentialsStateWatch` |
| `toolSettingsWatch` | `toolSettingsStateWatch` |

The rest.

| Kind | Today | Becomes | Types become |
| --- | --- | --- | --- |
| Keyed state watch | `clusterCacheHealthWatch` | `clusterCacheHealthKeyedStateWatch` | — |
| Event feed | `clusterEventsWatch` | `clusterEventFeed` | `EventWatchFrame` → `EventFeedFrame`, `EventFrameType` → `EventFeedFrameType` |
| Event feed | `eventsWatch` | `objectEventFeed` | the same |
| Time series feed | `clusterDataRateWatch` | `clusterDataRateTimeSeriesFeed` | `ClusterDataRateSeriesFrame` → `ClusterDataRateTimeSeriesFeedFrame` |
| Cursor tail | `logsFollow` | `logsTail` | `LogsFollowInput` → `LogsTailInput`, `LogsFollowFrame` → `LogsTailFrame`, `LogsFollowFrameType` → `LogsTailFrameType` |

Each field's description is rewritten to open with its kind.

### Sidecar: the cluster service (`services/cluster`)

| Today | Becomes |
| --- | --- |
| `Clusters().Watch(id)`, `Caches().Watch(id)`, `CachedKinds().Watch(id)` | `WatchCollectionByID` |
| `Clusters().WatchList`, `Caches().WatchList`, `CachedKinds().WatchList` | `WatchCollection` |
| `Caches().WatchByCluster` | `WatchCollectionByCluster` |
| `CachedKinds().WatchByCache` | `WatchCollectionByCache` |
| `CachedData().WatchKinds` | `WatchKindCollection` |
| `CachedData().WatchObjects` | `WatchObjectCollection` |
| `CachedData().WatchObjectsIn` | `WatchObjectCollectionByNamespaces` |
| `CachedData().WatchEvents` | `WatchEventCollection` |
| `Clusters().WatchSchedule` | `WatchScheduleState` |
| `Caches().WatchStats` | `WatchStatsState` |
| `Caches().WatchSyncStatus` | `WatchSyncState` |
| `Caches().WatchHealth` | `WatchHealthKeyedState` |
| `Clusters().WatchEvents` | `SubscribeEventFeed` |
| `service.WatchEvents(id ObjectID)` | `SubscribeObjectEventFeed` |
| `Clusters().WatchDataRate` | `SubscribeDataRateTimeSeriesFeed` |
| `deltaWatch` (`stream.go`) | `collectionWatch` |
| `gaugeSubscription`, `sharedgauge.go`, `gaugeCadence` | `sharedStateWatch`, `sharedstatewatch.go`, `stateWatchCadence` |
| `feed[T]` (`triggers.go`) | `changeSource[T]` |

The Go frame types take their GraphQL names. `service.go`'s family rule is rewritten to state
the rule in the guide.

### Sidecar: the cluster service's internals

| Package | Today | Becomes |
| --- | --- | --- |
| `kubestore` | `Store.Subscribe(keys...)`, `Manager.Subscribe(cacheID, keys...)` | `ListenForChanges` |
| `kubestore` | `type Subscription` | `ChangeNotifications` |
| `kubestore` | `Manager.WatchOpen`, `type OpenSubscription`, hub `opens` | `ListenForCacheOpenChanges`, `CacheOpenChangeNotifications`, `cacheOpenHub` |
| `kubestore` | `Manager.WatchSizeLimitNews`, `type SizeLimitNews` | `ListenForSizeLimitChanges`, `SizeLimitChangeNotifications` |
| `kubesync` | `WatchDiscoveryNews`, `type DiscoveryNews` | `ListenForDiscoveryChanges`, `DiscoveryChangeNotifications` |
| `kubesync` | `WatchKindNews`, `type KindNews` | `ListenForKindChanges`, `KindChangeNotifications` |
| `kubeconn` | `Service.Subscribe`, `type Subscription`, hub `signalHub` | `ListenForConnectionChanges`, `ConnectionChangeNotifications`, `connectionChangeHub` |
| `kubeconn` | `claim.WatchDataRates` (returns `Subscription`), hub `rateHub` | `ListenForDataRateChanges`, returning `DataRateChangeNotifications`; `dataRateChangeHub` |

### Sidecar: everything else

| Package | Today | Becomes | Kind |
| --- | --- | --- | --- |
| `agent/chat` | `WatchList` | `WatchCollection` | Collection watch |
| `agent/chat` | `WatchMessages(chatID)` | `WatchMessageCollection(chatID)` | Collection watch |
| `agent/chat` | `WatchGrants(chatID)`, `type GrantsNews` | `ListenForGrantChanges`, `GrantChangeNotifications` | Change notification |
| `services/memory` | `Watch(ctx, cluster)` | `WatchCollectionByCluster(ctx, clusterID)` | Collection watch |
| `services/logs` | `Follow`, `FollowRequest`, `FollowResponse`, `follow.go` | `TailFromCursor`, `TailRequest`, `TailResponse`, `tail.go` | Cursor tail |
| `services/logs/logstore` | `Store.Subscribe(ids...)` | `ListenForStreamChanges(ids...)` | Change notification |
| `services/connectors/*`, `internal/oauthconn` | `Subscribe()` | `WatchState()` | State watch |
| `services/connectors/openrouter`, `internal/oauthconn` | `WatchSession()` | `WatchSessionGenerationState()` | State watch |
| `services/providercredentials` | `Subscribe()` | `WatchCredentialsState()` | State watch |
| `services/providercredentials` | `WatchCredential(id)` | `WatchCredentialGenerationState(providerID)` | State watch |
| `llm` | `Subscribe()` | `WatchProvidersState()` | State watch |
| `services/toolsettings` | `Subscribe()` | `WatchSettingsState()` | State watch |
| `services/kubeconfig` | `Subscribe()`, `type Subscription` | `WatchConfigState()`, `ConfigStateWatch` | State watch |
| `lib/jsonsettings` | `Store[T].Subscribe()` | `WatchState()` | State watch |
| `tools/bash` | `WatchProbe()` | `WatchProbeState()` | State watch |
| `tools/bash` | `WatchConsole()` | `WatchConsoleState()` | State watch |
| `appdb` | `DB.Subscribe(keys...)` | `ListenForChanges(keys...)` | Change notification |
| `services/poke` | `Subscribe()`, `type Signal` | `ListenForNotifications()`, `Notification` | Broadcast notification |

`graph/`: each resolver takes its field's name through `gqlgen`. `watchStream`, `mapStream`,
`ptrStream`, `recordWatchFailure` and `WatchFailureExtension` (`watch_failure.go`) serve every
family; they become `subscriptionStream`, `mapSubscription`, `ptrSubscription`,
`recordSubscriptionFailure` and `SubscriptionFailureExtension` (`subscription_failure.go`). The
wire marker `extensions.watchFailed` becomes `subscriptionFailed`, in the sidecar and in the
webview's `subscribe-exchange.ts` together.

Not subscriptions, and unchanged: `tools.Watch`, `tools.Watches` and `Box.WatchResult` (a
background task's records), `ipc.Listen` and `browser.Listen` (sockets), `LogRecordsPage.follow`
(a polling cursor of `logsFetch`), and the kubelet's own vocabulary in `services/logs`:
`logstest.FollowsOpened`, `FollowsClosed` and `Tails`, `kubelet.FollowLimits`, and the per-stream
`tailer` with `TailGrace` and `Tail.Committed`. `kubeconn`'s `claim.WatchState` and
`StateSubscription` also stay, until [its behaviour](#behaviour-that-does-not-match) is settled.

`poke` stays a broadcast notification: its notice names nothing to read. It rides
`gochan/broadcast`, which drops the oldest value for a receiver that falls behind, and the
guide's contract says so.

### Host (`src-tauri/`)

| Today | Becomes | Why |
| --- | --- | --- |
| `updater/platform.rs` `spawn_watch` | `spawn_update_checker` | A 24-hour poller, not a watch |
| `tray/mod.rs` `watch_auth_state` | unchanged | A state watch |
| `graphql_subscribe`, `graphql_unsubscribe`, `log_window_subscribe` | unchanged | `Subscription` is the generic noun |

### Webview (`src/`)

- **Operations.** Each operation is named for its field, Pascal-cased. Eight operations drop or
  change the field's name today (`ClusterCacheStats`, `ClusterSchedule`, `ClusterDataRate`,
  `ClusterConnectionEvents`, `ClusterSyncEvents`, `ClusterDiscoveryEvents`,
  `ClusterCacheSyncStatus`, `ClusterCachedKinds`). Where one field backs several operations, the
  operation adds what it is for after the field's name: `ClusterEventFeedConnection`,
  `ObjectEventFeedSync`, `ObjectEventFeedDiscovery`.
- **Document constants** all end in `Document`. Twenty end in `Subscription` today.
- **Hooks and helpers.**

  | Today | Becomes |
  | --- | --- |
  | `useWatchSubscription`, `use-watch-subscription.ts` | `useFoldedSubscription`, `use-folded-subscription.ts` |
  | `WatchReducer`, `WatchOptions`, `watchPhase` | `SubscriptionReducer`, `SubscriptionOptions`, `subscriptionPhase` |
  | `useCacheDeltaWatch`, `use-cache-delta-watch.ts`, `DeltaSet` | `useCacheCollectionWatch`, `use-cache-collection-watch.ts`, `CollectionSnapshot` |
  | `DeltaState` (`clusters.tsx`) | `CollectionState` |
  | `seriesReducer`, `SeriesFrame`, `seriesSlots` | `timeSeriesFeedReducer`, `TimeSeriesFeedFrame`, `timeSeriesFeedSlots` |
  | `useDataRate`, `useNextCheck`, `useCacheContents` | `useClusterDataRateTimeSeriesFeed`, `useClusterScheduleStateWatch`, `useClusterCacheStatsStateWatch` |
  | `useSyncEvents`, `useDiscoveryEvents`, `useConnectionAttempts`, `useCachedKinds`, `useCacheSyncStatus` | `use` plus the operation name |
  | `Timeline`, `foldFrame` (`panel.tsx`) | `EventFeedHistory`, `eventFeedReducer`, moved to `src/lib/graphql/` |
  | `fold`, `Folded` (`chats.tsx`) | the shared `collectionWatchReducer`, `CollectionState` |

- **One reducer per kind**, in `src/lib/graphql/`: `collectionWatchReducer`, `stateWatchReducer`
  (keep the last frame), `keyedStateWatchReducer`, `eventFeedReducer`, `timeSeriesFeedReducer`.
  A provider calls the one for its kind instead of writing its own.
- **One name for "the first delivery has arrived"**: `snapshotComplete`. It replaces the fields
  and variables that mean it today (`synced`, `loaded`, `ready`, and `loading` inverted) in what a
  reducer or a subscription hook returns. The words stay everywhere else.
- `logs-client.ts`: `LogsFollowSubscription` → `LogsTailDocument`, and the follow vocabulary of the
  subscription with it.

## Tasks

Each task is one PR into `wip/subscription-naming`, compiles, and passes its area's checks.
Tasks 2 to 5 each change the schema and the webview's operations together, so the wire never
splits. A reducer a task adds returns `snapshotComplete` from the start.

1. **Guide and vocabulary.** Land `docs/subscriptions.md`. Write the ADR that
   [When it lands](#when-it-lands) names. No code.
2. **Collection watches.** Schema fields, frame types, `CollectionWatchFrameType`; the cluster,
   chat and memory Go methods; `collectionWatch`; the webview's operations, `collectionWatchReducer`
   and `useCacheCollectionWatch`.
3. **State and keyed state watches.** Schema fields; the Go `Watch…State` methods across the
   connectors, `llm`, `toolsettings`, `providercredentials`, `kubeconfig`, `jsonsettings` and
   `bash`; the cluster service's own four (`WatchScheduleState`, `WatchStatsState`,
   `WatchSyncState`, `WatchHealthKeyedState`), not `kubeconn`'s `claim.WatchState`;
   `sharedStateWatch`; the webview's operations and reducers.
4. **Feeds.** `clusterEventFeed`, `objectEventFeed`, `clusterDataRateTimeSeriesFeed`, their types
   and Go methods; `eventFeedReducer` and `timeSeriesFeedReducer`.
5. **Tail.** `logsTail` and its types; `services/logs`' `TailFromCursor`, `TailRequest`,
   `TailResponse` and `tail.go`; `logs-client.ts`; the log viewer's use of it. In `services/logs`
   "a follow" becomes "a tail" and the per-stream `tailer` keeps its name, so the package's guide
   says which is which.
6. **Notifications.** Every `ListenFor…` method and `…ChangeNotifications` type; `poke`'s
   `ListenForNotifications` and `Notification`; `changeSource[T]`; the hub fields. Go only.
7. **Generic names.** `useFoldedSubscription` and its types; the `graph/` adapters; document
   constants; `subscriptionFailed`; `snapshotComplete` in the hooks tasks 2 to 4 left;
   `spawn_update_checker`.
8. **Sweep.** Run the greps under [The sweep](#the-sweep). Each finds nothing.

Tasks 2 to 6 do not depend on each other once task 1 has merged, but tasks 2 to 5 all edit
`schema.graphqls` and `schema.resolvers.go` and regenerate `generated.go`, `models_gen.go` and
`src/gql/`. Merge them one at a time. On a conflict, resolve the hand-written files and
regenerate; never hand-merge a generated file. Task 7 follows them, so it renames each call site
once. Task 8 is last.

### The sweep

Searched in `sidecar/`, `src/`, `src-tauri/`, `proto/` and every `CLAUDE.md`, skipping
`docs/adr/` and generated output. Each must find nothing:

- **A distinctive name, anywhere.** A *Today* name that is not an ordinary word is searched as a
  whole word: every renamed GraphQL field and type, and names such as `WatchSizeLimitNews`,
  `WatchObjectsIn`, `deltaWatch`, `gaugeSubscription`, `useWatchSubscription`, `useCacheDeltaWatch`,
  `seriesReducer`, `watchPhase`, `spawn_watch`. Also `News\b` as an identifier suffix,
  `DeltaFrame`, `SeriesFrame\b` not preceded by `Time`, `WatchFrame\b` not preceded by
  `Collection`, `LogsFollow`, `watchFailed` and `WatchFailure`.
- **A generic name, where it lives.** A *Today* name that is an ordinary word is searched as its
  declaration and its qualified uses, in its own package or file:

  | Name | Searched as |
  | --- | --- |
  | `Subscribe`, `Watch`, `WatchList`, `WatchEvents`, `Follow` methods | `) Subscribe(`, `) Watch(`, `) WatchList(`, `) WatchEvents(`, `) Follow(` in the packages the tables name, and `.Subscribe(`, `.Watch(`, `.WatchList(`, `.Follow(` on their receivers |
  | `Subscription`, `OpenSubscription` types | `kubestore.Subscription`, `kubestore.OpenSubscription`, `kubeconn.Subscription`, `kubeconfig.Subscription`, and `type Subscription`, `type OpenSubscription` in those three packages |
  | `Signal` | `poke.Signal`, and `type Signal` in `services/poke` |
  | `feed` | `feed[` in `services/cluster/triggers.go` |
  | `fold`, `Folded` | in `src/lib/chats.tsx` |
  | `Timeline`, `foldFrame` | in `cluster-sync-panel/panel.tsx` |
  | `DeltaState`, `DeltaSet` | in `src/lib/clusters.tsx` and `src/lib/graphql/` |
  | `opens`, `signalHub`, `rateHub` | as fields, in `kubestore/manager.go` and `kubeconn/` |

- **A phrase.** `delta watch`, `delta-watch`, `record watch`, `gauge`, `timeline watch`,
  `event-timeline`, `change feed`, `a series is`, `series frame` and `the series'`,
  case-insensitive.
- **The first-delivery flag.** In `src/`, `synced`, `loaded` and `ready` as a field that a reducer
  or a subscription hook returns.

A row marked unchanged is not searched: `authStateWatch`, `watch_auth_state`, `graphql_subscribe`,
`graphql_unsubscribe`, `log_window_subscribe`, `claim.WatchState`, `StateSubscription`.

Allowed, and not searched for:

- `Stream[T]`, `NewStream`, `subscriptionStream`, `ListenForStreamChanges` and "stream" for a
  log stream or an SSE connection.
- `Subscription` as GraphQL's root type and `gqlgen`'s `Subscription()` resolver, and
  "subscription" and "subscribe" for the transport.
- `tools.Watch`, `tools.Watches`, `Box.WatchResult` and the `LogsWatch` tool.
- "fold" as a word in comments and in `useFoldedSubscription`.
- "watch" for a Kubernetes watch (`kubesync`), a file watch, and the Watch family.
- "follow" for the kubelet's parameter, `LogRecordsPage.follow`, the names listed as unchanged
  above, and the log viewer following the newest line.
- "series", "loading", "ready", "signal", "ping" and "news" in their ordinary senses ("a series
  of retries"), outside the identifiers and phrases above.

## Verification

Per `docs/specs/README.md`, *Verification commands*. Every task that touches the schema runs the
wire checks. Each task also greps for the names it retired and finds none outside `docs/adr/`.

No test changes meaning. A test is renamed with the unit it covers, and an assertion changes
only in the names it spells: a field, a type, `subscriptionFailed`, a description's text. An
assertion that changes in any other way means the task changed behaviour, and is wrong.

## Behaviour that does not match

Found while taking the inventory. Each is its own issue, filed when this spec is accepted; none
is fixed here.

- **`kubeconn`'s `claim.WatchState` delivers nothing on attach.** A state watch delivers the
  current value. Its caller reads `Lease.State` first, which is the change-notification pattern
  with a payload. This spec leaves its name and `StateSubscription` alone; the issue decides
  whether it becomes a state watch or is renamed for what it is.
- **`Caches().WatchStats` and `WatchSyncStatus` send nothing for a pair that names no live
  cache**, and stay open. A state watch delivers a value on subscribe, so either the value says
  "no such cache" or the subscribe is refused.
- **An empty scope ends three ways.** Some watches send a `Bookmark` and stay open, some send a
  `Bookmark` and end, some send nothing. A collection watch sends a `Bookmark` and stays open.
- **`clusterCacheHealth` has no removal frame**, which the keyed state watch contract allows
  only because its subscriber joins it onto the cache collection. The guide records that; the
  issue is to confirm no other subscriber reads it alone.
- **Two watches return a bare channel** (`WatchSchedule`, `WatchDataRate`) where the others
  return `*Stream[T]`, and `Stream[T]` is declared three times (`cluster`, `chat`, `memory`).
  One shared `Stream[T]` is a signature change, not a rename.
- **GraphQL arguments named `id` that are not the field's own record.** `logsFollow(id:)` and
  the `clusterCache…` and `clusterCachedData…` fields take a cluster's id as `id`;
  `memoriesWatch(clusterID:)` already names it.

## When it lands

- Root `CLAUDE.md`, *Host↔sidecar transport*: replace the delta watch, gauge and series
  paragraph with the family and kind names, and link `docs/subscriptions.md`. Add a line under
  *Writing standards* pointing to the guide for any new subscription.
- `sidecar/CLAUDE.md`, `src/CLAUDE.md`, `src-tauri/CLAUDE.md` and each package guide that names a
  subscription: the new names, and one line linking the guide.
- Task 1's ADR, *Name every subscription for its family and kind*, recording the taxonomy, the
  reserved verbs and the names rejected. It amends the delta-watch, watch-failure and series
  ADRs, whose terms it replaces; each gains an `amended_by` line.
- Delete this spec.
