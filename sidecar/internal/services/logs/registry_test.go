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
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// filed is webView's first stream, web-1's app, resolved on svc and filed in its file,
// with its row's id.
func filed(t *testing.T, svc *Service, f *fakeService) (target, stream, int64) {
	t.Helper()
	tg, err := resolve(t.Context(), svc.clusters, "7", webView, true)
	require.NoError(t, err)
	rows, err := fileStreams(t.Context(), fileOf(t, svc, f), tg.streams)
	require.NoError(t, err)
	return tg, tg.streams[0], rows[0].ID
}

// attach attaches to web-1's app's tailer from watermark, failing the test on an
// error; the handle is released when the test ends.
func attach(t *testing.T, svc *Service, f *fakeService, watermark logstore.Pos) *handle {
	t.Helper()
	tg, st, row := filed(t, svc, f)
	h, err := svc.registryFor("7", tg).attach(t.Context(), st, row, watermark)
	require.NoError(t, err)
	t.Cleanup(h.Release)
	return h
}

// rowID is the file row of pod/container's instance-th instance, of a fixture pod,
// once a resolve has filed it.
func rowID(t *testing.T, svc *Service, f *fakeService, pod, container string, instance int) int64 {
	t.Helper()
	var id int64
	require.Eventually(t, func() bool {
		rows, err := fileOf(t, svc, f).PodStreams(t.Context(), "prod", pod, "")
		require.NoError(t, err)
		for _, row := range rows {
			if row.Container == container && row.ContainerID == containerID("uid-"+pod, container, instance) {
				id = row.ID
				return true
			}
		}
		return false
	}, testutil.Timeout, time.Millisecond, "the instance filed")
	return id
}

// web1Since is the watermark at d past 10:00 in web-1's app: every line of that moment.
func web1Since(d time.Duration) logstore.Pos { return logstore.Pos{TS: at(d).UnixNano(), Ord: -1} }

// Two follows of one stream share its tailer: one read of the backend.
func TestTwoFollowsShareOneTailer(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	backend.set("web-1/app", fakeRead{wait: never})
	svc, f := serviceOn(t, backend)

	attach(t, svc, f, web1Since(0))
	attach(t, svc, f, web1Since(0))

	backend.opened.Await(t, "the tailer's read")
	_, again := backend.opened.TryAwait()
	assert.False(t, again, "a second read")
}

// A tailer whose last follow lets go runs on through its grace, and a follow that
// attaches inside it reads nothing new; one that attaches after it starts a tailer
// afresh.
func TestATailerOutlivesItsFollowsByItsGrace(t *testing.T) {
	t.Parallel()
	for name, grace := range map[string]time.Duration{"inside": time.Hour, "after": 0} {
		t.Run(name, func(t *testing.T) {
			backend := newFakeBackend()
			backend.set("web-1/app", fakeRead{wait: never})
			svc, f := serviceOn(t, backend)
			svc.grace = grace
			first := attach(t, svc, f, web1Since(0))
			backend.opened.Await(t, "the first tailer's read")

			first.Release()
			if grace == 0 {
				testutil.Wait(t, first.Done(), "the tailer to end")
			}
			attach(t, svc, f, web1Since(0))

			_, again := backend.opened.TryAwait()
			assert.Equal(t, grace == 0, again, "a read afresh")
		})
	}
}

// A tailer in its grace that ends is dropped, and the next follow asking for its
// stream starts one afresh.
func TestATailerThatEndsInItsGraceIsDropped(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	stop := make(chan struct{})
	backend.set("web-1/app", fakeRead{wait: stop})
	svc, f := serviceOn(t, backend)
	svc.grace = time.Hour
	first := attach(t, svc, f, web1Since(0))
	backend.opened.Await(t, "the first tailer's read")
	first.Release()

	close(stop)
	testutil.Wait(t, first.Done(), "the tailer to end")
	attach(t, svc, f, web1Since(0))

	backend.opened.Await(t, "a read afresh")
}

// pinged waits for a commit of lines to the stream row of the file behind svc.
func pinged(t *testing.T, svc *Service, f *fakeService, row int64) func() {
	t.Helper()
	rx, err := fileOf(t, svc, f).Subscribe(row)
	require.NoError(t, err)
	t.Cleanup(rx.Close)
	return func() {
		t.Helper()
		testutil.Recv(t, rx.Chan(), "a commit of the tailer's lines")
	}
}

// A tailer reads from the later of its stream's newest line and the watermark of the
// follow that started it, so it never reads what lies below every watermark.
func TestATailerStartsAtItsNewestLineOrItsWatermark(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(0, time.Second)})

	h := attach(t, svc, f, web1Since(time.Hour))
	require.Eventually(t, func() bool { return len(s.requestsOf("prod/web-1/app")) == 1 }, testutil.Timeout, time.Millisecond, "the tailer's read")
	assert.Equal(t, "2026-10-08T11:00:00Z", s.requestsOf("prod/web-1/app")[0].Get("sinceTime"))
	h.Release()
	testutil.Wait(t, h.Done(), "the tailer to end")

	attach(t, svc, f, web1Since(-time.Hour))
	require.Eventually(t, func() bool { return len(s.requestsOf("prod/web-1/app")) == 2 }, testutil.Timeout, time.Millisecond, "the next tailer's read")
	assert.Equal(t, "2026-10-08T10:00:01Z", s.requestsOf("prod/web-1/app")[1].Get("sinceTime"))
}

// A tailer resumed at its stream's newest line extends that line's chunk when its
// read returns the line's moment whole, and links a chunk of its own across the hole
// when the kubelet has rotated that moment away.
func TestATailerResumesTheChunkItContinues(t *testing.T) {
	t.Parallel()
	for name, rotated := range map[string]bool{"whole": false, "rotated": true} {
		t.Run(name, func(t *testing.T) {
			s := newLogServer()
			if !rotated {
				s.add("prod/web-1/app", stamped(0, "0"), stamped(time.Second, "1"))
			}
			s.add("prod/web-1/app", stamped(2*time.Second, "2"))
			f := webMirror(t)
			svc, _ := serviceOver(t, f, s)
			row := seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(0, time.Second)})
			committed := pinged(t, svc, f, row)

			attach(t, svc, f, web1Since(0))
			committed()

			first, second, third := logstore.Pos{TS: at(0).UnixNano()}, logstore.Pos{TS: at(time.Second).UnixNano()}, logstore.Pos{TS: at(2 * time.Second).UnixNano()}
			chunks := chunksOf(t, svc, f, "web-1", "app")
			if rotated {
				require.Len(t, chunks, 2)
				assert.Equal(t, []logstore.Pos{first, second}, []logstore.Pos{chunks[0].From, chunks[0].To})
				assert.Equal(t, []logstore.Pos{third, third}, []logstore.Pos{chunks[1].From, chunks[1].To})
				assert.Equal(t, &second, chunks[1].Prev)
			} else {
				require.Len(t, chunks, 1)
				assert.Equal(t, []logstore.Pos{first, third}, []logstore.Pos{chunks[0].From, chunks[0].To})
			}
			assert.True(t, chunks[len(chunks)-1].Open, "the tailer's chunk is open")
		})
	}
}

// A tailer's read that fails ends it, and its holders learn how through Done: the
// user's credentials refused, the namespace's log denied, or a server's failure.
func TestATailersFailureReachesItsHolders(t *testing.T) {
	t.Parallel()
	for code, kind := range map[int]failure{401: failFatal, 403: failDenied, 500: failTransient} {
		s := newLogServer()
		s.refuse["prod/web-1/app"] = refusal{code, "no"}
		f := webMirror(t)
		svc, _ := serviceOver(t, f, s)

		h := attach(t, svc, f, web1Since(0))

		testutil.Wait(t, h.Done(), "the tailer to end")
		assert.Equal(t, kind, failureKind(h.Err()), code)
	}
}

// The kubelet closes a log when its container ends, and the tailer ends with it, its
// chunk closed at its last line with no at_end, since a tailer cannot tell the log's
// end from a dropped connection. The next page's forward fill reads nothing more and
// marks it.
func TestAFinishedInstancesTailerEndsWithItsChunkClosed(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	_, _, row := filed(t, svc, f)
	committed := pinged(t, svc, f, row)
	h := attach(t, svc, f, web1Since(0))
	s.add("prod/web-1/app", stamped(time.Second, "last"))
	committed()

	f.store.Apply(t, finished(web1()))
	s.end("prod/web-1/app")
	testutil.Wait(t, h.Done(), "the tailer to end")
	require.NoError(t, h.Err())
	chunks := chunksOf(t, svc, f, "web-1", "app")
	require.Len(t, chunks, 1)
	assert.False(t, chunks[0].Open)
	assert.Equal(t, chunks[0].To, h.Committed(), "what it committed outlives its chunk's close")
	assert.False(t, chunks[0].AtEnd)

	_, err := svc.Fetch(t.Context(), "7", podView("web-1"), FetchQuery{Mode: FetchHead, Cursor: lineCursor("web-1", "app", time.Second, 0), Limit: 10})
	require.NoError(t, err)
	assert.True(t, chunksOf(t, svc, f, "web-1", "app")[0].AtEnd)
}

// A tailer whose batch the writer cannot take within WriteTick closes its chunk at
// its last committed line and reads again from now: the lines between are a live gap
// below its new start.
func TestATailerTheWriterCannotKeepUpWithReadsFromNow(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	f := webMirror(t)
	dir := t.TempDir()
	ret := testRetention
	ret.WriteBatch, ret.WriteQueue = 1, 1
	svc, _ := serviceIn(t, f, s, dir, ret)
	_, _, row := filed(t, svc, f)
	committed := pinged(t, svc, f, row)
	attach(t, svc, f, web1Since(0))
	s.add("prod/web-1/app", stamped(time.Second, "kept"))
	committed()

	// Another connection's exclusive lock holds the writer inside SQLite's busy wait.
	locker, err := sql.Open("sqlite", filepath.Join(dir, "11.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, locker.Close()) })
	lock, err := locker.Conn(t.Context())
	require.NoError(t, err)
	_, err = lock.ExecContext(t.Context(), "BEGIN EXCLUSIVE")
	require.NoError(t, err)
	// The writer's flush takes every batch queued by the time it blocks on the lock, so
	// lines go in until one finds the queue full.
	i := 0
	require.Eventually(t, func() bool {
		i++
		s.add("prod/web-1/app", stamped(time.Duration(1+i)*time.Second, "behind"))
		return s.followsClosed("prod/web-1/app") >= 1
	}, testutil.Timeout, time.Millisecond, "prod/web-1/app's stream to end")
	_, err = lock.ExecContext(t.Context(), "ROLLBACK")
	require.NoError(t, err)
	require.NoError(t, lock.Close())

	awaitReadAgain(t, s, "prod/web-1/app", "2026-10-08T11:00:00Z")
	start := svc.tailStart(f.active.Cache.ID, row)
	require.NotNil(t, start)
	assert.Equal(t, logstore.Pos{TS: at(time.Hour).UnixNano(), Ord: -1}, *start)
	for _, c := range chunksOf(t, svc, f, "web-1", "app") {
		assert.False(t, c.Open, "the chunk the writer fell behind on is closed")
	}
}

// A tailer in its grace holds its stream's row, so a sweep that reads the pod as gone
// leaves it while the tailer may still write to it.
func TestATailerInItsGraceHoldsItsRow(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	backend.set("web-1/app", fakeRead{wait: never})
	svc, f := serviceOn(t, backend)
	svc.grace = time.Hour
	everyPodGone(svc)
	h := attach(t, svc, f, web1Since(0))
	backend.opened.Await(t, "the tailer's read")
	h.Release()

	sweepFile(t, svc, f)

	assert.Len(t, rowsOf(t, svc, f, "web-1"), 1)
}

// A tailer whose write fails ends with the failure, so the lines the writer lost are a
// live gap a page fills rather than a range the tailer would cover as its own.
func TestATailerWhoseWriteFailsEnds(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	f := webMirror(t)
	dir := t.TempDir()
	svc, _ := serviceIn(t, f, s, dir, testRetention)
	_, _, row := filed(t, svc, f)
	committed := pinged(t, svc, f, row)
	h := attach(t, svc, f, web1Since(0))
	s.add("prod/web-1/app", stamped(time.Second, "kept"))
	committed()

	db, err := sql.Open("sqlite", filepath.Join(dir, "11.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	_, err = db.ExecContext(t.Context(), `CREATE TRIGGER refuse BEFORE INSERT ON lines BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	require.NoError(t, err)
	s.add("prod/web-1/app", stamped(2*time.Second, "lost"))
	testutil.Wait(t, h.Done(), "the tailer to end")
	require.Error(t, h.Err())
	assert.Equal(t, failTransient, failureKind(h.Err()))
	assert.Nil(t, svc.tailStart(f.active.Cache.ID, row), "no tailer holds the stream")

	_, err = db.ExecContext(t.Context(), `DROP TRIGGER refuse`)
	require.NoError(t, err)
	page, err := svc.Fetch(t.Context(), "7", podView("web-1"), FetchQuery{Mode: FetchHead, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"kept", "lost"}, messages(page.Records))
}

// A follow's walk reads a tailer's start holding one of the file's reader connections,
// and the registry holds its lock while a start reads the file, so reading the start
// takes no lock.
func TestATailersStartIsReadWithoutTheRegistrysLock(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	h := attach(t, svc, f, web1Since(0))

	h.t.r.mu.Lock()
	defer h.t.r.mu.Unlock()
	read := make(chan logstore.Pos, 1)
	go func() { read <- h.Start() }()
	assert.Equal(t, web1Since(0), testutil.Recv(t, read, "the start"))
}

// fills is every request for key that reads no follow: a page's or a follow's fill.
func fills(s *logServer, key string) int {
	n := 0
	for _, q := range s.requestsOf(key) {
		if q.Get("follow") != "true" {
			n++
		}
	}
	return n
}

// A page crosses every gap past a running tailer's start, filling none of them, and
// fills the range below the start, which the tailer never reads: before the tailer has
// written it ends at the start, and once the tailer has written, it reads to the
// tailer's last line.
func TestAPageCrossesWhatATailerHolds(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "0"), stamped(time.Second, "1"), stamped(5*time.Second, "below the start"))
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	row := seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(0, time.Second), Head: true})
	committed := pinged(t, svc, f, row)
	attach(t, svc, f, web1Since(time.Hour))
	head := FetchQuery{Mode: FetchHead, Limit: 10}

	page, err := svc.Fetch(t.Context(), "7", podView("web-1"), head)
	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1", "below the start"}, messages(page.Records))
	assert.Equal(t, 1, fills(s, "prod/web-1/app"), "the range below the start filled")
	committed() // the fill's

	s.add("prod/web-1/app", stamped(time.Hour+time.Second, "tailed"))
	committed()
	page, err = svc.Fetch(t.Context(), "7", podView("web-1"), head)
	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1", "below the start", "tailed"}, messages(page.Records))
	assert.Equal(t, 2, fills(s, "prod/web-1/app"), "the range below the start filled again, and nothing past it")
}

// A Tail page from the end crosses the range past a quiet tailer's start and fills the
// history below it, which the tailer never reads.
func TestATailPageFillsBelowAQuietTailersStart(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "0"), stamped(time.Second, "1"), stamped(5*time.Second, "below the start"))
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(0, time.Second), Head: true})
	attach(t, svc, f, web1Since(time.Hour))

	page, err := svc.Fetch(t.Context(), "7", podView("web-1"), FetchQuery{Mode: FetchTail, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1", "below the start"}, messages(page.Records))
}

// A Tail page cut by its limit inside the range below a quiet tailer's start still
// answers the newest lines of that range.
func TestATailPageCutBelowAQuietTailersStart(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "0"), stamped(time.Second, "1"), stamped(3*time.Second, "3"), stamped(4*time.Second, "4"), stamped(5*time.Second, "5"))
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)
	seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(0, time.Second), Head: true})
	attach(t, svc, f, web1Since(time.Hour))

	page, err := svc.Fetch(t.Context(), "7", podView("web-1"), FetchQuery{Mode: FetchTail, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, []string{"5"}, messages(page.Records))
}

// heldBackend is a fake backend whose Tail reads begin only once open closes, and
// end only once done closes, their cancel ignored, so a test can hold a tailer in
// its start or in its stop. entered fires as each read is asked for.
type heldBackend struct {
	*fakeBackend
	entered    *testutil.Probe[struct{}]
	open, done chan struct{}
}

func newHeldBackend() *heldBackend {
	b := &heldBackend{fakeBackend: newFakeBackend(), entered: testutil.NewProbe[struct{}](8), open: make(chan struct{}), done: make(chan struct{})}
	b.set("web-1/app", fakeRead{wait: b.done})
	return b
}

func (b *heldBackend) Tail(ctx context.Context, st stream, opts readOpts) *streamRead {
	b.entered.Fire(struct{}{})
	<-b.open
	return b.fakeBackend.Tail(context.WithoutCancel(ctx), st, opts)
}

// attachment is what one attach answered.
type attachment struct {
	h   *handle
	err error
}

// attachAsync attaches to st's tailer on r from watermark on a goroutine, answering
// on the channel returned.
func attachAsync(ctx context.Context, r *registry, st stream, row int64, watermark logstore.Pos) <-chan attachment {
	out := make(chan attachment, 1)
	go func() {
		h, err := r.attach(ctx, st, row, watermark)
		out <- attachment{h, err}
	}()
	return out
}

// A follow attaching while another starts the stream's tailer waits for that start
// and joins the tailer it made, and one whose context ends meanwhile gives up.
func TestAnAttachWaitsOnTheStartInProgress(t *testing.T) {
	t.Parallel()
	backend := newHeldBackend()
	svc, f := serviceOn(t, backend)
	// Ends the tailer before the file closes under it.
	t.Cleanup(func() { close(backend.done) })
	tg, st, row := filed(t, svc, f)
	r := svc.registryFor("7", tg)

	first := attachAsync(t.Context(), r, st, row, web1Since(0))
	backend.entered.Await(t, "the first attach to start the tailer")
	second := attachAsync(t.Context(), r, st, row, web1Since(0))
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	third := testutil.Recv(t, attachAsync(cancelled, r, st, row, web1Since(0)), "the cancelled attach")
	assert.ErrorIs(t, third.err, context.Canceled)

	close(backend.open)
	a, b := testutil.Recv(t, first, "the first attach"), testutil.Recv(t, second, "the second attach")
	require.NoError(t, a.err)
	require.NoError(t, b.err)
	t.Cleanup(a.h.Release)
	t.Cleanup(b.h.Release)
	assert.Same(t, a.h.t, b.h.t, "one tailer")
	backend.opened.Await(t, "the tailer's read")
	_, again := backend.opened.TryAwait()
	assert.False(t, again, "a second read")
}

// A follow attaching to a tailer that is stopping waits for it to end and starts
// one afresh, since a tailer started beside it would race its chunk's close; one
// whose context ends meanwhile gives up.
func TestAnAttachWaitsOnAStoppingTailer(t *testing.T) {
	t.Parallel()
	backend := newHeldBackend()
	close(backend.open)
	svc, f := serviceOn(t, backend)
	tg, st, row := filed(t, svc, f)
	r := svc.registryFor("7", tg)

	first := attach(t, svc, f, web1Since(0))
	backend.opened.Await(t, "the first tailer's read")
	first.Release()
	// The read ignores its cancel, so the tailer stays stopping until done closes.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	third := testutil.Recv(t, attachAsync(cancelled, r, st, row, web1Since(0)), "the cancelled attach")
	assert.ErrorIs(t, third.err, context.Canceled)
	second := attachAsync(t.Context(), r, st, row, web1Since(0))

	close(backend.done)
	testutil.Wait(t, first.Done(), "the stopping tailer to end")
	b := testutil.Recv(t, second, "the second attach")
	require.NoError(t, b.err)
	t.Cleanup(b.h.Release)
	assert.NotSame(t, first.t, b.h.t, "a tailer afresh")
	backend.opened.Await(t, "a read afresh")
}

// A start that cannot take the registry's claims — the cluster's, or the file's —
// fails the attach, drops the tailer and gives back whatever it took, so the next
// attach starts afresh.
func TestAStartThatCannotHoldItsClaimsFails(t *testing.T) {
	t.Parallel()
	t.Run("cluster", func(t *testing.T) {
		t.Parallel()
		backend := newFakeBackend()
		backend.set("web-1/app", fakeRead{wait: never})
		svc, f := serviceOn(t, backend)
		tg, st, row := filed(t, svc, f)
		r := svc.registryFor("7", tg)
		f.set(func() { f.acquireErr = cluster.ErrNotConnectable })

		_, err := r.attach(t.Context(), st, row, web1Since(0))
		require.ErrorIs(t, err, ErrRevoked)
		assert.Empty(t, r.tailers)
		assert.Nil(t, r.file, "the file given back")

		f.set(func() { f.acquireErr = nil })
		h, err := r.attach(t.Context(), st, row, web1Since(0))
		require.NoError(t, err)
		t.Cleanup(h.Release)
		backend.opened.Await(t, "the tailer's read")
	})
	t.Run("file", func(t *testing.T) {
		t.Parallel()
		backend := newFakeBackend()
		backend.set("web-1/app", fakeRead{wait: never})
		svc, f := serviceOn(t, backend)
		tg, st, row := filed(t, svc, f)
		require.NoError(t, svc.files.Close())

		_, err := svc.registryFor("7", tg).attach(t.Context(), st, row, web1Since(0))
		require.ErrorIs(t, err, logstore.ErrClosed)
		_, opened := backend.opened.TryAwait()
		assert.False(t, opened, "a read")
	})
}

// A tailer let go of ends once its grace has passed with no follow holding it.
func TestATailerStopsAtTheEndOfItsGrace(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	backend.set("web-1/app", fakeRead{wait: never})
	svc, f := serviceOn(t, backend)
	// The grace is the cadence under test, shrunk from the file's TailGrace.
	svc.grace = time.Millisecond
	h := attach(t, svc, f, web1Since(0))
	backend.opened.Await(t, "the tailer's read")

	h.Release()
	testutil.Wait(t, h.Done(), "the tailer to end at its grace")
}
