// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/lib/rawjson"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/clustertest"
)

// fakeService is the cluster service as the logs service reads it: one active
// cache, its health, its statements run over a real store, and the lease
// AcquireConnection hands out. Every method it
// does not set panics on the nil embedded service. Each call answers its error
// when one is set, and Health and Query answer no cache when told so. A test that
// changes a field while a follow runs does so through set.
type fakeService struct {
	cluster.Service
	mu     sync.Mutex
	active cluster.ActiveCluster
	health cluster.ClusterCacheHealth
	store  *clustertest.Store
	lease  *fakeLease
	// frames is what the record's watch sends, the watch failing once it is closed;
	// nil sends nothing.
	frames chan cluster.ClusterWatchFrame
	// pods is what the pods watch sends; closing it ends the watch with podsErr.
	pods    chan cluster.ClusterCachedDataObjectWatchFrame
	podsErr error
	// podNamespaces is what the pods watch was narrowed to.
	podNamespaces []string
	// podsDone closes once the pods watch's context has ended.
	podsDone chan struct{}
	// nodes is what the nodes watch sends; closing it ends the watch with nodesErr.
	nodes    chan cluster.ClusterCachedDataObjectWatchFrame
	nodesErr error
	// workloads is what each other kind's watch sends, by its resource, made on
	// first use; closing one ends that watch with workloadsErr.
	workloads    map[string]chan cluster.ClusterCachedDataObjectWatchFrame
	workloadsErr error

	acquireErr, watchErr, watchObjectsErr, readActiveErr, healthErr, queryErr error
	// queryErrOn is the statement text queryErr applies to, "" for every one.
	queryErrOn        string
	noHealth, noQuery bool
	// healthRead, when set, has each health the fake hands out.
	healthRead *testutil.Probe[cluster.ClusterCacheHealth]
	// mirrorRead, when set, fires as each reading of the mirror returns.
	mirrorRead *testutil.Probe[struct{}]
	// moreOn is the statement text whose answer says rows were left out.
	moreOn string
	// statements is every statement the mirror was asked, its args inlined, in
	// order.
	statements []string
	// beforeStatement, when set, runs before each statement a reading runs, handed
	// its text with its args inlined.
	beforeStatement func(text string)
	// answers, when not nil, keeps each statement's answer by its text and bounds,
	// for a test whose mirror does not change while it reads it.
	answers map[string]cluster.ClusterCachedDataQueryResult
}

// workload is the channel the watch of resource sends from.
func (f *fakeService) workload(resource string) chan cluster.ClusterCachedDataObjectWatchFrame {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.workloadLocked(resource)
}

func (f *fakeService) workloadLocked(resource string) chan cluster.ClusterCachedDataObjectWatchFrame {
	if f.workloads == nil {
		f.workloads = map[string]chan cluster.ClusterCachedDataObjectWatchFrame{}
	}
	if f.workloads[resource] == nil {
		f.workloads[resource] = make(chan cluster.ClusterCachedDataObjectWatchFrame)
	}
	return f.workloads[resource]
}

// set runs change under the fake's lock.
func (f *fakeService) set(change func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change()
}

func (f *fakeService) Caches() cluster.Caches         { return fakeCaches{f: f} }
func (f *fakeService) CachedData() cluster.CachedData { return fakeCachedData{f: f} }
func (f *fakeService) Clusters() cluster.Clusters     { return fakeClusters{f: f} }

func (f *fakeService) AcquireConnection(context.Context, cluster.ClusterID) (cluster.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.acquireErr != nil {
		return nil, f.acquireErr
	}
	return f.lease, nil
}

type fakeCaches struct {
	cluster.Caches
	f *fakeService
}

func (fc fakeCaches) Health(context.Context, cluster.ClusterID, cluster.ClusterCacheID) (cluster.ClusterCacheHealth, bool, error) {
	fc.f.mu.Lock()
	defer fc.f.mu.Unlock()
	if fc.f.healthRead != nil {
		fc.f.healthRead.Fire(fc.f.health)
	}
	return fc.f.health, !fc.f.noHealth, fc.f.healthErr
}

type fakeCachedData struct {
	cluster.CachedData
	f *fakeService
}

func (fc fakeCachedData) ReadOwn(ctx context.Context, _ cluster.ClusterID, _ cluster.ClusterCacheID, read func(cluster.OwnQuery) error) (bool, error) {
	fc.f.mu.Lock()
	noQuery := fc.f.noQuery
	fc.f.mu.Unlock()
	if noQuery {
		return false, nil
	}
	return true, fc.f.store.Read(ctx, func(query cluster.OwnQuery) error {
		return read(func(sql string, maxRows, maxBytes int, args ...any) (cluster.ClusterCachedDataQueryResult, error) {
			text := inlined(sql, args)
			key := fmt.Sprint(maxRows, " ", maxBytes, " ", text)
			fc.f.mu.Lock()
			fc.f.statements = append(fc.f.statements, text)
			queryErr, more := fc.f.queryErr, fc.f.moreOn != "" && strings.Contains(text, fc.f.moreOn)
			if !strings.Contains(text, fc.f.queryErrOn) {
				queryErr = nil
			}
			before := fc.f.beforeStatement
			res, kept := fc.f.answers[key]
			fc.f.mu.Unlock()
			if before != nil {
				before(text)
			}
			if queryErr != nil {
				return cluster.ClusterCachedDataQueryResult{}, queryErr
			}
			if !kept {
				var err error
				if res, err = query(sql, maxRows, maxBytes, args...); err != nil {
					return cluster.ClusterCachedDataQueryResult{}, err
				}
				fc.f.mu.Lock()
				if fc.f.answers != nil {
					fc.f.answers[key] = res
				}
				fc.f.mu.Unlock()
			}
			res.More = res.More || more
			return res, nil
		})
	})
}

func (fc fakeCachedData) ReadActive(ctx context.Context, id cluster.ClusterID, maxRows, maxBytes int, read func(cluster.CacheReading, cluster.ActiveCluster) error) error {
	defer func() {
		fc.f.mu.Lock()
		defer fc.f.mu.Unlock()
		if fc.f.mirrorRead != nil {
			fc.f.mirrorRead.Fire(struct{}{})
		}
	}()
	return cluster.ReadActiveCache(ctx, fc.f, id, maxRows, maxBytes, read)
}

func (fc fakeCachedData) WatchObjectsIn(ctx context.Context, id cluster.ClusterID, cacheID cluster.ClusterCacheID, apiVersion, resource string, namespaces []string) (*cluster.Stream[cluster.ClusterCachedDataObjectWatchFrame], error) {
	if resource == "pods" {
		fc.f.mu.Lock()
		fc.f.podNamespaces = namespaces
		fc.f.mu.Unlock()
	}
	return fc.WatchObjects(ctx, id, cacheID, apiVersion, resource)
}

func (fc fakeCachedData) WatchObjects(ctx context.Context, _ cluster.ClusterID, _ cluster.ClusterCacheID, _, resource string) (*cluster.Stream[cluster.ClusterCachedDataObjectWatchFrame], error) {
	fc.f.mu.Lock()
	watchErr, frames, framesErr, podsDone := fc.f.watchObjectsErr, fc.f.pods, fc.f.podsErr, fc.f.podsDone
	switch resource {
	case "pods":
	case "nodes":
		frames, framesErr, podsDone = fc.f.nodes, fc.f.nodesErr, nil
	default:
		frames, framesErr, podsDone = fc.f.workloadLocked(resource), fc.f.workloadsErr, nil
	}
	fc.f.mu.Unlock()
	if watchErr != nil {
		return nil, watchErr
	}
	if podsDone != nil {
		context.AfterFunc(ctx, func() { close(podsDone) })
	}
	return cluster.NewStream(ctx, func(ctx context.Context, out chan<- cluster.ClusterCachedDataObjectWatchFrame) error {
		for {
			select {
			case frame, ok := <-frames:
				if !ok {
					return framesErr
				}
				out <- frame
			case <-ctx.Done():
				return nil
			}
		}
	}), nil
}

type fakeClusters struct {
	cluster.Clusters
	f *fakeService
}

func (fc fakeClusters) ReadActive(ctx context.Context, _ cluster.ClusterID, read func(context.Context, cluster.ActiveCluster) error) error {
	fc.f.mu.Lock()
	active, err := fc.f.active, fc.f.readActiveErr
	fc.f.mu.Unlock()
	if err != nil {
		return err
	}
	return read(ctx, active)
}

func (fc fakeClusters) Watch(ctx context.Context, _ cluster.ClusterID) (*cluster.Stream[cluster.ClusterWatchFrame], error) {
	fc.f.mu.Lock()
	frames, err := fc.f.frames, fc.f.watchErr
	fc.f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return cluster.NewStream(ctx, func(ctx context.Context, out chan<- cluster.ClusterWatchFrame) error {
		if frames == nil {
			<-ctx.Done()
			return nil
		}
		for {
			select {
			case f, ok := <-frames:
				if !ok {
					return fmt.Errorf("watch failed")
				}
				out <- f
			case <-ctx.Done():
				return nil
			}
		}
	}), nil
}

// fakeLease is a claim on a cluster whose connection is conn, reached by serverUID
// alone; any other UID answers a mismatch.
type fakeLease struct {
	cluster.Lease
	serverUID string
	conn      *cluster.Connection
	released  atomic.Int32
}

func (l *fakeLease) ConnFor(_ context.Context, serverUID string) (*cluster.Connection, error) {
	if serverUID != l.serverUID {
		return nil, fmt.Errorf("%w: another cluster", cluster.ErrIdentityMismatch)
	}
	return l.conn, nil
}

func (l *fakeLease) Release() { l.released.Add(1) }

// serving is a service whose cluster uid-1 is reached at h.
func serving(t *testing.T, h http.Handler) *fakeService {
	t.Helper()
	// HTTP/2, as an API server speaks it: a read that closes its body early drops
	// one stream rather than its connection, so a test paging many logs dials a
	// handful of connections, not one per GET, and leaves the machine's ports alone.
	srv := httptest.NewUnstartedServer(h)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeService{lease: &fakeLease{serverUID: "uid-1", conn: &cluster.Connection{BaseURL: base, HTTPClient: srv.Client()}}}
}

// newMirror is a cluster whose cache is watching, its server uid-1, holding objects,
// with nothing yet on its pods and nodes watches.
func newMirror(t *testing.T, objects ...map[string]any) *fakeService {
	t.Helper()
	f := &fakeService{
		active: cluster.ActiveCluster{Cluster: &cluster.Cluster{}, Cache: &cluster.ClusterCache{RecordMeta: cluster.RecordMeta{ID: 11}, Spec: cluster.ClusterCacheSpec{ServerUID: "uid-1"}}},
		health: cluster.ClusterCacheHealth{Reason: "Watching"},
		store:  clustertest.NewStore(t),
		pods:   make(chan cluster.ClusterCachedDataObjectWatchFrame),
		nodes:  make(chan cluster.ClusterCachedDataObjectWatchFrame),
	}
	for _, o := range objects {
		f.store.Apply(t, o)
	}
	return f
}

// webObjects is a deployment web in prod whose replica set runs two pods, each
// with app and istio-proxy, on node-a and node-b, and a lone pod job-1 on no node
// yet.
func webObjects() []map[string]any {
	return []map[string]any{
		workload("apps/v1", "Deployment", "web", "dep-web", nil),
		workload("apps/v1", "ReplicaSet", "web-7d9f", "rs-web", controller("apps/v1", "Deployment", "web", "dep-web")),
		pod("web-1", "uid-web-1", "node-a", webReplicaSet(), container{name: "app"}, container{name: "istio-proxy"}),
		pod("web-2", "uid-web-2", "node-b", webReplicaSet(), container{name: "app"}, container{name: "istio-proxy"}),
		pod("job-1", "uid-job-1", "", nil, container{name: "main"}),
	}
}

// webReplicaSet is the controller reference onto web's replica set.
func webReplicaSet() map[string]any {
	return controller("apps/v1", "ReplicaSet", "web-7d9f", "rs-web")
}

// webNodes are node-a and node-b, in two zones of one region.
func webNodes() []map[string]any {
	return []map[string]any{
		node("node-a", "eu", "eu-1a", "linux", "amd64"),
		node("node-b", "eu", "eu-1b", "linux", "arm64"),
	}
}

// webMirror is webObjects and webNodes.
func webMirror(t *testing.T) *fakeService {
	t.Helper()
	return newMirror(t, append(webObjects(), webNodes()...)...)
}

// chunk makes the cluster's responses hand out at most n bytes per Read, so a
// fetch's budget can run out mid-line.
func (f *fakeService) chunk(n int) {
	c := f.lease.conn.HTTPClient
	c.Transport = chunked{c.Transport, n}
}

type chunked struct {
	next http.RoundTripper
	n    int
}

func (c chunked) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.next.RoundTrip(req)
	if err == nil {
		resp.Body = chunkedBody{resp.Body, c.n}
	}
	return resp, err
}

type chunkedBody struct {
	io.ReadCloser
	n int
}

func (b chunkedBody) Read(p []byte) (int, error) {
	return b.ReadCloser.Read(p[:min(len(p), b.n)])
}

// enabledCluster is a record Kstack connects to.
func enabledCluster() *cluster.Cluster {
	return &cluster.Cluster{Spec: cluster.ClusterSpec{
		Enabled: true,
		Source:  cluster.ClusterSpecSource{Kubeconfig: &cluster.ClusterSpecSourceKubeconfig{Context: "dev"}},
	}}
}

// logServer is the API server's log endpoint over containers' logs held in
// memory, honouring the parameters the fetcher sends.
type logServer struct {
	mu sync.Mutex
	// logs is each container's lines, "<ts> <message>", keyed "<ns>/<pod>/<container>",
	// with "/previous" for the instance before. A follow streams the lines added
	// after its read, so a line reaches every follow still open, and one opened
	// later reads it as part of the log: a follow the client has abandoned, whose
	// handler has not noticed yet, cannot take a line from the follow that
	// replaced it.
	logs map[string][]string
	// refuse answers a container's requests with a status and a message.
	refuse map[string]refusal
	// grew is closed and replaced when a container's log grows or its follows
	// end, by key. ended is the containers whose follows end, and any opened after.
	grew  map[string]chan struct{}
	ended map[string]bool
	// queries is every request's query, in order, and keys each one's container.
	queries []url.Values
	keys    []string
	// opened and closed count each container's follows that have opened and ended,
	// by key.
	opened, closed map[string]int
	// onRequest, when set, runs before each request is served, with its index, and
	// onOpen once each follow has opened, with its key. Set either before any request.
	onRequest func(n int)
	onOpen    func(key string)
}

type refusal struct {
	code    int
	message string
}

func newLogServer() *logServer {
	return &logServer{logs: map[string][]string{}, refuse: map[string]refusal{}, grew: map[string]chan struct{}{}, ended: map[string]bool{}, opened: map[string]int{}, closed: map[string]int{}}
}

// end ends key's follows, and any opened after.
func (s *logServer) end(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended[key] = true
	s.wake(key)
}

// wake tells key's follows its log changed. Call it holding s.mu.
func (s *logServer) wake(key string) {
	if grew := s.grew[key]; grew != nil {
		close(grew)
	}
	s.grew[key] = make(chan struct{})
}

// next is a copy of key's lines from the nth, whether its follows have ended,
// and a channel closed when either changes. A log emptied under a follow has
// fewer lines than the follow has sent.
func (s *logServer) next(key string, n int) ([]string, bool, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grew[key] == nil {
		s.grew[key] = make(chan struct{})
	}
	lines := s.logs[key]
	return append([]string(nil), lines[min(n, len(lines)):]...), s.ended[key], s.grew[key]
}

// add appends lines to key's log, as a running container writes them.
func (s *logServer) add(key string, lines ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs[key] = append(s.logs[key], lines...)
	s.wake(key)
}

// clear empties key's log.
func (s *logServer) clear(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.logs, key)
}

// setRefusal answers key's requests with r from now on, nil for none.
func (s *logServer) setRefusal(key string, r *refusal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r == nil {
		delete(s.refuse, key)
		return
	}
	s.refuse[key] = *r
}

// followsOpened is how many of key's follows have opened.
func (s *logServer) followsOpened(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened[key]
}

// followsClosed is how many of key's follows have ended.
func (s *logServer) followsClosed(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed[key]
}

// requests is every query so far.
func (s *logServer) requests() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.queries...)
}

// requestsOf is every query for key's container, in order. A follow's containers
// read concurrently, so a test about one container reads its requests alone.
func (s *logServer) requestsOf(key string) []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []url.Values
	for i, k := range s.keys {
		if k == key {
			out = append(out, s.queries[i])
		}
	}
	return out
}

// keyOf is the container a log path and query name, "" for a path that is no log's.
func keyOf(path string, q url.Values) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) != 7 || parts[0] != "api" || parts[6] != "log" {
		return ""
	}
	key := parts[3] + "/" + parts[5] + "/" + q.Get("container")
	if q.Get("previous") == "true" {
		key += "/previous"
	}
	return key
}

// get reads s in memory, as a claim's get reads the API server, so a read's
// bytes arrive as the server wrote them. It never follows.
func (s *logServer) get(_ context.Context, path string, q url.Values) (io.ReadCloser, error) {
	text, _, refused := s.serve(keyOf(path, q), q)
	if refused != nil {
		return nil, &statusError{refused.code, refused.message}
	}
	return io.NopCloser(strings.NewReader(text)), nil
}

func (s *logServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r.URL.Path, r.URL.Query())
	if key == "" {
		http.NotFound(w, r)
		return
	}
	text, n, refused := s.serve(key, r.URL.Query())
	if refused != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(refused.code)
		_, _ = fmt.Fprintf(w, `{"kind":"Status","message":%q}`, refused.message)
		return
	}
	_, _ = w.Write([]byte(text))
	if r.URL.Query().Get("follow") != "true" {
		return
	}
	w.(http.Flusher).Flush()
	s.mu.Lock()
	s.opened[key]++
	s.mu.Unlock()
	if s.onOpen != nil {
		s.onOpen(key)
	}
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closed[key]++
	}()
	for {
		lines, ended, grew := s.next(key, n)
		for _, line := range lines {
			_, _ = w.Write([]byte(line + "\n"))
			w.(http.Flusher).Flush()
		}
		n += len(lines)
		if ended {
			return
		}
		select {
		case <-grew:
		case <-r.Context().Done():
			return
		}
	}
}

// serve is key's log as the request's parameters cut it, and how many lines the
// log held, or the refusal set for key, after the request is counted and the
// hook has run.
func (s *logServer) serve(key string, q url.Values) (string, int, *refusal) {
	s.mu.Lock()
	n := len(s.queries)
	s.queries = append(s.queries, q)
	s.keys = append(s.keys, key)
	hook := s.onRequest
	s.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	s.mu.Lock()
	lines := append([]string(nil), s.logs[key]...)
	held := len(lines)
	refused, isRefused := s.refuse[key]
	s.mu.Unlock()
	if isRefused {
		return "", 0, &refused
	}
	if since := q.Get("sinceTime"); since != "" {
		at, _ := time.Parse(time.RFC3339, since)
		var kept []string
		for _, line := range lines {
			ts, _ := time.Parse(time.RFC3339Nano, strings.SplitN(line, " ", 2)[0])
			if !ts.Before(at) {
				kept = append(kept, line)
			}
		}
		lines = kept
	}
	if tail := q.Get("tailLines"); tail != "" {
		n, _ := strconv.Atoi(tail)
		lines = lines[max(0, len(lines)-n):]
	}
	text := ""
	if len(lines) > 0 {
		text = strings.Join(lines, "\n") + "\n"
	}
	if limit := q.Get("limitBytes"); limit != "" {
		n, _ := strconv.Atoi(limit)
		text = text[:min(n, len(text))]
	}
	return text, held, nil
}

// claimOn is a claim on a cluster whose API server serves s.
func claimOn(t *testing.T, s *logServer) *claim {
	t.Helper()
	c, err := acquireClaim(t.Context(), serving(t, s), "7", "uid-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	return c
}

// at is a moment on 2026-10-08 at 10:00, offset by d.
func at(d time.Duration) time.Time {
	return time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC).Add(d)
}

// stamped is a log line at d past 10:00 saying msg.
func stamped(d time.Duration, msg string) string {
	return at(d).Format(time.RFC3339Nano) + " " + msg
}

// drain is every record r sends until it closes.
func drain(r *streamRead) []Record {
	var out []Record
	for rec := range r.lines.Chan() {
		out = append(out, rec)
	}
	return out
}

// messages is each record's message.
func messages(recs []Record) []string {
	out := []string{}
	for _, r := range recs {
		out = append(out, r.Message)
	}
	return out
}

// webView reads the app container of deployment web in prod.
var webView = View{Sources: []ViewSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web", Containers: []string{"app"}}}}

// addPods adds n pods under web's replica set, each with one app container.
func addPods(t *testing.T, f *fakeService, n int) {
	t.Helper()
	for i := range n {
		name := "many-" + strconv.Itoa(i)
		f.store.Apply(t, pod(name, "uid-"+name, "node-a", webReplicaSet(), container{name: "app"}))
	}
}

// container is one running container of a fixture pod, with a previous instance
// when restarted is set.
type container struct {
	name      string
	restarted bool
}

// controller is an owner reference that controls its object.
func controller(apiVersion, kind, name, uid string) map[string]any {
	return map[string]any{"apiVersion": apiVersion, "kind": kind, "name": name, "uid": uid, "controller": true}
}

// workload is a body of kind in prod, owned by owner when it is not nil.
func workload(apiVersion, kind, name, uid string, owner map[string]any) map[string]any {
	meta := map[string]any{"uid": uid, "name": name, "namespace": "prod"}
	if owner != nil {
		meta["ownerReferences"] = []any{owner}
	}
	return map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": meta}
}

// pod is a Pod in prod on node ("" for none yet), owned by owner, running
// containers.
func pod(name, uid, node string, owner map[string]any, containers ...container) map[string]any {
	body := workload("v1", "Pod", name, uid, owner)
	var specs, statuses []any
	for _, c := range containers {
		specs = append(specs, map[string]any{"name": c.name, "image": c.name + ":1"})
		statuses = append(statuses, containerStatus(uid, c))
	}
	spec := map[string]any{"containers": specs}
	if node != "" {
		spec["nodeName"] = node
	}
	body["spec"] = spec
	body["status"] = map[string]any{"phase": "Running", "containerStatuses": statuses}
	return body
}

// containerStatus is c's status in the pod uid, with a runtime id per instance,
// as a runtime gives them: the last one's beside the running one's when it
// restarted.
func containerStatus(uid string, c container) map[string]any {
	id := func(instance int) string { return containerID(uid, c.name, instance) }
	status := map[string]any{"name": c.name, "restartCount": int64(0), "containerID": id(0), "state": map[string]any{"running": map[string]any{}}}
	if c.restarted {
		status["restartCount"] = int64(1)
		status["containerID"] = id(1)
		status["lastState"] = map[string]any{"terminated": map[string]any{"containerID": id(0)}}
	}
	return status
}

// containerID is the runtime id of the instance-th instance of the container
// name in the pod uid, as containerStatus writes it.
func containerID(uid, name string, instance int) string {
	return fmt.Sprintf("containerd://%s-%s-%d", uid, name, instance)
}

// withInit is body, a pod, with a terminated init container named name.
func withInit(body map[string]any, name string) map[string]any {
	body["spec"].(map[string]any)["initContainers"] = []any{map[string]any{"name": name, "image": name + ":1"}}
	body["status"].(map[string]any)["initContainerStatuses"] = []any{map[string]any{"name": name, "restartCount": int64(0), "state": map[string]any{"terminated": map[string]any{"containerID": "containerd://" + name}}}}
	return body
}

// finished is body, a pod, with every container terminated: its last instance's
// log is whole.
func finished(body map[string]any) map[string]any {
	for _, s := range body["status"].(map[string]any)["containerStatuses"].([]any) {
		status := s.(map[string]any)
		status["state"] = map[string]any{"terminated": map[string]any{"containerID": status["containerID"]}}
	}
	return body
}

// waiting is body, a pod, with every container waiting to start: no instance
// yet, so no log.
func waiting(body map[string]any) map[string]any {
	for _, s := range body["status"].(map[string]any)["containerStatuses"].([]any) {
		status := s.(map[string]any)
		delete(status, "containerID")
		status["state"] = map[string]any{"waiting": map[string]any{"reason": "ContainerCreating"}}
	}
	return body
}

// podFrame is a pods watch frame of the pod name in prod, and uidFrame one of
// the pod of that UID.
func podFrame(typ cluster.DeltaFrameType, name string) cluster.ClusterCachedDataObjectWatchFrame {
	return uidFrame(typ, name, "")
}

func uidFrame(typ cluster.DeltaFrameType, name, uid string) cluster.ClusterCachedDataObjectWatchFrame {
	return cluster.ClusterCachedDataObjectWatchFrame{Type: typ, Object: &cluster.ClusterCachedDataObject{UID: uid, APIVersion: "v1", Kind: "Pod", Namespace: "prod", Name: name}}
}

// nodeFrame is a nodes watch frame carrying body, a node.
func nodeFrame(typ cluster.DeltaFrameType, body map[string]any) cluster.ClusterCachedDataObjectWatchFrame {
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	name := body["metadata"].(map[string]any)["name"].(string)
	return cluster.ClusterCachedDataObjectWatchFrame{Type: typ, Object: &cluster.ClusterCachedDataObject{
		APIVersion: "v1", Kind: "Node", Name: name, RawJSON: rawjson.RawJSON(raw),
	}}
}

// node is a Node with the well-known topology, OS and architecture labels.
func node(name, region, zone, os, arch string) map[string]any {
	return map[string]any{
		"apiVersion": "v1", "kind": "Node",
		"metadata": map[string]any{"uid": "uid-" + name, "name": name, "labels": map[string]any{
			labelRegion: region, labelZone: zone, labelOS: os, labelArch: arch,
		}},
	}
}

// inlined is sql with each placeholder replaced by its arg as a SQL literal, so a
// test reads a statement whole.
func inlined(sql string, args []any) string {
	if n := strings.Count(sql, "?"); n != len(args) {
		panic(fmt.Sprintf("%d placeholders and %d args in %s", n, len(args), sql))
	}
	for _, a := range args {
		sql = strings.Replace(sql, "?", "'"+strings.ReplaceAll(fmt.Sprint(a), "'", "''")+"'", 1)
	}
	return sql
}

// from, after, to and before are bounds at the moment d past 10:00: since it,
// after it, until it, and before it.
func from(d time.Duration) bound   { return bound{position{Timestamp: at(d)}, true} }
func after(d time.Duration) bound  { return bound{position{Timestamp: at(d)}, false} }
func to(d time.Duration) bound     { return bound{position{Timestamp: at(d)}, true} }
func before(d time.Duration) bound { return bound{position{Timestamp: at(d)}, false} }

// lineAt is the position of key's line at d past 10:00, from the first instance
// of a fixture pod, the ordinal-th of its moment.
func lineAt(key StreamKey, d time.Duration, ordinal int) position {
	return position{Timestamp: at(d), Namespace: key.Namespace, Pod: key.Pod, Container: key.Container, ContainerID: containerID("uid-"+key.Pod, key.Container, 0), Ordinal: ordinal}
}

// lineCursor is the cursor of pod/container's line at d past 10:00, in prod, the
// ordinal-th of its moment, from the first instance of a fixture pod.
func lineCursor(pod, container string, d time.Duration, ordinal int) *string {
	return cursorOf(lineAt(StreamKey{Namespace: "prod", Pod: pod, Container: container}, d, ordinal))
}

// keysOf is each of t's streams' key, in order.
func keysOf(t target) []StreamKey {
	keys := make([]StreamKey, len(t.streams))
	for i, s := range t.streams {
		keys[i] = s.key
	}
	return keys
}

// placesOf is each of t's streams' place, in order.
func placesOf(t target) []Place {
	places := make([]Place, len(t.streams))
	for i, s := range t.streams {
		places[i] = s.place
	}
	return places
}

// line is a record of pod/container in prod at d past 10:00, from the first
// instance of a fixture pod, the first line of its moment, and nth the
// ordinal-th.
func line(pod, container string, d time.Duration, msg string) Record {
	return nth(pod, container, d, 0, msg)
}

func nth(pod, container string, d time.Duration, ordinal int, msg string) Record {
	return Record{Timestamp: at(d), Message: msg, Stream: StreamKey{Namespace: "prod", Pod: pod, Container: container}, ContainerID: containerID("uid-"+pod, container, 0), Ordinal: ordinal}
}

// cursor is the cursor of the moment d past 10:00.
func cursor(d time.Duration) *string {
	return cursorOf(position{Timestamp: at(d)})
}

// serviceOver is the service over f's mirror and the API server h, on a clock
// reading 11:00, its follows handing off and resolving at once, and retrying within
// milliseconds however often a stream drops.
func serviceOver(t *testing.T, f *fakeService, h http.Handler) (*Service, *fakeService) {
	t.Helper()
	f.lease = serving(t, h).lease
	svc := New(f)
	svc.now, svc.handoff, svc.settle = func() time.Time { return at(time.Hour) }, 0, 0
	svc.retry, svc.retryCap = time.Millisecond, 8*time.Millisecond
	return svc, f
}

// fakeSource is a Source that reads each stream as reads says, by
// "<pod>/<container>", and fires opened with that key on every read.
type fakeSource struct {
	mu     sync.Mutex
	reads  map[string]fakeRead
	opened *testutil.Probe[string]
}

// fakeRead is one read of a fake source: lines, each stamped with its stream,
// then, once wait closes, an end with err. A nil wait ends at once, and never
// ends only once the read is cancelled.
type fakeRead struct {
	lines []Record
	err   error
	wait  <-chan struct{}
}

// never is a wait that never closes.
var never = make(chan struct{})

func newFakeSource() *fakeSource {
	return &fakeSource{reads: map[string]fakeRead{}, opened: testutil.NewProbe[string](64)}
}

// set makes every later read of key read as r.
func (s *fakeSource) set(key string, r fakeRead) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads[key] = r
}

func (s *fakeSource) read(ctx context.Context, st stream) *streamRead {
	key := st.key.Pod + "/" + st.key.Container
	s.mu.Lock()
	r := s.reads[key]
	s.mu.Unlock()
	recs := make([]Record, len(r.lines))
	for i, rec := range r.lines {
		rec.Stream, rec.Place, rec.ContainerID = st.key, st.place, st.gen.instance
		recs[i] = rec
	}
	s.opened.Fire(key)
	return cannedUntil(ctx, st.key, recs, r.wait, r.err, position{})
}

func (s *fakeSource) Forward(ctx context.Context, st stream, _ readOpts) *streamRead {
	return s.read(ctx, st)
}

func (s *fakeSource) Backward(ctx context.Context, st stream, _ readOpts) *streamRead {
	return s.read(ctx, st)
}

func (s *fakeSource) Tail(ctx context.Context, st stream, _ readOpts) *streamRead {
	return s.read(ctx, st)
}

// serviceOn is the service over the web mirror, reading through src.
func serviceOn(t *testing.T, src Source) (*Service, *fakeService) {
	t.Helper()
	svc, f := serviceOver(t, webMirror(t), newLogServer())
	svc.source = func(getter) Source { return src }
	return svc, f
}
