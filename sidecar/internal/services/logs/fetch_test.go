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
	"fmt"
	"math/rand"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// Pages join: walked from either end through random logs with tied and sub-second
// moments and long lines, under small limits and a grep, the pages hold every line
// in range once, in order, each within its limit, whether the walk starts at an end
// or at a line's cursor. Some streams lost moments to a rotation the file never saw,
// and each hole between two lines the file holds rides on exactly one record, the
// first past it the grep passes, with the hole's own two times. A second of the
// kubelet's log louder than the limit stops its stream: its lines may be left out,
// all of them backward and the rest of the second past what the read took forward,
// and a walk from the start names the stream in skipped. Every page resolves the
// view through SQL the store parses afresh, so under the race detector the full
// sweep takes minutes; -short walks a prefix of the seeds, and the stress run walks
// them all.
func TestPagesJoinWithoutLossOrRepeat(t *testing.T) {
	t.Parallel()
	seeds := 150
	if testing.Short() {
		seeds = 25
	}
	for seed := range seeds {
		t.Run(strconv.Itoa(seed), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(int64(seed)))
			s, objects, all := randomLogs(rng)
			kept, runs := rotateAway(rng, s, all)
			// Windows written in batches of a few lines or bytes, so batches join too.
			ret := testRetention
			ret.WriteBatch, ret.WriteBatchBytes = 1+rng.Intn(4), int64(1+rng.Intn(64))
			svc, f := serviceIn(t, newMirror(t, objects...), s, t.TempDir(), ret)
			for key, held := range runs {
				var fills []logstore.Fill
				for _, run := range held {
					var lines []logstore.Line
					for _, rec := range run {
						lines = append(lines, lineOf(rec))
					}
					fills = append(fills, logstore.Fill{Lines: lines})
				}
				seedStamped(t, svc, f, rowOf(key.Pod, key.Container), true, fills...)
			}
			// Every page resolves the view again; the mirror holds still, and SQLite
			// parsing the same statements is most of what the walks cost.
			f.answers = map[string]cluster.ClusterCachedDataQueryResult{}
			svc.caps = randomCaps(rng)
			loud := loudLines(s, svc.caps.second)
			f.chunk(1 + rng.Intn(64))
			view := Scope{Sources: []ScopeSource{{Namespace: "prod", Kind: SourceDeployment, Name: "web", AllContainers: true}}}
			view.Grep = []string{"", "odd", "#.*7"}[rng.Intn(3)]
			grep := regexp.MustCompile(view.Grep)
			matches := func(rec Record) bool { return grep.MatchString(rec.Message) }
			// expected is the lines w holds: those of its range the grep passes and every
			// line of its padding, each carrying the holes between it and the line of its
			// stream before it that passes, since the grep searches the range alone.
			expected := func(w Window) []Record {
				inRange := func(rec Record) bool {
					return (w.Since == nil || !rec.Timestamp.Before(*w.Since)) && (w.Until == nil || !rec.Timestamp.After(*w.Until))
				}
				return inWindow(wanted(all, kept, func(rec Record) bool { return !inRange(rec) || matches(rec) }), w)
			}
			want := expected(Window{})
			limit := 1 + rng.Intn(25)

			joins := func(want, got []Record, name string) {
				t.Helper()
				assert.True(t, ordered(got), "%s: in order, none repeated", name)
				assert.Equal(t, marks(without(want, loud)), marks(without(got, loud)), name)
			}
			sizes := func() (int, int) { return 1 + rng.Intn(25), []int{0, 0, 64, 512}[rng.Intn(4)] }
			got, skipped, firsts := walkPages(t, svc, FetchRequest{Scope: view, PageFrom: PageFromFirst, PageSize: limit}, sizes)
			joins(want, got, "first")
			for _, key := range loud {
				assert.Contains(t, skipped, key.Pod+"/"+key.Container, "a loud second is named")
			}
			got, _, lasts := walkPages(t, svc, FetchRequest{Scope: view, PageFrom: PageFromLast, PageSize: limit}, sizes)
			joins(want, got, "last")
			if len(want) == 0 {
				return
			}

			// Turning back from a page reads what the pages before it read.
			if k := rng.Intn(len(firsts)); firsts[k].Page.Prev != nil {
				var before []Record
				for _, page := range firsts[:k] {
					before = append(before, page.Records...)
				}
				back, _, _ := walkPages(t, svc, FetchRequest{PageCursor: firsts[k].Page.Prev, PageSize: limit}, sizes)
				joins(before, back, "back from a next")
			}
			if k := rng.Intn(len(lasts)); k > 0 && lasts[k].Page.Next != nil {
				var after []Record
				for _, page := range lasts[:k] {
					after = append(slices.Clone(page.Records), after...)
				}
				on, _, _ := walkPages(t, svc, FetchRequest{PageCursor: lasts[k].Page.Next, PageSize: limit}, sizes)
				joins(after, on, "on from a prev")
			}

			m := want[rng.Intn(len(want))].Timestamp
			joins(expected(Window{Since: &m}), walk(t, svc, FetchRequest{Scope: view, Window: Window{Since: &m}, PageFrom: PageFromFirst, PageSize: limit}), "since")
			upTo := m.Add(-time.Nanosecond)
			joins(expected(Window{Until: &upTo}), walk(t, svc, FetchRequest{Scope: view, Window: Window{Until: &upTo}, PageFrom: PageFromLast, PageSize: limit}), "until")

			// A window padded each side, by span or, with no loud second to leave lines
			// out of a count, by lines, holds its range and its padding, read either way.
			from, to := want[rng.Intn(len(want))].Timestamp, want[rng.Intn(len(want))].Timestamp
			if to.Before(from) {
				from, to = to, from
			}
			w := Window{Since: &from, Until: &to}
			pad := func() Padding {
				switch n := rng.Intn(3); {
				case n == 0 && len(loud) == 0:
					return Padding{Lines: 1 + rng.Intn(10)}
				case n == 1:
					return Padding{Span: time.Duration(rng.Intn(3000)) * time.Millisecond}
				}
				return Padding{}
			}
			w.Before, w.After = pad(), pad()
			for _, from := range []PageFrom{PageFromFirst, PageFromLast} {
				got, _, _ := walkPages(t, svc, FetchRequest{Scope: view, Window: w, PageFrom: from, PageSize: limit}, sizes)
				joins(expected(w), got, fmt.Sprintf("%s of %+v", from, w))
			}
		})
	}
}

// randomLogs is a deployment web of one to three pods, each with an app and an
// istio-proxy container, whose logs hold up to eighty lines each at moments that
// often tie within and across containers, some inside a second, some opening
// with a burst, some quiet for a day part way, every other line saying odd, and
// every line in the order a view of web reads them.
func randomLogs(rng *rand.Rand) (*logServer, []map[string]any, []Record) {
	s := newLogServer()
	objects := []map[string]any{
		workload("apps/v1", "Deployment", "web", "dep-web", nil),
		workload("apps/v1", "ReplicaSet", "web-7d9f", "rs-web", controller("apps/v1", "Deployment", "web", "dep-web")),
		node("node-a", "eu", "eu-1a", "linux", "amd64"),
	}
	place := Place{Node: "node-a", Region: "eu", Zone: "eu-1a", OS: "linux", Arch: "amd64"}
	var all []Record
	for i := 1; i <= 1+rng.Intn(3); i++ {
		name := "web-" + strconv.Itoa(i)
		key := "prod/" + name
		objects = append(objects, pod(name, "uid-"+name, "node-a", webReplicaSet(), container{name: "app"}, container{name: "istio-proxy"}))
		for _, container := range []string{"app", "istio-proxy"} {
			stream := StreamKey{Namespace: "prod", Pod: name, Container: container}
			// Some logs hold long lines, which make some seconds loud.
			long := []int{0, 0, 1500}[rng.Intn(3)]
			moments := make([]time.Duration, rng.Intn(81))
			for i := range moments {
				moments[i] = time.Duration(rng.Intn(12))*time.Second + []time.Duration{0, 0, 0, 250 * time.Millisecond, 500 * time.Millisecond}[rng.Intn(5)]
			}
			slices.Sort(moments)
			// A log that opens with a burst, more lines at its first moment than a
			// window holds.
			if len(moments) > 0 && rng.Intn(2) == 0 {
				for i := range min(len(moments), 1+rng.Intn(30)) {
					moments[i] = moments[0]
				}
			}
			// A log quiet for a day between its older lines and its newer, which a
			// read back across widens past.
			if len(moments) > 1 && rng.Intn(3) == 0 {
				for i := range 1 + rng.Intn(len(moments)-1) {
					moments[i] -= 24 * time.Hour
				}
			}
			ordinal := 0
			for i, d := range moments {
				if i > 0 && moments[i-1] == d {
					ordinal++
				} else {
					ordinal = 0
				}
				parity := "even"
				if i%2 == 1 {
					parity = "odd"
				}
				msg := name + "/" + container + "#" + strconv.Itoa(i) + " " + parity
				if long > 0 && rng.Intn(3) == 0 {
					msg += " " + strings.Repeat("x", rng.Intn(long))
				}
				s.add(key+"/"+container, stamped(d, msg))
				all = append(all, Record{Timestamp: at(d), Message: msg, Stream: stream, Place: place, ContainerID: containerID("uid-"+name, container, 0), Ordinal: ordinal})
			}
		}
	}
	slices.SortStableFunc(all, CompareLines)
	return s, objects, all
}

// rotateAway drops, from about half of all's streams with three moments or more, the
// lines at random moments, the newest never, as rotations the file never saw. Each
// run of moments left but the newest is the file's alone, and the kubelet serves only
// the newest run. It answers the lines left, in all's order, and each such stream's
// runs the file holds.
func rotateAway(rng *rand.Rand, s *logServer, all []Record) ([]Record, map[StreamKey][][]Record) {
	byStream := map[StreamKey][]Record{}
	var keys []StreamKey
	for _, rec := range all {
		if _, ok := byStream[rec.Stream]; !ok {
			keys = append(keys, rec.Stream)
		}
		byStream[rec.Stream] = append(byStream[rec.Stream], rec)
	}
	dropped := map[StreamKey]map[time.Time]bool{}
	runs := map[StreamKey][][]Record{}
	for _, key := range keys {
		lines := byStream[key]
		var moments []time.Time
		for _, rec := range lines {
			if len(moments) == 0 || !moments[len(moments)-1].Equal(rec.Timestamp) {
				moments = append(moments, rec.Timestamp)
			}
		}
		if len(moments) < 3 || rng.Intn(2) == 0 {
			continue
		}
		dropped[key] = map[time.Time]bool{}
		for _, m := range moments[:len(moments)-1] {
			if rng.Intn(4) == 0 {
				dropped[key][m] = true
			}
		}
		var cur []Record
		var held [][]Record
		for _, rec := range lines {
			if dropped[key][rec.Timestamp] {
				if cur != nil {
					held, cur = append(held, cur), nil
				}
				continue
			}
			cur = append(cur, rec)
		}
		// The newest run is the kubelet's.
		var served []string
		for _, rec := range cur {
			served = append(served, stamped(rec.Timestamp.Sub(at(0)), rec.Message))
		}
		s.mu.Lock()
		s.logs[key.Namespace+"/"+key.Pod+"/"+key.Container] = served
		s.mu.Unlock()
		if len(held) > 0 {
			runs[key] = held
		}
	}
	kept := slices.DeleteFunc(slices.Clone(all), func(rec Record) bool { return dropped[rec.Stream][rec.Timestamp] })
	return kept, runs
}

// loudLines is each line of the kubelet's logs in s whose second carries more than
// limit bytes, by message: a read may leave it out.
func loudLines(s *logServer, limit int64) map[string]StreamKey {
	out := map[string]StreamKey{}
	for key, lines := range s.logs {
		parts := strings.Split(key, "/")
		stream := StreamKey{Namespace: parts[0], Pod: parts[1], Container: parts[2]}
		bytes := map[time.Time]int64{}
		for _, line := range lines {
			rec, _ := parseLine([]byte(line))
			bytes[rec.Timestamp.Truncate(time.Second)] += int64(len(line) + 1)
		}
		for _, line := range lines {
			if rec, _ := parseLine([]byte(line)); bytes[rec.Timestamp.Truncate(time.Second)] > limit {
				out[rec.Message] = stream
			}
		}
	}
	return out
}

// without is recs less the loud lines, with no hole on a record of a stream that
// holds one: a loud second's unread part is a live gap, which no record looks back
// past.
func without(recs []Record, loud map[string]StreamKey) []Record {
	streams := map[StreamKey]bool{}
	for _, key := range loud {
		streams[key] = true
	}
	out := slices.DeleteFunc(slices.Clone(recs), func(rec Record) bool {
		_, ok := loud[rec.Message]
		return ok
	})
	for i := range out {
		if streams[out[i].Stream] {
			out[i].Gap = nil
		}
	}
	return out
}

// ordered is whether recs are in order with none repeated.
func ordered(recs []Record) bool {
	for i := 1; i < len(recs); i++ {
		if CompareLines(recs[i-1], recs[i]) >= 0 {
			return false
		}
	}
	return true
}

// wanted is the lines of kept that pass, each carrying the holes between it and
// the line of its stream before it that passes: wherever all, the lines before the
// rotations, held lines between two neighbours in kept.
func wanted(all, kept []Record, pass func(Record) bool) []Record {
	left := map[string]bool{}
	for _, rec := range kept {
		left[rec.Message] = true
	}
	last := map[StreamKey]*Record{}
	hole := map[StreamKey]bool{}
	pending := map[StreamKey]*Gap{}
	gaps := map[string]*Gap{}
	for _, rec := range all {
		if !left[rec.Message] {
			hole[rec.Stream] = last[rec.Stream] != nil
			continue
		}
		if hole[rec.Stream] {
			pending[rec.Stream] = joined(pending[rec.Stream], &Gap{After: last[rec.Stream].Timestamp, Before: rec.Timestamp})
			hole[rec.Stream] = false
		}
		last[rec.Stream] = &rec
		if pass(rec) {
			gaps[rec.Message], pending[rec.Stream] = pending[rec.Stream], nil
		}
	}
	var out []Record
	for _, rec := range kept {
		if pass(rec) {
			rec.Gap = gaps[rec.Message]
			out = append(out, rec)
		}
	}
	return out
}

// walk reads req's first page and every page on from it the way it reads, and is
// every record, oldest first, each page within its size.
func walk(t *testing.T, svc *Service, req FetchRequest) []Record {
	t.Helper()
	out, _, _ := walkPages(t, svc, req, nil)
	return out
}

// walkPages is walk, each stream a page skipped, and every page in the order read.
// sizes names each continuation's page size and byte cap; nil repeats req's.
func walkPages(t *testing.T, svc *Service, req FetchRequest, sizes func() (int, int)) ([]Record, []string, []FetchResponse) {
	t.Helper()
	newer := req.PageFrom == PageFromFirst || req.PageCursor != nil && cursorIn(t, req.PageCursor).newer
	out := []Record{}
	var skipped []string
	var pages []FetchResponse
	for range 1000 {
		page := fetch(t, svc, req)
		pages = append(pages, page)
		for _, line := range skippedOf(page) {
			skipped = append(skipped, strings.SplitN(line, ":", 2)[0])
		}
		require.LessOrEqual(t, len(page.Records), page.Request.PageSize)
		c := page.Page.Prev
		if newer {
			out, c = append(out, page.Records...), page.Page.Next
		} else {
			out = append(slices.Clone(page.Records), out...)
		}
		if c == nil {
			return out, skipped, pages
		}
		req = FetchRequest{PageCursor: c, PageSize: req.PageSize, MaxPageBytes: req.MaxPageBytes}
		if sizes != nil {
			req.PageSize, req.MaxPageBytes = sizes()
		}
	}
	t.Fatal("the pages never end")
	return nil, nil, nil
}

// inWindow is the lines of want, oldest first, that w holds: those of its range,
// and of each side's padding, its last or first Lines, or those within its Span.
// A nil bound holds every line past it.
func inWindow(want []Record, w Window) []Record {
	var before, in, after []Record
	for _, rec := range want {
		switch {
		case w.Since != nil && rec.Timestamp.Before(*w.Since):
			if w.Before.Span > 0 && !rec.Timestamp.Before(w.Since.Add(-w.Before.Span)) || w.Before.Lines > 0 {
				before = append(before, rec)
			}
		case w.Until != nil && rec.Timestamp.After(*w.Until):
			if w.After.Span > 0 && !rec.Timestamp.After(w.Until.Add(w.After.Span)) || w.After.Lines > 0 {
				after = append(after, rec)
			}
		default:
			in = append(in, rec)
		}
	}
	if n := w.Before.Lines; n > 0 && len(before) > n {
		before = before[len(before)-n:]
	}
	if n := w.After.Lines; n > 0 && len(after) > n {
		after = after[:n]
	}
	return slices.Concat(before, in, after)
}

// web1Scope reads the app container of the pod web-1 in prod.
var web1Scope = Scope{Sources: []ScopeSource{{Namespace: "prod", Kind: SourcePod, Name: "web-1", Containers: []string{"app"}}}}

// fileOf is the log file of f's active cache, for a test to read.
func fileOf(t *testing.T, svc *Service, f *fakeService) *logstore.Store {
	t.Helper()
	file, err := svc.files.OpenOrCreate(int64(f.active.Cache.ID))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, file.Close()) })
	return file
}

// chunksOf is the unlabelled chunks of pod/container's stream of the first instance
// of a fixture pod, in the file behind svc.
func chunksOf(t *testing.T, svc *Service, f *fakeService, pod, container string) []logstore.Chunk {
	t.Helper()
	file := fileOf(t, svc, f)
	rows, err := file.PodStreams(t.Context(), "prod", pod, "")
	require.NoError(t, err)
	for _, row := range rows {
		if row.Container == container && row.ContainerID == containerID("uid-"+pod, container, 0) {
			chunks, err := file.Chunks(t.Context(), row.ID, "")
			require.NoError(t, err)
			return chunks
		}
	}
	return nil
}

// everyPodGone has the sweeps of svc's log files read every pod as gone.
func everyPodGone(svc *Service) {
	svc.files.SetPodsGone(func(_ context.Context, _ int64, uids []string) ([]string, error) { return uids, nil })
}

// rowsOf is the file rows of pod in the file behind svc.
func rowsOf(t *testing.T, svc *Service, f *fakeService, pod string) []logstore.Stream {
	t.Helper()
	rows, err := fileOf(t, svc, f).PodStreams(t.Context(), "prod", pod, "")
	require.NoError(t, err)
	return rows
}

// sweepFile runs one sweep of the file behind svc.
func sweepFile(t *testing.T, svc *Service, f *fakeService) {
	t.Helper()
	require.NoError(t, fileOf(t, svc, f).Sweep(t.Context()))
}

// loudAround is web-1/app and web-2/app each logging a line a second from 10:00:00 to
// 10:00:09, with web-1/app also logging a loud second at 10:00:05, and a service
// reading it under smallCaps.
func loudAround(t *testing.T) (*Service, *logServer) {
	t.Helper()
	s := newLogServer()
	for i := range 10 {
		d := time.Duration(i) * time.Second
		s.add("prod/web-2/app", stamped(d+500*time.Millisecond, "web-2 "+strconv.Itoa(i)))
		s.add("prod/web-1/app", stamped(d, "web-1 "+strconv.Itoa(i)))
		if i == 5 {
			for j := range 12 {
				s.add("prod/web-1/app", stamped(d+time.Duration(1+j)*time.Millisecond, loudLine))
			}
		}
	}
	svc, _ := serviceOver(t, webMirror(t), s)
	svc.caps = smallCaps
	return svc, s
}

// hundredStreams is a mirror of a hundred pods of web, and the key of each one's
// app container.
func hundredStreams(t *testing.T) (*fakeService, []string) {
	t.Helper()
	f := webMirror(t)
	addPods(t, f, 98)
	keys := []string{"prod/web-1/app", "prod/web-2/app"}
	for i := range 98 {
		keys = append(keys, "prod/many-"+strconv.Itoa(i)+"/app")
	}
	return f, keys
}

// fetch is the page req answers on cluster 7.
func fetch(t *testing.T, svc *Service, req FetchRequest) FetchResponse {
	t.Helper()
	resp, err := svc.Fetch(t.Context(), "7", req)
	require.NoError(t, err)
	return resp
}

// goOn is the continuation of c, which must be set, of size lines.
func goOn(t *testing.T, svc *Service, c *string, size int) FetchResponse {
	t.Helper()
	require.NotNil(t, c)
	return fetch(t, svc, FetchRequest{PageCursor: c, PageSize: size})
}

// A request is a first page, from PageFromFirst or PageFromLast, or a continuation, a page
// cursor alone; Seen goes with PageFromFirst and no padding. Anything else is
// conflicting-parameters (rule 6).
func TestAMixedRequestIsRefused(t *testing.T) {
	t.Parallel()
	svc, _ := serviceOver(t, webMirror(t), newLogServer())
	c := encodePageCursor(openCursor(true))
	seen := []string{line("web-1", "app", 0, "").Cursor()}
	for name, req := range map[string]FetchRequest{
		"neither":                 {Scope: webScope},
		"both":                    {Scope: webScope, PageFrom: PageFromFirst, PageCursor: &c},
		"another page":            {Scope: webScope, PageFrom: "middle"},
		"a continuation's scope":  {Scope: webScope, PageCursor: &c},
		"a continuation's window": {Window: Window{Since: moment(0)}, PageCursor: &c},
		"a continuation's seen":   {Seen: seen, PageCursor: &c},
		"seen from the last":      {Scope: webScope, PageFrom: PageFromLast, Seen: seen},
		"seen beside padding":     {Scope: webScope, PageFrom: PageFromFirst, Seen: seen, Window: Window{Since: moment(0), After: Padding{Span: time.Second}}},
	} {
		_, err := svc.Fetch(t.Context(), "7", req)
		assert.Equal(t, CodeConflictingParameters, codeOf(err), name)
	}
}

// A page's size and byte caps are checked on every page.
func TestABadPageIsRefused(t *testing.T) {
	t.Parallel()
	svc, _ := serviceOver(t, webMirror(t), newLogServer())
	for name, req := range map[string]FetchRequest{
		"a negative size":        {PageSize: -1},
		"too large a size":       {PageSize: MaxPageSize + 1},
		"a negative message cap": {MaxMessageBytes: -1},
		"a negative page cap":    {MaxPageBytes: -1},
	} {
		req.Scope, req.PageFrom = webScope, PageFromFirst
		_, err := svc.Fetch(t.Context(), "7", req)
		assert.Equal(t, CodeBadPage, codeOf(err), name)
	}
}

// The request is echoed as it ran: defaults filled, caps applied, and on a
// continuation the scope and window the cursor holds, beside the first page's
// clock (rule 13).
func TestTheRequestIsEchoedAsItRan(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(3)...)
	svc, _ := serviceOver(t, webMirror(t), s)
	w := Window{Since: moment(0)}

	first := fetch(t, svc, FetchRequest{Scope: webScope, Window: w, PageFrom: PageFromFirst, PageSize: 1, MaxMessageBytes: 1 << 30})

	assert.Equal(t, FetchRequest{Scope: webScope, Window: w, PageFrom: PageFromFirst, PageSize: 1, MaxMessageBytes: MaxMessageBytesCap, MaxPageBytes: MaxPageBytesCap}, first.Request)
	assert.Equal(t, svc.now(), first.ResolvedAt)
	assert.NotNil(t, first.Notices)

	cont := fetch(t, svc, FetchRequest{PageCursor: first.Page.Next, MaxPageBytes: 10})
	assert.Equal(t, FetchRequest{Scope: webScope, Window: w, PageCursor: first.Page.Next, PageSize: DefaultPageSize, MaxMessageBytes: MaxMessageBytesCap, MaxPageBytes: 10}, cont.Request)
	assert.Equal(t, first.ResolvedAt, cont.ResolvedAt)
}

// A bound includes every line at its moment, so a range of one nanosecond is the
// lines stamped with it; Since after Until is invalid-range (rule 1).
func TestABoundIncludesItsMoment(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a"), stamped(time.Second, "b"), stamped(time.Second, "b'"), stamped(2*time.Second, "c"))
	svc, _ := serviceOver(t, webMirror(t), s)

	for _, from := range []PageFrom{PageFromFirst, PageFromLast} {
		page := fetch(t, svc, FetchRequest{Scope: webScope, Window: Window{Since: moment(time.Second), Until: moment(time.Second)}, PageFrom: from})
		assert.Equal(t, []string{"b", "b'"}, messages(page.Records), from)
	}
	_, err := svc.Fetch(t.Context(), "7", FetchRequest{Scope: webScope, Window: Window{Since: moment(2 * time.Second), Until: moment(time.Second)}, PageFrom: PageFromFirst})
	assert.Equal(t, CodeInvalidRange, codeOf(err))
}

// PageFromFirst reads the window's oldest lines, PageFromLast its newest, each answered
// oldest first (rule 5).
func TestTheFirstPageComesFromItsEdge(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(5)...)
	svc, _ := serviceOver(t, webMirror(t), s)

	first := fetch(t, svc, FetchRequest{Scope: webScope, PageFrom: PageFromFirst, PageSize: 2})
	last := fetch(t, svc, FetchRequest{Scope: webScope, PageFrom: PageFromLast, PageSize: 2})

	assert.Equal(t, []string{"0", "1"}, messages(first.Records))
	assert.Equal(t, []string{"3", "4"}, messages(last.Records))
}

// A continuation reads on the way its cursor says, never crossing the window's
// edge; Prev is set past a continuation's start, Next while a line is known to be
// newer, and neither past a closed window's end, where no Follow is set either
// (rules 6 and 7).
func TestPagesGoOnByTheirCursors(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(6)...)
	svc, _ := serviceOver(t, webMirror(t), s)
	w := Window{Since: moment(0), Until: moment(4 * time.Second)}

	p1 := fetch(t, svc, FetchRequest{Scope: webScope, Window: w, PageFrom: PageFromFirst, PageSize: 2})
	assert.Equal(t, []string{"0", "1"}, messages(p1.Records))
	assert.Equal(t, [3]bool{false, true, false}, edgesOf(p1.Page))

	p2 := goOn(t, svc, p1.Page.Next, 2)
	assert.Equal(t, []string{"2", "3"}, messages(p2.Records))
	assert.Equal(t, [3]bool{true, true, false}, edgesOf(p2.Page))

	p3 := goOn(t, svc, p2.Page.Next, 2)
	assert.Equal(t, []string{"4"}, messages(p3.Records), "the window ends at 4s")
	assert.Equal(t, [3]bool{true, false, false}, edgesOf(p3.Page))

	back := goOn(t, svc, p3.Page.Prev, 2)
	assert.Equal(t, []string{"2", "3"}, messages(back.Records))
	back = goOn(t, svc, back.Page.Prev, 2)
	assert.Equal(t, []string{"0", "1"}, messages(back.Records))
	assert.Nil(t, back.Page.Prev, "the window starts at 0s")
}

// edgesOf is which of Prev, Next and Follow page sets.
func edgesOf(page Page) [3]bool {
	return [3]bool{page.Prev != nil, page.Next != nil, page.Follow != nil}
}

// A live window's newest page sets Follow, never beside Next, and a poll from it
// answers what was written since, then hands the poll back.
func TestALiveWindowsNewestPageFollows(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(3)...)
	svc, _ := serviceOver(t, webMirror(t), s)

	p1 := fetch(t, svc, FetchRequest{Scope: webScope, PageFrom: PageFromFirst, PageSize: 2})
	assert.Equal(t, [3]bool{false, true, false}, edgesOf(p1.Page))
	p2 := goOn(t, svc, p1.Page.Next, 2)
	assert.Equal(t, []string{"2"}, messages(p2.Records))
	assert.Equal(t, [3]bool{true, false, true}, edgesOf(p2.Page))

	quiet := goOn(t, svc, p2.Page.Follow, 2)
	assert.Empty(t, quiet.Records)
	assert.Equal(t, p2.Page.Follow, quiet.Page.Follow, "a poll that passed nothing hands its cursor back")
	assert.Nil(t, quiet.Page.Prev)

	s.add("prod/web-1/app", stamped(3*time.Second, "3"))
	poll := goOn(t, svc, quiet.Page.Follow, 2)
	assert.Equal(t, []string{"3"}, messages(poll.Records))
	assert.NotNil(t, poll.Page.Follow)
}

// A Prev chain from the window's end reaches its start, and a Next turning back
// re-delivers what lies past the page.
func TestAPrevChainTurnsBack(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(5)...)
	svc, _ := serviceOver(t, webMirror(t), s)

	last := fetch(t, svc, FetchRequest{Scope: webScope, PageFrom: PageFromLast, PageSize: 2})
	assert.Equal(t, []string{"3", "4"}, messages(last.Records))
	assert.Equal(t, [3]bool{true, false, true}, edgesOf(last.Page), "an open window's last page polls")
	p := goOn(t, svc, last.Page.Prev, 2)
	assert.Equal(t, []string{"1", "2"}, messages(p.Records))
	p = goOn(t, svc, p.Page.Prev, 2)
	assert.Equal(t, []string{"0"}, messages(p.Records))
	assert.Equal(t, [3]bool{false, true, false}, edgesOf(p.Page))

	p = goOn(t, svc, p.Page.Next, 2)
	assert.Equal(t, []string{"1", "2"}, messages(p.Records))
}

// A continuation resolves its cursor's scope again: one the service now refuses
// keeps the scope's code, an entry for a stream that left is dropped, and a stream
// that joined reads from the window's start.
func TestAContinuationResolvesItsScopeAgain(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(5)...)
	f := webMirror(t)
	svc, _ := serviceOver(t, f, s)

	bad := openCursor(true)
	bad.Filters = []ScopeFilter{{Field: "color", Values: []string{"red"}}}
	_, err := svc.Fetch(t.Context(), "7", FetchRequest{PageCursor: encoded(bad)})
	assert.Equal(t, CodeBadScope, codeOf(err))

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: Window{Since: moment(2 * time.Second)}, PageFrom: PageFromFirst})
	assert.Equal(t, []string{"2", "3", "4"}, messages(page.Records))
	c, err := decodePageCursor(*page.Page.Follow)
	require.NoError(t, err)
	c.seen[name("gone", "app")] = entry("gone", "app", 0)

	s.add("prod/web-3/app", stamped(time.Second, "too old"), stamped(3*time.Second, "w3"))
	f.store.Apply(t, pod("web-3", "uid-web-3", "node-a", webReplicaSet(), container{name: "app"}))
	poll := fetch(t, svc, FetchRequest{PageCursor: encoded(c)})

	assert.Equal(t, []string{"w3"}, messages(poll.Records), "web-3 reads from the window's start")
	after, err := decodePageCursor(*poll.Page.Follow)
	require.NoError(t, err)
	assert.NotContains(t, after.seen, name("gone", "app"))
	assert.Contains(t, after.seen, name("web-3", "app"))
}

// countedService is a service over web-1's app writing a line a second from 10:00
// to 10:00:19, each saying its second.
func countedService(t *testing.T) *Service {
	t.Helper()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(20)...)
	svc, _ := serviceOver(t, webMirror(t), s)
	return svc
}

// between is the window from 10:00:10 to 10:00:12, padded as given.
func between(before, after Padding) Window {
	return Window{Since: moment(10 * time.Second), Until: moment(12 * time.Second), Before: before, After: after}
}

// cursorIn is the page cursor c decoded.
func cursorIn(t *testing.T, c *string) pageCursor {
	t.Helper()
	require.NotNil(t, c)
	out, err := decodePageCursor(*c)
	require.NoError(t, err)
	return out
}

// A counted Before finds its lines back from Since and starts the window at the
// farthest, setting no edge past it; with fewer to find, the window starts at the
// log's edge and the page says the padding came up short (rule 2).
func TestACountedBeforeStartsAtItsFarthestLine(t *testing.T) {
	t.Parallel()
	svc := countedService(t)

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: between(Padding{Lines: 3}, Padding{}), PageFrom: PageFromFirst})
	assert.Equal(t, []string{"7", "8", "9", "10", "11", "12"}, messages(page.Records))
	assert.Equal(t, [3]bool{false, false, false}, edgesOf(page.Page))
	assert.Empty(t, page.Notices)

	page = fetch(t, svc, FetchRequest{Scope: webScope, Window: between(Padding{Lines: 30}, Padding{}), PageFrom: PageFromFirst})
	assert.Equal(t, messages(linesOf(lineEvery(13))), messages(page.Records))
	assert.Equal(t, []Notice{{CodePaddingShort, "the padding before since holds 10 of the 30 lines asked for"}}, page.Notices)
}

// The grep searches the range alone, and a count counts every line: the padding
// arrives unfiltered before and after the range, read first or last, across a
// continuation over the bound, and under a span (rule 11).
func TestTheGrepLeavesThePaddingAlone(t *testing.T) {
	t.Parallel()
	svc := countedService(t)
	scope := webScope
	scope.Grep = "^[0-9]*[02468]$"
	counted := between(Padding{Lines: 2}, Padding{Lines: 2})

	page := fetch(t, svc, FetchRequest{Scope: scope, Window: counted, PageFrom: PageFromFirst})
	assert.Equal(t, []string{"8", "9", "10", "12", "13", "14"}, messages(page.Records))
	assert.Equal(t, [3]bool{false, false, false}, edgesOf(page.Page))

	page = fetch(t, svc, FetchRequest{Scope: scope, Window: counted, PageFrom: PageFromFirst, PageSize: 3})
	assert.Equal(t, []string{"8", "9", "10"}, messages(page.Records))
	page = goOn(t, svc, page.Page.Next, 3)
	assert.Equal(t, []string{"12", "13", "14"}, messages(page.Records))
	assert.Nil(t, page.Page.Next)

	page = fetch(t, svc, FetchRequest{Scope: scope, Window: counted, PageFrom: PageFromLast, PageSize: 3})
	assert.Equal(t, []string{"12", "13", "14"}, messages(page.Records))
	page = goOn(t, svc, page.Page.Prev, 3)
	assert.Equal(t, []string{"8", "9", "10"}, messages(page.Records))
	assert.Nil(t, page.Page.Prev)

	spanned := between(Padding{Span: 2 * time.Second}, Padding{Span: 2 * time.Second})
	page = fetch(t, svc, FetchRequest{Scope: scope, Window: spanned, PageFrom: PageFromFirst})
	assert.Equal(t, []string{"8", "9", "10", "12", "13", "14"}, messages(page.Records))
}

// A page smaller than its padding ends inside it, and its Next goes on from the
// page's last line with the count of padding still to come; a counted After read
// from the end mirrors it.
func TestAPageSmallerThanItsPaddingGoesOnInsideIt(t *testing.T) {
	t.Parallel()
	svc := countedService(t)

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: between(Padding{Lines: 5}, Padding{}), PageFrom: PageFromFirst, PageSize: 3})
	assert.Equal(t, []string{"5", "6", "7"}, messages(page.Records))
	assert.Nil(t, page.Page.Prev)
	assert.Equal(t, 2, cursorIn(t, page.Page.Next).before)
	page = goOn(t, svc, page.Page.Next, 3)
	assert.Equal(t, []string{"8", "9", "10"}, messages(page.Records))
	assert.Equal(t, 0, cursorIn(t, page.Page.Next).before)
	page = goOn(t, svc, page.Page.Next, 3)
	assert.Equal(t, []string{"11", "12"}, messages(page.Records))
	assert.Nil(t, page.Page.Next)

	page = fetch(t, svc, FetchRequest{Scope: webScope, Window: between(Padding{}, Padding{Lines: 5}), PageFrom: PageFromLast, PageSize: 3})
	assert.Equal(t, []string{"15", "16", "17"}, messages(page.Records))
	assert.Nil(t, page.Page.Next)
	assert.Nil(t, page.Page.Follow, "the count is whole")
	assert.Equal(t, 2, cursorIn(t, page.Page.Prev).after)
	page = goOn(t, svc, page.Page.Prev, 3)
	assert.Equal(t, []string{"12", "13", "14"}, messages(page.Records))
}

// A counted After is delivered across Next pages and closes the window once its
// lines are in: the trimmed page sets neither Next nor Follow.
func TestACountedAfterClosesTheWindow(t *testing.T) {
	t.Parallel()
	svc := countedService(t)

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: between(Padding{}, Padding{Lines: 4}), PageFrom: PageFromFirst, PageSize: 3})
	assert.Equal(t, []string{"10", "11", "12"}, messages(page.Records))
	page = goOn(t, svc, page.Page.Next, 3)
	assert.Equal(t, []string{"13", "14", "15"}, messages(page.Records))
	assert.Equal(t, 3, cursorIn(t, page.Page.Next).after)
	page = goOn(t, svc, page.Page.Next, 3)
	assert.Equal(t, []string{"16"}, messages(page.Records))
	assert.Equal(t, [3]bool{true, false, false}, edgesOf(page.Page))
}

// A Prev chain from the end reads back through a counted Before by its count,
// stopping at its Lines, and a Next turning back re-delivers the padding with the
// count falling to 0.
func TestAPrevChainCountsItsPadding(t *testing.T) {
	t.Parallel()
	svc := countedService(t)

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: between(Padding{Lines: 4}, Padding{}), PageFrom: PageFromLast, PageSize: 2})
	assert.Equal(t, []string{"11", "12"}, messages(page.Records))
	var got []string
	for page.Page.Prev != nil {
		page = goOn(t, svc, page.Page.Prev, 2)
		got = append(messages(page.Records), got...)
		c := cursorIn(t, page.Page.Next)
		assert.True(t, c.before >= 0 && c.before <= 4, "%d", c.before)
	}
	assert.Equal(t, []string{"6", "7", "8", "9", "10"}, got)

	var counts []int
	for page.Page.Next != nil {
		counts = append(counts, cursorIn(t, page.Page.Next).before)
		page = goOn(t, svc, page.Page.Next, 2)
	}
	assert.Equal(t, []string{"11", "12"}, messages(page.Records))
	assert.Equal(t, []int{3, 1, 0}, counts)
}

// A counted Before that came up short starts the window at the log's edge, and a
// Next back into the range brings the count to 0.
func TestAShortBeforeSetsTheStart(t *testing.T) {
	t.Parallel()
	svc := countedService(t)

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: Window{Since: moment(2 * time.Second), Until: moment(3 * time.Second), Before: Padding{Lines: 5}}, PageFrom: PageFromFirst, PageSize: 2})

	assert.Equal(t, []string{"0", "1"}, messages(page.Records))
	c := cursorIn(t, page.Page.Next)
	assert.Equal(t, lineAt(StreamKey{Namespace: "prod", Pod: "web-1", Container: "app"}, 0, 0), c.Start)
	assert.Equal(t, 0, c.before)
	page = goOn(t, svc, page.Page.Next, 2)
	assert.Equal(t, []string{"2", "3"}, messages(page.Records))
}

// A stream with no entry reads from the window's start on a Next, under a counted
// Before from the line the first page's read found, never older.
func TestAStreamWithNoEntryReadsFromTheStart(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(20)...)
	s.add("prod/web-2/app", stamped(5*time.Second, "w2 5"), stamped(8*time.Second, "w2 8"), stamped(11*time.Second, "w2 11"))
	svc, _ := serviceOver(t, webMirror(t), s)

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: between(Padding{Lines: 3}, Padding{}), PageFrom: PageFromFirst, PageSize: 1})
	assert.Equal(t, []string{"8"}, messages(page.Records), "the start is 3 lines back: 8, w2 8 and 9")
	var got []string
	for page.Page.Next != nil {
		page = goOn(t, svc, page.Page.Next, 1)
		got = append(got, messages(page.Records)...)
	}
	assert.Equal(t, []string{"w2 8", "9", "10", "11", "w2 11", "12"}, got)
}

// A first page whose padding read stopped at a loud second starts at the stop:
// its Prev reads the rest of the padding from there, and its Next goes on from
// its last line.
func TestAPaddingReadStoppedAtALoudSecond(t *testing.T) {
	t.Parallel()
	svc, _ := loudAround(t)

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: Window{Since: moment(9 * time.Second), Before: Padding{Lines: 100}}, PageFrom: PageFromFirst})

	assert.Equal(t, []string{"web-2 5", "web-1 6", "web-2 6", "web-1 7", "web-2 7", "web-1 8", "web-2 8", "web-1 9", "web-2 9"}, messages(page.Records))
	assert.Contains(t, page.Notices, Notice{CodeStreamSkipped, "web-1/app: " + loudAt(at(5*time.Second)).Error()})
	prev := cursorIn(t, page.Page.Prev)
	assert.Equal(t, momentAt(at(5*time.Second)), prev.at)
	assert.Equal(t, 7, prev.before)
	older := goOn(t, svc, page.Page.Prev, 100)
	assert.Equal(t, []string{"web-1 0", "web-2 0", "web-1 1", "web-2 1", "web-1 2", "web-2 2", "web-1 3", "web-2 3", "web-1 4", "web-2 4"}, messages(older.Records))
}

// A last page whose padding read past Until stopped at a loud second has its Next
// read on from the stop, the rest of the second left out as a forward read leaves
// it, with the lines found as its count; no line comes twice.
func TestAnAfterReadStoppedAtALoudSecond(t *testing.T) {
	t.Parallel()
	svc, _ := loudAround(t)

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: Window{Since: moment(2 * time.Second), Until: moment(3 * time.Second), After: Padding{Lines: 100}}, PageFrom: PageFromLast})

	assert.Equal(t, []string{"web-1 2", "web-2 2", "web-1 3"}, messages(page.Records[:3]))
	assert.Equal(t, "web-2 5", page.Records[len(page.Records)-1].Message)
	assert.Contains(t, page.Notices, Notice{CodeStreamSkipped, "web-1/app: " + loudAt(at(5*time.Second)).Error()})
	assert.Nil(t, page.Page.Follow)
	c := cursorIn(t, page.Page.Next)
	assert.Equal(t, momentAt(at(6*time.Second-1)), c.at)
	assert.Equal(t, len(page.Records)-3, c.after, "the padding lines found")
	next := goOn(t, svc, page.Page.Next, 100)
	assert.Equal(t, []string{"web-1 6", "web-2 6", "web-1 7", "web-2 7", "web-1 8", "web-2 8", "web-1 9", "web-2 9"}, messages(next.Records))
	assert.True(t, ordered(append(slices.Clone(page.Records), next.Records...)))
}

// A padding read with a stream skipped comes up short as the log's edge would,
// and the page names the stream beside saying so.
func TestAPaddingReadWithASkippedStream(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-2/app", lineEvery(6)...)
	s.setRefusal("prod/web-1/app", &refusal{404, `pods "web-1" not found`})
	svc, _ := serviceOver(t, webMirror(t), s)

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: Window{Since: moment(4 * time.Second), Until: moment(5 * time.Second), Before: Padding{Lines: 10}}, PageFrom: PageFromFirst})

	assert.Equal(t, messages(linesOf(lineEvery(6))), messages(page.Records))
	assert.ElementsMatch(t, []Code{CodePaddingShort, CodeStreamSkipped}, codesOf(page.Notices))
}

// A padding read cut short by a stream skipped on a transient failure has not
// reached the log's edge: the window's start stays to be found, and Prev reads on
// from where the read stopped, finding the skipped stream's lines once it answers.
func TestAPaddingReadCutByATransientFailureGoesOn(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "a0"))
	s.add("prod/web-2/app", lineEvery(6)...)
	s.setRefusal("prod/web-1/app", &refusal{503, "unavailable"})
	svc, _ := serviceOver(t, webMirror(t), s)

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: Window{Since: moment(4 * time.Second), Until: moment(5 * time.Second), Before: Padding{Lines: 10}}, PageFrom: PageFromFirst})

	assert.Equal(t, messages(linesOf(lineEvery(6))), messages(page.Records))
	assert.Equal(t, []Code{CodeStreamSkipped}, codesOf(page.Notices), "the log's edge is not reached")
	require.NotNil(t, page.Page.Prev)
	assert.True(t, cursorIn(t, page.Page.Prev).Start.isZero(), "the window's start is still to be found")

	s.setRefusal("prod/web-1/app", nil)
	prev := goOn(t, svc, page.Page.Prev, 10)
	assert.Equal(t, []string{"a0"}, messages(prev.Records))
}

// codesOf is each notice's code.
func codesOf(notices []Notice) []Code {
	out := make([]Code, len(notices))
	for i, n := range notices {
		out[i] = n.Code
	}
	return out
}

// Padding past an open bound is measured from the log's edge: none before an open
// Since; past an open Until, from the first page's clock, and a first page reaching
// the window's end with no line at or past it says the padding is empty. Only the
// first page says so (rule 3).
func TestPaddingPastAnOpenBound(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(3)...)
	svc, _ := serviceOver(t, webMirror(t), s)

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: Window{Until: moment(time.Second), Before: Padding{Lines: 5}}, PageFrom: PageFromFirst})
	assert.Equal(t, []string{"0", "1"}, messages(page.Records))
	assert.Equal(t, []Code{CodeEmptyPadding}, codesOf(page.Notices))
	page = fetch(t, svc, FetchRequest{Scope: webScope, Window: Window{Until: moment(time.Second), Before: Padding{Lines: 5}}, PageFrom: PageFromFirst, PageSize: 1})
	assert.Equal(t, []Code{CodeEmptyPadding}, codesOf(page.Notices))
	page = goOn(t, svc, page.Page.Next, 1)
	assert.Equal(t, []string{"1"}, messages(page.Records))
	assert.Empty(t, page.Notices, "a continuation does not repeat it")

	page = fetch(t, svc, FetchRequest{Scope: webScope, Window: Window{After: Padding{Span: time.Minute}}, PageFrom: PageFromLast})
	assert.Equal(t, []string{"0", "1", "2"}, messages(page.Records))
	assert.Equal(t, []Code{CodeEmptyPadding}, codesOf(page.Notices))
	assert.Nil(t, page.Request.Until, "the echoed until stays open")
	assert.NotNil(t, page.Page.Follow, "the window ends a minute past now")

	s.add("prod/web-1/app", stamped(time.Hour, "late"))
	page = fetch(t, svc, FetchRequest{Scope: webScope, Window: Window{After: Padding{Lines: 2}}, PageFrom: PageFromFirst})
	assert.Equal(t, []string{"0", "1", "2", "late"}, messages(page.Records))
	assert.Empty(t, page.Notices)
	assert.Equal(t, 1, cursorIn(t, page.Page.Follow).after, "a count short of its lines leaves the window live")
	page = goOn(t, svc, page.Page.Follow, 100)
	assert.Empty(t, page.Records)
	assert.Empty(t, page.Notices, "a poll with nothing new says nothing of lines already delivered")
}

// A record's place in the window is its timestamp against the echoed bounds:
// padding by span reaches the lines that far past each bound (rule 4).
func TestARecordsPlaceIsItsTimestamp(t *testing.T) {
	t.Parallel()
	svc := countedService(t)

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: between(Padding{Span: 2 * time.Second}, Padding{Span: time.Second}), PageFrom: PageFromFirst})

	assert.Equal(t, []string{"8", "9", "10", "11", "12", "13"}, messages(page.Records))
	var padding []string
	for _, rec := range page.Records {
		if rec.Timestamp.Before(*page.Request.Since) || rec.Timestamp.After(*page.Request.Until) {
			padding = append(padding, rec.Message)
		}
	}
	assert.Equal(t, []string{"8", "9", "13"}, padding)
}

// A stream the page could not read is a stream-skipped notice of its own; with
// every stream failed and nothing read, the page is refused as read-failed.
func TestASkippedStreamIsANotice(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-2/app", lineEvery(2)...)
	s.setRefusal("prod/web-1/app", &refusal{404, `pods "web-1" not found`})
	svc, _ := serviceOver(t, webMirror(t), s)

	page := fetch(t, svc, FetchRequest{Scope: webScope, PageFrom: PageFromFirst})
	assert.Equal(t, []Notice{{CodeStreamSkipped, `web-1/app: pods "web-1" not found`}}, page.Notices)

	s.setRefusal("prod/web-2/app", &refusal{404, `pods "web-2" not found`})
	_, err := svc.Fetch(t.Context(), "7", FetchRequest{Scope: webScope, Window: Window{Since: moment(time.Minute)}, PageFrom: PageFromFirst})
	assert.Equal(t, CodeReadFailed, codeOf(err))
}

// A first page's Seen reads the named stream past its line, and every other
// stream, and a stream whose line is older than the window's start, from the
// start.
func TestSeenReadsEachStreamPastItsLine(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(time.Second, "a1"), stamped(3*time.Second, "a3"), stamped(5*time.Second, "a5"))
	s.add("prod/web-2/app", stamped(time.Second, "b1"), stamped(3*time.Second, "b3"), stamped(5*time.Second, "b5"))
	svc, _ := serviceOver(t, webMirror(t), s)
	w := Window{Since: moment(2 * time.Second)}

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: w, PageFrom: PageFromFirst, Seen: []string{line("web-1", "app", 3*time.Second, "").Cursor()}})
	assert.Equal(t, []string{"b3", "a5", "b5"}, messages(page.Records))

	page = fetch(t, svc, FetchRequest{Scope: webScope, Window: w, PageFrom: PageFromFirst, Seen: []string{line("web-1", "app", time.Second, "").Cursor()}})
	assert.Equal(t, []string{"a3", "b3", "a5", "b5"}, messages(page.Records), "a line older than the start reads from the start")

	_, err := svc.Fetch(t.Context(), "7", FetchRequest{Scope: webScope, PageFrom: PageFromFirst, Seen: []string{encodeLineCursor(momentAt(at(0)))}})
	assert.Equal(t, CodeCursorInvalid, codeOf(err), "a moment")
}

// A page's caps apply as it reads: a message is cut and says by how much, a record
// the page cap dropped sets the edge it lay past and is the next page's first, and
// a PageFromLast keeps its newest (rules 8 and 9).
func TestAPagesCapsSetItsEdges(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "aaaa"), stamped(time.Second, "bbbb"), stamped(2*time.Second, "cccc"))
	svc, _ := serviceOver(t, webMirror(t), s)
	w := Window{Since: moment(0), Until: moment(2 * time.Second)}

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: w, PageFrom: PageFromFirst, MaxPageBytes: 8, MaxMessageBytes: 3})
	assert.Equal(t, []string{"aaa", "bbb"}, messages(page.Records))
	assert.Equal(t, 1, page.Records[0].TruncatedBytes)
	page = fetch(t, svc, FetchRequest{PageCursor: page.Page.Next})
	assert.Equal(t, []string{"cccc"}, messages(page.Records))
	assert.Equal(t, 0, page.Records[0].TruncatedBytes)

	page = fetch(t, svc, FetchRequest{Scope: webScope, Window: w, PageFrom: PageFromLast, MaxPageBytes: 8})
	assert.Equal(t, []string{"bbbb", "cccc"}, messages(page.Records))
	page = fetch(t, svc, FetchRequest{PageCursor: page.Page.Prev})
	assert.Equal(t, []string{"aaaa"}, messages(page.Records))
}

// A line that reaches the file after a page was read, stamped inside that page's
// span, is on the next Next page and on no later one: in a stream that had a line
// on the page, one that had none, and one that joined the scope since (rule 12).
func TestAStragglerIsOnTheNextPage(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", stamped(0, "w1 0"), stamped(time.Second, "w1 1"))
	s.add("prod/web-2/app", stamped(2*time.Second, "w2 2"), stamped(3*time.Second, "w2 3"), stamped(4*time.Second, "w2 4"))
	f := webMirror(t)
	f.store.Apply(t, pod("web-3", "uid-web-3", "node-a", webReplicaSet(), container{name: "app"}))
	svc, _ := serviceOver(t, f, s)

	page := fetch(t, svc, FetchRequest{Scope: webScope, PageFrom: PageFromFirst, PageSize: 4})
	require.Equal(t, []string{"w1 0", "w1 1", "w2 2", "w2 3"}, messages(page.Records))

	s.add("prod/web-1/app", stamped(2500*time.Millisecond, "late w1"))
	s.add("prod/web-3/app", stamped(1500*time.Millisecond, "late w3"))
	s.add("prod/web-4/app", stamped(500*time.Millisecond, "late w4"))
	f.store.Apply(t, pod("web-4", "uid-web-4", "node-a", webReplicaSet(), container{name: "app"}))
	page = goOn(t, svc, page.Page.Next, 10)
	assert.Equal(t, []string{"late w4", "late w3", "late w1", "w2 4"}, messages(page.Records))

	for range 2 {
		page = goOn(t, svc, page.Page.Follow, 10)
		assert.Empty(t, page.Records, "no later page holds them")
	}
}

// A poll of a last page's live window answers what each stream wrote since, each
// line once, however the streams' stamps are skewed.
func TestAPollDeliversSkewedLinesOnce(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(3)...)
	s.add("prod/web-2/app", stamped(500*time.Millisecond, "b"))
	svc, _ := serviceOver(t, webMirror(t), s)

	page := fetch(t, svc, FetchRequest{Scope: webScope, PageFrom: PageFromLast, PageSize: 10})
	require.Equal(t, []string{"0", "b", "1", "2"}, messages(page.Records))

	s.add("prod/web-1/app", stamped(20*time.Second, "20"))
	s.add("prod/web-2/app", stamped(15*time.Second, "b15"))
	page = goOn(t, svc, page.Page.Follow, 10)
	assert.Equal(t, []string{"b15", "20"}, messages(page.Records))

	s.add("prod/web-2/app", stamped(16*time.Second, "b16"))
	page = goOn(t, svc, page.Page.Follow, 10)
	assert.Equal(t, []string{"b16"}, messages(page.Records), "behind the newest line read, and still delivered")

	page = goOn(t, svc, page.Page.Follow, 10)
	assert.Empty(t, page.Records)
	assert.NotNil(t, page.Page.Follow)
}

// A counted After fills by polls as its lines arrive: the poll that finds the
// last of them sets no Follow, and a late line in the range, written meanwhile,
// is delivered and counts toward nothing (rule 7).
func TestACountedAfterFillsByPolls(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(31)...)
	svc, _ := serviceOver(t, webMirror(t), s)
	addSeconds := func(from, to int) {
		for i := from; i < to; i++ {
			s.add("prod/web-1/app", stamped(time.Duration(i)*time.Second, strconv.Itoa(i)))
		}
	}

	page := fetch(t, svc, FetchRequest{Scope: webScope, Window: Window{Until: moment(10 * time.Second), After: Padding{Lines: 100}}, PageFrom: PageFromFirst, PageSize: 500})
	require.Len(t, page.Records, 31)
	assert.Equal(t, 20, cursorIn(t, page.Page.Follow).after)

	addSeconds(31, 71)
	page = goOn(t, svc, page.Page.Follow, 500)
	assert.Len(t, page.Records, 40)
	assert.Equal(t, 60, cursorIn(t, page.Page.Follow).after)

	s.add("prod/web-2/app", stamped(5*time.Second, "late"))
	addSeconds(71, 116)
	page = goOn(t, svc, page.Page.Follow, 500)
	assert.Equal(t, "late", page.Records[0].Message)
	assert.Equal(t, messages(linesOf(lineEvery(111)))[71:], messages(page.Records[1:]), "the forty to the hundredth, and no more")
	assert.Equal(t, [3]bool{true, false, false}, edgesOf(page.Page))
}

// A page cursor holds nothing of the file: once a clear refills it, a Next answers
// what the same Next answered before.
func TestANextAnswersTheSameAcrossAClear(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	s.add("prod/web-1/app", lineEvery(10)...)
	s.add("prod/web-2/app", stamped(2500*time.Millisecond, "b"))
	svc, f := serviceOver(t, webMirror(t), s)

	page := fetch(t, svc, FetchRequest{Scope: webScope, PageFrom: PageFromFirst, PageSize: 3})
	before := goOn(t, svc, page.Page.Next, 3)
	require.NoError(t, svc.Clear(int64(f.active.Cache.ID)))
	after := goOn(t, svc, page.Page.Next, 3)

	assert.Equal(t, []string{"b", "3", "4"}, messages(before.Records))
	assert.Equal(t, before.Records, after.Records)
	assert.Equal(t, before.Page, after.Page)
}

// A scope of MaxStreams live pods beside a gone pod whose logs the file keeps
// pages on through cursors that name every container, more than MaxStreams.
func TestAScopePastMaxStreamsPagesOn(t *testing.T) {
	t.Parallel()
	s := newLogServer()
	f, keys := hundredStreams(t)
	require.Len(t, keys, MaxStreams)
	var want []string
	for i, key := range keys {
		s.add(key, stamped(time.Duration(i)*time.Second, "line "+strconv.Itoa(i)))
		want = append(want, "line "+strconv.Itoa(i))
	}
	svc, _ := serviceOver(t, f, s)
	seed(t, svc, f, rowOf("old-1", "app"), logstore.Fill{Lines: []logstore.Line{{Pos: sec(1000), Message: "gone"}}, Head: true})
	want = append(want, "gone")
	scope := Scope{Sources: append(slices.Clone(webScope.Sources), ScopeSource{Namespace: "prod", Kind: SourcePod, Name: "old-1", Containers: []string{"app"}})}

	got := pagesOn(t, svc, fetch(t, svc, FetchRequest{Scope: scope, PageFrom: PageFromFirst, PageSize: 60}), 60)

	assert.Equal(t, want, recordsOf(got, PageFromFirst))
	last := got[len(got)-1]
	assert.Len(t, cursorIn(t, last.Page.Follow).seen, MaxStreams+1)
	assert.Empty(t, goOn(t, svc, last.Page.Follow, 60).Records)
}
