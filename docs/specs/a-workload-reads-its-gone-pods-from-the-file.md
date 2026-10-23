# A workload reads its gone pods from the file

**Area:** the sidecar's `internal/services/logs` and its `logstore`. The webview changes not at
all.
**Answers:** the known gap [#392's closing comment](https://github.com/kstackhq/kstack/issues/392#issuecomment-6380848108)
leaves by choice, which the review of [`bounded-log-window.md`](bounded-log-window.md) raised again.
**Prerequisites:** [`bounded-log-window.md`](bounded-log-window.md). The two land in one PR and
are retired together.
**Status:** Planned.

## Goal

A workload's view reads the pods the workload no longer runs the way a Pod source reads its gone
instances: from the log cache, in time order, each line once, marked previous. A line the window
dropped comes back on a scroll toward its edge whatever became of its pod, and a pod replaced
before the landing is read too. The view's answer depends on what the file holds, never on what
one window remembers.

Read `sidecar/internal/services/logs/CLAUDE.md`'s paragraphs on the resolve and on
`fileOnlyStreams` first, and `bounded-log-window.md`'s *Where a dropped edge reads back from*.

## The gap

A workload scope resolves inside the mirror by walking controller references down from the
workload to its pods (`podsOf` in `resolve.go`). The mirror drops a deleted pod's rows, owner
references included, so the walk reaches only the pods the workload runs now. The file keeps a
gone pod's streams and lines, but holds nothing that ties the pod to the workload: a `streams` row
carries the pod's name and UID and no owner. So `fileOnlyStreams` reads a gone pod for a Pod
source, which names the pod, and for a container the cursor's entries or `seen` name, which the
caller was shown. A pod the window has dropped every line of is named by neither, and its lines
are skipped though they remain cached.

## The design

**The file records each pod's controller chain.** A new table beside `streams`, in each cache's
log file, added to `0001_init.sql`, since nothing has shipped and the schema is that one file. A
dev cache file made before the change lacks the table and its statements fail to prepare:
`make clean-data` first.

```sql
CREATE TABLE pod_owners (
  pod_uid   TEXT NOT NULL,
  owner_uid TEXT NOT NULL,
  PRIMARY KEY (owner_uid, pod_uid)
) STRICT;
```

A row per controller above the pod, walked up through controller references alone to depth 8,
the walk `podsOf` makes read upward. Ownership is the pod's, not the stream's, so the table keys
on the pod's UID: a pod's streams are filed on different pages, and its chain is one fact.
`podsStatement` selects the chain as one more column, a JSON array of UIDs, for every source kind,
so a pod filed under a Pod source is found later by its Deployment. `stream` carries it as
`owners []string` and `fileStreams` writes it with the rows: `PutStreams` takes `Owners` on
`Stream` and inserts the pairs the table lacks in the transaction that files the streams, on
every filing, and its no-write check counts the chain among what the file holds as it is.

The chain grows with what the mirror shows, and a pair is never removed but with the pod. A Pod
can reach the mirror before its ReplicaSet: the pod's own owner reference names the ReplicaSet,
but the walk up from it finds no Deployment until the ReplicaSet's object arrives. The page that
reads the pod files the pairs it has, and the next page that reads it files the rest. A pod that
leaves the mirror before any page reads it whole is found by the owners it recorded, its
ReplicaSet and not its Deployment, and that is the guarantee's edge. An adoption adds its pairs
the same way; an orphaning takes none away. The chain is by UID: a workload deleted and
recreated under its name owns only its own pods, as `TestAPodOfTheSameNameIsAnotherPod` says of
pods.

**A page reads a workload's gone pods from the file.** `fileOnlyStreams` gains a branch beside
its Pod branch. For each source that is not a Pod source, the file answers the streams of the
pods the source's UID owns whose UID the resolve did not hand out (`OwnedStreams` on the store).
The resolve reads each workload source's object once, by kind, namespace and name, for its UID;
`idleOrMissing` becomes that read, made for every workload source rather than only one with no
pods. `target` carries the UID per source, empty for a `Missing` source, which owns nothing the
file can be asked for. The rows are grouped by pod UID, and each
pod's go through `fileRows` as a Pod source's do, with the source as it is: the containers it
names or the pod's default, in a place the filters keep, each container's newest `maxInstances`.
A gone pod has no current instance, so every row keeps and `fileStream` marks each `Previous`.
`podOf` takes the row's pod UID and counts the pod live only when the mirror's pod of that name
is that UID, so a gone pod's rows keep the place and default filed with them even while a pod of
its name runs, in a mixed scope naming both or under a StatefulSet. A row the Pod branch or the entries added
is not added again: `add`'s dedupe by run stands.

**The read is bounded by each stream's own position.** A gone pod's times in `streams` cannot
bound it: `started_at` and `finished_at` come from a status a resolve read, and a pod deleted in
a rollout leaves the mirror before any resolve reads its finish. The file knows each stream's
lines exactly, through the `lines` index, and the bound is in those. `OwnedStreams` answers the
candidates: the streams of the pods the source owns whose UID the resolve did not hand out, each
with its oldest and newest line, leaving out a stream with no line to read. A gone pod's stream
the janitor has evicted is an empty row until a sweep removes it, and has none. The rows are
held as soon as they are known, before anything is decided on them, as the Pod branch holds its.

A stream then reaches a read when it has a line on the read's side of where that stream's read
would start: its `keyFor`, past its entry, else past the moment, else the window's edge. A page
has no one position: on a continuation each stream goes on from its own entry, and a stream with
none, left out of the page before, must be found from the window's start, not from where the
other streams are. A first page whose padding is counted makes two reads from its moment, one
each way, over one stream set, so a stream reaches the page when it reaches either read. A
`Head` or `Tail` landing's edge is open, and every candidate reaches it. A follow builds its
stream set once in `follow.open` and reads it over several pages, so it takes every candidate
that reaches its start the same way.

One more trim is free: when the read's far bound is closed, a stream whose every line lies past
it is left out, which the extents already answer. The bound is the read's resolved far edge,
`farEdge` on the cursor and `pageQuery.far` on the read, which lies past the padding: a window's
raw `Since` or `Until` would leave out a stream whose only lines are padding the page owes.

That is the whole bound, and it is one-sided: an older page takes every gone pod within
retention with a line at or before its start, and a `Tail` landing takes every candidate. On a
Deployment that replaces a pod every few minutes, a scroll back reads hundreds of gone streams,
each a hold, a `fileRows` pass, a seek on `lines_position` and an entry in the merge. Tens of
milliseconds a page is the target; task 2's benchmark establishes the cost, and the PR carries
the number. A stream that reaches a read may
deliver a line to it, so nothing cheaper is exact. No cap on gone pods, and no page cut by one:
`Fetch`'s rule 10, exact paging, holds through them. A workload's gone pods do not count toward
`MaxStreams`, as `TestAScopePastMaxStreamsPagesOn` pins for a Pod source's.

**The entry path stays.** A container the cursor's entries or `seen` name whose pod the resolve no
longer hands out is still read from the file as today. It covers a `Missing` workload, whose
gone pods have no owner to ask by.

**The janitor removes a chain whose pod has no stream.** Each sweep, after `removeStreams`, runs
one statement that deletes the `pod_owners` rows of a pod the file holds no stream of. It needs
no transaction: an orphaned chain is harmless until then, and the sweep is the one writer of
removals. So the table is bounded as `streams` is: by the budget the janitor evicts lines to,
and the removal of a gone pod's emptied streams that follows. A chain is a few rows of two UIDs
beside lines measured in kilobytes.

**What the user sees.** A workload's landing, not only a scroll back, shows the pods the workload
replaced within the file's retention, marked previous, beside the pods it runs. A Pod source
shows a gone pod's instances the same way today. This is a visible change for `Head` and `Tail`
landings on a workload with recent rollouts, and the ADR records it as chosen.

## Invariants the tests pin

- A pod's controller chain is filed with its streams, the pairs the file lacks on each filing;
  a pod filed as it is, chain included, takes no write.
- A pod read before its ReplicaSet reached the mirror has its Deployment filed by the next page
  that reads it; a pod gone before that is found by its ReplicaSet alone.
- A gone pod's rows keep their filed place and default while a pod of its name runs.
- A workload source reads, from the file, each pod its UID owns whose UID the resolve did not
  hand out, through `fileRows`, each instance once across the scope, marked previous.
- A `Missing` workload reads no pod by ownership; the entries it names still read.
- A workload recreated under its name reads none of the old workload's pods.
- A read takes no owned stream without a line on its side of that stream's own start, none with
  every line past its resolved far edge, padding included, and none with no line at all; a
  landing's open edge leaves out only the last.
- Along one direction a workload's gone pods repeat no line and skip none, whatever each page's
  size, with more gone pods than any one page delivers lines of, a pod whose lines begin pages
  past the first included.
- A pod's chain leaves the file in the sweep after its last stream does.
- A page cursor positions a gone pod's streams as any other's: paging on from a page that read
  one reads it on from where that page left it.

## Tasks

Do them in order. Each one passes `make test-changed`. The two specs are one pull request.

0. **The store.** `pod_owners` in `0001_init.sql`; `Owners` on `Stream`; `PutStreams` writes
   them; `OwnedStreams` answering the candidates with their extents; the sweep's orphan delete.
   Tests in `streams_test.go` and `janitor_test.go`: a chain written, a later filing with more
   owners adding the new pairs and one with the same taking no write, the empty stream left out,
   the extents, the orphan removed on the next sweep.
1. **The resolve.** `podsStatement` selects the controller chain; `stream.owners`; `fileStreams`
   passes it; `idleOrMissing` reads each workload source's UID and `target` carries it. Tests in
   `resolve_test.go`: the chain of a pod under a Deployment is the ReplicaSet and the Deployment;
   a pod whose ReplicaSet the mirror lacks carries the ReplicaSet alone, and both once it
   arrives; a Pod source's pod carries its chain too; a source with pods carries its UID; a
   `Missing` source carries none.
2. **The page and the follow.** The workload branch in `fileOnlyStreams`: the candidates, held,
   that reach one of the page's reads from their own `keyFor`, through `fileRows`. Tests in
   `fetch_test.go`: a Deployment whose pod was replaced and deleted from the mirror reads the old
   pod's lines on a first page naming nothing, marked previous, and on across a page boundary;
   `Head` and `Tail` landings read them; more gone pods than a page delivers lines of page on
   with no line repeated or skipped, one pod's lines beginning pages past the first; an older
   page leaves out a pod whose oldest line is after its start, one whose newest line is before
   its resolved far edge, and keeps one whose only lines are padding past the window's `Since`;
   a padded first page keeps a pod on either side of its moment; all
   checked through the rows held; a workload recreated under its name reads none; a gone pod the
   entries also name is read once; a pod filed before its ReplicaSet reached the mirror is read
   by its Deployment after the page that completes its chain; a gone pod's rows under a running
   pod of its name keep their filed node against a node filter and their filed default. A benchmark pages back over a Deployment with a few hundred
   gone pods, each with lines, so the cost of the one-sided bound is a number in the PR. In `follow_test.go`: a follow from a `Since` reads a gone pod's lines
   first, as `follow.open` reads a Pod source's. Update `Fetch` rule 6 and `Follow` rule 7,
   `README.md` rule 15, and the package `CLAUDE.md`'s `fileOnlyStreams` sentence.

## Acceptance

- Following a workload through a rollout until the old pod's lines are all dropped, then
  scrolling back, shows the old pod's lines again, once, in order, marked previous.
- The same over a page back past the bound and a scroll forward.
- A `Tail` landing on a workload replaced within the retention shows the replaced pod's lines
  before the live pod's, marked previous. A `Head` landing on a workload replaced many times
  within the retention starts at the oldest replaced pod's first line and pages forward through
  every one.
- `make lint-go` and `make vet-go` pass; the Go coverage gate does not fall.

## Verification

```sh
make test-changed
cd sidecar && go test ./internal/services/logs/...
make lint-go vet-go
```

## When it lands

`sidecar/internal/services/logs/README.md` and `CLAUDE.md` state that the file records each pod's
controller chain and that a workload reads its gone pods from it, each from its own start. The
ADR written for `bounded-log-window.md` records why ownership is filed with the stream rather
than carried by the client, that a workload's landing shows its replaced pods, and that a chain
grows with the mirror and loses nothing but with its pod, so an orphaning is not followed and a
pod gone before its chain completed is found by the owners it recorded, by choice.
#392's closing comment gets a reply pointing at the change. This spec and
`bounded-log-window.md` are deleted in the same change.
