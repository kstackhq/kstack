---
title: Name every subscription for its family and kind
date: 2026-11-25
scope: cross-cutting
status: Superseded by [2026-11-25-name-every-data-access-for-its-family-kind-and-operation](2026-11-25-name-every-data-access-for-its-family-kind-and-operation.md)
---

# Name every subscription for its family and kind

## Context

By November 2026 Kstack had 29 GraphQL subscription fields, one gRPC stream and about 40 Go
methods that keep delivering after the call returns. They used `Watch`, `Subscribe` and `Follow`
interchangeably. `…Watch` named four contracts on the wire: a set mirrored item by item
(`clustersWatch`), one whole value (`modelsWatch`), an event history (`clusterEventsWatch`) and a
sample window (`clusterDataRateWatch`). In Go, `Subscribe()` returned whole state in the connectors
and a key-only notice in `kubestore`, and `Watch…News()` returned a notice too. Comments and guides
called the same contracts "delta watch", "gauge", "series", "timeline", "follow" and "news". A
reader could not tell from a name what arrives first, what arrives later, or what to do with it,
and each webview provider wrote its own fold.

## Decision

Every subscription is one of eight kinds in four families, and its name says which.
`docs/subscriptions.md` states each kind's delivery and the subscriber's
job.

| Family | Verb | Kinds |
| --- | --- | --- |
| Watch | `Watch` | collection watch, state watch, keyed state watch |
| Feed | `Subscribe` | event feed, time series feed |
| Tail | `Tail` | cursor tail |
| Notification | `Listen` | change notification, broadcast notification |

A Go method is the verb, the noun, then the kind's qualifier (`WatchKindCollection`,
`SubscribeEventFeed`, `TailFromCursor`, `ListenForKindChanges`); a GraphQL field is the same words
with the family noun last (`clusterCollectionWatch`, `clusterEventFeed`, `logsTail`). The four
verbs are reserved for one subscription of one kind; `Subscription` and `subscribe` are the generic
words for the transport alone. The wire marker for a dead subscription is
`extensions.subscriptionFailed`. The webview folds each kind through one reducer in
`src/lib/graphql/`, and every reducer reports `snapshotComplete` once the first delivery is in.

This amends three ADRs, whose terms it replaces and whose decisions stand: the
[delta-watch protocol](2026-08-09-delta-watch-protocol.md) is the collection watch,
[watch-failure reporting](2026-08-14-watch-failure-reporting.md) is subscription-failure
reporting, and [the series](2026-11-02-a-series-is-a-third-kind-of-stream-and-the-pool-owns-its-clock.md)
is the time series feed.

## Alternatives considered

- **Keep `Watch` for everything and say the kind in the description.** It costs nothing to
  rename, but the description is the one place a reader of a call site never looks, and the
  inventory showed four contracts already sharing the name.
- **Name by transport (`…Stream`, `…Subscription`).** The transport is the same for every kind,
  so the name would carry nothing.
- **`Follow` for the log subscription.** It collides with the kubelet's `follow` parameter, which
  the logs service also speaks; `Tail` names what the subscriber does and leaves `follow` to the
  kubelet.
- **`Changes`/`News` for notifications without a verb.** A notification is still a subscription a
  caller opens and must close, so it takes a verb like the rest; `Listen` says the caller is told,
  not handed data.
- **Fold the keyed state watch into the collection watch.** Health values change independently
  and have no membership of their own; giving them `Bookmark` and `Deleted` would duplicate the
  cache collection they join onto.

## Consequences

Every field name changed in one release; the webview and the sidecar ship together and the host
forwards operations unexamined, so no deployed client reads the old names. Names are long
(`clusterCachedDataObjectCollectionWatch`), and that is the price of a name that states its
contract. A new subscription chooses its kind first, which can surface a delivery that matches no
kind; that is a bug in the subscription, not a ninth kind. Subscriptions whose behaviour does not
yet match their kind's contract (`kubeconn`'s `claim.WatchState`, `WatchStatsState` and
`WatchSyncState` for a cache that is not live) are tracked as their own issues.
