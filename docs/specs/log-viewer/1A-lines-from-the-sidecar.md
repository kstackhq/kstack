---
title: Lines from the sidecar
scope: sidecar
status: Planned
---

# Lines from the sidecar

**Needs:** `wip/log-views` as it stands after rung 1. **Unblocks:** step 2. Can be built beside
step 1B.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

The sidecar learns to serve log lines. Given a cluster and the sources, filters and grep of a
`LogsViewAction`, it answers a page of lines around a cursor, or streams new lines as they are
written. It reads each container's log through the API server with the user's own credentials,
merges the lines by time, and strips terminal control sequences before anything leaves it.

The reading code is copied from kubetail's `modules/shared/logs` package. The merge and the
source resolution are written fresh over the mirror, which already knows which pods belong to a
workload.

Nothing in the webview changes. The two operations wait for step 2 to call them.

## What is not in this step

- **No webview change.** The viewer is still the placeholder.
- **No cluster agent.** Kubetail can read logs faster through an agent it installs in the
  cluster. Kstack reads through the API server only.
- **No colours.** Escape sequences are stripped, not kept.
- **No download**, and no `LogsRead` tool. Rung 4 of the log views ladder builds the tool over
  this step's fetch.

## Design

### 1. The wire

Two operations on `sidecar/graph/schema.graphqls`, each taking the cluster and the view.

```graphql
"One log line, as the sidecar read it. Every string came from the cluster, so draw it as text."
type LogRecord {
  "When the container runtime stamped it."
  timestamp: Time!
  "The line, with terminal control sequences stripped and cut at 16 KiB."
  message: String!
  source: LogSource!
}

"Where a line came from."
type LogSource {
  namespace: String!
  pod: String!
  container: String!
  "Whether the line is from the instance before the last restart."
  previous: Boolean!
  "The node the pod ran on, and its labels. Empty when the mirror does not know."
  node: String!
  region: String!
  zone: String!
  os: String!
  arch: String!
}

"A page of lines. `nextCursor` is set while there are more in the direction asked."
type LogRecordsPage {
  records: [LogRecord!]!
  nextCursor: String
  "Streams the page could not read, one line each, in the sidecar's words. Empty when every stream answered."
  skipped: [String!]!
}

"What a view reads: the `LogsViewAction` fields the backend takes, as an input."
input LogsViewInput {
  sources: [LogsViewSourceInput!]!
  filters: [LogsViewFilterInput!]!
  grep: String!
}

input LogsViewSourceInput {
  namespace: String!
  kind: LogsViewSourceKind!
  name: String!
  containers: [String!]!
  allContainers: Boolean!
  previous: Boolean!
}

input LogsViewFilterInput {
  field: LogsViewFilterField!
  values: [String!]!
}

"Which end of the range a page is cut from when it holds more than `limit`."
enum LogsFetchMode {
  Head
  Tail
}

extend type Query {
  """
  A page of a view's lines. `since` and `until` include the cursor's line, `after` and `before`
  do not. With no cursor, `Head` reads from the oldest line and `Tail` from the newest.
  """
  logsFetch(
    id: ClusterID!
    view: LogsViewInput!
    mode: LogsFetchMode!
    since: String
    until: String
    after: String
    before: String
    limit: Int!
  ): LogRecordsPage!
}

extend type Subscription {
  "A view's new lines as they are written, after the cursor, or from now with none."
  logsFollow(id: ClusterID!, view: LogsViewInput!, after: String): LogRecord!
}
```

The input is a twin of `LogsViewAction` with the anchor and `pinToEnd` left out, since those are
the viewer's business, not the backend's. GraphQL cannot reuse an output type as an input, so the
twin is spelled out; step 2 writes the one function that turns an action into it.

The four fetch arguments are kubetail's. The client's four calls map onto them one to one, and
one query is simpler than four.

`limit` is at most 500. A larger value is a validation error.

### 2. The package

`services/logs`, built in `app` with the cluster service, as `logsview.New` is:

```go
type Page struct {
    Records    []Record
    NextCursor *string
    // Streams the page could not read, one line each.
    Skipped    []string
}

func (s *Service) Fetch(ctx context.Context, id cluster.ClusterID, view tools.LogsViewInput, q FetchQuery) (Page, error)
func (s *Service) Follow(ctx context.Context, id cluster.ClusterID, view tools.LogsViewInput, after *string) (*cluster.Stream[Record], error)
```

Each resolves the sources inside one `Clusters().ReadActive`, as the LogsView tool's resolve
does, so a cluster whose cache is not ready is refused the same way the tool refuses it, and
reads the identity the pods came from, `ActiveCluster.Cache.Spec.ServerUID`. The streams run
**outside** it: `ReadActive` checks the
identity again only once its read returns, so an unending follow inside it would never be
checked and a moved identity would replay it whole. The streams run the way the bash tool's
upstream does (`tools/bash/upstream.go`): `AcquireConnection`, then `Lease.ConnFor(serverUID)`,
which refuses a connection to any API server but the one the mirror's pods came from, and a
`Clusters().Watch` that ends the streams once the cluster is disabled or deleted. The request
goes through that connection's `BaseURL` and `HTTPClient`, as `kubeproxy.Endpoint` takes them;
nothing in `kubeconn` changes.

**Resolving sources.** A `Pod` source is one pod. A workload source is the pods under it in the
mirror's `ancestors` table, read as `tools/logsview`'s `mirror.go` reads them. Each pod gives one
stream per container the source names; with none named, per container the pod has when
`allContainers` is set, else for the pod's own default container, as `kubectl logs` picks it. The
`previous` flag goes through to `PodLogOptions.Previous`.

**Filters.** A pod's node is `spec.nodeName`; the node's region, zone, OS and architecture are its
well-known labels (`topology.kubernetes.io/region`, `topology.kubernetes.io/zone`,
`kubernetes.io/os`, `kubernetes.io/arch`), read off the mirror's `Node` objects. A pod whose
values are not among a filter's is left out before any stream opens. A `Node` filter reads
`spec.nodeName` and needs no `Node` object. The other four need one: a cluster whose
credentials cannot list nodes mirrors none, and a filter on region, zone, OS or architecture
there is refused with a plain error saying so — *Kstack cannot read this cluster's nodes, so it
cannot filter by zone* — rather than answering nothing. A pod whose node the mirror does not
hold while it holds others is left out.

**Reading.** A container's log is one raw `GET` on
`/api/v1/namespaces/{ns}/pods/{pod}/log` with `timestamps=true` and, as the call needs them,
`container`, `follow`, `sinceTime`, `previous`, `tailLines` and `limitBytes`. The sidecar sends it
through the cluster connection's `HTTPClient` and `BaseURL` (§2, *The claim*), reading the
response body as a stream, since a follow never ends. No typed clientset: `k8s.io/client-go/kubernetes`
is not imported, and nothing new from `client-go` is.

What is copied from kubetail's `log-fetcher.go` is the algorithm, not its client: the forward
stream that splits lines and parses the timestamp each starts with, and the backward stream that
reads growing windows (`sinceTime` stepping back, `limitBytes` bounding each) until it has
`limit` lines or reaches the start. Each call to `clientset.CoreV1().Pods().GetLogs(...)` in the
original becomes a raw `GET` on the path above.

**Merging a fetch.** One goroutine per stream, one merge that pops the oldest line across them
(a heap keyed on timestamp, then pod and container for ties) and sends it on. Every stream of a
fetch ends, so the heap never waits on one. The merge stops at `limit` and hands back the cursor
of the last line, for `Tail` the oldest.

**Merging a follow.** Not a heap: a heap sends nothing until every stream has a line waiting,
so one quiet container — a sidecar proxy, an idle replica — would hold back every other's lines
for as long as it stays quiet. A follow sends each line as its stream delivers it, in that
container's own order; across containers the order is arrival. Kubetail's `runForwarder` does
the same, and its handoff is copied: the follow opens `after` the cursor and the lines that
arrive in its first 50 ms are sorted before they go. *Cursors and the second* below is what
keeps the page the client holds and the follow's first frames from overlapping; what a
reconnect repeats is step 1B's to drop, and its rule is written against this order.

**Cursors and the second.** The log endpoint's `sinceTime` has one-second precision and a
line's timestamp has nanoseconds, so a request for lines after a cursor rounds the cursor down
to its second and the sidecar drops what came back at or before the cursor itself, as
kubetail's forward stream does. `since`/`until` keep the cursor's line, `after`/`before` drop
it, and the sidecar enforces the difference, never the API server.

**A budget for a fetch.** A backward read for a rare grep pattern would otherwise pull every
container's whole retained log through the API server in growing windows. Each fetch has a
byte budget, 32 MiB read across its streams, and each stream a window cap of 16 reads. When
either runs out, each stream has stopped at a time of its own, and a page holding every line
read would leave the next page skipping or repeating around them. So the page keeps only the
lines on the far side of the **nearest** stopping point — on a backward read, newer than the
newest point any stream stopped at; on a forward read, older than the oldest — and `nextCursor`
is that point. The page may then be empty with a cursor set, which is the honest answer: the
client can ask again from there, and a slow grep shows progress rather than hanging.

**A stream that fails.** One bad container must not empty a page. A pod still creating answers
`400`, so does `previous` on a container that never restarted, and a pod deleted between the
mirror read and the `GET` answers `404`. Such a stream is skipped and named: the page's
`skipped` lists one line per stream, *api-7f9c/istio-proxy: container is waiting to start*, in
the sidecar's words off the API server's. A page fails only when every stream failed, or on
`401`/`403`, since those are the user's credentials and not one pod's state. A follow has no
page to name a skipped stream on: it reopens one quietly when the pod's object changes in the
mirror, and until then the user sees that container's lines missing and nothing saying so.
That is accepted for this step; a follow-side notice is a later addition if it is missed.

**Grep.** A regular expression over the message, compiled once per call with Go's `regexp`,
applied after `stripControl` so a pattern matches what the user sees. The tool already refused a
pattern that does not compile, so a failure here is a plain error.

**A pod that comes or goes during a follow.** The follow opens `CachedData().WatchObjects` on
`v1`/`pods` for the cache and checks each frame against the view's workloads through
`ancestors`. A pod added under one opens its streams from the follow's start time; a pod
deleted ends its streams, and the follow goes on. A `Pod` source whose pod is deleted ends when
the API server closes it.

**How a follow ends.** `Follow` answers a `*cluster.Stream[Record]`, as every watch whose source
can die does (`sidecar/CLAUDE.md`, *Streams and watches*), with `Err` set before `Frames`
closes. `Err` is set when the claim is revoked (the cluster disabled or deleted), when a
`401`/`403` arrives mid-stream, when every stream is gone, and when a `Pod` source's pod is
deleted — each with its reason in the sidecar's words. The context ending is the one clean
end. A bare channel close could not tell the webview any of these from a clean end, and the
`watchFailed` reason step 2's error line and `subscribe-exchange`'s reconnect read would never
arrive.

**Cleaning.** Every message goes through `stripControl` before it is sent: ESC-led CSI and OSC
sequences and their one-character C1 forms (U+009B, U+009D), every other C1 control
(U+0080–U+009F), DEL, and every C0 control but tab, are removed. A line longer than 16 KiB is
cut there, on a UTF-8 character boundary, and ends with `…`. Rung 4's `LogsRead` reads lines through the same function, so the model never sees
what the user does not.

### 3. The resolvers

`schema.resolvers.go`: `LogsFetch` calls `Fetch` and maps the page, `skipped` included;
`LogsFollow` calls `Follow` and streams through `watchStream`, as every watch with an `Err`
does — never `ptrStream`, which is for the schedule gauge, whose source cannot die. The input is mapped onto
`tools.LogsViewInput` field by field. `LogsViewSourceKind` and `LogsViewFilterField` are bound
in `gqlgen.yml` already; `LogsFetchMode` is new and binds to a type in `services/logs`.

## Decisions this step asks for

- **Copy the fetcher's algorithm, write the merge.** Kubetail's `NewStream` needs its
  `ConnectionManager` (its own clientsets and informers) and a source watcher that resolves
  workloads itself. Kstack has the mirror for that, so only the part that reads one container's
  log is worth taking.
- **A raw request, not the typed clientset.** The log endpoint is one `GET`, and the connection
  already sends raw paths. Importing `k8s.io/client-go/kubernetes` for it would pull the whole
  typed API into the binary, the opposite of what #20 is doing.
- **One query with four cursor arguments, not four queries.** They are kubetail's, and the client
  maps onto them one to one.
- **Node labels from the mirror, not from the pod.** A pod does not carry its node's labels. The
  mirror holds nodes already, and reading them there costs nothing.
- **Strip in the sidecar, not in the webview.** One place, shared with `LogsRead` later, and the
  wire never carries a control sequence.
- **No limit on how long a follow lives.** The subscription ends when the client goes, or the
  cluster is disabled or deleted. The sidecar already handles both for every watch.
- **A follow is ordered per container, not across them.** A total order needs a line from
  every stream, and a quiet stream would stall the rest. Arrival order is what `kubectl logs`
  gives for several pods, and the fetch, which is bounded, stays ordered.
- **A fetch has a budget.** 32 MiB and 16 windows per stream are large enough that an ordinary
  page never hits them and small enough that a rare grep cannot read the cluster's logs whole.
  Fewer lines with a cursor is honest; a page that never answers is not.

## Tasks

1. **Schema.** §1 in `sidecar/graph/schema.graphqls`; `gqlgen generate`; root `pnpm codegen`.
2. **The input type.** `tools.LogsViewInput` in `tools/tool.go`, the action's three fields, so
   the tool and the service share it. `LogsViewAction` embeds it.
3. **The claim.** `services/logs/claim.go`: the lease, `ConnFor` and the revocation watch, as
   `tools/bash/upstream.go`'s `claim` has them; the request over the connection's `BaseURL` and
   `HTTPClient`, handing back the body as an `io.ReadCloser`.
4. **The fetcher.** `services/logs/fetcher.go`: the forward and backward streams from kubetail's
   `log-fetcher.go` and `helpers.go` over that request, with the agent fetcher left out, the
   second rounding, the budget and the window cap.
5. **Sources and filters.** `services/logs/sources.go`: pods off the mirror, node labels, filters.
6. **The merge and the service.** `services/logs/service.go`, `merge.go`: §2.
7. **Cleaning.** `services/logs/clean.go`: `stripControl`.
8. **Resolvers.** §3. Built in `app/app.go` beside the tools.
9. **Docs.** `sidecar/CLAUDE.md` gains a `services/logs` paragraph, per *When it lands*.

## Tests

Go, beside the files they cover:

- `services/logs/claim_test.go`: a connection for another server UID is refused; a cluster
  disabled under a follow ends it; the body keeps reading while the server keeps writing and
  ends when the context ends.
- `services/logs/fetcher_test.go`: against an `httptest` server that serves
  `/api/v1/namespaces/{ns}/pods/{pod}/log` with timestamps — a forward read since a time, a
  backward read that needs two windows, `previous` sent, a follow that delivers a line written
  after it opened; `after` a cursor inside a second sends that second and drops the lines at and
  before the cursor; a backward grep for a pattern that never matches stops at the budget with
  fewer lines and `nextCursor` set. Kubetail's tests are the starting point.
- `services/logs/sources_test.go`: a deployment's pods off a seeded mirror; `allContainers`
  reads every container, and none named reads each pod's own default; a filter on `zone` leaves a pod out; a `Node` filter works with
  no `Node` object; a `zone` filter on a mirror with no nodes is refused with the sentence.
- `services/logs/merge_test.go`: a fetch's lines from three streams come out in time order; a
  tie keeps a fixed order; `limit` cuts at the right end for `Head` and `Tail` and hands back
  the right cursor; a stream answering `400` or `404` is skipped and named in `skipped` while
  the others fill the page; every stream failing fails the page, and one `403` fails it alone;
  a follow sends one stream's lines while another stays silent; a backward read that spends
  its budget keeps only the lines newer than the newest stopping point and hands that point
  back, and may answer no lines with a cursor.
- `services/logs/clean_test.go`: ANSI colour and cursor sequences gone, the C1 forms gone, DEL
  gone, tabs kept, a line of 20 KiB cut to 16 with `…` on a character boundary (a multi-byte
  character astride the cut is dropped whole); grep matches the stripped text.
- `services/logs/service_test.go`: a follow on a deployment picks up a pod added to the mirror
  after it opened, and goes on after one is deleted; over a fake fetcher, since this is the
  merge's behaviour, not the API's.
- `sidecar/graph/schema.resolvers_test.go`: `logsFetch` serves a page with its `skipped` and
  refuses `limit: 501`; `logsFollow` streams, and a follow whose `Err` is set ends with a
  `watchFailed` frame carrying the reason, as the other watches' tests pin it.
- `services/logs/service_test.go` also: each ending in *How a follow ends* sets `Err` with its
  reason, and a cancelled context sets none.

## Security

**Widens:** what the sidecar reads from the cluster, through the credentials the user already
gave it. Reading a container's log is a `get` on `pods/log`, which `kubectl logs` does today; a
cluster that refuses it answers a plain error. The model reads none of it in this step.

**What the user sees** is log text, which is cluster data. `stripControl` is the protection the
root `CLAUDE.md`'s *log-tail windows inherit this* line asks for, and `clean_test.go` pins it.
Add the row to `docs/security-model.md` as **Enforced** when it lands.

**Residual:** a log line can hold a secret, and this step shows it to the user as `kubectl logs`
would. The model's reading is rung 4's question.

## When it lands

- `sidecar/CLAUDE.md`: a `services/logs` paragraph beside the LogsView tool's, and the two
  operations in the schema's list.
- `docs/security-model.md`: the `stripControl` row.
- This README's row keeps its link until the sequence lands.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/services/logs ./graph/...`; `gqlgen generate`, root
`pnpm codegen`, `pnpm build`; `make test-changed` while working; `make lint-go`, `make vet-go`.

By hand, with the sidecar running against a cluster: a `logsFetch` over the socket with `curl`
for a deployment's last 20 lines answers them in time order with `nextCursor` set; the same with
`grep: "error"` answers only matching lines; a `logsFollow` keeps delivering while a pod writes.
