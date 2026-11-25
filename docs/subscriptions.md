# Subscriptions

A subscription is any API that keeps delivering after the call returns: a GraphQL subscription
field, a gRPC server stream, or a Go method that returns a channel, a receiver or a `*Stream`.

Every subscription is one of eight kinds, in four families. The family says what the subscriber
does with what arrives. The kind says exactly what arrives and when. Pick the kind first; the
name follows from it.

## The taxonomy

| Family | Verb | Subscriber's job | Kind | First delivery | Later deliveries | Ends |
| --- | --- | --- | --- | --- | --- | --- |
| **Watch** | `Watch` | Maintain current state from the data delivered | Collection watch | Every item, then one `Bookmark` | One `Added`, `Modified` or `Deleted` per item | Never |
| | | | State watch | The whole value | The whole value again on each change | Never |
| | | | Keyed state watch | One whole value per key | The whole value of the key that changed | Never |
| **Feed** | `Subscribe` | Maintain a retained history from the data delivered | Event feed | The events so far, then one `Bookmark` | New events; a repeat upserts by id | Never |
| | | | Time series feed | The whole window of samples, even if empty | Only the samples newer than the last | Never |
| **Tail** | `Tail` | Consume an immutable sequence from a position | Cursor tail | The entries past the start position | Each entry once, in order | With a final `Ended` frame |
| **Notification** | `Listen` | React to an occurrence without receiving the state | Change notification | Nothing until a change | A key-only notice; bursts coalesce | When the publisher closes |
| | | | Broadcast notification | Nothing until an occurrence | Every occurrence, in order, never merged | When the publisher closes |

"Never" means the contract has no ending of its own. Every subscription still stops when its
context is cancelled, and a broken source ends one with an error (see [Endings](#endings-and-failures)).

## Naming

A Go method is the family's verb, the noun, then the kind's qualifier. A GraphQL field is the
same words, camel-cased, with the family noun last.

| Kind | Go method | GraphQL field | Frame type |
| --- | --- | --- | --- |
| Collection watch | `Watch<Noun>Collection` | `<noun>CollectionWatch` | `<Noun>CollectionWatchFrame` |
| State watch | `Watch<Noun>State` | `<noun>StateWatch` | None: the value itself |
| Keyed state watch | `Watch<Noun>KeyedState` | `<noun>KeyedStateWatch` | None: the value, carrying its key |
| Event feed | `Subscribe<Noun>EventFeed` | `<noun>EventFeed` | `EventFeedFrame` |
| Time series feed | `Subscribe<Noun>TimeSeriesFeed` | `<noun>TimeSeriesFeed` | `<Noun>TimeSeriesFeedFrame` |
| Cursor tail | `Tail<Noun>FromCursor` | `<noun>Tail` | `<Noun>TailFrame` |
| Change notification | `ListenFor<Noun>Changes` | Go only | None: the key alone |
| Broadcast notification | `ListenFor<Noun>Notifications` | Go only | None: one `<Noun>Notification` |

A frame is an envelope around the data, so only the kinds that need one have a frame type. A
notification is its own delivery. One delivery is singular and what a `ListenFor…` method returns
is plural: a change notification's receiver type is `<Noun>ChangeNotifications`, and a broadcast
notification's method returns a channel of `<Noun>Notification`.

Rules that apply to every kind:

- **The noun names what one delivery is about, in its natural number.** A collection watch's noun
  is one item, so it is singular: `clusterCollectionWatch`, `chatMessageCollectionWatch`. A state
  watch's noun is the value, so it is plural when the value is a list or a set: `modelsStateWatch`,
  `toolSettingsStateWatch`. A feed's or a tail's noun is the history or sequence as it is
  ordinarily called: `clusterEventFeed`, `logsTail`. The noun never names the scope.
- **A Go method drops the noun when it is its receiver's subject.** `Clusters().WatchCollection()`,
  not `Clusters().WatchClusterCollection()`. A GraphQL field always carries it.
- **A scope is a `By<Scope>` suffix in Go and an argument in GraphQL**:
  `Caches().WatchCollectionByCluster(ctx, clusterID)`.
- **A qualifier is never repeated.** A noun that already ends in `State` takes no second one:
  `authStateWatch`. A noun ending in `Status` is renamed to end in `State`.
- **`Watch`, `Subscribe`, `Tail` and `Listen` are reserved** for these families wherever a name
  opens one subscription of one kind. A poller or a background task takes another verb.
- **`Subscription` is the generic noun, and `subscribe` the generic verb, for code that carries
  any kind.** That is the transport and nothing else: GraphQL's own `subscription` keyword, the
  host's `graphql_subscribe`, `graphql_unsubscribe` and `log_window_subscribe` commands, the
  webview's `subscribe-exchange.ts` and `useFoldedSubscription`, and prose about "every
  subscription".
- **A name from outside keeps its meaning.** A Kubernetes watch, the kubelet's `follow`
  parameter, `net.Listen` and a library's `Subscribe` are not renamed and do not make a kind.
- **A GraphQL field's description opens with its kind**: `"A collection watch of …"`.
- **Notifications stay inside the sidecar.** Nothing on the wire tells a client to re-read.

In the webview, an operation is named for its field, Pascal-cased (`ClusterCollectionWatch`),
its document constant adds `Document`, and its hook is `use` plus the operation name.

## Choosing a kind

1. **Is the subscriber handed the data?** If it only learns that something happened and reads
   the source itself, it is a **Notification**.
2. **Does the subscriber keep only what is true now?** That is a **Watch**. If it keeps a
   history, it is a **Feed**. If it keeps nothing and consumes each entry once, it is a **Tail**.
3. Within **Watch**: one value, small and rarely changing, is a **state watch**, even when the
   value is a list. A value per key, each changing on its own, is a **keyed state watch**. A set
   that is large or changes often is a **collection watch**.
4. Within **Feed**: events with identities are an **event feed**. Samples on a fixed interval are
   a **time series feed**.
5. Within **Notification**: if the notice names something the subscriber can read (a row, a
   key, a cache), it is a **change notification**. If it reports an occurrence with nothing to
   read behind it (the machine woke), it is a **broadcast notification**.

The same data is often layered. `appdb` publishes a change notification per table; the chat
service listens, re-reads and diffs, and publishes a collection watch. The layers have different
kinds and different names.

The same list can be watched three ways. With `[a, b, c]` held and `d` added, a collection watch
sends `Added(d)`, a state watch sends `[a, b, c, d]`, and a change notification sends a notice
that carries nothing.

## The Watch family

The subscriber holds a copy of the current state and nothing else. It never needs a second
call: what the subscription delivers is enough to rebuild the state from nothing.

### Collection watch

The publisher mirrors a keyed set, one item per frame.

**Delivery**

- On subscribe, one `Added` frame per item in the set, in no promised order.
- Then exactly one `Bookmark`, carrying no item. It closes the snapshot. An empty set sends the
  `Bookmark` alone.
- Then one `Added`, `Modified` or `Deleted` per change. A `Deleted` carries the item's last-known
  state.
- The publisher never filters. A record awaiting deletion arrives as an ordinary `Modified`
  carrying its mark; `Deleted` means it is gone, and only the `Deleted` is promised.
- A frame of a scoped watch carries its scope (`cacheID`, and for objects `apiVersion` and
  `resource`), so a subscriber can tell which subscription a frame came from.

**The subscriber's job**

- Keep a map keyed by `id`. Upsert on `Added` and `Modified`, remove on `Deleted`.
- Treat everything before the `Bookmark` as loading. Never draw an empty state before it.
- Detect the `Bookmark` as `type === 'Bookmark'`, never as a missing item: a nested non-null
  field that errors nulls its parent, so a null item also arrives on ordinary changes.
- Drop a change that carries no item. Fold the `Bookmark` away; never key it.
- On a reconnect, start again from an empty map. The new subscription replays the whole set.
- Reject a frame whose scope is not the one now subscribed to. A superseded subscription can
  still deliver.
- Join to other collections client-side, by id. A publisher never embeds one collection in
  another.

### State watch

The publisher holds one value and sends all of it.

**Delivery**

- On subscribe, the current value, at once. There is no `Bookmark`: the first frame's arrival is
  the boundary between "not known yet" and "known".
- Then the whole value again each time it changes.
- Every frame is a level, not an edge. A publisher may skip intermediate values for a slow
  subscriber, and a frame equal to the last one is harmless.

**The subscriber's job**

- Replace what it holds with each frame. Nothing is merged or diffed.
- Treat "no frame yet" as loading.
- Never infer what changed from two frames in a row; some may have been skipped.

A state whose value is not yet known says so in the value (`Restoring`), rather than delaying
the first frame.

### Keyed state watch

A state watch over many keys on one subscription, for values that change independently and
often enough that resending all of them would be wasteful.

**Delivery**

- On subscribe, one frame per key that has a value now. Each frame carries its key.
- Then one frame, the whole value, for each key whose value changes.
- There is no `Bookmark` and no removal frame.

**The subscriber's job**

- Keep a map by key and replace the entry on each frame.
- Take membership from somewhere else. With no `Bookmark` it cannot tell "no keys" from "not
  yet", and with no removal frame it cannot tell when a key is gone. Join the map onto the
  collection watch that owns the keys, and ignore entries with no owner.

If the subscriber needs membership from the subscription itself, the kind is a collection watch.

## The Feed family

The subscriber holds a history: more than the current value, less than everything that ever
happened. The publisher retains a bounded history too, and replays it on subscribe.

### Event feed

**Delivery**

- On subscribe, the retained events, newest first, then exactly one `Bookmark`.
- Then each new event as it happens. An event that changes (a run that gains an occurrence) is
  sent again under the same id.
- The publisher prunes old events and never announces it. There is no `Deleted`.

**The subscriber's job**

- Upsert by the event's id, and order by the event's own fields, not by arrival.
- Treat everything before the `Bookmark` as loading.
- Cap what it keeps. The publisher will not evict for it, so a long-lived subscriber's history
  can outgrow the publisher's.
- On a reconnect, start again from an empty history.

### Time series feed

**Delivery**

- The first frame is the whole retained window, oldest first, sent even if empty. Its arrival is
  the boundary a `Bookmark` marks elsewhere.
- Each later frame holds only the samples newer than the last one sent.
- Every frame carries `intervalSeconds` and `windowSeconds`.
- A sample is sent on every tick, zero included. A quiet source is a flat line, not silence.

**The subscriber's job**

- Append each frame's samples and keep the last `windowSeconds ÷ intervalSeconds`. The webview's
  one fold for this is `timeSeriesFeedReducer`.
- Count samples; never trim or order by time. A wall clock that steps would drop or reorder them.
- Subscribe unshared. A subscriber that joins a stream already running misses the window.

## The Tail family

### Cursor tail

The source is an append-only sequence. The subscriber consumes it from a position and holds no
state the publisher gave it: an entry, once delivered, is the subscriber's.

**Delivery**

- Delivery starts at the position the subscriber names: a cursor, a time, or "now" when it names
  neither. Nothing is replayed from before it.
- Each entry is delivered once, in the source's order, and carries the cursor that names it.
- A `Notice` frame may arrive between entries, with something the reader should know.
- The last frame is `Ended`, saying why. Tailing again from the same position would end the
  same way.

**The subscriber's job**

- Append. Never upsert, reorder or deduplicate by content.
- Remember the cursor of the last entry consumed. A source made of several streams has one such
  cursor per stream. To resume after a drop, tail again from the same start, naming them.
- Treat `Ended` as final, and show its reason. A drop without `Ended` is a failure to resume
  from.
- Detect each frame by `type`, never by which field is missing.

## The Notification family

The subscriber is told that something happened and nothing about what the state now is. These
are Go APIs inside the sidecar.

### Change notification

**Delivery**

- Nothing on subscribe.
- One notice per change, carrying the key that changed and no value.
- Notices for one key coalesce: a subscriber that falls behind gets one notice, not one per
  change. A notice for one key is never lost behind another key's.
- The receiver closes when the publisher does.

**The subscriber's job**

- Listen first, then read. A change that lands between a read and a later listen is missed.
- On each notice, re-read the source for that key. The read is the truth; the notice is only
  the reason to make it.
- Never count notices. One notice can stand for many changes.
- Close the receiver when done.

A change notification carries no value on purpose. A value handed through a queue that coalesces
would be written from a state that has already moved on.

### Broadcast notification

**Delivery**

- Nothing on subscribe.
- One delivery per occurrence, in order, each with its own payload: what happened and when.
  Occurrences are never merged.
- The publisher never blocks. Its buffer is bounded, so a subscriber that falls further behind
  than the buffer loses the oldest occurrences.
- The channel closes when the publisher does, or when the subscriber cancels.

**The subscriber's job**

- Act on each occurrence, and keep up: do the work elsewhere if it is slow.
- Make the reaction safe to run once for several occurrences, since the oldest can be lost.
- Cancel when done; an abandoned channel leaks its feeder.

A notice that names something to re-read is a change notification, which cannot lose a key.

## Endings and failures

- **Go.** A Watch, Feed or Tail whose source can fail returns a `*Stream[T]`: `Frames`, plus
  `Err()`. `Frames` closes on every exit, and `Err()` is read after it closes. A nil `Err()` is a
  clean end. A subscribe that fails before the first frame returns the error from the method
  instead.
- **GraphQL.** A resolver over a `*Stream` uses `subscriptionStream`, which turns a non-nil
  `Err()` into one terminal errors-only frame marked `extensions: {"subscriptionFailed": true}`. The webview's
  subscribe exchange treats that frame, and any completion, as a drop: it reports, marks the
  subscription disconnected and reconnects.
- **A reconnect is a new subscription.** The first delivery is sent again, and the subscriber
  starts from nothing. A cursor tail is the exception: it resumes from its cursor.
- **The only ending that is part of a contract is a tail's `Ended`.**

→ [ADR: delta-watch protocol](adr/2026-08-09-delta-watch-protocol.md),
[ADR: watch failure reporting](adr/2026-08-14-watch-failure-reporting.md),
[ADR: a series is a third kind of stream](adr/2026-11-02-a-series-is-a-third-kind-of-stream-and-the-pool-owns-its-clock.md).

## Adding a subscription

1. Choose the kind with [Choosing a kind](#choosing-a-kind).
2. Name the method, field and frame type from [Naming](#naming).
3. Implement the kind's delivery rules in full. A subscription that sends a snapshot with no
   `Bookmark`, or a state watch that sends nothing until the first change, is a different kind
   and misleads every subscriber written against the name.
4. Open the GraphQL field's description with the kind.
5. In the webview, fold it with the reducer for its kind.
6. Test the first delivery, one later delivery and the ending, each on its own.
