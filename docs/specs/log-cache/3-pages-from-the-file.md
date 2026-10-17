---
title: Pages from the file
scope: sidecar
status: Planned
---

# Pages from the file

**Needs:** step 1, the store; step 2, the cursor and the `Backend` seam. **Unblocks:** step 4,
whose follow reads the chunks this step's fills write; step 5, whose eviction needs something
to evict.

Go paths are under `sidecar/internal/`. The design is the README's §3 (coverage), §4 (a page)
and the fetch half of §9, and the sidecar wire note's §2 (the edges); this file says what to
build and how to know it is built.

## In short

`Service.Fetch` answers from the file. It resolves the view as today, adds the file's older
instances and gone pods for its Pod sources, reads what the file covers, fills the gaps from the
cluster's backends in order until each stream has a page's worth past the cursor, and answers
one index walk per stream, grepped in Go and merged by `merge.go` in one read transaction. The
page's edges are the log viewer README's table. `logsFetch` takes `mode`, `cursor`, `at` and
`limit`, and the webview's generated types follow. The pages walk runs over the store, and a
new walk pins the invariant.

## What is not in this step

- **No follow over the file.** `Follow` still reads the kubelet through the seam. Step 4.
- **No tailers**, so a page's live end is always a forward fill to each running stream's end.
- **No eviction, no `read_at_ms` stamps.** Step 5. Chunks only grow in this step.
- **No production backend but the kubelet.** Labelled chunks and the tier rules are exercised by
  test-only backends, since no real backend writes a label or sits behind another yet.
- **No change to the resolve.** `resolve` hands out what it does today: a container's current
  instance, or its last one for a source with `previous`, at most `maxStreams` of them. The
  follow, the `LogsView` tool, its prompt and its receipt keep their meaning.
- **No `Backend.Streams`.** The kubelet's streams are the mirror's resolve, and the file's are
  the store's own lookup below; the interface gains `Streams(view)` with the first backend that
  answers it differently.
- **No file streams for a workload source.** A Deployment, Job or other workload reads the pods
  the mirror holds for it, as today. A stream row carries no owner, and tying a gone pod to its
  workload would mean matching its stored labels against a selector, which nothing in this
  sequence reads. So a Job's finished pods and a rollout's replaced ones are read while the
  mirror holds them and not after. It is a residual, listed under *Residuals*.
- **No `kstack_grep`.** The grep runs in Go over each stream's walk (§3), so no function is
  registered with SQLite.
- **No webview change.** The generated types change; nothing in `src/` uses `logsFetch` yet.

## Design

### 1. Coverage

The README's §3, built in `logstore` as `Coverage(stream, pattern, from, to)`: the chunks
covering a range for a pattern, which are the chunks labelled with it and the unlabelled ones,
and the gaps between them, each gap **live or lost**. A fill writes as §3 says, its `origin` the
backend that read it: a chunk from the first line returned, merged with the one ending at the
moment it began in only when that moment came back whole; `head` when the backend said so;
`at_end` when a finished instance's forward read reached its last line. Rows and the chunk
change in one `InTx`.

**`head` from either direction.** A backward read that reached the backend's oldest line marks
its chunk `head`, and so does a forward read with no lower bound, which starts at that line: a
`Head` page with no cursor fills forward from the start of the log. The kubelet backend reports
both.

**The link says the backend served nothing between two chunks.** A chunk's `prev_ns` and
`prev_ord` name the last line of the chunk before it, and mean that its origin backend holds no
line between that position and the chunk's first. Positions alone cannot prove it, and without
the link `Coverage` would read every seam as a gap and fill it on every page. Three fills write
one:

- A fill that reaches `ChunkBytes` or `ChunkSpan` closes its chunk at the last line written and
  opens the next at the line after, linked to the closed one.
- **A fill that read its whole range links the chunk at its far bound.** A fill's range ends at
  the neighbouring chunk's edge, exclusive, and a read that was not cut short (`short` false, no
  failure) served every line in it. So a forward fill into the next chunk sets that chunk's link
  to the fill's last line, and a backward fill back to the chunk before links its own chunk to
  that chunk's last line. A fill cut short leaves the seam a live gap, and the next page fills
  what is left.
- **A forward fill whose first line is past where it was asked to start links across the
  hole.** The kubelet answers a `sinceTime` with the lines it still has from that moment on, so a
  first line later than the moment means it holds nothing in between: those lines were rotated
  away, and rotation never gives them back. The chunk starts at the first line returned, as §3
  says, linked to the chunk the gap started at.

Two chunks are one span when the later one's link is the earlier one's last position, and a
stream's coverage is a run of spans. A chunk whose link is NULL opened on a gap, and one whose
link names a chunk that is gone (evicted, in step 5) starts a span, since the lines before it
are gone. The same link joins a tailer's chunk to the one it extends, in step 4.

**A gap is lost when no backend can fill it**, and a walk crosses a lost gap as it crosses a
seam. A gap is lost when it is older than a `head` chunk of the stream from the cluster's last
backend, since that backend holds nothing older and no backend after it exists; when the stream
is exhausted (§2.2), since nothing serves it; or when a link spans it. The first needs no write:
a `Tail` page that reached the kubelet's oldest line at 10:30 over a stream whose `Around`
landing earlier filled 9:55 to 10:05 marks the 10:30 chunk `head`, and the gap between is lost
by that mark alone. Every other gap is live, and a walk stops at it. With tiered backends `head`
and the link are the origin backend's word, so a gap that is lost to one backend is offered to
the next as §3's tier rule says, and is lost only past the last. What was in a lost gap is a
residual: the viewer cannot tell a lost range from a quiet one.

**A fill is filed under the instance the backend served, never only the one the resolve
named.** The resolve reads the mirror, and the mirror lags a restart, so the resolve can name
instance A while the kubelet already serves B's log; a fill written under A would claim A's
coverage of a range that holds B's lines, and a later read of A as previous would never ask for
them again. Two rules close it:

- **Check the instance once per stream, after its reads and before its write.** A stream's
  reads for a page are buffered (§2.3), so each stream makes one check: `Fetch` reads the pod's
  container status again in a short `ReadActive`, and if the id the stream was resolved as is no
  longer the one the kubelet was asked for (the pod's current id for a current instance, its last
  id for a source with `previous`), the reads are thrown away and the stream is the page's
  `skipped` entry, with *restarted while reading* as its line. The next page resolves the new
  instance.
- **Trim a finished stream to its end.** A container status's time is a `metav1.Time`, whole
  seconds, while a line's stamp is nanoseconds, so the cut is the end of `finished_at`'s second.
  The store deletes the stream's lines stamped past the cut and trims its chunks to it, and a
  trim that removed anything clears `at_end` on the stream's chunks, since the end that read
  reached was another instance's. It runs when a resolve first sets `finished_at`, and again
  after every write to a stream whose `finished_at` is set, since a read the check passed on a
  lagging mirror can still have been served the next instance's log. So lines the mirror's lag
  filed under the old instance are read again under the new one. A restart inside one second
  leaves the new instance's first lines of that second under the old one; it is a residual.

### 2. The page

`Service.Fetch` in `fetch.go`, over `logstore` and the `Backend`:

1. **Resolve.** Inside `CachedData().ReadActive` as today, the resolve maps each stream onto a
   `streams` row with its snapshot: the place, `labels`, `pod_uid`, whether it is the pod's
   default container, and `started_at` and `finished_at` off the pod's body (the container
   status's `state.running.startedAt` or `state.terminated`'s times, and `lastState.terminated`'s
   for a previous instance). Nothing on the log file happens inside the mirror reading, which may
   run the resolve twice and holds one of the mirror's pooled connections; the target carries
   the snapshot out.
2. **The file's streams.** Outside the mirror reading, in one `InTx` on the log file: insert the
   missing rows (`INSERT OR IGNORE`), update the place, the labels and `finished_at` of the ones
   there, trim a stream whose `finished_at` is newly set, then add the file's streams **that
   the resolve handed out for no source of the view, each once across the view**. A view can
   name one pod twice, plain and with `previous`, which is what the tool's prompt tells the
   model to do for a pod running again after a restart; the resolve keys its streams by
   `StreamKey`, so those are two kubelet streams, the current instance and the last, and the
   file must not add either again nor an older instance twice. So the page keeps one set of
   stream ids: the resolve's rows go in first, then each Pod source's file rows that are not
   in it yet. **Which rows a source adds follows the follow's rule: a pod of its name later is
   another pod.** For a Pod source whose pod the mirror holds, the file's streams with that
   `pod_uid`; for one whose pod the mirror does not hold, the file's streams of that namespace
   and name, any `pod_uid`: the gone pod. A Pod source names its containers as the resolve
   does: by name, every one under `allContainers`, else the rows marked default. `previous` on
   the source then drops a row whose `container_id` is the pod's current one, so for a gone
   pod, whose instances are all finished, it drops none. **A container's file rows are capped
   at its newest `maxInstances` (8) by `started_at`**, since a pod that crash-looped for days
   can hold hundreds, each a walk statement sharing `walkBudget`; the older ones are a
   residual. The view's node and label filters run over `streams` for the file's rows, since a
   gone pod has no node to join to. So a plain view reads its container's running instance
   from the kubelet and whatever the file holds of the instances before it; a view with
   `previous` reads the last instance from the kubelet and the file's older ones; the prompt's
   two-source view reads the current and the last from the kubelet and the older ones from the
   file, once each. A stream the file adds is **exhausted**: no backend serves it, so no fill
   is offered for it, its gaps are lost, and on the merged page it counts as having nothing
   older than its oldest line and nothing newer than its newest, whatever its chunks say. The
   file's rows are not counted against `maxStreams`, which bounds the kubelet reads; the cap
   above and the walk budget bound what they cost.
3. **Fill, per stream and direction.** Each stream the resolve handed out is filled until, in
   the page's direction from the cursor, it holds `limit + 1` lines that pass the grep, or has
   no live gap left that way (a `head` chunk from the last backend, an `at_end` chunk, or a
   forward read to a running instance's end), or its share of the fill's byte budget,
   `maxBudget` split across the streams as today, is spent. The stream walks the file first
   (step 4) up to its first live gap, counting its matches; while short it offers that gap to
   the cluster's backends in order, the first backend whole and the next only for the range the
   one before answered `head` for, **buffering the lines it reads and counting their matches**,
   then walks the file's next span past the gap, counting again, then the next gap, and so on:
   **the count covers every span between the gaps it fills**, so a stream the file covers in
   pieces is not filled past what the page needs. The buffer is bounded by the stream's budget share, as a page's readings are
   today. Then the instance check, then one `InTx` writes every read as §1 says. The kubelet
   backend's budget split and `maxGets` are unchanged. A kubelet fill is unfiltered, so a sparse
   grep reads far: the budget is what bounds it, as it bounds a grepped backward read today. A
   stream whose fill failed before any line in range, or whose check failed, is the page's
   `skipped` entry as today, and its coverage is left alone; **its walk still runs, up to its
   first live gap, and a stop at that gap does not constrain the cut**. The file may hold minutes
   of that stream from earlier pages, and showing them once the kubelet cannot is what the cache
   is for; its lines past the gap were unreadable this page anyway, as today. A walk that stops
   at `limit + 1` lines or at its share of `walkBudget` constrains the cut as any stream's does,
   since the lines past that stop are in the file and the next page must reach them. A stream
   whose fill spent its budget before writing a line in range still refuses the page with
   `errShort`, as today, since any cut would pass its lines.
4. **Answer, in one read transaction.** For each stream, one walk of `lines_position` from the
   cursor's position in the page's direction, **bounded by the span the cursor sits in, lost
   gaps crossed**: the walk reads only lines the file covers for the page's pattern, and stops at
   the first live gap, so a gap a fill left (out of budget, a backend that failed) ends the
   stream there with a `stop`, and the next page resumes from it. The invariant allows nothing
   else: a line past a live gap is in a chunk, but the gap's lines are not, and a walk that
   crossed it would skip them for good. The walk is one statement per stream, read whole inside
   the `InReadTx` before the merge runs, since `mergePage` pulls lazily and the transaction ends
   with its callback:

   ```sql
   SELECT ts_ns, ord, message FROM lines
   WHERE stream_id = ? AND (ts_ns, ord) > (?, ?) AND (ts_ns, ord) <= (?, ?)
   ORDER BY ts_ns, ord
   ```

   (reversed for `Tail`). Go greps each row, counts its bytes, and stops the scan at `limit + 1`
   matching lines or at the stream's share of the walk's own budget, `walkBudget`, a second
   constant split as `maxBudget` is, so a fill that spends everything leaves the walk its own. A
   walk cut by its budget stops where it reached. What is read is at most `limit + 1` lines per
   stream, bounded by the walk budget, so the merge's input is bounded in memory.

   **Each stream's bound is worked out from the cursor in Go.** A cursor is one position across
   the view, time then names then ordinal then id, and the statement's `(ts_ns, ord)` bound is
   that stream's own: at the cursor's moment a stream whose names sort before the cursor's starts
   after the moment, one whose names sort after starts at the moment inclusive, the cursor's own
   stream starts past its ordinal, and another instance of the same container starts at the
   cursor's ordinal, inclusive when its id sorts after the cursor's and exclusive when before.
   That is `Head`; `Tail` is the mirror image, each "after" read "before" and each "before"
   "after". A cursor that is a moment alone bounds every stream at the moment, inclusive or
   exclusive as the mode's edge says. `comparePositions` is the one definition and the test
   drives it with a stream of each kind at one moment, in both modes.

   The walks are `streamRead`s and `mergePage` merges them as today, and the page's cut is
   `mergePage`'s. One statement over every stream ordered by position cannot use the index, so
   SQLite would sort every line in range before `LIMIT`, and a `Tail` page over a full file
   would sort all of it; and a merge of per-stream walks is the one shape whose cut can name
   where each stream stopped. The order within a stream is the index's, `(ts_ns, ord)`, and
   across streams `comparePositions`', so the pages join without losing or repeating a line.

   **The edges** follow the log viewer README's table: the cut is the merge's; the live end is
   the newest line of each stream's newest chunk once step 3 has read each running stream to its
   end, or an exhausted stream's newest line; `before` is null only when every stream has
   nothing older to read, which is a `head` chunk from the cluster's last backend anywhere in
   its coverage, or being exhausted. Every gap a page stops at is live and the next page fills
   it, so no page answers `more: true` at the same spot twice.

`Record.stream.previous` is the view's for a stream the resolve handed out, as the follow keys
it. For a stream only the file holds it is computed here: true when its `container_id` is not
the instance a read with no `previous` serves now — the running one, or the one before while the
container waits — which for a gone pod is every stream. The wire's `LogStream` does not tell
two earlier instances of one container apart; a line's cursor does, and the viewer draws by the
container. It is a residual for the real client, which may ask for `startedAt` on the stream.

**The grep is the page's.** `readOpts.grep` goes: a backend read filters nothing, since a
kubelet fill must see every line of a moment to number it. The kubelet backend's budget and
windows stay.

**The grep is checked on the wire.** The tool's parse caps a pattern at `maxGrep` (1,000
bytes), but `logsFetch` and `logsFollow` take any `LogsViewInput` the webview sends, so
`services/logs` owns the cap: `MaxGrep`, which the tool's parse reads, and `compileGrep` refuses
a pattern past it or one that does not compile, for `Fetch` and `Follow` alike.

### 3. The grep

The grep runs in Go, over each stream's walk and over each fill's buffered lines, with the one
compiled pattern a page holds. Nothing is registered with SQLite: a page is per-stream walks
read in Go, so a filter in Go is the boring construction, and a SQL function would copy each
message into Go before matching it anyway. It runs over every chunk a walk reads, labelled or
not, since a labelled chunk's lines already match and one path is simpler than two. The walk's
bound is its span and its budget share, which counts every message's bytes, matched or not,
since a sparse grep over a dense log is what the budget is for.

### 4. The wire

The sidecar wire note's §2, built here: `FetchQuery` becomes `{Mode, Cursor, At, Limit}`;
`FetchAround` is the two halves inside one `Fetch`; `PageEdge` is `{Cursor, More}`; `Page`
carries `Before`, `After`, `Anchor` and `Skipped`; `errBadQuery` refuses `Around` without `at`
or with a cursor, and `Head` or `Tail` with `at`. A bound names its absence as a nil position,
never as a zero moment, which closes the note's open problem 2. `Follow` keeps building: it takes
its `after` bound through a helper of its own over `parseCursor` rather than through
`readOptsOf`, which goes with `FetchQuery`'s old fields, and its reads keep `readOpts` less the
grep. `Around`'s live end is its `Head` half's `after`, as the table says (open problem 10).

`logsFetch(id, view, mode, cursor, at, limit)` answers `LogRecordsPage` with `records`,
`before`, `after`, `anchor` and `skipped`, per the log viewer README's *The wire*. `gqlgen.yml`
binds `LogRecordsPageEdge` onto `logs.PageEdge` and `Around` onto `logs.FetchAround`. The old
`since`, `until`, `after` and `before` arguments go from `logsFetch`. `logsFollow` is untouched
until step 4. Run `gqlgen generate` and `pnpm codegen`; the webview builds, since nothing in
`src/` queries `logsFetch`.

## Tasks

1. **The seam's report and the schema.** `streamRead` says how its read ended: `head`, the
   backend holds nothing older, from a backward read or a forward one with no lower bound;
   `atEnd`, it reached a finished instance's last line, beside `short` and `stop`. `readOpts`
   loses `grep`, and the kubelet backend filters nothing. `follow.go` takes its `after` bound
   without `readOptsOf`. The schema gains `pod_uid` and `is_default` on `streams` and
   `prev_ns`, `prev_ord` on `chunks`, edited in place.
2. **Coverage and fills.** `logstore/coverage.go` and `logstore/fill.go`: the lookup with live
   and lost gaps, the chunk rules, the link and its three writers, the tier offer, the trim, and
   the statements they run, added to `statements.go` here, with `OnBoth` for a read a fill makes
   inside its write transaction. `logs.New` takes the manager. Tests over a temporary file with a
   fake backend.
3. **The page.** `fetch.go` over the store: the four steps above, the buffered reads and the
   one check per stream, the file's streams, each stream's bound off the cursor, the grep in Go,
   the edges, `previous` computed; `MaxGrep` and `compileGrep`, the tool's parse reading the
   constant. `TestPagesJoinWithoutLossOrRepeat` runs over the store.
4. **The wire.** `graph/schema.graphqls`, `gqlgen.yml`, the resolver, `gqlgen generate`,
   `pnpm codegen`.
5. **The walk.** The invariant's seeded walk, under *Tests*.

## Tests

Go, beside the files they cover, with the fake kubelet from the existing fetcher tests extended
to rotate its files and restart its pods.

- `coverage_test.go`: a read from a moment claims from its first returned line; a read that
  began in a chunk's last moment and returned it whole merges; one that did not leaves a gap;
  `head` from a backward read that ran out and from a forward read with no lower bound, and
  from no other; `at_end` only on a finished instance; a chunk closes at `ChunkBytes` or
  `ChunkSpan` and the next continues it, so the seam is one span and a page over it makes no
  read; a forward fill that reads its whole range links the chunk it ran into, and a backward
  one links its chunk to the one it ran back to, so neither seam is read again; a fill cut short
  leaves its seam a live gap; a forward read whose first line is past the gap's start links
  across the hole; a gap older than a `head` chunk is lost, and one older than a `head` from an
  earlier backend is offered to the next; a chunk whose link names a chunk deleted by hand starts
  a span; a page for pattern `P` reads `P` and unlabelled chunks and treats `Q` as a gap; an
  unfiltered page treats every labelled chunk as a gap; with two backends in order, the second
  is asked only for what the first answered `head` for, the edge moment belongs to one of them
  whole, and `before` is null only when the second says `head` too; a trim deletes the lines past
  the end of `finished_at`'s second, keeps the instance's own lines inside that second, cuts the
  chunks to it and clears `at_end` when it removed anything; a write to a finished stream is
  trimmed after it.
- `fetch_test.go`: `TestPagesJoinWithoutLossOrRepeat` over the store, unchanged in its cases; a
  page over a gap fills it; a second page over the same range makes no backend read; a line found
  by a grepped fill and again by an unfiltered one is stored once; a deleted pod's streams,
  lines and place survive and a node filter still finds them; a pod of the deleted pod's name
  with another UID reads its own streams alone; a plain view reads the running instance from the
  kubelet and the file's older ones with no fill, one with `previous` the last from the kubelet
  and the older ones from the file, and a gone pod the same rows either way; a default-container
  view of a gone pod reads the rows marked default; **the prompt's two-source view**, a pod
  named plain and with `previous`, walks the current and the last instance once each from the
  kubelet and each older one once from the file, every line out once and one answer per stream
  about more; a container with more than `maxInstances` file rows reads its newest ones; an instance followed as current and read as
  previous after a restart has one row and one copy of each line; **a restart that lands between
  the resolve and the read** files nothing under the old instance, names the stream in `skipped`,
  and the next page reads the new one; **a restart the mirror reports late** leaves the old
  instance's lines under it and the new instance's under the new row once the trim runs; a
  stream makes one instance check per page however many gaps it filled; a finished instance read
  to its end gets `at_end` and no forward fill after; the fill runs outside the mirror reading,
  pinned by a `ReadActive` that counts its readings; a sparse grep on a `Tail` page fills back
  until it holds `limit + 1` matches or reaches `head`, and stops at its budget with a cut and
  `more: true`; **a walk stops at a live gap**: a fill out of budget halfway through a gap ends
  the page at the gap and the next page resumes inside it, every line shown once; **a walk
  crosses a lost gap**: a `Tail` page that reached `head` over a stream with an older chunk below
  answers `before` null, and a `Head` page over an exhausted stream with a gap inside it reads
  past the gap, so no page answers `more: true` at one spot twice; a stream whose fill failed
  before any line is `skipped`, its lines the file holds up to its first live gap still answer,
  and its stop at that gap does not move the cut, while a skipped stream whose file span is
  denser than the page stops at `limit + 1` lines and holds the cut there, every line shown once; a fill over a stream the file covers in pieces counts
  each span and stops filling once the count is met; one whose fill spent its budget before
  a line in range refuses the page; a fill that spends `maxBudget` leaves the walk its own budget;
  **each stream's bound off one cursor**: at the cursor's moment a stream sorting before it, one
  sorting after, the cursor's own and another instance of its container each start where
  `comparePositions` says, and a moment cursor bounds every stream at the moment, for `Head` and for `Tail`; the `before`
  and `after` edges match the log viewer README's table in each mode; `Around` is its two halves
  and is refused with a cursor or without `at`; a grep past `MaxGrep` is refused by `Fetch` and
  `Follow`; a pattern matches as Go's `regexp` does over stored text.
- `walk_test.go`: the README's invariant over 100 seeds of 60 steps, 25 seeds under `-short`,
  each in parallel, with evictions done by hand through the store since the janitor is step 5.
- `graph`: `logsFetch` binds the new arguments and refuses `at` with `Head` or `Tail` and a
  cursor with `Around`.

## Residuals

- **A workload source's gone pods are not read from the file** (see *What is not in this
  step*). Closing it needs an owner on the stream row or a selector match over `labels`.
- **A plain view shows an earlier instance only as far as the file holds it.** The last instance
  is filled from the kubelet only for a source with `previous`, as today; a view that never
  looked before the restart has nothing of it. An archive backend, or a tailer that outlives the
  instance (step 4), is what fills it.
- **A container's instances older than its newest `maxInstances` in the file are not read**
  until the older ones are evicted. A view that needs them is a later change to the view's
  shape.
- **Two earlier instances of one container share a `LogStream`** on the wire; a cursor tells
  them apart. Settled with the real client.
- **What was in a lost gap is gone from the backend that lost it**, and the viewer cannot tell a
  lost range from a quiet one. An archive backend is what would fill it.
- **A restart inside one second** leaves the new instance's first lines of that second under the
  old one, since `finished_at` is whole seconds.
- **A line delivered late, stamped before a `Head` cut**, lands in the file under its stamp and
  is never on a page after the cut (the note's open problem 8). Rare, and the follow's watermark
  in step 4 is per stream, so a follow still sends it.

## Security

**Widens:** cluster data is written to disk. From this step the file holds log lines in the
clear under `<cache>/logs/`, which the bash tool's `DeniedDirs` covers and `Read`, `Write` and
`Edit` fence. `docs/security-model.md` gains its row in this step, under *By decision*, pointing
at the ADR step 6 writes; the row moves with the change, as the root `CLAUDE.md` asks.

**Residual:** a log holds whatever the container wrote, a secret included, as `kubectl logs`
would show it. The grep runs a pattern the model or the webview sent over text the cluster
wrote; Go's `regexp` is linear in both, the pattern is capped at `MaxGrep` on the wire and in the
tool, and the message at 16 KiB by `stripControl`. The model reaches no line through the file.

## When it lands

- `sidecar/CLAUDE.md`: the `logsFetch` paragraph says the file, the coverage with its live and
  lost gaps, the tiered backends, the per-stream walks and the grep in Go; the cursor paragraph
  says a cursor is a bare position because the file holds each stream's extent.
- `docs/security-model.md`: the row above.
- The `LogsView` prompt (`tools/logsview/prompts/logsview.md`) says a source shows the
  running instance; from this step a plain view also shows what the file holds of the instances
  before it. Nothing the model opens reads the new wire until the real client lands, so the
  prompt's sentence and its goldens change with that step, not this one; this step leaves a line
  in the log viewer README's *What is left* saying so.
- The README's §2, §3 and §4 already say what this step builds; its row reads Built, with the
  pull request.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/services/logs/... ./internal/graph/...`; the wire
checks: `cd sidecar && go run github.com/99designs/gqlgen generate`, then `pnpm codegen`,
`pnpm build`, `make test-js`, `make lint-js`; `make lint-go`, `make vet-go`; `make test-changed`
while working.
