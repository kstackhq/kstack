---
title: The cursor and the Backend seam
scope: sidecar
status: Planned
---

# The cursor and the `Backend` seam

**Needs:** nothing in this sequence. **Unblocks:** steps 3 and 4, which each move one read onto
the store and would otherwise each carry the other's cursor change.

Go paths are under `sidecar/internal/services/logs/`. The design is the README's §4 (the
`Backend`) and §8 (the cursor); this file says what to build and how to know it is built.

## In short

Two changes that alter what a page and a follow do in one corner alone. A position names its
stream's instance by `container_id` in place of `previous`, so a line keeps its key across a
restart. And the code that reads lines from the kubelet moves behind a `Backend` interface, so a
read has one shape whoever serves it. `Fetch` and `Follow` both run over the seam when this step
lands. The fetch and follow behaviour tests pass over it; the tests that spell a cursor or build
a position change with the encoding, and one that pins how a 400 ends a stream changes with the
failure kinds below.

## What is not in this step

- **No store.** Nothing here touches `logstore`. The fetcher and the follow still read the
  kubelet directly and hold nothing between requests.
- **No second backend.** The kubelet is the only implementation. A test-only backend lives in the
  test files and nowhere else.
- **No resolve behind the seam.** `resolve` in `sources.go` stays where it is and keeps its
  signature. Which streams a view holds is the mirror's answer today, and a backend's answer only
  once there is a second backend; step 3 adds `Streams` to the interface when it needs it.
- **No claim behind the seam.** A claim belongs to one request, not one cluster: `Fetch` takes
  it with `acquireClaim` under the server UID its own resolve read and closes it with the page,
  and `Follow` holds it for its life, waiting on `revoked`, checking `isRevoked` as each stream
  ends and handing `get` to every read. Both keep doing so, and build the kubelet backend over
  the claim's `get`, per request. One backend per cluster waits for step 3, where the cluster's
  list of backends is what a fill walks.
- **No wire change.** `LogRecord.stream.previous` keeps its meaning on the wire; it is computed
  at the record, as today, from the resolve.

## Design

### 1. The position

`position` in `cursor.go` is a moment, a stream and an ordinal. The stream half keeps `Namespace`,
`Pod` and `Container` and takes `ContainerID` in `Previous`'s place: the id the resolve already has
for every stream it hands out (`container_id`, or `last_container_id` for a previous instance).
`comparePositions` orders by time, then namespace, pod and container name as today, then the
ordinal, then the id; `comparePrevious` goes. So lines at one moment keep their order on the wire,
and the one order that moves is two instances of one container at one moment, which interleave by
ordinal where they sorted the previous first — a tie only equal timestamps make, to the nanosecond,
and nothing else. The ordinal goes before the id because the id is the resolve's, which lags a
restart: a page can end on a line keyed with the old id that the next page keys with the new one,
and were the id to decide the tie, the new instance's later lines at that moment would all sort
before the cursor and be lost; a follow reopening a stream would keep or drop them by how two ids
happen to sort. With the ordinal first both do what they did before. The encoding stays as it is,
the id in `previous`'s field, and `parseCursor` checks what the resolve guarantees of an id and
nothing more: not empty, no tab or control character, at most 256 bytes. A runtime's id is text the
server wrote — `containerd://<hex>` on a real node, whatever a virtual kubelet or a test fixture
spells — so a shape check would refuse cursors the server itself handed out. The id is the cluster's
own status text, so the resolve applies the same three checks to each `container_id` and
`last_container_id` it reads and skips a stream whose id fails one, as it skips one with no id: the
server never hands out a cursor it would refuse. A cursor is longer than before and still opaque.

`StreamKey` on the wire keeps `namespace`, `pod`, `container` and `previous`. The record carries
the id too, since the merge keys on the position, and nothing on the wire changes. The follow's
`tracked` map stays keyed by `StreamKey`; its `last` is a position compared by time first, so a
stream reopened past a restart resumes past its last line as today, the id deciding only a tie
at one moment.

**The id is the resolve's, not the read's.** A read of the current log answers whichever
instance the kubelet serves now, and the mirror lags it: around a restart, the new instance's
first lines can be keyed with the old id, then with the new one on a later read, so a line can
go out twice under two keys — and, from step 3 on, be stored twice. Nothing in this step closes
it; step 4's tailer, which reads one instance from its own last line, is where it is judged
again. Recorded here so it is not read as a bug in the seam.

### 2. The `Backend`

In `backend.go`:

```go
type Backend interface {
    Forward(ctx, stream, readOpts) *streamRead
    Backward(ctx, stream, readOpts) *streamRead
    Tail(ctx, stream, readOpts) *streamRead
}
```

**A read is a stream, not a batch.** It answers today's `streamRead`: lines on a channel the
caller pulls one at a time, a `cancel`, and once the channel closes how it ended — `err`, and
`short` with its `stop` edge. That is what the merge needs: `mergePage` asks a reading for a line
only when the merge wants one and cancels every reading once the page is cut, so a page of a
busy stream reads nothing past its last line, and `trimToStops` reads each stream's edge. A
finished batch would read every stream to its budget and lose the edge. `readOpts` goes through
unchanged — `since`, `until`, `grep`, `budget`, `batch`, `windows` — so the bounds, the budget
split and `maxGets` stay where they are and the grep still runs inside the reader. `Tail` is
`Forward` with `follow` set, kept as its own method because an archive will not offer it.

**A failure is one of four kinds**, and `failureKind(err)` in `backend.go` is the one
classification, with `fatal` and `dropped` folded into it:

| Kind | The kubelet backend | What it means |
| --- | --- | --- |
| `Fatal` | a 401, a revoked claim | the user's credentials, or Kstack no longer connecting: fails the whole page or follow |
| `Denied` | a 403 | this stream's alone: a page's `skipped` entry; a follow ends the stream as `Ended` does, reopening it at a later generation, and remembers the reason, which ends the follow once every stream has ended |
| `Ended` | any other status under 500 | the server's answer for this log now (`container is waiting to start`, a 404): a page's `skipped` entry; a follow ends the stream and reopens it at a later generation |
| `Transient` | a 5xx, a 408, a 429, a transport error | dropped and retried as today, under the follow's backoff; a request the server timed out or throttled says nothing about the log, so a follow the API server's rate limiting refused does not wait for the pod to change |

A clean end stays what it is today: for a fetch the end of the range, and for a follow on a
container the view sees running, a drop. `errRetired` stays a short read, not a failure kind.
`Ended` is what keeps `TestAFollowWaitsForAWaitingContainer` and `TestDropped` true: a 400 is
not retried on backoff, it waits for the generation to move.

The kubelet backend is `kubelet.go`: `Forward` and `Backward` are the two halves of `fetcher.go`,
each `GET` over a claim as today, and `Tail` is the forward stream `follow.go` opens. This is a
move, not a rewrite: the fetcher's tests keep their cases.

`Fetch` builds its readings over `Backend.Forward` and `Backend.Backward`, and `Follow`'s reconcile
opens streams through `Backend.Tail`. Each builds its backend once its claim is made, through a
`func(get getter) Backend` field on the service, a test seam like `now` and `retry`: `logs.New`
sets it to the kubelet backend and a test sets it to its fake, so nothing outside the package
changes.

The interface names `stream`, `readOpts` and `*streamRead`, all unexported, so only code in
`logs` can implement it — the kubelet backend and the test fake, the two there are. "One shape
whoever serves it" holds inside the package for now; exporting the types is step 3's call, when
the first backend outside the kubelet's shape is designed.

## Tasks

1. **The position.** `cursor.go` and its test: `ContainerID` in `Previous`'s place,
   `comparePrevious` gone, `parseCursor`'s id check, and the resolve handing each stream its id.
   The `position{…}` literals in the other tests move with it.
2. **The interface and the kubelet backend.** `backend.go` with `failureKind`, `kubelet.go`, the
   fetcher and the follow moved behind it, `Fetch` and `Follow` over it, `logs.New` taking the
   backend function.
3. **The seam's tests.** A fake `Backend` in `testutil_test.go` and the tests under *Tests* that
   drive it.

## Tests

Go, beside the files they cover.

- `cursor_test.go`: a cursor round-trips the id; a line's cursor is the same before and after
  its instance becomes the previous one; `parseCursor` refuses an empty id, one holding a
  control character, and one past 256 bytes, and takes the fixtures' `containerd://<uid>-<name>-<n>`
  (a tab in the id splits the cursor, so the field count refuses it before the id check runs);
  two instances of one container at one moment order by ordinal, the id deciding an equal
  ordinal alone, a line past a cursor's ordinal at its moment sorts after it whichever id it
  carries, and lines of two containers at one moment order by name as before.
- `fetch_test.go`, `follow_test.go`, `fetcher_test.go`, `merge_test.go`: the behaviour cases pass
  over the seam; the cases that spell a cursor or build a `position{…}` change with the encoding,
  and `TestDropped` becomes a test of `failureKind`. `TestPagesJoinWithoutLossOrRepeat` and
  `TestAFollowWaitsForAWaitingContainer` are the gate.
- `backend_test.go`: over the fake, a `Fatal` read fails the page, and a `Denied` or `Ended` one
  becomes a `skipped` entry with the merge of the other streams untouched; a `Fatal` tailer ends
  the follow, a `Denied` one ends its stream alone and is the reason once every stream has ended,
  an `Ended` one holds the stream until the generation moves, and a `Transient` one is reopened
  after the backoff.

## Security

**Widens:** nothing. A cursor is still text the server wrote, and `parseCursor` still refuses one
that names neither a moment nor a line; the id check refuses what no server writes — an empty
id, a control character, 256 bytes — and no more, since a stricter shape would refuse the
server's own ids. The claim stays with `Fetch` and `Follow`, and the kubelet backend reads only
through its `get`, so a disabled or deleted cluster still cuts every read.

**Residual:** a line keyed with the resolve's id around a restart can go out twice under two
keys (§1). A repeat on the wire, never a missing line.

## When it lands

- `sidecar/CLAUDE.md`: the cursor paragraph says a position names its stream's instance by
  `container_id` beside the names; the `services/logs` paragraphs name the `Backend`, its four
  failure kinds, and that the kubelet is the one behind it.
- This README's row reads Built, with the pull request.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/services/logs/...`; `make lint-go`, `make vet-go`;
`make test-changed` while working.
