---
title: The cursor and the Source seam
scope: sidecar
status: Planned
---

# The cursor and the `Source` seam

**Needs:** nothing in this sequence. **Unblocks:** steps 3 and 4, which each move one read onto
the store and would otherwise each carry the other's cursor change.

Go paths are under `sidecar/internal/services/logs/`. The design is the README's §4 (the
`Source`) and §8 (the cursor); this file says what to build and how to know it is built.

## In short

Two changes that alter no behaviour. A position names its stream by `container_id` instead of
the container's name and `previous`, so a line keeps its key across a restart. And the code that
reads lines from the kubelet moves behind a `Source` interface, so a read has one shape whoever
serves it. `Fetch` and `Follow` both run over the seam when this step lands, and every existing
test passes unchanged over it.

## What is not in this step

- **No store.** Nothing here touches `logstore`. The fetcher and the follow still read the
  kubelet directly and hold nothing between requests.
- **No second source.** The kubelet is the only implementation. A test-only source lives in the
  test files and nowhere else.
- **No wire change.** `LogRecord.stream.previous` keeps its meaning on the wire; it is computed
  at the record, as today, from the resolve.

## Design

### 1. The position

`position` in `cursor.go` is a moment, a stream and an ordinal. The stream field becomes the
`container_id`, which the resolve already has for every stream it hands out (`container_id`, or
`last_container_id` for a previous instance). `comparePrevious` goes: two instances of one
container differ by id. The encoding stays as it is, the id in the container's place, and
`parseCursor` checks the id's shape: the runtime's prefix, a separator, hex, and at most 256
bytes, refusing anything else. A cursor is longer than before and still opaque.

`StreamKey` on the wire keeps `namespace`, `pod`, `container` and `previous`. The record carries
the id too, since the follow's `tracked` map and the merge key on the position, and nothing on
the wire changes.

### 2. The `Source`

In `source.go`:

```go
type Source interface {
    Streams(ctx, reading *cluster.CacheReading, view View) ([]Stream, error)
    Forward(ctx, Stream, from position, opts) (Read, error)
    Backward(ctx, Stream, until position, want int, opts) (Read, error)
    Tail(ctx, Stream, from position) (Tailer, error)
}
```

A `Read` is the lines with the source's own `ord`, whether it reached the oldest line the
source holds (`Head`) or, for a finished instance, the last (`End`), and the `Pattern` it
filtered by, empty for the kubelet. A `Tailer` delivers `Line`s and ends with a `Failure` or
nil. A `Failure` has a `Kind`, one of `Fatal`, `Denied` or `Transient`, and the reason in the
user's words; `Fatal` fails the whole page or follow, `Denied` is one stream's, `Transient` is
retried as today. The kubelet source maps a 401 and a revoked claim onto `Fatal`, a 403 onto
`Denied`, and the rest onto `Transient`.

The kubelet source is `kubelet.go`: `Streams` is the resolve in `sources.go`, `Forward` and
`Backward` are the two halves of `fetcher.go`, each `GET` over a claim as today, and `Tail` is
the forward stream `follow.go` opens. The budget and `maxGets` stay where they are, passed in
`opts`. This is a move, not a rewrite: the fetcher's tests keep their cases.

`Fetch` builds its readings over `Source.Forward` and `Source.Backward`, and `Follow`'s reconcile
opens streams through `Source.Tail`. `Service` holds one `Source` per cluster, from a function
`app` passes, which this step answers with the kubelet source for every cluster.

## Tasks

1. **The position.** `cursor.go` and its test: the id in the stream field, `comparePrevious`
   gone, `parseCursor`'s new checks, and the resolve handing each stream its id.
2. **The interface and the kubelet source.** `source.go`, `kubelet.go`, the fetcher and the
   follow moved behind it, `Fetch` and `Follow` over it, `logs.New` taking the source function.
3. **The seam's tests.** A fake `Source` in `testutil_test.go` and the two tests under *Tests*
   that drive it.

## Tests

Go, beside the files they cover.

- `cursor_test.go`: a cursor round-trips the id; a line's cursor is the same before and after
  its instance becomes the previous one; `parseCursor` refuses an id without the prefix, with a
  non-hex body, or past 256 bytes; two instances of one container order by id.
- `fetch_test.go`, `follow_test.go`, `fetcher_test.go`, `merge_test.go`: pass unchanged over the
  seam. `TestPagesJoinWithoutLossOrRepeat` is the gate.
- `source_test.go`: over the fake, a `Fatal` read fails the page and a `Denied` one becomes a
  `skipped` entry with its coverage of the merge untouched; a `Fatal` tailer ends the follow and
  a `Denied` one ends its stream alone.

## Security

**Widens:** nothing. A cursor is still text the server wrote and `parseCursor` refuses more
than it did. The claim stays in the kubelet source, so a disabled or deleted cluster still cuts
every read.

**Residual:** none.

## When it lands

- `sidecar/CLAUDE.md`: the cursor paragraph says a position names its stream by `container_id`;
  the `services/logs` paragraphs name the `Source` and that the kubelet is the one behind it.
- This README's row reads Built, with the pull request.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/services/logs/...`; `make lint-go`, `make vet-go`;
`make test-changed` while working.
