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
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// MaxLimit is the most lines one page holds.
const MaxLimit = 500

// errStopped ends a follow with no stream left running.
var errStopped = &Refusal{"every container of this view has stopped"}

// followHandoff is how long a follow holds its first lines to sort them.
const followHandoff = 50 * time.Millisecond

// followRetry is how long a follow waits to resolve its view again after the
// mirror refused it, to open its mirror watches again after a clean end, or to
// reopen a stream the connection dropped the first time. Each drop in a row doubles
// the stream's wait, up to followRetryCap.
const (
	followRetry    = 2 * time.Second
	followRetryCap = time.Minute
)

// followSettle is how long a follow waits after a pod frame before it resolves its
// view again, so a burst of frames costs one resolve.
const followSettle = 250 * time.Millisecond

// Service reads views' lines off the clusters the cluster service connects to.
type Service struct {
	clusters cluster.Service
	now      func() time.Time
	// handoff is how long a follow holds its first lines, retry how long it waits
	// to resolve its view again after the mirror refused it, to open its watches
	// again or to reopen a dropped stream, retryCap the longest a stream that keeps
	// dropping waits, settle how long it waits after a pod frame, and budget the
	// bytes one fetch may read.
	handoff  time.Duration
	retry    time.Duration
	retryCap time.Duration
	settle   time.Duration
	budget   int64
}

// New is the service over the cluster service.
func New(clusters cluster.Service) *Service {
	return &Service{clusters: clusters, now: time.Now, handoff: followHandoff, retry: followRetry, retryCap: followRetryCap, settle: followSettle, budget: maxBudget}
}

// FetchQuery is which page of a view to read: the end cut from when the range
// holds more than Limit lines, and the cursors bounding it. Since and Until keep
// their cursor's line, After and Before do not. With no cursor, Head reads from the
// oldest line and Tail from the newest.
type FetchQuery struct {
	Mode   FetchMode
	Since  *string
	Until  *string
	After  *string
	Before *string
	Limit  int
}

// Fetch is one page of view's lines on cluster id. The sources are resolved inside
// one reading of the cluster's mirror; the reads run outside it, over a claim on
// the server the pods came from.
func (s *Service) Fetch(ctx context.Context, id cluster.ClusterID, view View, q FetchQuery) (Page, error) {
	if q.Limit < 1 || q.Limit > MaxLimit {
		return Page{}, &Refusal{fmt.Sprintf("a limit must be between 1 and %d", MaxLimit)}
	}
	o, err := readOptsOf(view, q)
	if err != nil {
		return Page{}, err
	}
	t, err := resolve(ctx, s.clusters, id, view)
	if err == nil {
		err = t.tooMany()
	}
	if err != nil {
		return Page{}, err
	}
	if len(t.streams) == 0 {
		return Page{Records: []Record{}, Skipped: []string{}}, nil
	}
	c, err := acquireClaim(ctx, s.clusters, id, t.serverUID)
	if err != nil {
		return Page{}, err
	}
	defer c.close()
	o.batch, o.windows = int64(q.Limit)+1, maxWindows
	readCtx, stop := context.WithCancel(ctx)
	defer stop()
	readings := make([]*reading, len(t.streams))
	for i, st := range t.streams {
		o.budget = newBudget(s.budget / int64(len(t.streams)))
		if q.Mode == FetchTail {
			readings[i] = readBackward(readCtx, c.get, st.src, o)
		} else {
			readings[i] = readForward(readCtx, c.get, st.src, o)
		}
	}
	page, err := mergePage(ctx, stop, readings, q.Mode, q.Limit)
	switch {
	case err != nil:
		return Page{}, err
	case ctx.Err() != nil:
		return Page{}, ctx.Err()
	}
	return page, nil
}

// readOptsOf is the bounds and grep a page reads with.
func readOptsOf(view View, q FetchQuery) (readOpts, error) {
	var o readOpts
	var err error
	if o.since, err = edge(q.Since, q.After); err != nil {
		return readOpts{}, err
	}
	if o.until, err = edge(q.Until, q.Before); err != nil {
		return readOpts{}, err
	}
	if view.Grep != "" {
		// The tool refused a pattern that does not compile, so one here is a plain refusal.
		if o.grep, err = regexp.Compile(view.Grep); err != nil {
			return readOpts{}, &Refusal{"the grep is not a regular expression"}
		}
	}
	return o, nil
}

// edge is the bound one end of a range reads with: the cursor kept keeping its
// line, the cursor dropped dropping it, the second winning when both are set, and
// zero with neither.
func edge(kept, dropped *string) (bound, error) {
	cursor, incl := dropped, false
	if cursor == nil {
		cursor, incl = kept, true
	}
	if cursor == nil {
		return bound{}, nil
	}
	p, err := parseCursor(*cursor)
	return bound{p, incl}, err
}

// Follow is view's new lines on cluster id as they are written: after the cursor,
// or from the moment it started with none. Each line goes out as its container
// writes it, so lines are ordered per container and across containers by arrival.
// A view with no container yet — a Job not scheduled, a Deployment at zero — waits
// for its pods.
//
// A follow that ends for a reason the user can read — the claim revoked, the
// cluster refusing the user's credentials, every container of the view stopped,
// every source of the view a Pod whose pod was deleted — sends one Ended frame
// saying so and ends clean, since following again would end the same way. Any
// other ending sets Err, and its context ending is a clean end with no frame.
func (s *Service) Follow(ctx context.Context, id cluster.ClusterID, view View, after *string) (*cluster.Stream[FollowFrame], error) {
	o, err := readOptsOf(view, FetchQuery{After: after})
	if err != nil {
		return nil, err
	}
	o.follow = true
	t, err := resolve(ctx, s.clusters, id, view)
	if err == nil {
		err = t.tooMany()
	}
	if err != nil {
		return nil, err
	}
	c, err := openClaim(ctx, s.clusters, id, t.serverUID, s.retry)
	if err != nil {
		return nil, err
	}
	// owned ends the mirror watches and every reading's relay on any ending. The
	// stream itself runs on ctx: one ended by its own context files no error.
	owned, stop := context.WithCancel(ctx)
	f := &follow{
		s: s, id: id, view: view, o: o, claim: c, target: t, start: s.now(),
		fan: newFanIn(), containers: map[container]*tracked{}, nodes: map[string]RecordSource{},
		deleted: map[string]string{}, gone: map[string]bool{}, due: make(chan container),
	}
	if err := f.watch(owned, t.cacheID); err != nil {
		stop()
		c.close()
		return nil, err
	}
	return cluster.NewStream(ctx, func(ctx context.Context, out chan<- FollowFrame) error {
		defer c.close()
		defer stop()
		err := f.run(owned, out)
		// A cancel ends the watches too, which can read as an ending of their own.
		if ctx.Err() != nil {
			return nil
		}
		var refusal *Refusal
		if errors.As(err, &refusal) {
			send(ctx, out, FollowFrame{Type: FollowEnded, Reason: refusal.Reason})
			return nil
		}
		return err
	}), nil
}

// watches is what a follow watches of the mirror besides its streams, and the
// cancel that ends both.
type watches struct {
	pods, nodes *cluster.Stream[cluster.ClusterCachedDataObjectWatchFrame]
	stop        context.CancelFunc
}

// watch opens the follow's mirror watches on cacheID. A node's labels moving sends
// no pod frame, so the nodes are watched too. Only the view's namespaces' pods can
// move it, and nothing reads a pod's body.
func (f *follow) watch(ctx context.Context, cacheID cluster.ClusterCacheID) error {
	ctx, stop := context.WithCancel(ctx)
	data := f.s.clusters.CachedData()
	pods, err := data.WatchObjectsIn(ctx, f.id, cacheID, "v1", "pods", f.view.namespaces())
	var nodes *cluster.Stream[cluster.ClusterCachedDataObjectWatchFrame]
	if err == nil {
		nodes, err = data.WatchObjects(ctx, f.id, cacheID, "v1", "nodes")
	}
	if err != nil {
		stop()
		return err
	}
	f.w = &watches{pods, nodes, stop}
	return nil
}

// watchEnded takes the end of a mirror watch: its error, or, for a clean end — the
// cache cleared or torn down — both watches closed, to be opened again by the
// reconcile after the retry, whose resolve says what became of the cache.
func (f *follow) watchEnded(w *cluster.Stream[cluster.ClusterCachedDataObjectWatchFrame]) error {
	if err := w.Err(); err != nil {
		return err
	}
	f.w.stop()
	f.w, f.synced, f.nodesSynced, f.nodes = nil, false, false, map[string]RecordSource{}
	f.askAgain()
	return nil
}

// follow is one Follow's state. Its streams are reconciled against the view after
// every event — a pod frame, a node's labels moving, a stream's end, a retry — by
// one rule: a container the view wants runs unless it ended at the generation the
// view sees now.
type follow struct {
	s      *Service
	id     cluster.ClusterID
	view   View
	o      readOpts
	claim  *claim
	target target
	start  time.Time
	fan    *fanIn
	// w is the follow's mirror watches, nil while they are to be opened again.
	w *watches
	// containers is what the follow knows of each container it has read.
	containers map[container]*tracked
	handoff    handoff
	// again fires when a resolve the mirror refused is due to be asked again, or
	// the mirror watches opened again; settled fires once the pod frames a resolve
	// waits on have settled.
	again, settled <-chan time.Time
	// due carries each container whose dropped stream has waited out its backoff.
	due chan container
	// synced is whether the pods watch's snapshot has closed, and nodesSynced the
	// nodes watch's.
	synced, nodesSynced bool
	// nodes is each node's labels a line carries, as the nodes watch last sent them.
	nodes map[string]RecordSource
	// deleted is each Pod source's pod the pods watch deleted, by "<ns>/<name>",
	// with its UID, for the next resolve to judge: gone unless the mirror holds that
	// pod again, as a cache's clear and relist leave it.
	deleted map[string]string
	// gone is each Pod source's pod that was deleted, by "<ns>/<name>". Its streams
	// leave the view, and a pod of the same name later is another pod.
	gone map[string]bool
}

// container is a stream's identity. A RecordSource also carries its node's
// labels, which can change under a running stream, so the follow keys on this.
type container struct {
	namespace, pod, name string
	previous             bool
}

func containerOf(src RecordSource) container {
	return container{src.Namespace, src.Pod, src.Container, src.Previous}
}

// tracked is one container as the follow knows it: the stream running on it,
// the generation its last stream ended at, when one ended on its own, with the
// cluster's refusal when that is how it ended, and the last line it sent, which a
// stream reopened resumes past. A container whose stream dropped has neither a
// stream nor an end: it is held for its backoff, then the next reconcile opens it.
// drops is how many of its streams dropped in a row.
type tracked struct {
	stream  *opened
	ended   bool
	endedAt generation
	refused string
	last    position
	held    bool
	drops   int
}

// opened is a running stream: what it was opened for, when, its reading and its
// cancel.
type opened struct {
	src     RecordSource
	gen     generation
	at      time.Time
	reading *reading
	cancel  context.CancelFunc
}

// run opens the target's streams from the follow's since, then relays their lines
// and follows the view's pods until an ending.
func (f *follow) run(ctx context.Context, out chan<- FollowFrame) error {
	defer func() {
		for _, t := range f.containers {
			if t.stream != nil {
				t.stream.cancel()
			}
		}
	}()
	for _, st := range f.target.streams {
		f.open(ctx, st, f.since())
	}
	window := time.NewTimer(f.s.handoff)
	defer window.Stop()
	err := f.relay(ctx, out, window.C)
	// An ending still delivers the lines the handoff holds; a cancel does not.
	if err != nil && !f.release(ctx, out) {
		return nil
	}
	return err
}

// relay sends the follow's lines, holding the first until window fires, and
// reconciles its streams on every event until an ending, which it answers.
func (f *follow) relay(ctx context.Context, out chan<- FollowFrame, window <-chan time.Time) error {
	for {
		var again bool
		var err error
		// A nil channel never delivers, so no frame is read while the watches are closed.
		var pods, nodes <-chan cluster.ClusterCachedDataObjectWatchFrame
		if f.w != nil {
			pods, nodes = f.w.pods.Frames, f.w.nodes.Frames
		}
		select {
		case <-ctx.Done():
			return nil
		case <-f.claim.revoked:
			return ErrRevoked
		case h := <-f.fan.lines:
			if !f.accept(h) {
				continue
			}
			if !f.handoff.hold(h.rec) && !sendLine(ctx, out, h.rec) {
				return nil
			}
		case <-window:
			if !f.release(ctx, out) {
				return nil
			}
		case <-f.again:
			again = true
		case <-f.settled:
			again = true
		case r := <-f.fan.ended:
			again, err = f.ended(ctx, r)
		case c := <-f.due:
			if t := f.containers[c]; t != nil {
				t.held = false
			}
			again = true
		case frame, ok := <-pods:
			switch {
			case !ok:
				err = f.watchEnded(f.w.pods)
			case f.podsMoved(frame, pods):
				again = f.settle()
			}
		case frame, ok := <-nodes:
			if !ok {
				err = f.watchEnded(f.w.nodes)
			} else {
				again = f.nodesMoved(frame, nodes)
			}
		}
		if err != nil {
			return err
		}
		if again {
			if err := f.reconcile(ctx); err != nil {
				return err
			}
		}
	}
}

// askAgain arms the retry, unless it is armed already.
func (f *follow) askAgain() {
	if f.again == nil {
		f.again = time.After(f.s.retry)
	}
}

// settle is whether a pod frame's resolve runs now: with no settle it does, and
// otherwise it waits for the frames after it, the first frame arming the wait.
func (f *follow) settle() bool {
	if f.s.settle == 0 {
		return true
	}
	if f.settled == nil {
		f.settled = time.After(f.s.settle)
	}
	return false
}

// release sends the lines the handoff holds, and is false once ctx has ended.
func (f *follow) release(ctx context.Context, out chan<- FollowFrame) bool {
	for _, rec := range f.handoff.release() {
		if !sendLine(ctx, out, rec) {
			return false
		}
	}
	return true
}

// sendLine sends rec as a line, and is false once ctx has ended.
func sendLine(ctx context.Context, out chan<- FollowFrame, rec Record) bool {
	return send(ctx, out, FollowFrame{Type: FollowLine, Record: &rec})
}

// track is what the follow knows of src's container, new when nothing yet.
func (f *follow) track(src RecordSource) *tracked {
	c := containerOf(src)
	t := f.containers[c]
	if t == nil {
		t = &tracked{}
		f.containers[c] = t
	}
	return t
}

// since is where a container that has sent nothing starts: the follow's cursor,
// else the moment the follow started.
func (f *follow) since() bound {
	if f.o.since.at.isZero() {
		return bound{position{Timestamp: f.start}, true}
	}
	return f.o.since
}

// open follows st from since, its lines joining the follow's.
func (f *follow) open(ctx context.Context, st stream, since bound) {
	o := f.o
	o.since = since
	readCtx, cancel := context.WithCancel(ctx)
	r := readForward(readCtx, f.claim.get, st.src, o)
	t := f.track(st.src)
	t.stream, t.ended = &opened{st.src, st.gen, f.s.now(), r, cancel}, false
	f.fan.add(ctx, r)
}

// accept is whether h's line goes out: only from its container's running stream,
// whose last line it becomes, and which a line shows is not dropping. A line from
// a stream the follow replaced is dropped, since the replacement resumes past the
// last line accepted and reads it again.
func (f *follow) accept(h headed) bool {
	if !f.current(h.from) {
		return false
	}
	t := f.track(h.rec.Source)
	t.last, t.drops = positionOf(h.rec), 0
	return true
}

// current is whether r is its container's running stream, not one the follow
// replaced or ended.
func (f *follow) current(r *reading) bool {
	t := f.containers[containerOf(r.src)]
	return t != nil && t.stream != nil && t.stream.reading == r
}

// ended takes a reading's end, and is whether the view is to be resolved again:
// an ending of the follow when it was the cluster refusing the user's credentials
// or Kstack no longer connecting to it. A stream the connection dropped, or the
// server failed, opens again once its backoff passes, and so does a clean end on a
// container the view sees running, since a proxy or the API server can close an
// idle stream. Any other end is the server's answer for this log, and the stream
// reopens once the view sees a later generation of it.
func (f *follow) ended(ctx context.Context, r *reading) (bool, error) {
	if !f.current(r) {
		return false, nil
	}
	c := containerOf(r.src)
	t := f.containers[c]
	t.stream.cancel()
	gen, at := t.stream.gen, t.stream.at
	t.stream = nil
	if f.claim.isRevoked() {
		return true, ErrRevoked
	}
	if err := fatal(r.err); err != nil {
		return true, err
	}
	if dropped(r.err) || r.err == nil && !r.src.Previous && gen.state == stateRunning {
		f.hold(ctx, c, t, f.s.now().Sub(at))
		return false, nil
	}
	t.ended, t.endedAt, t.refused = true, gen, refusalOf(r.err)
	return true, nil
}

// hold keeps the dropped container c closed for its backoff, then sends it on due.
func (f *follow) hold(ctx context.Context, c container, t *tracked, ran time.Duration) {
	wait := f.backoff(t, ran)
	t.held = true
	time.AfterFunc(wait, func() {
		select {
		case f.due <- c:
		case <-ctx.Done():
		}
	})
}

// backoff counts a drop of t's stream, which ran for ran, and is how long t waits:
// the service's retry, doubled for each drop in a row, up to retryCap. A stream
// that ran for retryCap or longer starts the count again, so an idle stream a proxy
// closes now and then reopens after the retry alone.
func (f *follow) backoff(t *tracked, ran time.Duration) time.Duration {
	if ran >= f.s.retryCap {
		t.drops = 0
	}
	wait := min(f.s.retry<<min(t.drops, 16), f.s.retryCap)
	t.drops++
	return wait
}

// A running container's state in the mirror, and one waiting to start.
const (
	stateRunning = "running"
	stateWaiting = "waiting"
)

// refusalOf is the cluster's message when err is it refusing to show the log.
func refusalOf(err error) string {
	var se *statusError
	if errors.As(err, &se) && se.code == http.StatusForbidden {
		return se.Error()
	}
	return ""
}

// dropped is whether err ended a stream for a reason of the connection's or the
// server's, not the log's: anything but a status under 500.
func dropped(err error) bool {
	var se *statusError
	return err != nil && !(errors.As(err, &se) && se.code < http.StatusInternalServerError)
}

// podsMoved takes frame and every frame already waiting behind it, and is whether
// the view is to be resolved again: when the watch's opening snapshot closed among
// them, since the view was resolved before the watch opened, or when one is a pod
// past it, every one of which is in the view's namespaces. A Pod source's pod
// deleted is kept for that resolve to judge.
func (f *follow) podsMoved(frame cluster.ClusterCachedDataObjectWatchFrame, more <-chan cluster.ClusterCachedDataObjectWatchFrame) bool {
	relevant := false
	for {
		switch {
		case frame.Type == cluster.DeltaFrameBookmark:
			f.synced, relevant = true, true
		case f.synced:
			f.noteDeleted(frame)
			relevant = relevant || frame.Object != nil
		}
		select {
		case next, ok := <-more:
			if !ok {
				return relevant
			}
			frame = next
		default:
			return relevant
		}
	}
}

// nodesMoved takes frame and every frame already waiting behind it, and is whether
// the view is to be resolved again: when the watch's opening snapshot closed among
// them, since the view was resolved before the watch opened, or when a node past
// it came, went, or moved one of the labels a line carries. A node's other changes
// are its status, which a follow does not read. With no filter on a label, only a
// node a stream runs on can move the view, and no other is read.
func (f *follow) nodesMoved(frame cluster.ClusterCachedDataObjectWatchFrame, more <-chan cluster.ClusterCachedDataObjectWatchFrame) bool {
	relevant := false
	labels := filtersLabels(f.view.Filters)
	for {
		switch {
		case frame.Type == cluster.DeltaFrameBookmark:
			f.nodesSynced, relevant = true, true
		case frame.Object == nil:
		case !labels && !f.runsOn(frame.Object.Name):
			delete(f.nodes, frame.Object.Name)
		case frame.Type == cluster.DeltaFrameDeleted:
			delete(f.nodes, frame.Object.Name)
			relevant = relevant || f.nodesSynced
		default:
			place, ok := placeOf(string(frame.Object.RawJSON))
			was, known := f.nodes[frame.Object.Name]
			f.nodes[frame.Object.Name] = place
			relevant = relevant || f.nodesSynced && (!ok || !known || was != place)
		}
		select {
		case next, ok := <-more:
			if !ok {
				return relevant
			}
			frame = next
		default:
			return relevant
		}
	}
}

// filtersLabels is whether any of filters reads a node's labels.
func filtersLabels(filters []ViewFilter) bool {
	for _, flt := range filters {
		if flt.Field != FilterNode {
			return true
		}
	}
	return false
}

// runsOn is whether a stream of the follow runs on node.
func (f *follow) runsOn(node string) bool {
	for _, t := range f.containers {
		if t.stream != nil && t.stream.src.Node == node {
			return true
		}
	}
	return false
}

// placeOf is the labels a line carries of the node whose body is raw, as
// resolve reads them, and false for a body that does not parse.
func placeOf(raw string) (RecordSource, bool) {
	var node struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		return RecordSource{}, false
	}
	l := node.Metadata.Labels
	return RecordSource{Region: l[labelRegion], Zone: l[labelZone], OS: l[labelOS], Arch: l[labelArch]}, true
}

// reconcile resolves the view and brings the streams to it: one of a container
// that left the view, or whose source moved, ends; one the view wants opens
// unless it is running, held for its backoff, or ended at the generation the view
// sees, and resumes past the last line its container sent, or, having sent none,
// from the follow's cursor or its start. A view whose every container has ended,
// none of them waiting to start or held, ends the follow; one with no container
// waits for one. A mirror with no cache, or still syncing, or holding no node a
// label filter reads, is asked again after the service's retry, since its
// recovery sends no pod frame. Closed mirror watches open again on the cache the
// view resolved against.
func (f *follow) reconcile(ctx context.Context) error {
	f.again, f.settled = nil, nil
	t, err := resolve(ctx, f.s.clusters, f.id, f.view)
	var refusal *Refusal
	switch {
	case errors.Is(err, errCut):
		return err
	case errors.As(err, &refusal):
		f.askAgain()
		return nil
	case err != nil:
		return err
	case t.serverUID != f.target.serverUID:
		return ErrRevoked
	}
	if err := t.tooMany(); err != nil {
		return err
	}
	if f.w == nil {
		if err := f.watch(ctx, t.cacheID); err != nil {
			return err
		}
	}
	if t, err = f.podsGone(t); err != nil {
		return err
	}
	want := map[container]stream{}
	for _, st := range t.streams {
		want[containerOf(st.src)] = st
	}
	running := 0
	for c, tr := range f.containers {
		if tr.stream == nil {
			continue
		}
		if st, ok := want[c]; !ok || st.src != tr.stream.src {
			tr.stream.cancel()
			tr.stream = nil
			continue
		}
		running++
	}
	waiting := false
	for _, st := range t.streams {
		tr := f.track(st.src)
		if tr.stream != nil {
			continue
		}
		if tr.held {
			waiting = true
			continue
		}
		if tr.ended && tr.endedAt == st.gen {
			// A container waiting to start has a log to come, whatever its last
			// stream answered; a previous instance's never grows.
			waiting = waiting || !st.src.Previous && st.gen.state == stateWaiting
			continue
		}
		since := f.since()
		if !tr.last.isZero() {
			since = bound{tr.last, false}
		}
		f.open(ctx, st, since)
		running++
	}
	if running == 0 && !waiting && len(t.streams) > 0 {
		return f.stopped(t.streams)
	}
	return nil
}

// stopped is the ending of a follow whose every container ended: the cluster's
// refusal when that is how one ended, since following again would end the same way.
func (f *follow) stopped(streams []stream) error {
	for _, st := range streams {
		if tr := f.containers[containerOf(st.src)]; tr.refused != "" {
			return &Refusal{"the cluster refused to show this log: " + tr.refused}
		}
	}
	return errStopped
}

// noteDeleted keeps the pod frame deletes when it is a Pod source's.
func (f *follow) noteDeleted(frame cluster.ClusterCachedDataObjectWatchFrame) {
	if frame.Type != cluster.DeltaFrameDeleted || frame.Object == nil {
		return
	}
	for _, src := range f.view.Sources {
		if src.Kind == SourcePod && src.Namespace == frame.Object.Namespace && src.Name == frame.Object.Name {
			f.deleted[src.Namespace+"/"+src.Name] = frame.Object.UID
			return
		}
	}
}

// podsGone is t less the streams of every Pod source's pod that was deleted: a
// pod deleted that t does not hold again by its UID is gone, and a pod of the same
// name with another UID is another pod. A view whose every source is a pod gone
// ends the follow; any other follows its other sources.
func (f *follow) podsGone(t target) (target, error) {
	for key, uid := range f.deleted {
		if !t.holdsPod(key, uid) {
			f.gone[key] = true
		}
		delete(f.deleted, key)
	}
	if len(f.gone) == 0 {
		return t, nil
	}
	t.streams = slices.DeleteFunc(slices.Clone(t.streams), func(st stream) bool {
		return f.gone[st.src.Namespace+"/"+st.src.Pod]
	})
	var keys []string
	for _, src := range f.view.Sources {
		key := src.Namespace + "/" + src.Name
		if src.Kind != SourcePod || !f.gone[key] {
			return t, nil
		}
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 1 {
		return t, &Refusal{"pod " + keys[0] + " was deleted"}
	}
	return t, &Refusal{"pods " + strings.Join(keys, ", ") + " were deleted"}
}
