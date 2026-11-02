---
title: Connection data rate history
scope: sidecar, webview
status: Planned
---

# Connection data rate history

**Answers:** #636. **Builds on:** #634, which added `clusterDataRateWatch`.

Go paths below are under `sidecar/internal/services/cluster/` unless they say otherwise.

## In short

A cluster's connection detail shows how fast its connection moves data, as two numbers. We want
a small line chart (a sparkline) beside them that shows the last 60 samples, normally two
minutes, and we want it full the moment the row opens.

Today that is not possible. Each subscriber to `clusterDataRateWatch` runs its own sampler, so:

- history starts when the panel opens, not before;
- two panels on one cluster sample it twice;
- a quiet connection sends nothing, so the client cannot tell "zero" from "no news".

After this change, **the connection pool samples every connection on one clock and keeps the
last 60 samples**. The subscription sends that window first, then one new sample per tick.

## What is not in this change

- **No history across restarts.** Nothing is written to disk.
- **No history past the last claim.** The ring lives exactly as long as some claim on the
  context does. Disabling a cluster drops the controller's claim, but a log stream or a Bash
  tool call holds its own, so the ring may outlive the disable by as long as they do. Nothing
  resets it on purpose.
- **No per-request or per-kind split.** The counter sits on the socket, and HTTP/2 puts every
  request on one connection, so the bytes cannot be told apart.
- **No client-chosen interval.** There is one cadence. A client that wants fewer points drops
  some.

## Design

### 1. The pool samples

`internal/kubeconn` gains one sampler for the whole pool, in a new `datarate.go` beside
`bytecount.go`.

**One ticker, every 2 seconds.** On each tick the sampler:

1. takes `mu` and copies each claimed context, its `entry` and the entry's `conn`;
2. lets go of `mu`;
3. reads `Connection.Bytes` on each connection it copied;
4. takes `mu` again, and for each entry still in `claimed` appends one sample to its ring and
   nudges its context.

The connection is `entry.conn`, the pool's own copy, which a probe pass sets. The sampler never
calls `Lease.Conn`: that reads the supervisor, and must not run under `mu`.

An entry can be released, and its context claimed again, between steps 2 and 4. Step 4 skips an
entry that `claimed` no longer holds, as `record` does, so a dropped ring is not written and the
new holder is not nudged for a sample it never had.

Reading a counter is two atomic loads, so one pass over every connection is cheap. There is no
goroutine per connection. The two loads are not one snapshot: received and sent may come from
instants a few nanoseconds apart. At a 2-second cadence that does not matter.

**Two exported constants, `DataRateInterval` (2 seconds) and `DataRateWindow` (2 minutes)**,
set the ticker's cadence and the ring's size, and are what the cluster service puts on a frame.
One place, so the frame cannot disagree with the ring.

**Where it runs.** The cadence and the clock are fields on `Service`, set by `New` to
`DataRateInterval` and `time.Now`. A test sets them to a channel it ticks by hand and a clock it moves. `Start`
runs the sampler on `s.wg` under `loopCtx`, beside the kubeconfig watch, so the stop function
`Start` returns ends it the same way.

**The ring lives on the pool's `entry`**, one per claimed context. It holds the last 60
samples, which is two minutes of regular ticks. It keeps readings, not an age: after the
machine sleeps, the oldest sample may be hours old, and nothing filters it. It is created with
the entry and dropped with it. The cluster controller holds a
claim on every enabled cluster, so an enabled cluster always has a ring.

**`mu` guards the ring.** The sampler appends under it, and `DataRates` reads under it. Sixty
small slots make this cheap. Nothing else needs a lock.

**Each sample is numbered.** The ring counts samples from 1 for the entry's life. The number is
how a reader tells which samples it has already seen. Readers never compare times: wall time can
step backwards, and the number cannot.

**Each tick adds one sample to each ring**, computed from this reading and the last one:

| Case | Sample |
| --- | --- |
| Same connection as last tick (`Seq` matches) | bytes moved ÷ seconds elapsed |
| The connection was replaced (`Seq` differs) | zero |
| No connection | zero |
| First tick after the entry was created | none; it only records the reading |

A replacement counts from zero, so comparing its count to the old one would be wrong. A zero
keeps the line unbroken.

**The time on a sample is when it was read**, not when the tick was scheduled. The sampler
calls the clock in step 3, after releasing `mu`, so a tick that ran late carries its real time.
That is also where a test's clock can run code mid-tick, which the re-claim test needs. Elapsed seconds come from Go's monotonic clock, so a wall-clock step
does not make a rate negative.

A stored `At` keeps its monotonic reading. `UTC`, `In` and `Round(0)` strip it, and the
elapsed-seconds division relies on it, so any formatting happens at the resolver.

A sample is:

```go
type DataRateSample struct {
	// Seq numbers the sample within its ring, from 1. A reader keeps the last Seq it saw and
	// takes what is newer.
	Seq                    uint64
	At                     time.Time
	ReceivedBytesPerSecond float64
	SentBytesPerSecond     float64
}
```

### 2. The lease exposes it

`Lease` gains two methods:

```go
// DataRates is the context's last 60 samples, oldest first. Empty until the second tick
// after the context was first claimed.
DataRates() []DataRateSample

// WatchDataRates fires after each tick adds a sample. It carries no value: the reader calls
// DataRates for the samples. The caller closes it, as with WatchState.
WatchDataRates() Subscription
```

`DataRates` follows the same rule as `Conn` and `State`: it answers while the pool holds the
entry the claim was made for, and reads as empty once it does not. Reading through a claim you
released is the holder's bug, as it is for `Conn`.

The notification is only a nudge, and the ring is the record. A reader that falls behind is
nudged once and finds every sample it missed in the ring, so nothing is lost. It sits on a new
`conflate` hub keyed by context, apart from `signalHub`, so a tick never wakes a reconcile.

Adding methods to `Lease` means every fake that implements it grows them: `fakeLease` in
`testutil_test.go`, and any fake in `kubesync`.

### 3. The cluster service streams it

`datarate.go` in the cluster service loses its sampler, and `service.newTicker` goes with it.
It gains `ClusterDataRateSeriesFrame` and `ClusterDataRateSample`, which mirror the schema as
`ClusterDataRate` does today, mapped in `sidecar/gqlgen.yml`. The frame's interval and window
are `kubeconn.DataRateInterval` and `kubeconn.DataRateWindow` in seconds. `Seq` is not copied
across. `Clusters().WatchDataRate(ctx, id)` becomes:

1. Claim the cluster's context, as it does today. A disabled cluster, or one being deleted, is
   still refused.
2. Subscribe to `WatchDataRates`, then read `DataRates` and send it all as the first frame,
   even if it is empty. Remember the last `Seq` sent.
3. On each nudge, read `DataRates` and send the samples with a `Seq` above the last one sent.
   If there are none, send nothing.

Subscribing before the first read means a tick between the two cannot be missed: its nudge
arrives, the read finds no new samples, and nothing is sent. The `Seq` comparison is what makes
that safe. Every frame after the first holds one sample unless the reader fell behind.

### 4. The wire

```graphql
"""
How fast a cluster's connection moved data over one sampling interval. Counted on the wire:
TLS, headers and framing included, every stream to the API server counted.
"""
type ClusterDataRateSample {
  "When the sample was read."
  at: Time!
  "Bytes per second received from the API server."
  receivedBytesPerSecond: Float!
  "Bytes per second sent to the API server."
  sentBytesPerSecond: Float!
}

type ClusterDataRateSeriesFrame {
  "Seconds between samples. The same on every frame."
  intervalSeconds: Int!
  "How far back the series reaches. The same on every frame. The client keeps the last window ÷ interval points."
  windowSeconds: Int!
  "Oldest first. The first frame holds the whole window, and each later frame the samples since."
  samples: [ClusterDataRateSample!]!
}

type Subscription {
  "A cluster's connection data rate as a series. The first frame arrives at once and holds the whole window, possibly none. Each later frame holds the samples since the one before. Every tick is sent, zeros included. A replaced or missing connection reads as zero. Refused for a disabled cluster or one being deleted."
  clusterDataRateWatch(id: ClusterID!): ClusterDataRateSeriesFrame!
}
```

`Seq` stays inside the sidecar. The wire carries `at` for a reader that wants it, such as a
tooltip; the chart does not use it.

Changes from today:

- The `interval` argument is gone. The pool has one cadence, and the frame says what it is.
- The "send only when it moved" rule is gone. A zero is the flat part of the line and must
  arrive.
- `ClusterDataRate` is gone, replaced by `ClusterDataRateSample` inside a frame.
- The frame is an object, not a bare list, like every other subscription in the schema.

**This is a third kind of stream.** It is not a delta watch (no keys, no `Bookmark`) and not a
gauge (it sends a window, not one current value). It is a **series**: a window on subscribe,
then samples appended. The docs name it that way, and an ADR records the decision.

**The series convention.** This is the first series, and more will follow, so its shape is
the rule for all of them:

1. **The first frame is the whole retained window, sent even if empty.** The client treats
   the first frame's arrival the way it treats a delta watch's `Bookmark`: before it, nothing
   is known; after it, an empty list means empty.
2. **Every frame is a list of points, oldest first, each with `at`.** A later frame holds only
   points newer than the last one sent. The frame carries `intervalSeconds` and
   `windowSeconds`; their quotient is how many points the client keeps.
3. **Names follow the other streams.** The subscription ends in `Watch`, like every
   subscription. The frame type ends in `SeriesFrame`, as a delta watch's ends in
   `WatchFrame`. A future `clusterCacheSizeWatch` returns a `ClusterCacheSizeSeriesFrame`.
4. **The client folds every series the same way**: append the frame's points, then keep the
   last `windowSeconds ÷ intervalSeconds`. It never compares times, so a wall clock that steps
   cannot freeze or reorder it. One shared helper does it (section 5).

There is no cursor for resuming after a reconnect. The window is 60 points, so sending it
again is cheaper than the cursor would be.

### 5. The webview

In `src/components/widgets/cluster-sync-panel.tsx`:

- **A shared fold, `seriesReducer`, in `src/lib/graphql/use-watch-subscription.ts`** beside
  `perFrame`. It appends each frame's points and keeps the last `windowSeconds ÷
  intervalSeconds`. It reads no timestamps, so the client has nothing to get wrong when the
  clock steps. Every series uses it, so a new series is a type and a query, not a new reducer.
  `useWatchSubscription` already starts the fold over on a reconnect, so a reconnect's new
  window does not duplicate points.
- **`useDataRate` returns the samples and the interval**, oldest first, through that fold. It
  passes `{ unshared: true }`. `ConnectionDetail` is mounted in two places, the sync panel's
  row and the status popover, and consumers of one query share one stream by default. A view
  that mounts into a running stream never sees its first frame, so it would take its first
  one-sample frame for the whole history. An unshared stream costs one more subscription per
  open view, which the small window makes cheap.
- **The text row reads the last sample**, as it reads the single value today.
- **A new `DataRateSparkline`** draws the samples as an inline SVG sized to the row: one line
  for received and one for sent, one point per slot, the newest at the right edge, fewer
  points leaving the left empty. Both lines share one vertical scale, from zero to the larger
  of the two maxima, so they can be read against each other. All zeros draws two flat lines on
  the baseline. A late tick after sleep is one more sample in the next slot, not a gap;
  what value it carries depends on the bytes and the clock, and the chart does not care. Colors are two series
  colors from the `dataviz` skill's palette, chosen at implementation. The SVG is
  `aria-hidden`, since the text row already says the numbers.
- One subscription feeds the text row and the sparkline.

## Tests

- **Pool (`kubeconn/datarate_test.go`):** driving the ticker by hand,
  - a sample per tick per claimed context, with the right rate;
  - a replaced connection gives a zero sample;
  - no connection gives a zero sample;
  - the ring keeps the newest 60;
  - samples are numbered from 1 and the numbers keep rising past 60;
  - the ring goes when the last claim is released, and a released claim reads empty;
  - a context released and re-claimed between a tick's read and its write gets no sample and
    no nudge from that tick;
  - two leases on one context see the same ring (one sampler);
  - the stop function `Start` returns ends the sampler.
- **Cluster service (`datarate_test.go`):** the first frame is the whole window, even empty;
  each nudge sends only the new samples; a nudge with nothing new sends nothing; a reader
  that falls behind by fewer samples than the ring holds still gets every one; a disabled
  cluster is refused. The tests on the old sampler move to
  the pool or go.
- **Resolver:** the frame maps through with its interval and window, and the `interval`
  argument test goes.
- **Webview:** `seriesReducer` appends, keeps the last `window ÷ interval` points, and folds
  an empty first frame to an empty array rather than `undefined`; points whose `at` values
  decrease or repeat still append, and the text row shows the latest received value; a second `ConnectionDetail`
  mounted into a running stream gets the whole window; the text row shows the last sample;
  the sparkline draws two lines on one scale and a flat baseline for all zeros.

## Done when

- [ ] Opening a cluster's detail row shows a sparkline with up to 60 samples of history,
      normally two minutes, at once, including flat zero stretches.
- [ ] Two panels on one cluster share one sampler, pinned by a pool test.
- [ ] A replaced connection draws a zero, not a gap or a spike.
- [ ] `clusterDataRateWatch` sends every tick.

## When it lands

- Root `CLAUDE.md`: the streams paragraph names the series as a third kind beside delta
  watches and gauges, states the four rules of the series convention, and describes
  `clusterDataRateWatch` as one.
- `sidecar/internal/services/cluster/CLAUDE.md`: the data-rate bullet under *Streams and
  watches* describes the stream; *The connection pool* gains the sampler, the ring and its two
  `Lease` methods.
- `sidecar/graph/schema.graphqls`: the comment at the top of `Subscription` names two live
  shapes. It names three, with the series described in one sentence.
- `src/CLAUDE.md`: `seriesReducer` listed beside `perFrame` as the fold for a series.
- An ADR, "a series is a third kind of stream, and the pool owns its clock", linked from both
  `CLAUDE.md` files. It records the convention's rules and why the window rides the
  subscription rather than a separate query.
- No row in `docs/persisted-data.md`: nothing is persisted.
