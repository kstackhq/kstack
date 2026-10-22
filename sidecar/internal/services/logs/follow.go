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
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/amorey/gobus/conflate"
	"github.com/amorey/gochan/mpsc"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// errStopped ends a follow with no stream left running.
var errStopped = refuse(CodeFinished, "every container of this view has stopped")

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

// FollowFrame is one frame of a follow: a line; a checkpoint, whose cursor a follow
// opened after misses no line; or the follow's end, with its code and the reason in
// the user's words. The end is the last frame.
type FollowFrame struct {
	Type   FollowFrameType
	Record *Record
	Cursor *string
	Code   Code
	Reason string
}

// FollowFrameType is what a follow frame carries. The GraphQL LogsFollowFrameType
// enum binds member by member onto it.
type FollowFrameType string

const (
	FollowLine       FollowFrameType = "line"
	FollowCheckpoint FollowFrameType = "checkpoint"
	FollowEnded      FollowFrameType = "ended"
)

// followBatch and followBatchBytes are the most lines the follow's walks read, and
// the most bytes of lines they keep, across every stream, before it sends them and
// walks on.
const (
	followBatch      = 1000
	followBatchBytes = 4 << 20
)

// namespaces is each namespace v's sources read, once: what a follow's pods watch
// is narrowed to.
func (v Scope) namespaces() []string {
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
func (v Scope) workloadKinds() []mirrorKind {
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
// or from the moment it started with none. The lines are read from the cluster
// cache's log file, which a tailer per stream writes, shared by every follow of it:
// each stream is walked from a watermark on every commit to it, and its lines go
// out in line order, so lines are ordered per container. After each walk that
// sent a line a Checkpoint names the oldest watermark across the view, and a follow
// opened after it misses no line. A view with no container yet — a Job not
// scheduled, a Deployment at zero — waits for its pods.
//
// A follow that ends for a reason the user can read — the claim revoked, the
// cluster refusing the user's credentials, every container of the view stopped,
// every source of the view a Pod whose pod was deleted, the view grown past what
// Kstack reads at once, a kind the cache no longer mirrors — sends one Ended frame
// saying so and ends clean, since following again would end the same way. Any
// other ending sets Err, the log file closing under it included, and its context
// ending is a clean end with no frame.
func (s *Service) Follow(ctx context.Context, id cluster.ClusterID, scope Scope, after *string) (*cluster.Stream[FollowFrame], error) {
	since, err := boundAfter(after)
	if err != nil {
		return nil, err
	}
	grep, err := compileGrep(scope.Grep)
	if err != nil {
		return nil, err
	}
	t, err := resolve(ctx, s.clusters, id, scope, true)
	if err != nil {
		return nil, err
	}
	c, err := openClaim(ctx, s.clusters, id, t.serverUID, s.retry)
	if err != nil {
		return nil, err
	}
	file, err := s.files.OpenOrCreate(int64(t.cacheID))
	if err != nil {
		c.close()
		return nil, err
	}
	if since.at.isZero() {
		since = bound{momentAt(s.now()), true}
	}
	// owned ends the mirror watches and every tailer's watch on any ending. The
	// stream itself runs on ctx: one ended by its own context files no error.
	owned, stop := context.WithCancel(ctx)
	f := &follow{
		s: s, id: id, scope: scope, since: since, grep: grep, claim: c, file: file, tails: s.registryFor(id, t), target: t,
		streams: map[StreamKey]*tracked{}, nodes: map[string]Place{},
		deleted: map[string]string{}, podUIDs: map[string]string{}, gone: map[string]bool{}, held: map[int64]func(){},
		dueHub: mpsc.New[StreamKey](0, mpsc.WithKeepOpen()), endedHub: mpsc.New[*handle](0, mpsc.WithKeepOpen()),
		filled: make(chan fillDone),
	}
	for _, b := range s.backends {
		f.backends, f.names = append(f.backends, b.build(c.get)), append(f.names, b.name)
	}
	f.notePods(t)
	if err := f.watch(owned, t.cacheID); err != nil {
		stop()
		file.Close()
		c.close()
		return nil, err
	}
	return cluster.NewStream(ctx, func(ctx context.Context, out chan<- FollowFrame) error {
		defer c.close()
		defer file.Close()
		defer f.letGo()
		// A fill in flight reads over the claim and writes the file: both outlive it.
		defer f.fills.Wait()
		defer stop()
		f.out = out
		err := f.run(owned)
		// A cancel ends the watches too, which can read as an ending of their own.
		if ctx.Err() != nil {
			return nil
		}
		if refusal, ok := AsRefusal(err); ok {
			send(ctx, out, FollowFrame{Type: FollowEnded, Code: refusal.Code, Reason: refusal.Reason})
			return nil
		}
		return err
	}), nil
}

// letGo releases every row the follow holds.
func (f *follow) letGo() {
	for _, release := range f.held {
		release()
	}
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
	namespaces := f.scope.namespaces()
	w := &watches{workloads: mpsc.New[workloadEvent](0, mpsc.WithKeepOpen()), stop: stop}
	var err error
	w.pods, err = data.WatchObjectsIn(ctx, f.id, cacheID, "v1", "pods", namespaces)
	if err == nil {
		w.nodes, err = data.WatchObjects(ctx, f.id, cacheID, "v1", "nodes")
	}
	for _, kind := range f.scope.workloadKinds() {
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
// every event — a pod or workload frame, a node's labels moving, a tailer's end, a
// retry — by one rule: a container the view wants is tailed unless it ended at the
// generation the view sees now.
type follow struct {
	s     *Service
	id    cluster.ClusterID
	scope Scope
	grep  *regexp.Regexp
	claim *claim
	// since is where a stream that joins starts before any checkpoint: the follow's
	// cursor, or the moment it started.
	since bound
	// file is the follow's claim on the cache's log file, tails its tailers, and
	// backends and names the cluster's backends over the follow's claim, which fill a
	// live gap a walk meets.
	file     *logstore.Store
	tails    *registry
	backends []Backend
	names    []string
	target   target
	out      chan<- FollowFrame
	// w is the follow's mirror watches, nil while they are to be opened again.
	w *watches
	// streams is what the follow knows of each stream it has wanted, and wanted the
	// streams the view wants now, in its order.
	streams map[StreamKey]*tracked
	wanted  []StreamKey
	// pings is the file's bus under pingRows, the rows of the streams the view wants.
	pings    *conflate.Receiver[int64, struct{}]
	pingRows []int64
	// held is the release of each row the follow has filed, which it holds until it
	// ends so no sweep takes a row it may fill.
	held map[int64]func()
	// batch is the lines walked and not yet sent, and checkpoint the last checkpoint
	// sent, zero before the first.
	batch      []Record
	checkpoint bound
	// lines and bytes are what the walks may still read and keep before the next send.
	lines, bytes int
	// again fires when a resolve the mirror refused is due to be asked again, or
	// the mirror watches opened again; settled fires once the frames a resolve
	// waits on have settled. rejudge is set when a stream ended at its generation,
	// so the view's end is judged again.
	again, settled <-chan time.Time
	rejudge        bool
	// dueHub carries each stream that dropped and has waited out its backoff, from a
	// timer of its own, and endedHub each tailer that ended.
	dueHub   *mpsc.Hub[StreamKey]
	endedHub *mpsc.Hub[*handle]
	// filled carries each gap fill's end from its goroutine, and fills counts the
	// fills in flight.
	filled chan fillDone
	fills  sync.WaitGroup
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

// tracked is one stream as the follow knows it: the stream as the view last wanted
// it, its file row, the tailer it holds, nil while it holds none, since when, and
// unwatch, which stops telling the follow of that tailer's end, and its watermark,
// past every line it sent of the row. A stream that ended on its own has no tailer,
// and records the generation it ended at, with the cluster's refusal when that is how
// it ended. A stream whose tailer dropped is held for its backoff, then the next
// reconcile attaches it again. drops is how many of its tailers dropped in a row.
// restarted is set once the view sees a later instance of the container than the
// one the tailer reads, whose end is then its log's, however the old instance stood.
// filling is set while a fill of its row's live gap is in flight, which no walk of
// it waits on: its watermark stays at the gap until the fill ends. crossed and
// release are what the walk after a fill takes: that the gap at its watermark was
// read, and the hold on what the fill wrote. looked is set once the follow has sent
// a line of the stream, the first looking back for its holes, and gap is the holes
// its walks crossed since the last line it sent, which the next carries.
type tracked struct {
	st        stream
	row       int64
	h         *handle
	attached  time.Time
	unwatch   context.CancelFunc
	wm        logstore.Pos
	ended     bool
	endedAt   generation
	refused   string
	held      bool
	drops     int
	restarted bool
	filling   bool
	crossed   bool
	release   func()
	looked    bool
	gap       *Gap
}

// tail is where tr's tailer reads on from, every gap past which is the tailer's;
// nil with no tailer.
func (tr *tracked) tail() *logstore.Pos {
	if tr.h == nil {
		return nil
	}
	start := tr.h.Start()
	return &start
}

// committed is how far tr's tailer has written into the file, its start at least;
// nil with no tailer, which leaves no gap tailed.
func (tr *tracked) committed() *logstore.Pos {
	if tr.h == nil {
		return nil
	}
	c := tr.h.Committed()
	return &c
}

// pageStream is tr's stream as a page's fill reads it.
func (tr *tracked) pageStream() pageStream {
	key := tr.st.key
	return pageStream{
		row: logstore.Stream{ID: tr.row, Namespace: key.Namespace, Pod: key.Pod, Container: key.Container, ContainerID: tr.st.gen.instance},
		st:  &tr.st, key: key, place: tr.st.place,
	}
}

// record is l, a line of tr's stream, as the follow sends it.
func (tr *tracked) record(l logstore.Line) Record {
	return recordOf(l, tr.st.key, tr.st.place, tr.st.gen.instance)
}

// run brings the target's streams up, then follows the view's pods and walks its
// streams on every commit until an ending.
func (f *follow) run(ctx context.Context) error {
	defer func() {
		for _, tr := range f.streams {
			f.detach(tr)
		}
		if f.pings != nil {
			f.pings.Close()
		}
	}()
	if err := f.bring(ctx, f.target); err != nil {
		return err
	}
	return f.relay(ctx)
}

// relay walks the streams each ping names and reconciles the streams on every event
// until an ending, which it answers.
func (f *follow) relay(ctx context.Context) error {
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
		case ping, ok := <-f.pings.Chan():
			if !ok {
				return fmt.Errorf("follow: %w", logstore.ErrClosed)
			}
			err = f.walkRows(ctx, f.pinged(ping.Key))
		case <-f.again:
			again = true
		case <-f.settled:
			again = true
		case h := <-f.endedHub.Receiver().Chan():
			err = f.ended(ctx, h)
		case d := <-f.filled:
			err = f.filledGap(ctx, d)
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
		if again || f.rejudge {
			if err := f.reconcile(ctx); err != nil {
				return err
			}
		}
	}
}

// pinged is row and every row whose ping is already waiting behind it.
func (f *follow) pinged(row int64) []int64 {
	rows := []int64{row}
	more, _ := f.pings.TryRecvAll() // an error is the bus closed, which the next receive reports
	for _, ping := range more {
		rows = append(rows, ping.Key)
	}
	return rows
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

// cursor is where a stream that joins the view starts: the last checkpoint, else
// the follow's own start.
func (f *follow) cursor() bound {
	if f.checkpoint.at.isZero() {
		return f.since
	}
	return f.checkpoint
}

// attach holds st's tailer for tr, its row's, and tells endedHub once it ends.
func (f *follow) attach(ctx context.Context, tr *tracked) error {
	h, err := f.tails.attach(ctx, tr.st, tr.row, tr.wm)
	if err != nil {
		return err
	}
	ctx, unwatch := context.WithCancel(ctx)
	tr.h, tr.attached, tr.unwatch = h, f.s.now(), unwatch
	ended := f.endedHub.Sender()
	go func() {
		defer ended.Close()
		select {
		case <-h.Done():
			_ = ended.SendContext(ctx, h) // an error is the follow ending, which no longer reads it
		case <-ctx.Done():
		}
	}()
	return nil
}

// detach lets go of tr's tailer, if it holds one.
func (f *follow) detach(tr *tracked) {
	if tr.h != nil {
		tr.unwatch()
		tr.h.Release()
		tr.h, tr.unwatch = nil, nil
	}
}

// ended takes the end of the tailer h: an ending of the follow when it was Kstack
// no longer connecting to the cluster, else the stream walked once more, since the
// tailer's last lines are in the file, and judged by how the tailer ended. A log
// that ended with its instance may have lines a page cached past the tailer's last,
// which the walk under the tailer stops short of: once judged ended, the stream is
// walked to its end as a finished instance is, filling what the file lacks.
func (f *follow) ended(ctx context.Context, h *handle) error {
	var tr *tracked
	for _, t := range f.streams {
		if t.h == h {
			tr = t
		}
	}
	if tr == nil {
		return nil
	}
	if f.claim.isRevoked() {
		return ErrRevoked
	}
	if err := f.walkStreams(ctx, []*tracked{tr}); err != nil {
		return err
	}
	if err := f.failed(ctx, tr, h.Err()); err != nil {
		return err
	}
	if tr.ended && tr.st.finished() {
		return f.walkStreams(ctx, []*tracked{tr})
	}
	return nil
}

// failed takes how tr's stream stopped being read, by its tailer's end or a fill
// that failed, letting its tailer go: an ending of the follow when it was the
// cluster refusing the user's credentials. A stream the connection dropped, or the
// server failed, is held for its backoff, and so is a clean end on a container the
// view sees running, since a proxy or the API server can close an idle stream. Any
// other end is the server's answer for this log: the stream is tailed again once the
// view sees a later generation of it, and the view's end is judged again.
func (f *follow) failed(ctx context.Context, tr *tracked, err error) error {
	ran := time.Duration(0)
	if tr.h != nil {
		ran = f.s.now().Sub(tr.attached)
	}
	f.detach(tr)
	kind := failureKind(err)
	switch {
	case kind == failFatal:
		return fatalError(err)
	case kind == failTransient, kind == failNone && !tr.restarted && !tr.st.key.Previous && tr.st.gen.state == stateRunning:
		f.hold(ctx, tr, ran)
		return nil
	}
	refused := ""
	if kind == failDenied {
		refused = err.Error()
	}
	tr.ended, tr.endedAt, tr.refused = true, tr.st.gen, refused
	f.rejudge = true
	return nil
}

// hold keeps the dropped stream tr, which ran for ran, untailed for its backoff,
// then sends its key on dueHub. A held stream is read again, so it is not ended,
// though its tailer's end judged it so while a fill of it was in flight.
func (f *follow) hold(ctx context.Context, tr *tracked, ran time.Duration) {
	wait := f.backoff(tr, ran)
	tr.held, tr.ended = true, false
	due := f.dueHub.Sender()
	key := tr.st.key // read here: the follow's goroutine owns tr
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
	labels := filtersLabels(f.scope.Filters)
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
func filtersLabels(filters []ScopeFilter) bool {
	for _, flt := range filters {
		if flt.Field != FilterNode {
			return true
		}
	}
	return false
}

// runsOn is whether a stream the follow tails runs on node.
func (f *follow) runsOn(node string) bool {
	for _, t := range f.streams {
		if t.h != nil && t.st.place.Node == node {
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

// reconcile resolves the view and brings the streams to it. A mirror with no cache,
// or still syncing, or holding no node a label filter reads, is asked again after
// the service's retry, since its recovery sends no pod frame. Closed mirror watches
// open again on the cache the view resolved against.
func (f *follow) reconcile(ctx context.Context) error {
	f.again, f.settled, f.rejudge = nil, nil, false
	t, ok, err := f.resolve(ctx)
	if !ok || err != nil {
		return err
	}
	if f.w == nil {
		if err := f.watch(ctx, t.cacheID); err != nil {
			return err
		}
	}
	return f.bring(ctx, t)
}

// bring brings the streams to t: each is filed in the file; a stream that left the
// view lets go of its tailer, and so does one whose pod another of its name on
// another node replaced, while a restarted container's tailer runs on until its log
// ends. A stream the view wants is tailed unless it is tailed already, held for its
// backoff, or ended at the generation the view sees; one that joins starts at the
// follow's cursor. An instance that has finished gets no tailer: its log never grows,
// so it is walked to its end once. Then each stream just tailed is walked, and every
// one when the bus's receiver was made anew for the streams the view wants. A view whose every stream has ended, none of them
// waiting to start or held and no container of it starting, ends the follow; one
// with no stream waits for one.
func (f *follow) bring(ctx context.Context, t target) error {
	rows, err := fileStreams(ctx, f.file, t.streams)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if f.held[row.ID] == nil {
			f.held[row.ID] = f.file.Hold(row.ID)
		}
	}
	want := map[StreamKey]int{}
	for i, st := range t.streams {
		want[st.key] = i
	}
	for key, tr := range f.streams {
		if tr.h == nil {
			continue
		}
		i, ok := want[key]
		switch {
		case !ok:
			f.detach(tr)
		case rows[i].ID == tr.row:
			// The place a line carries follows the node's labels.
			tr.st = t.streams[i]
		case t.streams[i].place != tr.st.place:
			// A pod of its name elsewhere replaced its pod, whose log is gone.
			f.detach(tr)
		default:
			// A restarted container's old tailer runs on until its log ends.
			tr.restarted = true
		}
	}
	ids := rowIDs(rows)
	// A restarted container's old tailer writes its own row until its log ends.
	for _, tr := range f.streams {
		if tr.h != nil && !slices.Contains(ids, tr.row) {
			ids = append(ids, tr.row)
		}
	}
	slices.Sort(ids)
	resubscribed, err := f.subscribe(ids)
	if err != nil {
		return err
	}
	f.wanted = f.wanted[:0]
	var drain []*tracked
	var attached []int64
	for i, st := range t.streams {
		f.wanted = append(f.wanted, st.key)
		tr := f.track(st.key)
		if tr.h != nil || tr.held && tr.row == rows[i].ID || tr.ended && tr.endedAt == st.gen {
			continue
		}
		if tr.row != rows[i].ID {
			// A hold was the instance before's, whose end was its log's.
			tr.row, tr.wm = rows[i].ID, streamBound(f.cursor(), st.run(), newer)
			tr.held, tr.restarted, tr.filling = false, false, false
		}
		tr.st, tr.ended, tr.refused = st, false, ""
		if st.finished() {
			drain = append(drain, tr)
			continue
		}
		if err := f.attach(ctx, tr); err != nil {
			return err
		}
		attached = append(attached, tr.row)
	}
	if err := f.drain(ctx, drain); err != nil {
		return err
	}
	// A commit before a new receiver reached only the last, so every stream is walked
	// then; otherwise only the ones just attached are new.
	walk := attached
	if resubscribed {
		walk = f.pingRows
	}
	if err := f.walkRows(ctx, walk); err != nil {
		return err
	}
	running, waiting := 0, t.starting
	for _, st := range t.streams {
		tr := f.streams[st.key]
		switch {
		case tr.h != nil:
			running++
		case tr.held, tr.filling:
			waiting = true
		case tr.ended && tr.endedAt == st.gen:
			// A container waiting to start has a log to come, whatever its last read
			// answered; a previous instance's never grows.
			waiting = waiting || !st.key.Previous && st.gen.state == stateWaiting
		}
	}
	if running == 0 && !waiting && len(t.streams) > 0 {
		return f.stopped(t.streams)
	}
	return nil
}

// subscribe makes the bus's receiver anew for ids, the rows of the streams the view
// wants and of every tailer the follow holds, sorted, unless it has them already, and
// is whether it did.
func (f *follow) subscribe(ids []int64) (bool, error) {
	if f.pings != nil && slices.Equal(ids, f.pingRows) {
		return false, nil
	}
	pings, err := f.file.Subscribe(ids...)
	if err != nil {
		return false, err
	}
	if f.pings != nil {
		f.pings.Close()
	}
	f.pings, f.pingRows = pings, ids
	return true, nil
}

// drain walks each of streams, finished instances, to its end, filling what the file
// lacks of it, and ends each at its generation unless its walk held it or left a
// fill in flight, whose end finishes the drain.
func (f *follow) drain(ctx context.Context, streams []*tracked) error {
	if err := f.walkStreams(ctx, streams); err != nil {
		return err
	}
	for _, tr := range streams {
		f.drained(tr)
	}
	return nil
}

// drained ends tr, a finished instance walked to its end, at its generation unless
// it is held, ended already, or filling.
func (f *follow) drained(tr *tracked) {
	if !tr.held && !tr.ended && !tr.filling {
		tr.ended, tr.endedAt = true, tr.st.gen
	}
}

// walkRows walks each stream the follow tails whose row is in rows.
func (f *follow) walkRows(ctx context.Context, rows []int64) error {
	var streams []*tracked
	for _, key := range f.wanted {
		if tr := f.streams[key]; tr.h != nil && slices.Contains(rows, tr.row) {
			streams = append(streams, tr)
		}
	}
	return f.walkStreams(ctx, streams)
}

// walkStreams walks each of streams, one at a time, sends what they read in line
// order, then a checkpoint, and walks again at once each that stopped with more to
// read.
func (f *follow) walkStreams(ctx context.Context, streams []*tracked) error {
	for len(streams) > 0 {
		f.lines, f.bytes = followBatch, followBatchBytes
		var again []*tracked
		for _, tr := range streams {
			more, err := f.walk(ctx, tr)
			if err != nil {
				return err
			}
			if more {
				again = append(again, tr)
			}
		}
		if !f.flush(ctx) {
			return nil
		}
		streams = again
	}
	return nil
}

// walk reads tr's stream from its watermark, in line order, as far as reach allows,
// keeping the lines the grep passes for the next send and moving the watermark to
// the last line it read. At a live gap it holds the watermark and starts a fill of
// the gap, then stops: the fill's end walks the stream again. A stream already
// filling is not walked. It answers whether it stopped on the send's allowance of
// lines or bytes with more to read.
func (f *follow) walk(ctx context.Context, tr *tracked) (bool, error) {
	if tr.filling {
		return false, nil
	}
	// A fill that read the gap from the watermark lets the walk cross it, lines or
	// none. Its chunks are held, not stamped, until the walk has read them.
	crossed := tr.crossed
	if tr.release != nil {
		defer tr.release()
	}
	tr.crossed, tr.release = false, nil
	if f.lines <= 0 || f.bytes <= 0 {
		return true, nil
	}
	// Read before the snapshot, so the snapshot holds every line up to it.
	committed := tr.committed()
	var gap *logstore.Gap
	reach, more := tr.wm, false
	err := f.file.Read(ctx, func(r logstore.Reader) error {
		chunks, err := r.Chunks(tr.row, f.scope.Grep)
		if err != nil {
			return err
		}
		cov := logstore.CoverageOf(chunks, f.names, tr.tail())
		reach, gap = f.reach(cov, tr, crossed, committed)
		held := heldBy(chunks)
		upto, first := tr.wm, -1
		err = r.Lines(tr.row, tr.wm, reach, func(l logstore.Line) bool {
			tr.gap, upto = joined(tr.gap, holesBetween(cov.Gaps, committed, upto, l.Pos)), l.Pos
			if held(l.Pos) && matches(f.grep, l.Message) {
				rec := tr.record(l)
				rec.Gap = tr.gap
				if !tr.looked {
					first = len(f.batch)
				}
				tr.gap, tr.looked = nil, true
				f.batch = append(f.batch, rec)
				f.bytes -= len(l.Message)
			}
			if f.lines--; f.lines <= 0 || f.bytes <= 0 {
				reach, more = l.Pos, true
				return false
			}
			return true
		})
		if err != nil {
			return err
		}
		tr.gap = joined(tr.gap, holesBetween(cov.Gaps, committed, upto, reach))
		if first < 0 {
			return nil
		}
		// The follow may start past a hole no page carried: after the last line a
		// page sent, or a checkpoint past the hole. A look its bytes cut keeps what
		// the walk crossed.
		l := looker{
			r: r, row: tr.row, cov: cov, committed: committed, held: held, grep: f.grep,
			bytes: f.s.files.Retention().LookBytes,
		}
		found, err := l.back(lineOf(f.batch[first]).Pos)
		f.batch[first].Gap = joined(f.batch[first].Gap, found)
		return err
	})
	if err != nil {
		return false, err
	}
	tr.wm = logstore.MaxPos(tr.wm, reach)
	if !more && gap != nil {
		f.fill(ctx, tr, *gap)
	}
	return more, nil
}

// reach is how far from tr's watermark a walk may read: along the spans of cov,
// across every gap no fill may be offered, a lost gap or its tailer's, and, once a
// fill read it, the gap the watermark sits in; and the live gap it stops at, nil for
// none. A span past a tailer's gap that the tailer has not committed up to is a
// page's, written ahead of it, and the lines before it are the tailer's, still to
// come: the walk stops at the gap and waits for the tailer's ping. committed is what
// the tailer had written before chunks were read.
func (f *follow) reach(cov logstore.Coverage, tr *tracked, crossed bool, committed *logstore.Pos) (logstore.Pos, *logstore.Gap) {
	reach := tr.wm
	for i, seg := range segmentsFrom(cov, tr.wm, newer) {
		switch {
		case seg.span != nil:
			reach = seg.span.To
		case seg.gap.Live() && !(i == 0 && crossed):
			return reach, seg.gap
		case seg.gap.Tailed && seg.gap.Before != nil && committed.Compare(seg.gap.Before.Prev()) < 0:
			return reach, nil
		}
	}
	return reach, nil
}

// errRowGone is a gap fill whose stream row the janitor took: the stream's pod is
// gone, so the stream waits out its backoff until a reconcile drops it.
var errRowGone = errors.New("logs: the stream's row is gone from the log file")

// fillDone is the end of a gap fill of tr's stream: the row it filled, what it read
// (with whether the janitor took the row first), the hold on what it wrote, and its
// failure, fatal or the file's.
type fillDone struct {
	tr      *tracked
	row     int64
	out     streamFill
	release func()
	err     error
}

// fill starts a read of the live gap g of tr's stream from its watermark, as a newer
// page's fill reads it, under the stream's share of one query's limits, and the write
// of what it read. It runs on its own, so a slow read holds up no other stream, and
// its end comes back on filled. What it writes is unstamped, since a follow's lines
// are in the viewer's window, and held until the walk after it has read them.
func (f *follow) fill(ctx context.Context, tr *tracked, g logstore.Gap) {
	st := tr.st // the fill's own copy: the follow's goroutine owns tr
	ps := tr.pageStream()
	ps.st = &st
	fl := &filler{
		file: f.file, clusters: f.s.clusters, id: f.id, ret: f.s.files.Retention(), ps: ps, backends: f.backends, names: f.names,
		q:      pageQuery{scope: f.scope, grep: f.grep, mode: newer, limit: MaxPageSize},
		limits: followLimits(f.s.caps, len(f.wanted)),
	}
	wm := tr.wm
	tr.filling = true
	f.fills.Add(1)
	go func() {
		defer f.fills.Done()
		d := f.readGap(ctx, fl, g, wm)
		d.tr = tr
		select {
		case f.filled <- d:
		case <-ctx.Done():
			d.release()
		}
	}()
}

// readGap runs fl's read of g from wm, writing what it read, held. On an error it
// releases what it held, and the release it answers is a no-op.
func (f *follow) readGap(ctx context.Context, fl *filler, g logstore.Gap, wm logstore.Pos) fillDone {
	var releases []func()
	fl.save = func(ctx context.Context, fills []logstore.Fill) (logstore.Written, error) {
		w, release, err := f.file.WriteHeld(ctx, fills)
		releases = append(releases, release)
		return w, err
	}
	release := func() {
		for _, r := range releases {
			r()
		}
	}
	d := fillDone{row: fl.ps.row.ID, release: func() {}}
	if _, d.err = fl.fillGap(ctx, g, wm, true); d.err != nil {
		release()
		return d
	}
	d.out, d.release = fl.out, release
	return d
}

// filledGap takes the end of a gap fill. A fill of a row the stream no longer reads
// is thrown away. One that read nothing lets the stream go, held or ended as its
// failure says; a failure fatal or of the file ends the follow. Otherwise the stream
// is walked on across the gap, unless it has left the view or lost its tailer in the
// meantime, when the next attach walks it from its watermark; a finished instance's
// walk to its end finishes its drain.
func (f *follow) filledGap(ctx context.Context, d fillDone) error {
	tr := d.tr
	if tr.row != d.row {
		d.release()
		return nil
	}
	tr.filling = false
	// An untailed stream filling was judged waiting, so the view's end is judged again.
	f.rejudge = f.rejudge || tr.h == nil
	if d.err != nil {
		d.release()
		return d.err
	}
	if d.out.wrote && f.s.afterFill != nil {
		f.s.afterFill()
	}
	switch {
	case d.out.gone:
		// The row went before the follow held it: its pod is gone.
		d.release()
		return f.failed(ctx, tr, errRowGone)
	case !d.out.fromKey:
		d.release()
		cause := d.out.err
		if cause == nil {
			// The connection's retirement cut the read before its first line.
			cause = errRetired
		}
		return f.failed(ctx, tr, cause)
	case !slices.Contains(f.wanted, tr.st.key), tr.h == nil && !tr.st.finished():
		d.release()
		return nil
	}
	tr.crossed, tr.release = true, d.release
	if err := f.walkStreams(ctx, []*tracked{tr}); err != nil {
		return err
	}
	if tr.h == nil {
		f.drained(tr)
	}
	return nil
}

// flush sends the lines walked in line order, then, when it sent any, a
// checkpoint naming the oldest watermark across the view. It is false once the
// follow's context has ended.
func (f *follow) flush(ctx context.Context) bool {
	if len(f.batch) == 0 {
		return true
	}
	slices.SortStableFunc(f.batch, CompareLines)
	for _, rec := range f.batch {
		if !sendLine(ctx, f.out, rec) {
			return false
		}
		f.streams[rec.Stream].drops = 0
	}
	f.batch = f.batch[:0]
	var oldest bound
	for _, key := range f.wanted {
		tr := f.streams[key]
		if b := boundOf(tr.st.run(), tr.wm); oldest.at.isZero() || compareBounds(b, oldest) < 0 {
			oldest = b
		}
	}
	f.checkpoint = oldest
	cursor := cursorOfBound(oldest)
	return send(ctx, f.out, FollowFrame{Type: FollowCheckpoint, Cursor: &cursor})
}

// stopped is the ending of a follow whose every stream ended: the cluster's
// refusal when that is how one ended, since following again would end the same way.
func (f *follow) stopped(streams []stream) error {
	for _, st := range streams {
		if tr := f.streams[st.key]; tr.refused != "" {
			return refuse(CodeUnauthorized, "the cluster refused to show this log: "+tr.refused)
		}
	}
	return errStopped
}

// noteDeleted keeps the pod frame deletes when it is a live Pod source's.
func (f *follow) noteDeleted(frame cluster.ClusterCachedDataObjectWatchFrame) {
	if frame.Type != cluster.DeltaFrameDeleted || frame.Object == nil {
		return
	}
	for _, src := range f.scope.Sources {
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
func (f *follow) live() Scope {
	v := f.scope
	v.Sources = slices.DeleteFunc(slices.Clone(v.Sources), func(src ScopeSource) bool {
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
	for _, src := range f.scope.Sources {
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
	for _, src := range f.scope.Sources {
		key := src.Namespace + "/" + src.Name
		if src.Kind != SourcePod || !f.gone[key] {
			return newly, nil
		}
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 1 {
		return true, refuse(CodeFinished, "pod "+keys[0]+" was deleted")
	}
	return true, refuse(CodeFinished, "pods "+strings.Join(keys, ", ")+" were deleted")
}
