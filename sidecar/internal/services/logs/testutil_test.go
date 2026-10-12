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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/lib/rawjson"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster/mirrorsql"
)

// fakeService is the cluster service as the log service reads it. Every method it
// does not set panics on the nil embedded service. Each call answers its error
// when one is set, and Health and Query answer no cache when told so. A test that
// changes a field while a follow runs does so through set.
type fakeService struct {
	cluster.Service
	mu sync.Mutex
	// lease is what AcquireConnection hands out.
	lease *fakeLease
	// frames is what the record's watch sends, the watch failing once it is closed;
	// nil sends nothing.
	frames chan cluster.ClusterWatchFrame
	// active is the one reading ReadActive hands out, and health its cache's.
	active cluster.ActiveCluster
	health cluster.ClusterCacheHealth
	// answer answers a mirror statement by its text.
	answer func(sql string) [][]any
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

	acquireErr, watchErr, watchObjectsErr, readActiveErr, healthErr, queryErr error
	// queryErrOn is the statement text queryErr applies to, "" for every one.
	queryErrOn        string
	noHealth, noQuery bool
	// healthRead, when set, has each health the fake hands out.
	healthRead *testutil.Probe[cluster.ClusterCacheHealth]
	// moreOn is the statement text whose answer says rows were left out.
	moreOn string
	// statements is every statement the mirror was asked, in order.
	statements []string
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

func (fc fakeCachedData) Query(_ context.Context, _ cluster.ClusterID, _ cluster.ClusterCacheID, sql string, _, _ int) (cluster.ClusterCachedDataQueryResult, bool, error) {
	fc.f.mu.Lock()
	defer fc.f.mu.Unlock()
	fc.f.statements = append(fc.f.statements, sql)
	if fc.f.queryErr != nil && strings.Contains(sql, fc.f.queryErrOn) {
		return cluster.ClusterCachedDataQueryResult{}, false, fc.f.queryErr
	}
	if fc.f.noQuery {
		return cluster.ClusterCachedDataQueryResult{}, false, nil
	}
	more := fc.f.moreOn != "" && strings.Contains(sql, fc.f.moreOn)
	return cluster.ClusterCachedDataQueryResult{Rows: fc.f.answer(sql), More: more}, true, nil
}

func (fc fakeCachedData) WatchObjectsIn(ctx context.Context, id cluster.ClusterID, cacheID cluster.ClusterCacheID, apiVersion, resource string, namespaces []string) (*cluster.Stream[cluster.ClusterCachedDataObjectWatchFrame], error) {
	fc.f.mu.Lock()
	fc.f.podNamespaces = namespaces
	fc.f.mu.Unlock()
	return fc.WatchObjects(ctx, id, cacheID, apiVersion, resource)
}

func (fc fakeCachedData) WatchObjects(ctx context.Context, _ cluster.ClusterID, _ cluster.ClusterCacheID, _, resource string) (*cluster.Stream[cluster.ClusterCachedDataObjectWatchFrame], error) {
	fc.f.mu.Lock()
	watchErr, frames, framesErr, podsDone := fc.f.watchObjectsErr, fc.f.pods, fc.f.podsErr, fc.f.podsDone
	if resource == "nodes" {
		frames, framesErr, podsDone = fc.f.nodes, fc.f.nodesErr, nil
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
	err, active := fc.f.readActiveErr, fc.f.active
	if active.Cache != nil {
		cache := *active.Cache
		active.Cache = &cache
	}
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
	// with "/previous" for the instance before.
	logs map[string][]string
	// refuse answers a container's requests with a status and a message.
	refuse map[string]refusal
	// follows is what each container's follow writes after the log it opened on,
	// by key; closing one ends that follow.
	follows map[string]chan string
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
	return &logServer{logs: map[string][]string{}, refuse: map[string]refusal{}, follows: map[string]chan string{}, opened: map[string]int{}, closed: map[string]int{}}
}

// follow is what key's follow writes after the log it opened on.
func (s *logServer) follow(key string) chan string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.follows[key] == nil {
		s.follows[key] = make(chan string)
	}
	return s.follows[key]
}

// add appends lines to key's log.
func (s *logServer) add(key string, lines ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs[key] = append(s.logs[key], lines...)
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
	text, refused := s.serve(keyOf(path, q), q)
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
	text, refused := s.serve(key, r.URL.Query())
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
	follow := s.follow(key)
	for {
		select {
		case line, ok := <-follow:
			if !ok {
				return
			}
			_, _ = w.Write([]byte(line + "\n"))
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// serve is key's log as the request's parameters cut it, or the refusal set for
// key, after the request is counted and the hook has run.
func (s *logServer) serve(key string, q url.Values) (string, *refusal) {
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
	refused, isRefused := s.refuse[key]
	s.mu.Unlock()
	if isRefused {
		return "", &refused
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
	return text, nil
}

// claimOn is a claim on a cluster whose API server serves s.
func claimOn(t *testing.T, s *logServer) *claim {
	t.Helper()
	c, err := openClaim(t.Context(), serving(t, s), "7", "uid-1", time.Millisecond)
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
func drain(r *reading) []Record {
	var out []Record
	for rec := range r.lines {
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

// mirror is a cluster whose cache is watching, its server uid-1, and whose
// statements answer from pods and nodes: each pod a "<ns>/<name>" with its node
// and containers, each node its name and labels.
type mirror struct {
	mu    sync.Mutex
	pods  map[string]mirrorPod
	nodes []mirrorNode
	// under is each workload's pods, keyed "<kind> <ns>/<name>".
	under map[string][]string
}

type mirrorPod struct {
	node       string
	containers []string
	// The log's generation: the pod's uid, and each container's restarts and
	// state, "" for running.
	uid      string
	restarts int64
	state    string
	// states is a container's own state, over the pod's.
	states map[string]string
}

type mirrorNode struct{ name, region, zone, os, arch string }

func (m *mirror) service() *fakeService {
	f := &fakeService{
		active: cluster.ActiveCluster{Cluster: &cluster.Cluster{}, Cache: &cluster.ClusterCache{RecordMeta: cluster.RecordMeta{ID: 11}, Spec: cluster.ClusterCacheSpec{ServerUID: "uid-1"}}},
		health: cluster.ClusterCacheHealth{Reason: "Watching"},
		pods:   make(chan cluster.ClusterCachedDataObjectWatchFrame),
		nodes:  make(chan cluster.ClusterCachedDataObjectWatchFrame),
	}
	f.answer = m.answer
	return f
}

// The conditions a pods statement narrows by, as resolve writes them: the
// containers, the nodes, and a node label's values.
var (
	containersIn = regexp.MustCompile(`c\.name IN \(([^)]*)\)`)
	nodesIn      = regexp.MustCompile(`p\.node IN \(('[^)]*)\)`)
	labelIn      = regexp.MustCompile(`l\.key = '([^']*)' AND l\.value IN \(([^)]*)\)`)
)

// literalList is a list of SQL literals as its values.
func literalList(list string) []string {
	var out []string
	for v := range strings.SplitSeq(list, ", ") {
		out = append(out, strings.Trim(v, "'"))
	}
	return out
}

// passes is whether container c of pod p passes every condition sql narrows by,
// as the mirror would apply them: a label condition needs a node the mirror holds.
func (m *mirror) passes(sql string, p mirrorPod, c string) bool {
	if match := containersIn.FindStringSubmatch(sql); match != nil && !slices.Contains(literalList(match[1]), c) {
		return false
	}
	for _, match := range nodesIn.FindAllStringSubmatch(sql, -1) {
		if !slices.Contains(literalList(match[1]), p.node) {
			return false
		}
	}
	for _, match := range labelIn.FindAllStringSubmatch(sql, -1) {
		i := slices.IndexFunc(m.nodes, func(n mirrorNode) bool { return n.name == p.node })
		if i < 0 {
			return false
		}
		n := m.nodes[i]
		value := map[string]string{labelRegion: n.region, labelZone: n.zone, labelOS: n.os, labelArch: n.arch}[match[1]]
		if !slices.Contains(literalList(match[2]), value) {
			return false
		}
	}
	return true
}

// answer reads a statement as resolve writes it: the nodes, or one source's pods
// under the statement's conditions.
func (m *mirror) answer(sql string) [][]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.HasPrefix(sql, "SELECT n.name") {
		var rows [][]any
		for _, n := range m.nodes {
			rows = append(rows, []any{n.name, n.region, n.zone, n.os, n.arch})
		}
		return rows
	}
	var names []string
	for key, pods := range m.under {
		kind, nsName, _ := strings.Cut(key, " ")
		ns, name, _ := strings.Cut(nsName, "/")
		if strings.Contains(sql, "kind = "+mirrorsql.Literal(kind)) && strings.Contains(sql, "namespace = "+mirrorsql.Literal(ns)) && strings.Contains(sql, "name = "+mirrorsql.Literal(name)) {
			names = append(names, pods...)
		}
	}
	for key := range m.pods {
		ns, name, _ := strings.Cut(key, "/")
		if strings.Contains(sql, "p.namespace = "+mirrorsql.Literal(ns)) && strings.Contains(sql, "p.name = "+mirrorsql.Literal(name)) {
			names = append(names, key)
		}
	}
	var rows [][]any
	for _, key := range names {
		p, ok := m.pods[key]
		if !ok {
			continue
		}
		_, name, _ := strings.Cut(key, "/")
		var node any
		if p.node != "" {
			node = p.node
		}
		for _, c := range p.containers {
			if !m.passes(sql, p, c) {
				continue
			}
			state := p.state
			if own, ok := p.states[c]; ok {
				state = own
			}
			if state == "" {
				state = "running"
			}
			rows = append(rows, []any{name, node, c, p.uid, p.restarts, state})
		}
	}
	return rows
}

// setNodes replaces the mirror's nodes.
func (m *mirror) setNodes(nodes []mirrorNode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes = nodes
}

// setPod puts a pod in the mirror under the workload key, or with none takes it
// out of the mirror and every workload.
// finish marks every container of the pod key terminated.
func (m *mirror) finish(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.pods[key]
	p.state = "terminated"
	m.pods[key] = p
}

func (m *mirror) setPod(key string, p *mirrorPod, under string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p == nil {
		delete(m.pods, key)
		for w, pods := range m.under {
			m.under[w] = slices.DeleteFunc(pods, func(k string) bool { return k == key })
		}
		return
	}
	m.pods[key] = *p
	if under != "" {
		m.under[under] = append(m.under[under], key)
	}
}

// podFrame is a pods watch frame for the pod "<ns>/<name>", and uidFrame one for
// the pod of that UID.
func podFrame(typ cluster.DeltaFrameType, key string) cluster.ClusterCachedDataObjectWatchFrame {
	return uidFrame(typ, key, "")
}

func uidFrame(typ cluster.DeltaFrameType, key, uid string) cluster.ClusterCachedDataObjectWatchFrame {
	ns, name, _ := strings.Cut(key, "/")
	return cluster.ClusterCachedDataObjectWatchFrame{Type: typ, Object: &cluster.ClusterCachedDataObject{UID: uid, APIVersion: "v1", Kind: "Pod", Namespace: ns, Name: name}}
}

// nodeFrame is a frame of node n, its labels as the mirror's nodes carry them.
func nodeFrame(typ cluster.DeltaFrameType, n mirrorNode) cluster.ClusterCachedDataObjectWatchFrame {
	body, _ := json.Marshal(map[string]any{"metadata": map[string]any{"name": n.name, "labels": map[string]string{
		labelRegion: n.region, labelZone: n.zone, labelOS: n.os, labelArch: n.arch,
	}}})
	return cluster.ClusterCachedDataObjectWatchFrame{Type: typ, Object: &cluster.ClusterCachedDataObject{
		APIVersion: "v1", Kind: "Node", Name: n.name, RawJSON: rawjson.RawJSON(body),
	}}
}

// from, after, to and before are bounds at the moment d past 10:00: since it,
// after it, until it, and before it.
func from(d time.Duration) bound   { return bound{position{Timestamp: at(d)}, true} }
func after(d time.Duration) bound  { return bound{position{Timestamp: at(d)}, false} }
func to(d time.Duration) bound     { return bound{position{Timestamp: at(d)}, true} }
func before(d time.Duration) bound { return bound{position{Timestamp: at(d)}, false} }

// lineAt is the position of src's line at d past 10:00, the ordinal-th of its
// moment.
func lineAt(src RecordSource, d time.Duration, ordinal int) position {
	return position{at(d), src.Namespace, src.Pod, src.Container, src.Previous, ordinal}
}

// lineCursor is the cursor of pod/container's line at d past 10:00, in prod, the
// ordinal-th of its moment.
func lineCursor(pod, container string, d time.Duration, ordinal int) *string {
	return cursorOf(lineAt(RecordSource{Namespace: "prod", Pod: pod, Container: container}, d, ordinal))
}

// sourcesOf is each of t's streams' source, in order.
func sourcesOf(t target) []RecordSource {
	out := make([]RecordSource, len(t.streams))
	for i, s := range t.streams {
		out[i] = s.src
	}
	return out
}

// line is a record of pod/container in prod at d past 10:00, the first of its
// moment, and nth the ordinal-th.
func line(pod, container string, d time.Duration, msg string) Record {
	return nth(pod, container, d, 0, msg)
}

func nth(pod, container string, d time.Duration, ordinal int, msg string) Record {
	return Record{Timestamp: at(d), Message: msg, Source: RecordSource{Namespace: "prod", Pod: pod, Container: container}, Ordinal: ordinal}
}

// cursor is the cursor of the moment d past 10:00.
func cursor(d time.Duration) *string {
	return cursorOf(position{Timestamp: at(d)})
}
