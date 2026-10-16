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
	"slices"
	"strings"
	"time"

	"github.com/amorey/gochan/mpsc"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
)

// errStopped ends a follow with no stream left running.
var errStopped = Refusal{"every container of this view has stopped"}

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

// followSettle is how long a follow waits after a pod or workload frame before it
// resolves its view again, so a burst of frames costs one resolve.
const followSettle = 250 * time.Millisecond

// FollowFrame is one frame of a follow: a line, or the follow's end, with the
// reason in the user's words. The end is the last frame.
type FollowFrame struct {
	Type   FollowFrameType
	Record *Record
	Reason string
}

// FollowFrameType is what a follow frame carries. The GraphQL LogsFollowFrameType
// enum binds member by member onto it.
type FollowFrameType string

const (
	FollowLine  FollowFrameType = "line"
	FollowEnded FollowFrameType = "ended"
)

// namespaces is each namespace v's sources read, once: what a follow's pods watch
// is narrowed to.
func (v View) namespaces() []string {
	var out []string
	for _, src := range v.Sources {
		if !slices.Contains(out, src.Namespace) {
			out = append(out, src.Namespace)
		}
	}
	return out
}

// controls is the kind a workload kind's controller makes, which podsOf walks
// through to its pods.
var controls = map[SourceKind]SourceKind{SourceDeployment: SourceReplicaSet, SourceCronJob: SourceJob}

// workloadKinds is each kind v's workload sources walk through to their pods,
// once: a source's own, and the kind it controls.
func (v View) workloadKinds() []mirrorKind {
	var out []mirrorKind
	add := func(kind SourceKind) {
		if m := mirrorKinds[kind]; !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	for _, src := range v.Sources {
		if src.Kind == SourcePod {
			continue
		}
		add(src.Kind)
		if child, ok := controls[src.Kind]; ok {
			add(child)
		}
	}
	return out
}

// Follow is view's new lines on cluster id as they are written: after the cursor,
// or from the moment it started with none. Each line goes out as its container
// writes it, so lines are ordered per container and across containers by arrival,
// and the cursor is one point for every container: one behind it loses what it
// has not sent, which a client reopening a follow covers by asking a few seconds
// earlier and dropping each container's repeats. A view with no container yet — a
// Job not scheduled, a Deployment at zero — waits for its pods.
//
// A follow that ends for a reason the user can read — the claim revoked, the
// cluster refusing the user's credentials, every container of the view stopped,
// every source of the view a Pod whose pod was deleted, the view grown past what
// Kstack reads at once, a kind the cache no longer mirrors — sends one Ended frame
// saying so and ends clean, since following again would end the same way. Any
// other ending sets Err, and its context ending is a clean end with no frame.
func (s *Service) Follow(ctx context.Context, id cluster.ClusterID, view View, after *string) (*cluster.Stream[FollowFrame], error) {
	o, err := readOptsOf(view, FetchQuery{After: after})
	if err != nil {
		return nil, err
	}
	t, err := resolve(ctx, s.clusters, id, view, true)
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
		s: s, id: id, view: view, o: o, claim: c, src: s.source(c.get), target: t, start: s.now(),
		fan: newFanIn(), streams: map[StreamKey]*tracked{}, nodes: map[string]Place{},
		deleted: map[string]string{}, podUIDs: map[string]string{}, gone: map[string]bool{},
		dueHub: mpsc.New[StreamKey](0, mpsc.WithKeepOpen()),
	}
	f.notePods(t)
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
		if refusal, ok := asRefusal(err); ok {
			send(ctx, out, FollowFrame{Type: FollowEnded, Reason: refusal.Reason})
			return nil
		}
		return err
	}), nil
}

// objectWatch is a watch of mirrored objects.
type objectWatch = *cluster.Stream[cluster.ClusterCachedDataObjectWatchFrame]

// watches is what a follow watches of the mirror besides its streams, and the
// cancel that ends them all. workloads carries the watches of the view's workload
// kinds, each a frame at a time.
type watches struct {
	pods, nodes objectWatch
	workloads   *mpsc.Hub[workloadEvent]
	stop        context.CancelFunc
}

// workloadEvent is a frame of the workload watch w past its snapshot, the bookmark
// closing it included, or w's end.
type workloadEvent struct {
	w     objectWatch
	ended bool
}

// watch opens the follow's mirror watches on cacheID. A node's labels moving sends
// no pod frame, so the nodes are watched too, and so is each workload kind the
// view walks through to its pods, since an object of one arriving late or changing
// owner sends none either. Only the view's namespaces' objects can move it, and
// nothing reads a pod's or a workload's body.
func (f *follow) watch(ctx context.Context, cacheID cluster.ClusterCacheID) error {
	ctx, stop := context.WithCancel(ctx)
	data := f.s.clusters.CachedData()
	namespaces := f.view.namespaces()
	w := &watches{workloads: mpsc.New[workloadEvent](0, mpsc.WithKeepOpen()), stop: stop}
	var err error
	w.pods, err = data.WatchObjectsIn(ctx, f.id, cacheID, "v1", "pods", namespaces)
	if err == nil {
		w.nodes, err = data.WatchObjects(ctx, f.id, cacheID, "v1", "nodes")
	}
	for _, kind := range f.view.workloadKinds() {
		if err != nil {
			break
		}
		var kw objectWatch
		kw, err = data.WatchObjectsIn(ctx, f.id, cacheID, kind.apiVersion, kind.resource, namespaces)
		if err == nil {
			w.forward(ctx, kw)
		}
	}
	if err != nil {
		stop()
		return err
	}
	f.w = w
	return nil
}

// forward sends kw's frames past its snapshot on workloads, then its end. It stops
// once ctx ends, which ends kw too.
func (w *watches) forward(ctx context.Context, kw objectWatch) {
	events := w.workloads.Sender()
	go func() {
		defer events.Close()
		synced := false
		for frame := range kw.Frames {
			synced = synced || frame.Type == cluster.DeltaFrameBookmark
			if synced && events.SendContext(ctx, workloadEvent{w: kw}) != nil {
				return
			}
		}
		_ = events.SendContext(ctx, workloadEvent{w: kw, ended: true}) // an error is the watches stopping, which no longer read it
	}()
}

// watchEnded takes the end of a mirror watch: its error, or, for a clean end — the
// cache cleared or torn down — every watch closed, to be opened again by the
// reconcile after the retry, whose resolve says what became of the cache.
func (f *follow) watchEnded(w objectWatch) error {
	if err := w.Err(); err != nil {
		return err
	}
	f.w.stop()
	f.w, f.synced, f.nodesSynced, f.nodes = nil, false, false, map[string]Place{}
	f.askAgain()
	return nil
}

// follow is one Follow's state. Its streams are reconciled against the view after
// every event — a pod or workload frame, a node's labels moving, a stream's end, a
// retry — by one rule: a container the view wants runs unless it ended at the
// generation the view sees now.
type follow struct {
	s     *Service
	id    cluster.ClusterID
	view  View
	o     readOpts
	claim *claim
	// src tails the follow's streams over the claim.
	src    Source
	target target
	start  time.Time
	fan    *fanIn
	// w is the follow's mirror watches, nil while they are to be opened again.
	w *watches
	// streams is what the follow knows of each stream it has read.
	streams map[StreamKey]*tracked
	handoff handoff
	// again fires when a resolve the mirror refused is due to be asked again, or
	// the mirror watches opened again; settled fires once the frames a resolve
	// waits on have settled.
	again, settled <-chan time.Time
	// dueHub carries each stream that dropped and has waited out its backoff, from a
	// timer of its own.
	dueHub *mpsc.Hub[StreamKey]
	// synced is whether the pods watch's snapshot has closed, and nodesSynced the
	// nodes watch's.
	synced, nodesSynced bool
	// nodes is each node's place a line carries, as the nodes watch last sent it.
	nodes map[string]Place
	// deleted is each Pod source's pod the pods watch deleted, by "<ns>/<name>",
	// with its UID, for the next resolve to judge: gone unless the mirror holds that
	// pod again, as a cache's clear and relist leave it.
	deleted map[string]string
	// podUIDs is the UID of each Pod source's pod as a resolve first read it, by
	// "<ns>/<name>", so a pod of its name with another UID, or none in a snapshot,
	// reads as gone even when no Deleted frame came, as across a cache's clear.
	podUIDs map[string]string
	// snapshot is whether a pods watch's snapshot has closed since a resolve last
	// judged the pods: only then is a pod the mirror lacks known to be gone, not
	// yet to be listed again.
	snapshot bool
	// gone is each Pod source's pod that is gone, by "<ns>/<name>". Its source
	// leaves the view, and a pod of the same name later is another pod, which only
	// another source can read.
	gone map[string]bool
}

// tracked is one stream as the follow knows it: the read running on it, the
// generation its last read ended at, when one ended on its own, with the cluster's
// refusal when that is how it ended, and the last line it sent, which a read
// reopened resumes past. A stream whose read dropped has neither a read nor an
// end: it is held for its backoff, then the next reconcile opens it. drops is how
// many of its reads dropped in a row.
type tracked struct {
	stream  *opened
	ended   bool
	endedAt generation
	refused string
	last    position
	held    bool
	drops   int
}

// opened is a running read: the stream it was opened for, when, and the read.
type opened struct {
	st   stream
	at   time.Time
	read *streamRead
}

// run opens the target's streams from the follow's since, then relays their lines
// and follows the view's pods until an ending.
func (f *follow) run(ctx context.Context, out chan<- FollowFrame) error {
	defer func() {
		for _, t := range f.streams {
			if t.stream != nil {
				t.stream.read.cancel()
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
		var workloads <-chan workloadEvent
		if f.w != nil {
			pods, nodes, workloads = f.w.pods.Frames, f.w.nodes.Frames, f.w.workloads.Receiver().Chan()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-f.claim.revoked:
			return ErrRevoked
		case h := <-f.fan.linesHub.Receiver().Chan():
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
		case r := <-f.fan.endedHub.Receiver().Chan():
			again, err = f.ended(ctx, r)
		case key := <-f.dueHub.Receiver().Chan():
			if t := f.streams[key]; t != nil {
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
		case e := <-workloads:
			if e.ended {
				err = f.watchEnded(e.w)
			} else {
				again = f.settle()
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

// settle is whether a pod or workload frame's resolve runs now: with no settle it
// does, and otherwise it waits for the frames after it, the first frame arming the
// wait.
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

// send puts frame on out, and is false once ctx has ended.
func send(ctx context.Context, out chan<- FollowFrame, frame FollowFrame) bool {
	select {
	case out <- frame:
		return true
	case <-ctx.Done():
		return false
	}
}

// track is what the follow knows of key's stream, new when nothing yet.
func (f *follow) track(key StreamKey) *tracked {
	t := f.streams[key]
	if t == nil {
		t = &tracked{}
		f.streams[key] = t
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
	r := f.src.Tail(ctx, st, o)
	t := f.track(st.key)
	t.stream, t.ended = &opened{st, f.s.now(), r}, false
	f.fan.add(ctx, r)
}

// accept is whether h's line goes out: only from its stream's running read, whose
// last line it becomes, and which a line shows is not dropping. A line from a read
// the follow replaced is dropped, since the replacement resumes past the last line
// accepted and reads it again.
func (f *follow) accept(h headed) bool {
	if !f.current(h.from) {
		return false
	}
	t := f.track(h.rec.Stream)
	t.last, t.drops = positionOf(h.rec), 0
	return true
}

// current is whether r is its stream's running read, not one the follow replaced
// or ended.
func (f *follow) current(r *streamRead) bool {
	t := f.streams[r.key]
	return t != nil && t.stream != nil && t.stream.read == r
}

// ended takes a reading's end, and is whether the view is to be resolved again:
// an ending of the follow when it was the cluster refusing the user's credentials
// or Kstack no longer connecting to it. A stream the connection dropped, or the
// server failed, opens again once its backoff passes, and so does a clean end on a
// container the view sees running, since a proxy or the API server can close an
// idle stream. Any other end is the server's answer for this log, and the stream
// reopens once the view sees a later generation of it.
func (f *follow) ended(ctx context.Context, r *streamRead) (bool, error) {
	if !f.current(r) {
		return false, nil
	}
	t := f.streams[r.key]
	r.cancel()
	gen, at := t.stream.st.gen, t.stream.at
	t.stream = nil
	if f.claim.isRevoked() {
		return true, ErrRevoked
	}
	kind := failureKind(r.err)
	switch {
	case kind == failFatal:
		return true, fatalError(r.err)
	case kind == failTransient, kind == failNone && !r.key.Previous && gen.state == stateRunning:
		f.hold(ctx, r.key, t, f.s.now().Sub(at))
		return false, nil
	}
	refused := ""
	if kind == failDenied {
		refused = r.err.Error()
	}
	t.ended, t.endedAt, t.refused = true, gen, refused
	return true, nil
}

// hold keeps the dropped stream key closed for its backoff, then sends it on dueHub.
func (f *follow) hold(ctx context.Context, key StreamKey, t *tracked, ran time.Duration) {
	wait := f.backoff(t, ran)
	t.held = true
	due := f.dueHub.Sender()
	time.AfterFunc(wait, func() {
		defer due.Close()
		_ = due.SendContext(ctx, key) // an error is the follow ending, which no longer reads it
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
			f.synced, f.snapshot, relevant = true, true, true
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
			place, ok := placeOf(frame.Object.Name, string(frame.Object.RawJSON))
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
	for _, t := range f.streams {
		if t.stream != nil && t.stream.st.place.Node == node {
			return true
		}
	}
	return false
}

// placeOf is the place of node name, whose body is raw, as resolve reads it, and
// false for a body that does not parse.
func placeOf(name, raw string) (Place, bool) {
	var node struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		return Place{}, false
	}
	l := node.Metadata.Labels
	return Place{Node: name, Region: l[labelRegion], Zone: l[labelZone], OS: l[labelOS], Arch: l[labelArch]}, true
}

// reconcile resolves the view and brings the streams to it: the read of a stream
// that left the view, or whose place moved, ends; a stream the view wants opens
// unless it is running, held for its backoff, or ended at the generation the view
// sees, and resumes past the last line it sent, or, having sent none, from the
// follow's cursor or its start. A view whose every stream has ended, none of them
// waiting to start or held and no container of it starting, ends the follow; one
// with no stream waits for one. A mirror with no cache, or still syncing, or
// holding no node a label filter reads, is asked again after the service's retry,
// since its recovery sends no pod frame. Closed mirror watches open again on the
// cache the view resolved against.
func (f *follow) reconcile(ctx context.Context) error {
	f.again, f.settled = nil, nil
	t, ok, err := f.resolve(ctx)
	if !ok || err != nil {
		return err
	}
	if f.w == nil {
		if err := f.watch(ctx, t.cacheID); err != nil {
			return err
		}
	}
	want := map[StreamKey]stream{}
	for _, st := range t.streams {
		want[st.key] = st
	}
	running := 0
	for key, tr := range f.streams {
		if tr.stream == nil {
			continue
		}
		if st, ok := want[key]; !ok || st.place != tr.stream.st.place {
			tr.stream.read.cancel()
			tr.stream = nil
			continue
		}
		running++
	}
	waiting := t.starting
	for _, st := range t.streams {
		tr := f.track(st.key)
		if tr.stream != nil {
			continue
		}
		if tr.held {
			waiting = true
			continue
		}
		if tr.ended && tr.endedAt == st.gen {
			// A container waiting to start has a log to come, whatever its last
			// read answered; a previous instance's never grows.
			waiting = waiting || !st.key.Previous && st.gen.state == stateWaiting
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

// stopped is the ending of a follow whose every stream ended: the cluster's
// refusal when that is how one ended, since following again would end the same way.
func (f *follow) stopped(streams []stream) error {
	for _, st := range streams {
		if tr := f.streams[st.key]; tr.refused != "" {
			return Refusal{"the cluster refused to show this log: " + tr.refused}
		}
	}
	return errStopped
}

// noteDeleted keeps the pod frame deletes when it is a live Pod source's.
func (f *follow) noteDeleted(frame cluster.ClusterCachedDataObjectWatchFrame) {
	if frame.Type != cluster.DeltaFrameDeleted || frame.Object == nil {
		return
	}
	for _, src := range f.view.Sources {
		key := src.Namespace + "/" + src.Name
		if src.Kind == SourcePod && !f.gone[key] && src.Namespace == frame.Object.Namespace && src.Name == frame.Object.Name {
			f.deleted[key] = frame.Object.UID
			return
		}
	}
}

// resolve resolves the view less each Pod source whose pod is gone, and is false
// when the mirror is to be asked again. A resolve that finds a pod newly gone is
// run again without its source, since another source may read a pod of its name.
func (f *follow) resolve(ctx context.Context) (target, bool, error) {
	for {
		t, err := resolve(ctx, f.s.clusters, f.id, f.live(), true)
		var noNodes NoNodes
		switch {
		case errors.Is(err, ErrNoCache), errors.Is(err, ErrSyncing), errors.As(err, &noNodes):
			f.askAgain()
			return target{}, false, nil
		case err != nil:
			return target{}, false, err
		case t.serverUID != f.target.serverUID:
			return target{}, false, ErrRevoked
		}
		gone, err := f.podsGone(t)
		if err != nil || !gone {
			f.snapshot = false
			return t, err == nil, err
		}
	}
}

// live is the view less each Pod source whose pod is gone.
func (f *follow) live() View {
	v := f.view
	v.Sources = slices.DeleteFunc(slices.Clone(v.Sources), func(src ViewSource) bool {
		return src.Kind == SourcePod && f.gone[src.Namespace+"/"+src.Name]
	})
	return v
}

// notePods keeps the UID of each Pod source's pod t holds, unless one is kept.
func (f *follow) notePods(t target) {
	for key, uid := range t.pods {
		if f.podUIDs[key] == "" {
			f.podUIDs[key] = uid
		}
	}
}

// podsGone is whether t, resolved over the live view, shows a Pod source's pod
// newly gone: one deleted that t does not hold again by its UID, one whose name t
// holds under another UID than the first, or, after a snapshot, one t no longer
// holds. A view whose every source is a pod gone ends the follow; any other
// follows its other sources.
func (f *follow) podsGone(t target) (bool, error) {
	newly := false
	for _, src := range f.view.Sources {
		key := src.Namespace + "/" + src.Name
		if src.Kind != SourcePod || f.gone[key] {
			continue
		}
		uid, deleted := f.deleted[key]
		delete(f.deleted, key)
		held, first := t.pods[key], f.podUIDs[key]
		switch {
		case deleted && (held == "" || held != uid),
			first != "" && held != "" && held != first,
			first != "" && held == "" && f.snapshot:
			f.gone[key], newly = true, true
		}
	}
	f.notePods(t)
	var keys []string
	for _, src := range f.view.Sources {
		key := src.Namespace + "/" + src.Name
		if src.Kind != SourcePod || !f.gone[key] {
			return newly, nil
		}
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 1 {
		return true, Refusal{"pod " + keys[0] + " was deleted"}
	}
	return true, Refusal{"pods " + strings.Join(keys, ", ") + " were deleted"}
}

// asRefusal is the Refusal err is, bare or inside Unmirrored or NoNodes.
func asRefusal(err error) (Refusal, bool) {
	var refusal Refusal
	var unmirrored Unmirrored
	var noNodes NoNodes
	switch {
	case errors.As(err, &refusal):
		return refusal, true
	case errors.As(err, &unmirrored):
		return unmirrored.Refusal, true
	case errors.As(err, &noNodes):
		return noNodes.Refusal, true
	}
	return Refusal{}, false
}
