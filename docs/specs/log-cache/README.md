# The log cache

The sidecar keeps log lines in a SQLite file, one per cluster cache. `logsFetch` and
`logsFollow` answer from that file. When the file has a gap, the sidecar fills it from the API
server. Each container is tailed once, however many viewers are reading it.

The webview does not change. The `Client` contract, the log viewer library and the viewer stay
as they are. What changes is what sits behind `Service.Fetch` and `Service.Follow` in
`services/logs`, and what a cursor has to carry.

Go paths are under `sidecar/internal/`. This is a sequence of six steps, listed under
[*Steps*](#steps), each a pull request into `wip/log-views`, next to the log viewer sequence
([`../log-viewer/README.md`](../log-viewer/README.md)). It serves that README's *The wire*
section, and that README says `wip/log-views` may not merge until the sidecar serves the wire,
so this sequence shares its branch on purpose rather than taking a `wip/log-cache` of its own. This README is the shared design; each step's file states only what is its own.
It replaces §4 of [`../log-viewer/notes/the-sidecar-wire.md`](../log-viewer/notes/the-sidecar-wire.md),
which planned to put per-stream hints inside a page's cursor. The cache holds that state
instead, so a cursor stays a plain position. The note's §1 (the wire's types), §2 (the edges) and
§5 (the resolvers) still stand, and this sequence builds them.

## Why

Today, every page and every follow makes its own requests to the API server. The webview keeps
every line it has ever read, because it cannot count on reading a line again: a backward page is
kubetail's doubling `tailLines` windows, the kubelet rotates logs, and a pod takes its log with
it when it goes.

That has four costs. Two windows on one pod are two follows. The model's `LogsView` reads
nothing a viewer could share. Scrolling back re-reads the same bytes. A long follow grows the
webview without end.

A cache owned by the sidecar fixes each one. Reading a line again becomes an index lookup that
returns exactly what it returned before. That is what lets the webview drop lines later (*The
bounded window* in the log viewer README). One tailer serves every reader. Backward pages come
from the file. And the file keeps lines after the kubelet has dropped them, which is the only way
to show a log across two restarts.

## What is not in this sequence

- **No webview change.** The real client and the bounded window are the log viewer README's own
  bullets. They come after this. The window's margin must stay under this spec's `ReadTTL`, so
  that what the webview drops, the sidecar still has.
- **No `LogsRead`, and no model access to the cache.** The model reaches no line through this
  file. A later tool will read through `Service.Fetch` under its own gate.
- **No change to how a follow orders frames.** Still per stream, as the webview expects.
- **No change to the reads themselves.** The forward read, the backward windows, the budget per
  stream and `reader.lines` all stay. They become how a gap is filled, behind the `Backend`
  interface (§4).
- **No cluster agent, and no archive.** kubetail's agent, which greps on the node, is a second
  `Backend` for later, and an archive such as Loki or Datadog, which holds lines the kubelet has
  rotated away, is a third. The schema and the cursor are shaped so that each fits without a
  change to stored data (§3, §8), and that is all this sequence does for them.
- **No filter by label.** Selecting pods by label is a new kind of view source, a change to
  `LogsViewInput`, the tool's parse, the resolve and the wire, and none of it gets harder by
  waiting. The cache only has to keep each pod's labels on its streams, which it does (§2), since
  that is the one part that cannot be added after a pod is gone.
- **No stdout/stderr on a line.** The kubelet API strips the CRI tag, so no backend this sequence
  builds knows it; the agent reads the file and does. It is an attribute, not part of a line's
  key, and the cache file is one Kstack rebuilds, so adding a `stream` column then is a schema
  edit, not a migration. It lands with the first backend that fills it, together with the
  viewer's colouring and a filter for it.
- **No encryption and no redaction of cached lines.** The file holds cluster data in the clear,
  like the mirror's file does (see *Security*).

## Design

### 1. The file

One file per cluster cache: `<cache>/logs/<cache id>.db`. It sits next to the mirror's
`kube/<cache id>.db` and lives as long as it does. It is created on the first read of a view
over that cache. It is removed when the cache is removed.

`services/logs` owns the file through a manager shaped like `kubestore`'s: a refcounted open
whose lock is never held across a file's open, close or delete, `OpenOrCreate`, `Remove` (idempotent; the id stays tombstoned for the manager's
life), `Close`, and one janitor per open file. The open settings are `sqlitepool`'s (WAL,
`synchronous=NORMAL`, `busy_timeout`, incremental auto-vacuum). The statements are a
`sqlstmt.Set`. The schema is one `0001_init.sql`, edited in place under the pre-release schema
policy.

The cache pass's deleting arm removes the file before it clears `cacheFinalizer`, at the point
where it calls `kubestoreMgr.Remove` today. `services/cluster` cannot import `logs`, so it takes
a small interface, `CacheFileOwner` with `Remove(cacheID int64) error`. The logs manager
implements it and `app` wires the two together. A failed removal fails the pass, as the mirror's
does. A crash between the two removals needs no sweep: the finalizer is still set, so the
deleting arm runs again at the next start and `Remove` is idempotent. `clusterCacheClear`, the
user throwing a cluster's cached rows away, clears the log file too through the same interface
(`Clear(cacheID)`). A clear is what `kubestore`'s is: the entry is retired, a handle still held
answers `ErrClosed`, so every follow on the file ends and a page in flight fails, the next open
creates a fresh file under a new entry, and releasing an old handle never touches the new entry's
count. There is no tombstone, since the cache goes on. `clusterCacheClear` runs the log clear
first, outside `RunWithCacheSyncStopped`, since no sync worker writes the log file; a log clear
that fails errors before the mirror is touched, and the user runs it again. Logs hold secrets more often than
object bodies do, so a clear that left them would surprise. **The manager is built before the
cluster service and sits before it in `Runtime.parts`**, as `kubestore`'s manager sits before the
controllers inside `cluster.New`, so it closes after the service whose cache pass calls it. A
`Remove` on a closed manager still deletes the file, as `kubestore`'s does, since the deleting arm
clears the finalizer on its answer and nothing else would ever delete it; a `Clear` on one deletes
the file and opens nothing.

### 2. The schema

Three tables. Every moment is nanoseconds since the epoch. NULL is not zero.

```sql
CREATE TABLE streams (
  id            INTEGER PRIMARY KEY,
  namespace     TEXT NOT NULL,
  pod           TEXT NOT NULL,
  pod_uid       TEXT NOT NULL,             -- the pod the instance ran in; '' when unknown
  container     TEXT NOT NULL,
  is_default    INTEGER NOT NULL,          -- 1: the pod's default container, as kubectl picks it
  container_id  TEXT NOT NULL,             -- the runtime instance whose log these lines are; '' when the backend knows only the name
  started_at    INTEGER,                   -- when the instance started, unix ns; NULL when unknown
  finished_at   INTEGER,                   -- when it stopped; NULL while it runs
  node TEXT NOT NULL, region TEXT NOT NULL, zone TEXT NOT NULL, os TEXT NOT NULL, arch TEXT NOT NULL,
  labels        TEXT NOT NULL,             -- the pod's labels as a JSON object; '{}' when unknown
  UNIQUE (namespace, pod, container, container_id)
) STRICT;

CREATE TABLE chunks (
  id          INTEGER PRIMARY KEY,
  stream_id   INTEGER NOT NULL REFERENCES streams(id) ON DELETE CASCADE,
  pattern     TEXT NOT NULL,               -- the grep the fill ran under; '' for every line
  from_ns     INTEGER NOT NULL,            -- the first line's moment, unix nanoseconds
  from_ord    INTEGER NOT NULL,            -- and its ordinal: a chunk's edges are positions
  to_ns       INTEGER NOT NULL,            -- the last line's moment
  to_ord      INTEGER NOT NULL,
  prev_ns     INTEGER,                     -- the position this chunk continues from: the last line of the
  prev_ord    INTEGER,                     --   chunk before it, nothing between; NULL when it opened on a gap
  origin      TEXT NOT NULL,               -- the backend that filled it, by name
  at_head     INTEGER NOT NULL,            -- 1: the backend has nothing older than from_ns
  at_end      INTEGER NOT NULL,            -- 1: to_ns is the log's last line (a finished instance)
  open        INTEGER NOT NULL,            -- 1: a tailer is still writing into it
  bytes       INTEGER NOT NULL,
  read_at_ms  INTEGER NOT NULL,            -- the last read or write, unix milliseconds: the one ms column, since it is a clock and not a line's moment
  UNIQUE (stream_id, pattern, from_ns, from_ord)
) STRICT;

CREATE TABLE lines (
  id          INTEGER PRIMARY KEY,
  stream_id   INTEGER NOT NULL REFERENCES streams(id) ON DELETE CASCADE,
  ts_ns       INTEGER NOT NULL,
  ord         INTEGER NOT NULL,            -- the backend's number for the line within its moment
  message     TEXT NOT NULL                -- after stripControl, so at most 16 KiB
) STRICT;
CREATE UNIQUE INDEX lines_position ON lines (stream_id, ts_ns, ord);
```

A **stream** is one runtime instance of a container: its namespace, pod and container name plus
its `container_id`. A restart makes a new stream, and the old instance's lines stay under the old
one. **`previous` is not stored.** It is how the kubelet is asked for an instance, not a property
of the instance: the same `container_id` is the current one while it runs and the previous one
after a restart, and storing it under both would keep its lines twice. A view with `Previous`
resolves to the pod's `last_container_id`, as today, and that is simply a stream row; the file
adds what it holds of the instances the resolve handed out for no source, each once (§4). The wire's
`stream.previous` is computed when a page is read: true when the stream's `container_id` is not
the pod's current one. It describes the line and no longer names it.

**The place and the times are a snapshot, not a copy of the mirror.** For a pod the mirror
still holds, the node and its labels are there too. But the cache keeps lines after the pod is
gone, and the mirror holds only what is in the cluster now, so a gone pod's lines would have no
place to join to. The place is also where the pod ran *then*: a node's labels can change later.
So `node`, `region`, `zone`, `os`, `arch`, `labels`, `pod_uid`, `is_default`,
`started_at` and `finished_at` are written from the mirror when the stream row is created, and
the place and the labels are updated in place if they change while the instance runs. `pod_uid`
is what tells a gone pod's streams from a later pod of its name, and `is_default` is
which of a gone pod's streams a view naming no container reads; neither can be read once the pod
is gone. They describe the stream and never name it, the same rule as `previous`. `labels` is the pod's labels, kept so a filter by label can select a
gone pod's streams later; nothing reads it in this sequence, and it is taken now because it cannot
be taken after the pod is gone. Annotations are not kept: they can run to kilobytes, and a
filter over them can read the mirror for the pods still there. `finished_at` is set from the container status's `terminated.finishedAt`
when the follow's reconcile or a resolve sees the instance end. A finished instance never
writes again: its tailer stops, and a page over it never fills forward (§3, §4).

The first and last lines the cache holds are not stored on the stream. They are the `from_ns`
of the chunk marked `head` and the largest `to_ns`, and a second copy would be one more thing
to keep in step.

A **chunk** is the unit of coverage and of eviction. It states a fact: *every line the backend
served for this stream between `from_ns` and `to_ns` that matches `pattern` is in `lines`*. An
empty pattern means every line. Its `origin` is the backend that filled it, by name.

A **line**'s key is its position, the same three fields a cursor holds. `ord` is the backend's
own number for the line within its moment, so a line gets the same key whichever fill found it.
The kubelet API has no such number, so the kubelet backend counts lines within the moment itself,
as `reader.lines` does today. That count is only right when the read saw every line of the
moment, which is why a kubelet fill is always unfiltered. An agent backend names a line by its
offset in the file it reads, which is right for a grepped read too. **A range of a stream is
filled by one backend**, so two backends never number the same line two ways. The cluster's
backends are a list in priority order, picked by the `source` its clusters row carries and, later,
by the archives configured for it: a local cluster reads the kubelet first, a cluster sourced
elsewhere reads whatever serves it, and an archive is asked only for a range the backend before it
has said it does not hold (§3). A stream an archive knows only by name has `''` for its
`container_id` and NULL for `started_at`; it is one row across every restart, which is what the
archive knows, and its derived `previous` is unknown rather than guessed. The cursor does not
carry such a stream yet: `parseCursor` refuses an empty id (§8), so an archive backend brings a
cursor change of its own, decided when it lands. A page's
statement walks `lines_position`. Nothing else is indexed. `lines` is a rowid table, not
`WITHOUT ROWID`, for the reason `kubestore`'s `objects` is: a line is often past the row size at
which `WITHOUT ROWID` pays and can reach 16 KiB. Its `id` is the rowid, and nothing orders by it:
a follow walks `lines_position` from a watermark (§5). A chunk's edges are positions, moment and
ordinal, since a chunk closes at `ChunkBytes`
wherever that falls, inside a moment included. Every table is `STRICT`, as every table in the
repo is.

### 3. Coverage

The cache claims only what a read has proved. The proof comes from `reader.lines`, which already
numbers each line within its moment and reads each moment whole.

- A forward read from the API (`sinceTime` at moment `m`) creates or extends a chunk **starting at
  the first line it returned**, never at `m`. The kubelet answers from the oldest line it still
  has. A read after a rotation would otherwise claim lines it never saw. The new chunk joins the
  chunk that ends at `m` only if the read began inside `m`'s moment and returned that whole
  moment. When it does, the two chunks merge.
- A read that reached the oldest line its backend holds marks its chunk `head`: a backward read
  that ran out of lines, or a forward read with no lower bound, which starts there. `head` is the
  backend's, not the log's: the kubelet's first line is where its file starts, and an archive may
  hold older ones.
- **A chunk's link, `prev_ns` and `prev_ord`, is the backend's word that it holds nothing between
  the chunk before and this one.** A fill that read its whole range links the chunk at its far
  bound; a forward read whose first line is past where it was asked to start links across the
  hole, since the kubelet has rotated those lines away for good; a chunk closed by size or time
  links the next (step 3). Two linked chunks are one span for coverage.
- **A gap is live or lost.** A gap older than a `head` chunk from the cluster's last backend, a
  gap in a stream no backend serves, or one a link spans is lost: nothing can fill it, so a page
  crosses it as it crosses a seam. Every other gap is live, and a page stops at it and fills it.
- **Backends are tiered.** A gap is offered to the cluster's first backend. Only the range it
  answers with `head` for, the part older than its oldest line, is offered to the next. So one
  range has one backend, which the chunk records as its `origin`, and a stream's history is a run
  of chunks whose origin changes once at each tier's edge. The edge falls on a moment and never
  inside one: the moment of a backend's first line is that backend's whole.
- A forward read of a finished instance (`finished_at` set) that reached the end of its log marks
  its chunk `at_end`. A running instance has no end to reach, so no read of one sets it. A stream
  with an `at_end` chunk is read in full at its newest side: a page over it never fills forward,
  and its live end is that chunk's last line with no read at all.
- A tailer's chunk is `open` while the tailer writes to it, and every gap past a running
  tailer's start is the tailer's, crossed and never filled, chunk written yet or not: the
  registry says where each tailer started, the tailer reads continuously from there, and a
  page's live end for the stream is the newest line the file holds, with no forward read. A gap
  below the start stays live. When the tailer stops, for any reason, the chunk closes at its last
  line; one left open by a crash is closed the next time the file is opened. The next tailer starts reading in that line's moment. If it resumes
  cleanly, it extends the chunk. If a rotation happened in between, it starts a new chunk linked
  across the hole, since the kubelet has rotated those lines away for good.
- A chunk closes at `ChunkBytes` (1 MiB) or `ChunkSpan` (5 minutes), whichever comes first. The
  next line opens the next chunk, linked to the closed one. Two chunks so linked count as one
  span for coverage and two units for eviction; a chunk whose link names a chunk that is gone
  starts a span.
- **A chunk's pattern is the grep its fill ran under.** A page for pattern `P` is covered by a
  chunk labelled `P` and by an unlabelled one, since every line is a superset of the matches. A
  chunk labelled `Q` is a gap for it. An unfiltered page is covered by unlabelled chunks alone.
  Two chunks merge only when their labels are the same. Only a backend that greps on its side
  (the agent) fills a labelled chunk; the kubelet backend fills unlabelled ones and the grep runs
  locally over them (§4).

Rows and chunks change in one transaction. A reader's snapshot sees both or neither, so a range
the `chunks` table covers is never missing a row that matches its pattern. That sentence is the
invariant the tests pin.

### 4. A page

`Service.Fetch` resolves the view inside one `CachedData().ReadActive`, as it does today. It
answers `ErrNoCache`, `ErrSyncing` and the refusals as before. Each stream it resolves maps onto a
`streams` row, including its `container_id`; missing rows are inserted, outside the mirror
reading, since nothing on the log file runs inside it. **Then it adds the streams the file holds
for the view's Pod sources that the mirror no longer serves**: instances of a pod that is gone,
or an older instance of a pod that is still there, by `pod_uid`, so a later pod of the same name
is another pod, as the follow has it. A workload source reads the mirror's pods alone (step 3).
The view's label filters run twice, once in the mirror's pods statement as today and once over
`streams` for the rows the file adds, since a gone pod has no node left to join to. A stream no
backend serves is exhausted at what the file holds: no fill, and on the merged page it counts as
having nothing older than its oldest line and nothing newer than its newest. Then, per stream, in the mode's direction from the cursor:

1. **Read the coverage.** Load the stream's chunks from the cursor's position outward. A `Head`
   page needs coverage from the cursor onward. A `Tail` page needs it from the cursor back to a
   `head` chunk, or back far enough to fill `limit` lines across the view. `Around` needs both.
2. **Fill the gaps from the backends.** A `Backend` has `Streams(view)`, the streams it holds for
   a view, which for the kubelet is the mirror resolve above and for an archive is a query over
   its labels, since a pod deleted before Kstack saw the cluster is in no mirror (it lands with
   the first backend that answers it differently; step 2's seam carries the reads alone, the
   resolve staying where it is); two reads,
   `Forward` and `Backward` under the read's bounds; and a `Tail` for §5, which an archive does
   not offer. A read is a stream the caller pulls a line at a time and cancels, as the fetcher's
   is today (step 2): its lines carry the backend's own `ord`, and its end says whether it
   reached the oldest or the last line the backend holds, and the pattern it filtered by, empty
   for the kubelet, which is handed none. The gaps go to the cluster's backends in order, as §3
   says. **A failure is one of four kinds, and the backend classifies it**: `Fatal`, which fails
   the whole page or follow, since it is the user's credentials; `Denied`, which is one stream's
   and caches nothing for it; `Ended`, the backend's answer for that log now, which caches
   nothing and is asked again at a later generation; and `Transient`, which is retried as today. The kubelet backend maps a
   401 onto `Fatal`, a 403 onto `Denied`, a 408 and a 429 onto `Transient`, since a request the
   server timed out or throttled says nothing about the log, any other status under 500 onto
   `Ended` and the rest onto `Transient`; another backend maps its own errors. The kubelet backend is the fetcher as it stands: a forward
   read for a gap that starts at a chunk's end, kubetail's backward windows for history older
   than the oldest chunk, the budget split evenly across the streams that need a read, `maxGets`
   per stream, each `GET` over a claim (`claim.go`), so a disabled or deleted cluster cuts its
   reads as today. How another backend authenticates is its own business. The kubelet backend is
   the only one this sequence builds. Each read writes what it returned as §3 says, in batches on the
   writer. A stream that fails before any line in range becomes the page's `skipped` entry, as
   today, and its coverage is left alone.
3. **Answer from the file.** One walk of `lines_position` per stream from the cursor's
   position in the page's direction, bounded by the span the cursor sits in with lost gaps
   crossed, so a walk never crosses a live gap, cut at `limit + 1` lines or the stream's share of the walk's own byte budget,
   each read whole inside one read transaction on the reader pool, merged by `mergePage` in the order `comparePositions`
   keeps (§8), so a page's edges and the cursor agree with what was read. One statement across
   the streams ordered by position cannot walk the index and would sort every line in range,
   and a merge of per-stream walks is the one shape whose cut can say where each stream stopped.
   It runs after the fills commit, so it sees them. A fill runs until the stream's walk holds a
   page's worth past the cursor, has no gap left that way, or has spent its budget (step 3). Two
   instances of one container never tie on `ts_ns` in practice, and when they do the ordinal
   orders them, then the id; `started_at` is for the viewer, which draws the instances of a
   container in start order.

**Grep** runs in Go where the page is read, over each stream's walk and each fill's lines, with
the one compiled pattern a page holds, over text the cache stored after `stripControl`. It runs
over every chunk a walk reads, labelled or not: a labelled chunk's lines already match, and
running it again costs little and keeps one path. A backend read is never handed the grep. A
grep with few matches walks far, so each stream's walk is bounded by its share of the walk's own
byte budget, and a walk cut there stops the stream where it reached, which the merge reads as a
cut: an honest short page (`more: true`), the same as a page cut by a spent `GET` budget today.
The label filters narrow the streams, never the lines.

The edges follow the README's table. **The cut** is the nearest point up to which every stream
is covered in the direction read, or `limit`, whichever comes first. **The live end** is the
newest line of each stream's newest chunk, once a forward fill has read each running stream to
its end; a finished stream with an `at_end` chunk is already there. Its cursor is that moment. What each stream holds up to it lives in the file, not in the cursor.
A `before` edge is null with `more: false` when every stream's oldest chunk is `head` from the
cluster's last backend; a `head` from an earlier backend means only that the next one is asked.

The resolve runs inside the view's `ReadActive`, which may run it twice if the identity moves,
and carries its snapshot out; the rows, the fills and the walks run outside it, so no mirror
connection is held across a read of the kubelet. The fills are idempotent (`INSERT OR IGNORE` on
the position). A fill is filed under the instance the kubelet served: a restart between the
resolve and the read is caught by reading the instance again before the write, and one the
mirror reports late by trimming the old instance to its `finished_at` (step 3).

### 5. A follow

`Service.Follow` resolves the view and opens its mirror watches as today, and its reconcile
still decides which streams the view wants at each generation. What changes is who does the
reading. A reconcile asks the file's **tailer registry** for each stream. The registry runs one
tailer per `streams` row and pattern, shared by every follow that wants it, with a refcount. An
unlabelled tailer serves every pattern, since the grep runs where the lines are read, and it is
the only kind the kubelet backend runs. **A tailer stays alive for `TailGrace` (30 seconds)
after its last follow lets go.** This is what makes a viewer's scroll up and back an attach
rather than a restart: the webview's window closes its follow the moment it unpins and opens one
when it pins again, and the sidecar answers the new one from the file, which the tailer never
stopped writing, with the first frame on the first walk. Thirty seconds is sized for reading
something above and coming back; a longer absence pays one restart, which is what every scroll
pays today. The registry reconciles nothing: a tailer in its grace that ends is dropped, and the
next follow asking for the stream starts one afresh. The mirror watches stay each follow's own,
since opening them is a local read. The registry's contract is per stream, since that is what
the refcount is for; what a backend opens underneath is its own business, so a backend that
would rather carry many streams over one connection may.

A tailer is the backend's `Tail` read, which for the kubelet is the forward stream the follow runs
today. It starts at the later of the last line in the stream's newest chunk and the watermark
of the follow asking, its `after` or its opening moment, and its first read merges or links as a forward
fill's does (§3), so a resume after a rotation links across the hole. A finished instance gets
no tailer: the kubelet closes its log when the container ends, the tailer ends with it, its
chunk closed at its last line, and the reconcile that sees it finish sets `finished_at`, as a
stream's end ends it today; the next page's forward fill reads nothing more and marks the chunk
`at_end`. A tailer hands lines to the writer in batches of at most `WriteBatch` lines or every
`WriteTick` (50 ms), extending its `open` chunk. The tailers reach the writer through a `gochan`
`mpsc` with `WithKeepOpen`, one sender per tailer, closed when it ends, bounded to `WriteQueue`
batches: fan-in from producers that come and go, as `fanIn` is today. After every commit of
lines, a tailer's batch or a page's fill, the file sends on a `gobus/conflate` bus keyed by
stream id, one coalesced ping per stream written, as `app.db`'s change bus does per key. Its
failures are the backend's four kinds (§4): a `Fatal` one ends every follow on the file, an
`Ended` one holds the stream until its generation moves, a `Denied` one the same with the reason
remembered, and a `Transient` one is retried as the follow retries today. The kubelet backend's
tailer is the forward stream over a claim, so a revoked claim ends it.

A follow is then **a walk of the file from a watermark per stream, run again on every commit to
its streams**. It subscribes to the bus under its streams' ids, before its first walk, and
subscribes again as the reconcile changes them, so a noisy stream wakes only the follows reading
it. A follow with `after` sets each stream's watermark to that position, and one with none to
its opening moment. Each run walks each stream named from its watermark by position, along the
span the watermark sits in, lost gaps crossed, sends the lines the grep passes in position
order, per stream as the README says, and moves the watermark to the last line read. A fill
inserts rows too, and a line below the watermark is never read, which is what keeps a
backfilled line out of a follow. **A walk that reaches a live gap holds the watermark there, and
the follow fills the gap** as a `Head` page would before it walks on: a tailer that reopened
from now (§6), or started past the stream's newest chunk, leaves such a gap, and a follow that
crossed it would send lines past ones the file lacks. Then it sends a `Checkpoint` whose cursor
is the oldest watermark across its streams. A follow opened `after` that checkpoint misses
nothing and repeats only lines the webview already holds. A stream that joins the view later
starts from the follow's cursor, as the reconcile resumes it today. `Ended` is sent on a `Fatal`
failure, when the cluster is disabled or deleted, or when the reconcile ends the follow for a
reason it already has, with the reason in the user's words; the file closing ends the follow
with an error and no frame, since following again would not end the same way.

The follow's first walk reads what the file already holds past its watermarks, and after that a
line reaches it on the ping of the commit that wrote it. So the sidecar's own batching is the
follow's only latency, and no line is ever read from both the tailer and the file.

### 6. Backpressure

The writer takes batches from the `mpsc` in §5, `WriteQueue` deep. If a tailer cannot send its
batch within `WriteTick`, it **stops its read, closes its chunk at the last committed line, and
reopens from now**. The lines in between are a live gap in §3's coverage, past every watermark
on the stream: each follow fills it before it goes on, and a page that reaches it fills it too,
from the kubelet while it still has the lines (§5). There is no separate "lossy" state: dropped
means not covered.

**A tailer in its grace is shed first.** When the queue is full, a tailer with no follow attached
stops before one that has, so the grace costs nothing while the writer keeps up and is the first
thing dropped when it does not. There is no cap on what a tailer may write during its grace: a
noisy stream is the one whose gap the kubelet may no longer fill, since its file rotates fastest,
so it is the stream the grace helps most.

### 7. Eviction

One janitor per open file, in `kubestore`'s shape. It sweeps every `Interval` and after every
commit, through a capacity-one notify, so a burst owes one sweep. It runs on its own context, so
a close aborts it mid-statement. One sweep:

1. **Measures** the file's size as `bytes` summed over `chunks`, and each stream's size.
2. **Protects** every `open` chunk, and every chunk whose `read_at_ms` is within `ReadTTL`
   (10 minutes). A page stamps `read_at_ms` on each chunk it reads. A tailer stamps each chunk it
   writes. So a viewer's window and a live tail are kept by what they do, with no session to
   track.
3. **Evicts** while the file is over `Bytes` (256 MiB), or while any stream is over
   `StreamShare` (a quarter of `Bytes`). It picks the unprotected chunk with the oldest
   `read_at_ms`, taking an over-share stream's chunks first. It deletes the chunk's `lines` by the
   position index in bounded batches, then the chunk row, in one transaction per chunk on the
   writer. So an eviction never interleaves with a fill of the same chunk.
4. **Removes a stream** with no chunks left, and a stream whose pod is gone from the mirror once
   its last chunk is gone.
5. **Vacuums**, bounded by `VacuumPages` and gated on `freelist_count`. Then a `TRUNCATE`
   checkpoint when the WAL is bigger than the file.

`Retention` is the whole set of knobs, with `DefaultRetention`'s numbers: `Bytes` 256 MiB,
`StreamShare` a quarter of it, `ReadTTL` 10 minutes, `ChunkBytes` 1 MiB, `ChunkSpan` 5 minutes,
`TailGrace` 30 seconds, `WriteBatch` 1,000 lines or `WriteBatchBytes` 1 MiB, whichever a batch
reaches first, `WriteTick` 50 ms, `WriteQueue` 16 batches, so the queue holds at most 16 MiB of
text the cluster wrote and never the gigabyte that lines alone would allow at 16 KiB each,
`VacuumPages` 2,048 (`kubestore`'s, about 8 MiB a sweep), `Interval` 5 minutes (`kubestore`'s,
since the sweep also runs on every commit). Production passes `DefaultRetention`. A test
about anything else passes a zero `Interval`. A test about the janitor shrinks whatever it
drives.

### 8. The cursor

A cursor is a position, `cursor.go`'s type: a moment alone, or a moment with a stream and an
ordinal. **The stream's instance is its `container_id`**, in the place `previous` holds today,
beside the namespace, pod and container name. `previous` can change for one instance across a
restart, and a line's key must not; the id cannot. `comparePrevious` goes with it, and the order
is time, then the names as today, then the ordinal, then the id, so lines at one moment keep
their order and only two instances of one container at one moment sort differently (step 2). A
`container_id` is longer than a name, which a cursor can afford, and it stays valid across a
cache clear, which a file-local stream id would not. The ordinal is the backend's number for the
line (§2), which for the kubelet is the count `reader.lines` keeps today. A page cursor at the
live end carries the moment alone. Nothing per stream rides in it, because the file holds each
stream's extent. So `parseCursor` checks what it checks today and that the id is one the
resolve could have handed out — not empty, no tab or control character, at most 256 bytes — and
the note's §4 and its open problems 1, 6, 7 and 9 go away.

Open problem 4 is clock skew between the API server and a node. For a stream the file has read,
it is solved: its tailer resumes from its own last line's moment, which is the kubelet's clock. A
stream the file has never read starts from the follow's moment, as today, and that small gap
remains.

### 9. The resolvers

The note's §5. `logsFetch` and `logsFollow` take `mode`, `cursor`, `at` and `limit` as the
README's wire says. `gqlgen.yml` binds the new types. The four old bound arguments go.

## Decisions this sequence asks for

- **Coverage is per stream and per chunk, and claimed only from a read's first returned line.**
  Claiming from the requested `sinceTime` is simpler, but wrong after a rotation.
- **A stream is a runtime instance, and `previous` is derived.** Lines are keyed by the
  `container_id` that wrote them, so an instance keeps one row and one set of lines whether it is
  read as current or, after a restart, as previous. A log across a restart is a read over two
  rows of one container. Storing `previous` on the row would store the same instance twice.
- **A stream's place and times are a snapshot from the mirror.** The mirror holds only what is
  in the cluster now, and the cache outlives the pod. Copying the node, its labels and the
  instance's start and end onto the row is what lets a gone pod's lines keep their place and lets
  the filters run over them. The first and last line are not copied, since the chunks already
  hold them.
- **Messages are stored as text, not compressed.** Compressing one line on its own gains little,
  since the redundancy in a log is between lines. SQLite's transparent options are closed to us:
  ZIPVFS is proprietary C, which `modernc.org/sqlite` cannot load, and filesystem compression is
  not portable. The path that fits, if the budget's reach turns out short, is `sqlite-zstd`'s:
  zstd with a dictionary trained per stream on its own lines, which gains a lot on short rows,
  applied per row behind a view with triggers, with the compressor and the dictionary builder as
  Go functions registered on the driver, as `kubestore` registers `body()`. The row layout, the position
  index and eviction by chunk stay as they are, and `bytes` stays the uncompressed size, so adding
  it changes nothing above the store. Measure first.
- **`at_end` mirrors `at_head`.** Without it a page over a finished instance could not tell "read to
  the end" from "read to where the tailer stopped", and would fill forward on every read.
- **Eviction is LRU by read under a byte budget, protecting open and recently read chunks.** Not
  by age. A quiet pod's hour-old lines that someone is looking at are worth more than a firehose's
  minute-old lines nobody opened.
- **Grep is a Go filter over each stream's walk, not a SQL function.** A page is per-stream
  index walks read in Go and merged, so the filter sits where the rows already are, and the walk
  is bounded by its budget. A SQL function would copy each message into Go before matching it
  and gain nothing.
- **A grepped fill is a labelled chunk in the same file, not a webview cache and not a session.**
  A backend that greps on its side returns some of the lines, so its chunk can only vouch for the
  matches. Labelling the chunk keeps the promise honest, keeps eviction and protection as they
  are, and gives a searched view the same bounded webview as any other. The kubelet never fills
  one, so nothing in this sequence writes a label; the column is there so the agent can later.
- **A line's ordinal is the backend's.** The kubelet backend counts lines within a moment, as today.
  An agent names a line by its file offset. Both are stable within one stream, and a range of a
  stream has one backend, so a line keeps its key whichever fill found it. Deciding this now means
  no stored key changes when the agent arrives.
- **One `Backend` interface, with the kubelet behind it.** The fill and the tailer are the only
  places a line comes from outside, so it is the one seam the agent, a cluster sourced from
  Kstack's cloud, or an archive needs. The backends are the cluster's, in order; each classifies
  its own failures as `Fatal`, `Denied` or transient, and the claim is how the kubelet backend
  authenticates, not part of the contract. The registry asks a backend for one tailer per stream
  and lets it share a connection underneath.
- **Backends are tiered, and a range has one backend.** An archive holds the same recent lines the
  kubelet does, numbered its own way, so asking both for one range would store a line twice under
  two keys. Asking the next backend only for what the one before has said it lacks keeps one key
  per line with no cross-backend matching. `head` is therefore the backend's, and a chunk records
  its origin so a later tier can be added under existing chunks. A stream known only by name,
  the archive's usual case, is one row with an empty `container_id`.
- **The follow reads the file, never the tailer.** One path for every line, and a checkpoint that
  is true by construction. The cost is one `WriteTick` of latency.
- **A cursor stays a position.** Per-stream state lives in the file.

## Steps

Built one after another, each a pull request into `wip/log-views`. Each is small enough to read
whole, and each leaves the sidecar building and its tests passing. Nothing reads the new wire
until the real client lands, so a step may change its half of the schema without the other half
following in the same change. A step's file names its own tasks, tests and docs; this README
holds what they share.

| Step | Builds | Status |
| --- | --- | --- |
| [1 The store](1-the-store.md) | The file, its schema and manager, and its lifetime with the cluster cache | Built, [#293](https://github.com/kstackhq/kstack/pull/293) |
| [2 The cursor and the `Backend` seam](2-the-cursor-and-the-backend-seam.md) | A position names its instance; the fetcher behind an interface; no behaviour change | Built, [#295](https://github.com/kstackhq/kstack/pull/295) |
| [3 Pages from the file](3-pages-from-the-file.md) | `Fetch` over coverage, fills and the file; the grep; `logsFetch`'s wire | Built, [#301](https://github.com/kstackhq/kstack/pull/301) |
| [4 Tailers and the follow](4-tailers-and-the-follow.md) | One tailer per stream, the writer, the commit bus, `Follow` as a read; `logsFollow`'s wire | Planned |
| [5 Eviction and backpressure](5-eviction-and-backpressure.md) | The live janitor, the budget, shedding; the sequence's integration gate | Planned |
| [6 The docs](6-the-docs.md) | The ADRs, the note's retirement, the coherence pass | Planned |

Record each step's status and its pull request in this table as the work moves. A built step
stays listed until the sequence lands (*When it lands*).

## The invariant

One sentence every step serves, pinned by a seeded walk in step 3 and held by every step after
it. The walk runs over a fake kubelet whose logs rotate and whose pods restart, under a shrunk
`Retention`. After any sequence of pages, follows, tailer stops, evictions and fills:

> Every line a page answers is in a chunk that covers its pattern, or came from the backend on
> that call. No chunk is missing a line the fake served into its range that matches its pattern.

Every test in the sequence waits on a channel or on the janitor's notify. A negative assertion
(a chunk that must not be evicted) is a `select` against a window sized from the shrunk
`Interval`, with a comment saying so.

## Security

- **The file is cluster data in the clear.** It lives under `<cache>/`, which the bash tool's
  `DeniedDirs` already covers. Logs hold secrets more often than object bodies do, so this is a
  new row in `docs/security-model.md` under the decision *the cache is ordinary application data*
  (see the ADR). It is not folded into the mirror's row.
- **The model reaches no line through the file.** No KubeQuery view over `lines`, and no tool.
- **Per-stream authorization holds.** A `Denied` stream caches nothing. The kubelet backend reads
  every stream over a claim as today, so a disabled or deleted cluster's tailers end with it, and
  a backend added later must hold the same two lines: a denied stream caches nothing, and a
  cluster that is disabled or deleted stops every read.
- **The grep runs the user's pattern over text the cluster wrote.** Go's `regexp` runs in
  linear time, and the text was cut at 16 KiB before it was stored.
- **A cursor is still opaque text the server wrote.** `parseCursor` refuses anything it does not
  spell.

## When it lands

- `sidecar/CLAUDE.md`: the logs service paragraphs (`logsFetch`, `logsFollow`, the fetcher, the
  cursor) describe the file, the coverage, the tailers and the janitor. The directory table gains
  `logs/<cache id>.db`.
- ADRs: *the log cache* (the file, coverage per chunk, LRU by read), and *a cursor is a
  position* (what the cache holds instead of the hint). The second one closes the note's plan.
- `docs/security-model.md`: the row above.
- `docs/specs/log-viewer/README.md`: *What is left* loses the sidecar half of its first bullet.
  `notes/the-sidecar-wire.md` is deleted.
- A built step keeps its file and its row, marked Built with its pull request, so a later step
  can read what it settled. Every step's file goes with this README in the change that lands the
  sequence on `main`, as the log viewer's did.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first. Per step: `cd sidecar && go test ./internal/services/logs/...` and `make test-changed`.
Steps 3 and 4 change the wire and add its checks. Step 5 runs the integration gate: `make test`,
`make lint`, `make vet`, `make cover-go`, `make cover-js`, with no gate lowered.
