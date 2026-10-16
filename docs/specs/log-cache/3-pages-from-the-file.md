---
title: Pages from the file
scope: sidecar
status: Planned
---

# Pages from the file

**Needs:** step 1, the store; step 2, the cursor and the `Source` seam. **Unblocks:** step 4,
whose follow reads the chunks this step's fills write; step 5, whose eviction needs something
to evict.

Go paths are under `sidecar/internal/`. The design is the README's §3 (coverage), §4 (a page)
and the fetch half of §9; this file says what to build and how to know it is built.

## In short

`Service.Fetch` answers from the file. It resolves the view, adds the streams the file holds
that the mirror no longer does, reads what the file covers, fills the gaps from the cluster's
sources in order, and answers one statement over `lines`. Grep runs in that statement through
`kstack_grep`. The page's edges are the README's table. `logsFetch` takes `mode`, `cursor`,
`at` and `limit`, and the webview's generated types follow. The pages walk runs over the store,
and a new walk pins the invariant.

## What is not in this step

- **No follow over the file.** `Follow` still reads the kubelet through the seam. Step 4.
- **No tailers**, so a page's live end is always a forward fill to each running stream's end.
- **No eviction, no `read_at_ms` stamps.** Step 5. Chunks only grow in this step.
- **No production source but the kubelet.** Labelled chunks and the tier rules are exercised by
  test-only sources, since no real source writes a label or sits behind another yet.
- **No webview change.** The generated types change; nothing in `src/` uses `logsFetch` yet.

## Design

### 1. Coverage

The README's §3, built in `logstore` as `Coverage(stream, pattern, from, to)`: the chunks
covering a range for a pattern, which are the chunks labelled with it and the unlabelled ones,
and the gaps between them. A fill writes as §3 says: a chunk from the first line returned, merged
with the one ending at the moment it began in only when that moment came back whole; `head` when
the source said so; `end` when a finished instance's forward read reached its last line. Rows
and the chunk change in one `InTx`.

### 2. The page

`Service.Fetch` in `fetch.go`, over `logstore` and the `Source`:

1. Resolve the view inside `CachedData().ReadActive` as today, map each stream onto a
   `streams` row and insert the missing ones with their snapshot (place, `labels`,
   `started_at`), then add the file's streams for the view's sources that the mirror no longer
   has, with the label filters run over `streams` for those.
2. Per stream and direction, read the coverage and offer each gap to the cluster's sources in
   order: the first source whole, the next only for the range the one before answered `head`
   for. The kubelet source's budget split and `maxGets` are unchanged. Write each read.
3. Answer one statement over `lines` for the view's streams, ordered by
   `(ts_ns, container_id, ord)`, bounded by the cursor and `limit`, in one read transaction.
   Compute the edges: the cut, the live end, and `before` null only when every stream's oldest
   chunk is `head` from the last source.

The fills are `INSERT OR IGNORE`, so `ReadActive` running the reading twice writes nothing new.
`Record.stream.previous` is computed here: true when the stream's `container_id` is not the
pod's current one, unknown for a stream with an empty id.

### 3. `kstack_grep`

A Go function registered with the SQLite driver, `kstack_grep(pattern, message)`, since modernc
registers functions per driver and not per connection, so the pattern rides as the first
argument. It keeps a small cache of compiled patterns, sixteen, since a view has one pattern and
a file has few views open. The page's statement calls it only when the view has a grep, over
every chunk the page reads, labelled or not. The scan is bounded by the reading's byte budget,
and a page cut by the bound answers `more: true`.

### 4. The wire

`logsFetch(id, view, mode, cursor, at, limit)` answering `LogRecordsPage` with `records`,
`before`, `after`, `anchor` and `skipped`, per the log viewer README's *The wire*. `gqlgen.yml`
binds `LogRecordsPageEdge` onto `logs.PageEdge` and `Around` onto `logs.FetchAround`. The old
`since`, `until`, `after` and `before` arguments go from `logsFetch`. `logsFollow` is untouched
until step 4. Run `gqlgen generate` and `pnpm codegen`; the webview builds, since nothing in
`src/` queries `logsFetch`.

## Tasks

1. **Coverage and fills.** `logstore/coverage.go` and `logstore/fill.go`: the lookup, the chunk
   rules, the tier offer, and the statements they run, added to `statements.go` here, with
   `OnBoth` for a read a fill makes inside its write transaction. `logs.New` takes the manager.
   Tests over a temporary file with a fake source.
2. **The page.** `fetch.go` over the store: the three steps above, the edges, `previous`
   computed. `TestPagesJoinWithoutLossOrRepeat` runs over the store.
3. **`kstack_grep`.** `logstore/grep.go`, the registration, the cache, the bound.
4. **The wire.** `graph/schema.graphqls`, `gqlgen.yml`, the resolver, `gqlgen generate`,
   `pnpm codegen`.
5. **The walk.** The invariant's seeded walk, under *Tests*.

## Tests

Go, beside the files they cover, with the fake kubelet from the existing fetcher tests extended
to rotate its files and restart its pods.

- `coverage_test.go`: a read from a moment claims from its first returned line; a read that
  began in a chunk's last moment and returned it whole merges; one that did not leaves a gap;
  `head` only from the source's word; `end` only on a finished instance; a chunk closes at
  `ChunkBytes` or `ChunkSpan`; a page for pattern `P` reads `P` and unlabelled chunks and treats
  `Q` as a gap; an unfiltered page treats every labelled chunk as a gap; with two sources in
  order, the second is asked only for what the first answered `head` for, the edge moment belongs
  to one of them whole, and `before` is null only when the second says `head` too.
- `fetch_test.go`: `TestPagesJoinWithoutLossOrRepeat` over the store, unchanged in its cases; a
  page over a gap fills it; a second page over the same range makes no source read; a line found
  by a grepped fill and again by an unfiltered one is stored once; a deleted pod's streams,
  lines and place survive and a node filter still finds them; an instance followed as current
  and read as previous after a restart has one row and one copy of each line; a finished
  instance read to its end gets `end` and no forward fill after; `ReadActive` running twice
  writes nothing new; the `before` and `after` edges match the README's table in each mode.
- `grep_test.go`: a pattern matches as Go's `regexp` does over stored text; the cache evicts past
  sixteen; a scan past the budget cuts the page with `more: true`.
- `walk_test.go`: the README's invariant over 100 seeds of 60 steps, 25 seeds under `-short`,
  each in parallel, with evictions done by hand through the store since the janitor is step 5.
- `graph`: `logsFetch` binds the new arguments and refuses `at` with `Head` or `Tail` and a
  cursor with `Around`.

## Security

**Widens:** cluster data is written to disk. From this step the file holds log lines in the
clear under `<cache>/logs/`, which the bash tool's `DeniedDirs` covers and `Read`, `Write` and
`Edit` fence. `docs/security-model.md` gains its row in this step, under *By decision*, pointing
at the ADR step 6 writes; the row moves with the change, as the root `CLAUDE.md` asks.

**Residual:** a log holds whatever the container wrote, a secret included, as `kubectl logs`
would show it. `kstack_grep` runs a pattern the model may have written over text the cluster
wrote; Go's `regexp` is linear in both, the pattern is capped at 1,000 bytes by the tool's
parse, and the message at 16 KiB by `stripControl`. The model reaches no line through the
file.

## When it lands

- `sidecar/CLAUDE.md`: the `logsFetch` paragraph says the file, the coverage, the tiered
  sources and `kstack_grep`; the cursor paragraph says a cursor is a bare position because the
  file holds each stream's extent.
- `docs/security-model.md`: the row above.
- This README's row reads Built, with the pull request.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/services/logs/... ./internal/graph/...`; the wire
checks: `cd sidecar && go run github.com/99designs/gqlgen generate`, then `pnpm codegen`,
`pnpm build`, `make test-js`, `make lint-js`; `make lint-go`, `make vet-go`; `make test-changed`
while working.
