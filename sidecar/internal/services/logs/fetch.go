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
	"errors"
	"fmt"
	"log/slog"
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

// maxBudget is the bytes a page's fills read from the backends across its streams,
// shared evenly so no stream is starved by the others. An ordinary page never
// reaches it; a rare grep read backward does.
const maxBudget = 32 << 20

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
// Around. Since and Until are a Head or Tail page's range, in place of Cursor: each
// a moment, whose lines are in it, or a line a page handed back, which is not. A
// Head page reads from Since and stops at Until; a Tail page reads back from Until
// and stops at Since.
type FetchQuery struct {
	Mode   FetchMode
	Cursor *string
	At     *time.Time
	Since  *string
	Until  *string
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
		// The half after the moment fills first, so the half before reads back at
		// the rate of its lines.
		moment := position{Timestamp: *q.At}
		ms, err := s.page(ctx, id,
			pageQuery{view, grep, FetchHead, bound{moment, true}, bound{}, q.Limit},
			pageQuery{view, grep, FetchTail, bound{moment, false}, bound{}, q.Limit})
		if err != nil {
			return Page{}, err
		}
		return around(ms[1], ms[0]), nil
	}
	from, to, near, err := bounds(q)
	if err != nil {
		return Page{}, err
	}
	ms, err := s.page(ctx, id, pageQuery{view, grep, q.Mode, from, to, q.Limit})
	if err != nil {
		return Page{}, err
	}
	m := ms[0]
	out := Page{Records: m.records, Skipped: m.skipped}
	given := PageEdge{Cursor: near, More: near != nil}
	switch {
	case q.Mode == FetchHead:
		out.Before, out.After = given, m.after()
	case near != nil:
		out.Before, out.After = m.before(), given
	default:
		out.Before, out.After = m.before(), m.liveEnd()
	}
	return out, nil
}

// bounds is where a Head or Tail page starts and stops, and the cursor it starts
// from, nil for the start or the end: its Cursor, or its range's near and far ends,
// Since and Until for a Head page and the reverse for a Tail page.
func bounds(q FetchQuery) (from, to bound, near *string, err error) {
	if q.Since == nil && q.Until == nil {
		from, err = boundAfter(q.Cursor)
		return from, bound{}, q.Cursor, err
	}
	near, far := q.Since, q.Until
	if q.Mode == FetchTail {
		near, far = far, near
	}
	if from, err = boundIn(near); err != nil {
		return bound{}, bound{}, nil, err
	}
	to, err = boundIn(far)
	return from, to, near, err
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
// ever text a page handed back, at only the Around anchor's moment, and a range only
// a Head or Tail page's, in place of a cursor.
func checkQuery(q FetchQuery) error {
	switch q.Mode {
	case FetchAround:
		if q.At == nil {
			return Refusal{"an Around page needs at"}
		}
		if q.Cursor != nil {
			return Refusal{"an Around page takes no cursor"}
		}
		if q.Since != nil || q.Until != nil {
			return Refusal{"an Around page takes no since or until"}
		}
	case FetchHead, FetchTail:
		if q.At != nil {
			return Refusal{fmt.Sprintf("a %s page takes no at", modeNames[q.Mode])}
		}
		if q.Cursor != nil && (q.Since != nil || q.Until != nil) {
			return Refusal{fmt.Sprintf("a %s page takes a cursor or a range", modeNames[q.Mode])}
		}
	default:
		return Refusal{fmt.Sprintf("Kstack cannot read a page of mode %q", q.Mode)}
	}
	return nil
}

// pageQuery is what one direction's page reads: the view and its compiled grep, the
// direction, the bound it reads from, zero for the start or the end, the bound it
// stops at, zero for none, and its limit.
type pageQuery struct {
	view  View
	grep  *regexp.Regexp
	mode  FetchMode
	from  bound
	to    bound
	limit int
}

// need is the lines a stream answers: one past the limit, so the merge can tell a
// page that was cut.
func (q pageQuery) need() int { return q.limit + 1 }

// key is where the page starts in ps: the one cursor read as that stream's bound.
func (q pageQuery) key(ps pageStream) logstore.Pos { return streamBound(q.from, ps.instance(), q.mode) }

// far is where the page stops in ps, the first place past its range there, nil
// with no far bound: q.to read as the bound of a page the other way.
func (q pageQuery) far(ps pageStream) *logstore.Pos {
	if q.to.at.isZero() {
		return nil
	}
	back := FetchTail
	if q.mode == FetchTail {
		back = FetchHead
	}
	far := streamBound(q.to, ps.instance(), back)
	return &far
}

// page is a page of a view in progress, read one or more ways: a Head or Tail page,
// or the two halves of an Around page. It files the resolve's streams and the
// file's, fills the gaps each resolve stream needs from the backends for every
// direction, writing each window of a read once it is whole, then answers each
// direction with one walk of the file per stream, merged.
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
// of it and one claim. The queries share their view.
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
	streams, release, err := pageStreams(ctx, file, t, qs[0].view)
	defer release()
	if err != nil {
		return nil, err
	}
	for i := range streams {
		streams[i].tail, streams[i].committed = s.tailOf(t.cacheID, streams[i].row.ID)
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
	wrote := false
	for i := range streams {
		restarted := false
		for _, byStream := range fills {
			if byStream[i].gone {
				streams[i].st = nil
			}
			wrote = wrote || byStream[i].wrote
			restarted = restarted || errors.Is(byStream[i].err, errRestarted)
		}
		// A page answers once for each stream, so a restart one direction found skips
		// it in every direction.
		if restarted {
			for _, byStream := range fills {
				byStream[i] = streamFill{err: errRestarted}
			}
		}
	}
	if wrote && s.afterFill != nil {
		s.afterFill()
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
// claim on the server the pods came from. A Tail query after a Head one from the
// same bound, an Around page's halves, links each stream's read back from the bound
// up to the Head fill's first line, so the two chunks join there.
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
		if q.mode == FetchHead {
			seams = p.seams(fills[i])
		}
	}
	return fills, nil
}

// fillQuery fills each stream for q, each under its share of the fills' budget, all
// at once. seams is each stream's line just past q's bound, nil for none. The
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
		f := &filler{
			file: p.file, clusters: p.s.clusters, id: p.id, save: p.save, ret: p.s.files.Retention(), ps: ps, backends: backends, names: p.backends,
			q: q, budget: newBudget(share), walk: newBudget(p.walkShare),
		}
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

// save writes one batch's fills, stamped: a sweep between the write and the page's
// walk would otherwise take what it fetched, and the stamp keeps the read's lines
// past the page for the next scroll.
func (p *page) save(ctx context.Context, fills []logstore.Fill) (logstore.Written, error) {
	return p.file.Write(ctx, fills, true)
}

// seams is each stream's line a Head query's fill proved nothing lies before past
// its bound, nil where it proved none: a Tail read back from the bound links up to it.
func (p *page) seams(fills []streamFill) []*logstore.Pos {
	out := make([]*logstore.Pos, len(fills))
	for i, f := range fills {
		out[i] = f.up
	}
	return out
}

// answer walks each stream of the file from q's bound in its direction, in one read
// transaction, and merges the walks into the page. The live end is the newest line
// the file holds of any stream, or now when it holds none.
func (p *page) answer(ctx context.Context, q pageQuery, fills []streamFill) (merge, error) {
	readings := make([]*streamRead, len(p.streams))
	var end position
	var read []logstore.Chunk
	left := false
	err := p.file.Read(ctx, func(r logstore.Reader) error {
		for i, ps := range p.streams {
			w := walker{
				r: r, ps: ps, names: p.backendsOf(ps), q: q, budget: newBudget(p.walkShare), fill: fills[i],
				look: p.s.files.Retention().LookBytes,
			}
			walked, err := w.walk(q.key(ps))
			if err != nil {
				return err
			}
			readings[i] = walked.reading
			read = append(read, walked.read...)
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
	// A stamp is what keeps the chunks a viewer reads out of eviction; one that fails
	// costs a refill later, never this page.
	if err := p.file.Stamp(ctx, read); err != nil {
		slog.Warn("logs: a page's chunks could not be stamped", "err", err)
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
