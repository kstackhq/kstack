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
	"errors"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// The log cache's invariant, after any sequence of pages, appends, rotations,
// restarts, evictions and reads broken part way over a fake kubelet: every line a
// page answers is in a chunk that covers its pattern, and no chunk is missing a line
// the kubelet served into its range. It walks 100 seeds of 60 steps, 25 under
// -short, each in parallel.
func TestTheFileKeepsItsInvariant(t *testing.T) {
	t.Parallel()
	seeds := 100
	if testing.Short() {
		seeds = 25
	}
	for seed := range seeds {
		t.Run(strconv.Itoa(seed), func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, rand.New(rand.NewSource(int64(seed))))
			for range 60 {
				w.step()
				w.checkChunks()
			}
		})
	}
}

// world is a deployment's pods as a walk drives them, and every line each instance
// of their app containers ever wrote.
type world struct {
	t       *testing.T
	rng     *rand.Rand
	s       *logServer
	f       *fakeService
	svc     *Service
	pods    []*walkPod
	written map[string][]Record
	cursors []*string
	seen    []string
}

// walkPod is one pod of the walk: its instances so far, the current one the last,
// and the moment of its current instance's newest line.
type walkPod struct {
	name string
	runs int
	last time.Duration
}

func newWorld(t *testing.T, rng *rand.Rand) *world {
	w := &world{t: t, rng: rng, s: newLogServer(), written: map[string][]Record{}}
	objects := []map[string]any{
		workload("apps/v1", "Deployment", "web", "dep-web", nil),
		workload("apps/v1", "ReplicaSet", "web-7d9f", "rs-web", controller("apps/v1", "Deployment", "web", "dep-web")),
		node("node-a", "eu", "eu-1a", "linux", "amd64"),
	}
	for i := 1; i <= 1+rng.Intn(2); i++ {
		p := &walkPod{name: "web-" + strconv.Itoa(i), runs: 1}
		w.pods = append(w.pods, p)
		objects = append(objects, w.body(p))
	}
	// A budget a few pages' worth, with nothing kept for being read, so a sweep's own
	// pick runs inside the invariant.
	ret := testRetention
	ret.Bytes = int64(200 + rng.Intn(2000))
	ret.StreamShare, ret.ReadTTL = ret.Bytes/2, 0
	ret.WriteBatch, ret.WriteBatchBytes = 1+rng.Intn(4), int64(1+rng.Intn(64))
	w.svc, w.f = serviceIn(t, newMirror(t, objects...), w.s, t.TempDir(), ret)
	w.svc.caps = randomCaps(rng)
	w.f.chunk(1 + rng.Intn(64))
	return w
}

// id is the runtime id of p's instance n.
func (p *walkPod) id(n int) string { return containerID("uid-"+p.name, "app", n) }

// body is p as the mirror holds it: its app container running its current instance,
// the one before it terminated at the second of its last line.
func (w *world) body(p *walkPod) map[string]any {
	body := pod(p.name, "uid-"+p.name, "node-a", webReplicaSet(), container{name: "app"})
	status := body["status"].(map[string]any)["containerStatuses"].([]any)[0].(map[string]any)
	status["containerID"] = p.id(p.runs - 1)
	if p.runs > 1 {
		status["lastState"] = map[string]any{"terminated": map[string]any{
			"containerID": p.id(p.runs - 2), "finishedAt": at(p.last).Add(-time.Second).Format(time.RFC3339),
		}}
	}
	return body
}

// step does one random thing to the world, then checks what a page answered: a
// write, a rotation, a restart, an eviction by hand, a sweep, a page, or a page whose
// next read's body breaks part way, inside a window or between them.
func (w *world) step() {
	p := w.pods[w.rng.Intn(len(w.pods))]
	switch n := w.rng.Intn(11); {
	case n < 3:
		w.write(p)
	case n < 4:
		w.rotate(p)
	case n < 5:
		w.restart(p)
	case n < 6:
		w.evict()
	case n < 7:
		require.NoError(w.t, fileOf(w.t, w.svc, w.f).Sweep(w.t.Context()))
	case n < 8:
		w.s.breakNext(w.rng.Intn(6))
		w.page()
	default:
		w.page()
	}
}

// write appends one to eight lines to p's current instance, at moments that often
// tie and sometimes fall inside a second.
func (w *world) write(p *walkPod) {
	id := p.id(p.runs - 1)
	for range 1 + w.rng.Intn(8) {
		p.last += []time.Duration{0, 0, 250 * time.Millisecond, time.Second, 2500 * time.Millisecond}[w.rng.Intn(5)]
		lines := w.written[id]
		ordinal := 0
		if n := len(lines); n > 0 && lines[n-1].Timestamp.Equal(at(p.last)) {
			ordinal = lines[n-1].Ordinal + 1
		}
		parity := []string{"even", "odd"}[len(lines)%2]
		msg := p.name + "#" + strconv.Itoa(p.runs-1) + "." + strconv.Itoa(len(lines)) + " " + parity
		w.written[id] = append(lines, Record{Timestamp: at(p.last), Message: msg, ContainerID: id, Ordinal: ordinal})
		w.s.add("prod/"+p.name+"/app", stamped(p.last, msg))
	}
}

// rotate drops the oldest whole seconds of the log the kubelet serves of p's current
// or previous instance, as a rotation does: a moment is never split.
func (w *world) rotate(p *walkPod) {
	key := "prod/" + p.name + "/app"
	if w.rng.Intn(2) == 0 {
		key += "/previous"
	}
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	lines := w.s.logs[key]
	if len(lines) == 0 {
		return
	}
	cut := parsed(lines[w.rng.Intn(len(lines))]).Truncate(time.Second)
	var kept []string
	for _, line := range lines {
		if !parsed(line).Before(cut) {
			kept = append(kept, line)
		}
	}
	w.s.logs[key] = kept
}

// parsed is the moment of a line of the fake kubelet's.
func parsed(line string) time.Time {
	ts, _ := time.Parse(time.RFC3339Nano, strings.SplitN(line, " ", 2)[0])
	return ts
}

// restart ends p's current instance at the second of its last line and starts the
// next a second later: the kubelet serves the old one as previous, the one before it
// no more, and the mirror says so at once.
func (w *world) restart(p *walkPod) {
	p.last = p.last.Truncate(time.Second) + 2*time.Second
	p.runs++
	w.s.mu.Lock()
	w.s.logs["prod/"+p.name+"/app/previous"] = w.s.logs["prod/"+p.name+"/app"]
	w.s.logs["prod/"+p.name+"/app"] = nil
	w.s.mu.Unlock()
	w.f.store.Apply(w.t, w.body(p))
}

// evict deletes a random chunk of a random stream of the file, and its lines, as the
// janitor would.
func (w *world) evict() {
	file := fileOf(w.t, w.svc, w.f)
	p := w.pods[w.rng.Intn(len(w.pods))]
	rows, err := file.PodStreams(w.t.Context(), "prod", p.name, "")
	require.NoError(w.t, err)
	if len(rows) == 0 {
		return
	}
	row := rows[w.rng.Intn(len(rows))]
	chunks, err := file.Chunks(w.t.Context(), row.ID, "")
	require.NoError(w.t, err)
	if len(chunks) == 0 {
		return
	}
	require.NoError(w.t, file.Evict(w.t.Context(), chunks[w.rng.Intn(len(chunks))].ID, math.MaxInt64))
}

// page reads a random page of a random view, and checks every line it answered is
// in a chunk covering the view's grep.
func (w *world) page() {
	p := w.pods[w.rng.Intn(len(w.pods))]
	view := []Scope{
		{Sources: []ScopeSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web", Containers: []string{"app"}}}},
		podSource(p.name, false, "app"),
		podSource(p.name, true, "app"),
	}[w.rng.Intn(3)]
	view.Grep = []string{"", "odd"}[w.rng.Intn(2)]
	q := FetchRequest{Scope: view, PageFrom: []PageFrom{PageFromFirst, PageFromLast}[w.rng.Intn(2)]}
	switch n := w.rng.Intn(4); {
	case n == 0:
		moment := at(time.Duration(w.rng.Int63n(int64(p.last + time.Second))))
		q.Window = Window{Since: &moment, Before: Padding{Lines: 1 + w.rng.Intn(20)}}
		q.PageFrom = PageFromFirst
	case n == 1 && len(w.cursors) > 0:
		q = FetchRequest{PageCursor: w.cursors[w.rng.Intn(len(w.cursors))]}
	case n == 2 && len(w.seen) > 0:
		q.PageFrom, q.Seen = PageFromFirst, []string{w.seen[w.rng.Intn(len(w.seen))]}
	}
	q.PageSize = 1 + w.rng.Intn(20)
	page, err := w.svc.Fetch(w.t.Context(), "7", q)
	var refused Refusal
	if errors.As(err, &refused) {
		require.NotEmpty(w.t, refused.Code, "every refusal names its code")
		return
	}
	require.NoError(w.t, err)
	for _, c := range []*string{page.Page.Prev, page.Page.Next, page.Page.Follow} {
		if c != nil {
			w.cursors = append(w.cursors, c)
		}
	}
	file := fileOf(w.t, w.svc, w.f)
	grep := page.Request.Grep
	for _, rec := range page.Records {
		w.seen = append(w.seen, rec.Cursor())
		id := w.rowID(file, rec)
		chunks, err := file.Chunks(w.t.Context(), id, grep)
		require.NoError(w.t, err)
		at := logstore.Pos{TS: rec.Timestamp.UnixNano(), Ord: rec.Ordinal}
		require.True(w.t, claims(chunks, at), "%s answered outside every chunk of %q", rec.Message, grep)
		held := w.lines(file, id)
		require.Equal(w.t, rec.Message, held[at], "the file's line at %v", at)
	}
}

// rowID is the file's row of rec's instance.
func (w *world) rowID(file *logstore.Store, rec Record) int64 {
	rows, err := file.PodStreams(w.t.Context(), rec.Stream.Namespace, rec.Stream.Pod, "")
	require.NoError(w.t, err)
	for _, row := range rows {
		if row.Container == rec.Stream.Container && row.ContainerID == rec.ContainerID {
			return row.ID
		}
	}
	w.t.Fatalf("no row of %s", rec.ContainerID)
	return 0
}

// lines is every line the file holds of the stream id, by position.
func (w *world) lines(file *logstore.Store, id int64) map[logstore.Pos]string {
	held := map[logstore.Pos]string{}
	require.NoError(w.t, file.Read(w.t.Context(), func(r logstore.Reader) error {
		return r.Lines(id, logstore.Oldest, logstore.Newest, func(l logstore.Line) bool {
			held[l.Pos] = l.Message
			return true
		})
	}))
	return held
}

// claims is whether a chunk of chunks claims the line at p.
func claims(chunks []logstore.Chunk, p logstore.Pos) bool {
	for _, c := range chunks {
		if c.From.Compare(p) <= 0 && p.Compare(c.To) <= 0 {
			return true
		}
	}
	return false
}

// checkChunks checks no chunk is missing a line its instance wrote into its range,
// and no span either: a link promises nothing lies between, so a rotation leaves a
// gap.
func (w *world) checkChunks() {
	file := fileOf(w.t, w.svc, w.f)
	for _, p := range w.pods {
		rows, err := file.PodStreams(w.t.Context(), "prod", p.name, "")
		require.NoError(w.t, err)
		for _, row := range rows {
			chunks, err := file.Chunks(w.t.Context(), row.ID, "")
			require.NoError(w.t, err)
			held := w.lines(file, row.ID)
			for _, rec := range w.written[row.ContainerID] {
				at := logstore.Pos{TS: rec.Timestamp.UnixNano(), Ord: rec.Ordinal}
				if claims(chunks, at) {
					require.Equal(w.t, rec.Message, held[at], "a chunk of %s claims %v", row.ContainerID, at)
				}
				for _, sp := range logstore.CoverageOf(unlabelled(chunks), kubeletOnly, nil).Spans {
					if sp.From.Compare(at) <= 0 && at.Compare(sp.To) <= 0 {
						require.Equal(w.t, rec.Message, held[at], "a span of %s covers %v", row.ContainerID, at)
					}
				}
			}
		}
	}
}

// unlabelled is the chunks of chunks that claim every line, not a grep's.
func unlabelled(chunks []logstore.Chunk) []logstore.Chunk {
	var out []logstore.Chunk
	for _, c := range chunks {
		if c.Pattern == "" {
			out = append(out, c)
		}
	}
	return out
}

// A fill its limits cut halfway through a gap writes what it read, and the walk
// stops at the gap left past it, so the page ends there with more to read; the
// next page fills the rest of the gap from where the first stopped, into the same
// span, and every line is shown once.
func TestAWalkStopsAtALiveGap(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(100)...)
	svc, f := serviceOver(t, webMirror(t), s)
	f.chunk(64)
	svc.caps = capsOf(1 << 10)

	got := pages(t, svc, web1Scope, PageFromFirst, MaxPageSize)

	assert.Greater(t, len(got), 1, "the limits cut the first page")
	assert.Equal(t, messages(linesOf(lineEvery(100))), recordsOf(got, PageFromFirst))
	assert.Len(t, spansOf(t, svc, f), 1,
		"each page's fill continues the chunk the one before it wrote")
}

// A walk crosses a lost gap as it crosses a seam: a Tail page that reached the
// kubelet's oldest line over a stream with an older chunk below reads on into that
// chunk and answers nothing older, and a Head page over a stream no backend serves
// reads past the gaps inside it, so no page answers more at one spot twice.
func TestAWalkCrossesALostGap(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)[5:]...)
	svc, f := serviceOver(t, webMirror(t), s)
	seed(t, svc, f, rowOf("web-1", "app"),
		logstore.Fill{Lines: fileLines(0, time.Second)},
		logstore.Fill{Lines: fileLines(5*time.Second, 6*time.Second, 7*time.Second, 8*time.Second, 9*time.Second), Head: true})

	page, err := svc.Fetch(t.Context(), "7", FetchRequest{Scope: web1Scope, PageFrom: PageFromLast, PageSize: 100})

	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1", "5", "6", "7", "8", "9"}, messages(page.Records))
	assert.Nil(t, page.Page.Prev)

	gone := Scope{Sources: []ScopeSource{{Namespace: "prod", Kind: SourcePod, Name: "old-1"}}}
	seed(t, svc, f, rowOf("old-1", "app"),
		logstore.Fill{Lines: fileLines(0, time.Second)},
		logstore.Fill{Lines: fileLines(5 * time.Second)})
	page, err = svc.Fetch(t.Context(), "7", FetchRequest{Scope: gone, PageFrom: PageFromFirst, PageSize: 100})

	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1", "5"}, messages(page.Records))
	assert.Nil(t, page.Page.Next)
}

// A line is held by the page's chunks when it lies in one of them, two that touch or
// overlap read as one run, and a line in the gap between two runs is nobody's.
func TestHeldByJoinsChunksThatTouch(t *testing.T) {
	t.Parallel()
	pos := func(ts int64) logstore.Pos { return logstore.Pos{TS: ts} }
	held := heldBy([]logstore.Chunk{
		{From: pos(0), To: pos(10)},
		{From: pos(5), To: pos(8)},
		{From: pos(10), To: pos(20)},
		{From: pos(30), To: pos(40)},
	})
	for ts, want := range map[int64]bool{0: true, 7: true, 10: true, 15: true, 20: true, 25: false, 30: true, 40: true, 41: false} {
		assert.Equal(t, want, held(pos(ts)), ts)
	}
}

// A walk that spends its share of the walk budget stops short, and the page is cut
// at its last line rather than passing lines it did not read.
func TestAWalkThatSpendsItsBudgetCutsThePage(t *testing.T) {
	t.Parallel()
	svc, f := serviceOver(t, webMirror(t), newLogServer())
	var cached []time.Duration
	for i := range 50 {
		cached = append(cached, time.Duration(i)*time.Second)
	}
	seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(cached...), Head: true})
	svc.walkBudget = 64

	page, err := svc.Fetch(t.Context(), "7", FetchRequest{Scope: web1Scope, PageFrom: PageFromFirst, PageSize: 100})

	require.NoError(t, err)
	assert.NotEmpty(t, page.Records)
	assert.Less(t, len(page.Records), 50)
	assert.NotNil(t, page.Page.Next)
}

// pagedMarks is marks of every page a walk of web-1 from from at limit reads, oldest
// first, over a file holding twoHoles.
func pagedMarks(t *testing.T, grep string, from PageFrom, limit int) []string {
	t.Helper()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(9*time.Second, "y9"), stamped(10*time.Second, "x10"))
	svc, f := serviceOver(t, webMirror(t), s)
	seed(t, svc, f, rowOf("web-1", "app"), twoHoles...)
	view := web1Scope
	view.Grep = grep
	var out []string
	for _, page := range pages(t, svc, view, from, limit) {
		if from == PageFromFirst {
			out = append(out, marks(page.Records)...)
		} else {
			out = append(marks(page.Records), out...)
		}
	}
	return out
}

// A record carries every marked hole between it and the line of its stream before it
// that the view passes, wherever the pages cut, read either way.
func TestARecordCarriesTheHolesBeforeIt(t *testing.T) {
	t.Parallel()
	every := []string{"x0", "y1", "y5 (1s-5s)", "y6", "y9 (6s-9s)", "x10"}
	for _, mode := range []PageFrom{PageFromFirst, PageFromLast} {
		for _, limit := range []int{1, 2, 100} {
			name := fmt.Sprintf("%s by %d", mode, limit)
			assert.Equal(t, every, pagedMarks(t, "", mode, limit), name)
			assert.Equal(t, []string{"x0", "x10 (1s-9s)"}, pagedMarks(t, "x", mode, limit), name+" under x")
			assert.Equal(t, []string{"y1", "y5 (1s-5s)", "y6", "y9 (6s-9s)"}, pagedMarks(t, "y", mode, limit), name+" under y")
		}
	}
}

// A walk reports the first and last rows it read, kept or rejected by the grep:
// a page cut at its first kept line still read past it to fill its need.
func TestAWalkReportsTheRowsItRead(t *testing.T) {
	t.Parallel()
	svc, f := serviceOver(t, webMirror(t), newLogServer())
	seed(t, svc, f, rowOf("web-1", "app"), logstore.Fill{Lines: fileLines(0, time.Second, 2*time.Second, 3*time.Second, 4*time.Second), Head: true})
	grep := regexp.MustCompile("^[02]$")
	scope := web1Scope
	scope.Grep = grep.String()

	ms, err := svc.page(t.Context(), "7", pageQuery{scope: scope, grep: grep, mode: newer, limit: 1})

	require.NoError(t, err)
	require.Len(t, ms[0].reaches, 1)
	r := ms[0].reaches[0]
	web1 := StreamKey{Namespace: "prod", Pod: "web-1", Container: "app"}
	assert.Equal(t, lineAt(web1, 0, 0), r.first)
	assert.Equal(t, lineAt(web1, 2*time.Second, 0), r.last)
	assert.True(t, r.stop.isZero())
	assert.Equal(t, web1, StreamKey{Namespace: r.run.Namespace, Pod: r.run.Pod, Container: r.run.Container})
}
