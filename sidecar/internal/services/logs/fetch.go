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
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// MaxLimit is the most lines one page holds.
const MaxLimit = 500

// A page's fills' bounds: the bytes they read from the backends across its streams,
// shared evenly so no stream is starved by the others, and the GETs each read may
// send. An ordinary page never reaches either; a rare grep read backward does.
const (
	maxBudget  = 32 << 20
	maxWindows = 16
)

// FetchMode is which way a page reads: Head oldest first after its cursor, Tail
// newest first before it, Around both ways from a moment. The GraphQL LogsFetchMode
// enum binds member by member onto it.
type FetchMode string

const (
	FetchHead   FetchMode = "head"
	FetchTail   FetchMode = "tail"
	FetchAround FetchMode = "around"
)

// modeNames is each mode as the wire spells it.
var modeNames = map[FetchMode]string{FetchHead: "Head", FetchTail: "Tail", FetchAround: "Around"}

// FetchQuery is which page of a view to read: its mode, the cursor a Head or Tail
// page goes on from, nil to read from the start or the end, the moment an Around
// page reads both ways from, and the lines the page holds, on each side of At for
// Around.
type FetchQuery struct {
	Mode   FetchMode
	Cursor *string
	At     *time.Time
	Limit  int
}

// Page is a page of a view's lines, oldest first, whatever the mode. Before and
// After are where the next page each way goes on from, Anchor is an Around page's
// index of its first record at or after At, and Skipped names each stream the page
// could not read, one line each.
type Page struct {
	Records []Record
	Before  PageEdge
	After   PageEdge
	Anchor  *int
	Skipped []string
}

// PageEdge is one edge of a page: the cursor the next page that way goes on from,
// nil on a Before edge with nothing older, and whether there is more that way.
type PageEdge struct {
	Cursor *string
	More   bool
}

// Fetch is one page of view's lines on cluster id, answered from the cluster cache's
// log file after filling what the page needs of it from the backends. The sources
// are resolved inside one reading of the cluster's mirror; the reads run outside it,
// over a claim on the server the pods came from. Its edges follow the log viewer
// README's table.
func (s *Service) Fetch(ctx context.Context, id cluster.ClusterID, view View, q FetchQuery) (Page, error) {
	if q.Limit < 1 || q.Limit > MaxLimit {
		return Page{}, Refusal{fmt.Sprintf("a limit must be between 1 and %d", MaxLimit)}
	}
	if err := checkQuery(q); err != nil {
		return Page{}, err
	}
	grep, err := compileGrep(view.Grep)
	if err != nil {
		return Page{}, err
	}
	if q.Mode == FetchAround {
		moment := position{Timestamp: *q.At}
		ms, err := s.page(ctx, id,
			pageQuery{view, grep, FetchTail, bound{moment, false}, q.Limit},
			pageQuery{view, grep, FetchHead, bound{moment, true}, q.Limit})
		if err != nil {
			return Page{}, err
		}
		return around(ms[0], ms[1]), nil
	}
	from, err := boundAfter(q.Cursor)
	if err != nil {
		return Page{}, err
	}
	ms, err := s.page(ctx, id, pageQuery{view, grep, q.Mode, from, q.Limit})
	if err != nil {
		return Page{}, err
	}
	m := ms[0]
	out := Page{Records: m.records, Skipped: m.skipped}
	given := PageEdge{Cursor: q.Cursor, More: q.Cursor != nil}
	switch {
	case q.Mode == FetchHead:
		out.Before, out.After = given, m.after()
	case q.Cursor != nil:
		out.Before, out.After = m.before(), given
	default:
		out.Before, out.After = m.before(), m.liveEnd()
	}
	return out, nil
}

// around is an Around page of its Tail half before the moment and its Head half
// from it on. Both halves read one resolve of the view, so they read the same
// streams.
func around(tail, head merge) Page {
	anchor := len(tail.records)
	skipped := tail.skipped
	for _, line := range head.skipped {
		if !slices.Contains(skipped, line) {
			skipped = append(skipped, line)
		}
	}
	return Page{
		Records: append(tail.records, head.records...),
		Before:  tail.before(),
		After:   head.after(),
		Anchor:  &anchor,
		Skipped: skipped,
	}
}

// checkQuery refuses a query whose arguments its mode does not take: a cursor is only
// ever text a page handed back, and at only the Around anchor's moment.
func checkQuery(q FetchQuery) error {
	switch q.Mode {
	case FetchAround:
		if q.At == nil {
			return Refusal{"an Around page needs at"}
		}
		if q.Cursor != nil {
			return Refusal{"an Around page takes no cursor"}
		}
	case FetchHead, FetchTail:
		if q.At != nil {
			return Refusal{fmt.Sprintf("a %s page takes no at", modeNames[q.Mode])}
		}
	default:
		return Refusal{fmt.Sprintf("Kstack cannot read a page of mode %q", q.Mode)}
	}
	return nil
}

// pageQuery is what one direction's page reads: the view and its compiled grep, the
// direction, the bound it reads from, zero for the start or the end, and its limit.
type pageQuery struct {
	view  View
	grep  *regexp.Regexp
	mode  FetchMode
	from  bound
	limit int
}

// need is the lines a stream answers: one past the limit, so the merge can tell a
// page that was cut.
func (q pageQuery) need() int { return q.limit + 1 }

// key is where the page starts in ps: the one cursor read as that stream's bound.
func (q pageQuery) key(ps pageStream) logstore.Pos { return streamBound(q.from, ps.instance(), q.mode) }

// page is a page of a view in progress, read one or more ways: a Head or Tail page,
// or the two halves of an Around page. It files the resolve's streams and the
// file's, fills the gaps each resolve stream needs from the backends for every
// direction, checks the reads against the mirror and writes them in one
// transaction, then answers each direction with one walk of the file per stream,
// merged.
type page struct {
	s       *Service
	id      cluster.ClusterID
	t       target
	file    *logstore.Store
	streams []pageStream
	// backends is the cluster's backends by name, in the order a gap is offered to
	// them, and walkShare each stream's share of the walks' budget.
	backends  []string
	walkShare int64
}

// page is one merge per query of a view on cluster id, every query over one resolve
// of it, one claim and one write. The queries share their view.
func (s *Service) page(ctx context.Context, id cluster.ClusterID, qs ...pageQuery) ([]merge, error) {
	t, err := resolve(ctx, s.clusters, id, qs[0].view, false)
	if err != nil {
		return nil, err
	}
	file, err := s.files.OpenOrCreate(int64(t.cacheID))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	streams, err := pageStreams(ctx, file, t, qs[0].view)
	if err != nil {
		return nil, err
	}
	out := make([]merge, len(qs))
	if len(streams) == 0 {
		if t.noNodes != nil {
			return nil, t.noNodes
		}
		for i := range out {
			out[i] = merge{records: []Record{}, skipped: []string{}, end: position{Timestamp: s.now()}}
		}
		return out, nil
	}
	p := &page{s: s, id: id, t: t, file: file, streams: streams, walkShare: s.walkBudget / int64(len(streams))}
	for _, b := range s.backends {
		p.backends = append(p.backends, b.name)
	}
	fills, err := p.fill(ctx, qs)
	if err != nil {
		return nil, err
	}
	if err := checkInstances(ctx, s.clusters, id, streams, fills); err != nil {
		return nil, err
	}
	var writes []logstore.Fill
	for _, byStream := range fills {
		for _, f := range byStream {
			writes = append(writes, f.fills...)
		}
	}
	if len(writes) > 0 {
		if err := file.Write(ctx, writes); err != nil {
			return nil, err
		}
	}
	for i, q := range qs {
		if out[i], err = p.answer(ctx, q, fills[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// backendsOf is the backends that serve ps, by name: none for a stream the file
// alone holds.
func (p *page) backendsOf(ps pageStream) []string {
	if ps.exhausted() {
		return nil
	}
	return p.backends
}

// fill fills each stream the resolve handed out for each query in turn, over one
// claim on the server the pods came from. A Head query after a Tail one from the
// same bound, an Around page's halves, starts each stream's read at the bound from
// the Tail fill's newest line, so the two chunks join there.
func (p *page) fill(ctx context.Context, qs []pageQuery) ([][]streamFill, error) {
	fills := make([][]streamFill, len(qs))
	if len(p.t.streams) == 0 {
		for i := range fills {
			fills[i] = make([]streamFill, len(p.streams))
		}
		return fills, nil
	}
	c, err := acquireClaim(ctx, p.s.clusters, p.id, p.t.serverUID)
	if err != nil {
		return nil, err
	}
	defer c.close()
	backends := make([]Backend, len(p.s.backends))
	for i, b := range p.s.backends {
		backends[i] = b.build(c.get)
	}
	var seams []*logstore.Pos
	for i, q := range qs {
		if fills[i], err = p.fillQuery(ctx, q, backends, seams); err != nil {
			return nil, err
		}
		seams = nil
		if q.mode == FetchTail {
			seams = p.seams(fills[i])
		}
	}
	return fills, nil
}

// fillQuery fills each stream for q, each under its share of the fills' budget, all
// at once. seams is each stream's line just before q's bound, nil for none. The
// first fill to fail ends the others, since its error refuses the page.
func (p *page) fillQuery(ctx context.Context, q pageQuery, backends []Backend, seams []*logstore.Pos) ([]streamFill, error) {
	fills := make([]streamFill, len(p.streams))
	share := p.s.budget / int64(len(p.t.streams))
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	for i, ps := range p.streams {
		if ps.exhausted() {
			continue
		}
		f := &filler{file: p.file, ps: ps, backends: backends, names: p.backends, q: q, budget: newBudget(share), walk: newBudget(p.walkShare)}
		if seams != nil {
			f.seam = seams[i]
		}
		wg.Go(func() {
			var err error
			if fills[i], err = f.run(ctx, q.key(ps)); err != nil {
				cancel(err)
			}
		})
	}
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return fills, nil
}

// seams is each stream's newest line the first backend read back from a Tail
// query's bound, nil where it read none: the backend holds nothing between it and
// the bound, so a Head read from the bound continues its chunk.
func (p *page) seams(fills []streamFill) []*logstore.Pos {
	out := make([]*logstore.Pos, len(fills))
	for i, f := range fills {
		if !f.fromKey {
			continue
		}
		for _, fill := range f.fills {
			if fill.Origin == p.backends[0] && len(fill.Lines) > 0 {
				newest := fill.Lines[len(fill.Lines)-1].Pos
				out[i] = &newest
				break
			}
		}
	}
	return out
}

// answer walks each stream of the file from q's bound in its direction, in one read
// transaction, and merges the walks into the page. The live end is the newest line
// the file holds of any stream, or now when it holds none.
func (p *page) answer(ctx context.Context, q pageQuery, fills []streamFill) (merge, error) {
	readings := make([]*streamRead, len(p.streams))
	var end position
	left := false
	err := p.file.Read(ctx, func(r logstore.Reader) error {
		for i, ps := range p.streams {
			w := walker{r: r, ps: ps, names: p.backendsOf(ps), q: q, budget: newBudget(p.walkShare), fill: fills[i]}
			walked, err := w.walk(q.key(ps))
			if err != nil {
				return err
			}
			readings[i] = walked.reading
			left = left || walked.left
			if walked.newest == nil {
				continue
			}
			if at := positionAt(ps.instance(), *walked.newest); end.isZero() || comparePositions(at, end) > 0 {
				end = at
			}
		}
		return nil
	})
	if err != nil {
		return merge{}, err
	}
	m, err := mergePage(ctx, readings, q.mode, q.limit)
	if err != nil {
		return merge{}, err
	}
	m.skipped = []string{}
	for i, f := range fills {
		if f.err != nil {
			m.skipped = append(m.skipped, skippedLine(p.streams[i].key, f.err))
		}
	}
	if len(m.skipped) == len(p.t.streams) && len(m.records) == 0 {
		if err := everyStreamFailed(m.skipped); err != nil {
			return merge{}, err
		}
	}
	// The live end names the newest line, so a line another stream writes at its
	// moment is past it when that stream sorts after the line's.
	m.end = end
	if end.isZero() {
		m.end = position{Timestamp: p.s.now()}
	}
	// A skipped stream does not cut the page, but the page reads to the end of its
	// direction only once every stream has nothing left that way: the next page goes
	// on from the farthest line this one answered. With none, the next page would be
	// this one, so the page is refused instead.
	if m.cut.isZero() && left {
		if len(m.records) == 0 {
			way := "newer"
			if q.mode == FetchTail {
				way = "older"
			}
			return merge{}, Refusal{"Kstack could not read " + way + " lines of this view: " + strings.Join(m.skipped, "; ")}
		}
		m.cut = positionOf(m.records[len(m.records)-1])
		if q.mode == FetchTail {
			m.cut = positionOf(m.records[0])
		}
	}
	return m, nil
}

// MaxGrep is the longest grep a view takes, in bytes. The wire takes any view the
// webview sends, so the service checks it, and the LogsView tool's parse reads it.
const MaxGrep = 1_000

// compileGrep is a view's grep, nil for none, refusing one past MaxGrep or one
// that does not compile.
func compileGrep(grep string) (*regexp.Regexp, error) {
	switch {
	case grep == "":
		return nil, nil
	case len(grep) > MaxGrep:
		return nil, Refusal{fmt.Sprintf("a grep is at most %d bytes", MaxGrep)}
	}
	re, err := regexp.Compile(grep)
	if err != nil {
		return nil, Refusal{"the grep is not a regular expression"}
	}
	return re, nil
}

// matches is whether message passes grep, nil passing every line.
func matches(grep *regexp.Regexp, message string) bool {
	return grep == nil || grep.MatchString(message)
}
