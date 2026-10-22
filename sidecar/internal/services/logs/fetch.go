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
	"strings"
	"sync"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// A page's limits.
const (
	// MaxPageSize is the most lines one page holds, and DefaultPageSize the lines a
	// page holds when the request names none.
	MaxPageSize     = 500
	DefaultPageSize = 100
	// MaxPageBytesCap is the most a page's messages may sum to.
	MaxPageBytesCap = 1 << 20
	// MaxMessageBytesCap is the longest message a page carries: the file's own cap
	// plus the ellipsis clean writes, so a line the file cut arrives whole at the
	// default cap.
	MaxMessageBytesCap = maxMessage + len("…")
	// MaxContextSpan is the most a span of padding may be.
	MaxContextSpan = 7 * 24 * time.Hour
)

// PageFrom is where a window's first page comes from.
type PageFrom string

const (
	PageFromFirst PageFrom = "first" // the window's oldest lines
	PageFromLast  PageFrom = "last"  // the window's newest lines
)

// Padding is how far a window extends past one end of its range, to show the
// lines around it: a number of lines or a span of time. At most one is set; both
// zero means none.
type Padding struct {
	Lines int
	Span  time.Duration
}

// Window is when a read looks: Since and Until, the range's bounds, each a moment
// that includes every line at it, nil for open; and Before and After, its padding.
type Window struct {
	Since, Until  *time.Time
	Before, After Padding
}

// Code is one of the block below: why a request was refused, or what a page or a
// follow wants its caller to know. The wire and the tools pass it through unchanged.
type Code string

// The codes refusals and notices carry.
const (
	// CodeCursorInvalid is a page cursor this version of the service did not write,
	// or a Seen entry that is not a line cursor.
	CodeCursorInvalid Code = "cursor-invalid"
	// CodeConflictingParameters is a request whose arguments do not go together.
	CodeConflictingParameters Code = "conflicting-parameters"
	// CodeInvalidRange is a Since after its Until.
	CodeInvalidRange Code = "invalid-range"
	// CodeBadGrep is a grep that does not compile, or one over MaxGrep.
	CodeBadGrep Code = "bad-grep"
	// CodeBadPage is a PageSize outside 0 to MaxPageSize, or a negative byte cap.
	CodeBadPage Code = "bad-page"
	// CodeBadPadding is a Padding past its limits.
	CodeBadPadding Code = "bad-padding"
	// CodeBadScope is a filter field or a source kind the service cannot read.
	CodeBadScope Code = "bad-scope"
	// CodeNotMirrored, CodeNoNodes and CodeTooMany are a scope the mirror refuses:
	// Unmirrored, NoNodes, and more streams than the service reads at once.
	CodeNotMirrored Code = "not-mirrored"
	CodeNoNodes     Code = "no-nodes"
	CodeTooMany     Code = "too-many"
	// CodeNoContainer is a container the tools' scope check finds in no pod. The
	// service never raises it.
	CodeNoContainer Code = "no-container"
	// CodeNoCache, CodeSyncing and CodeRevoked are a cluster with no cache, one still
	// syncing, and one Kstack no longer connects to.
	CodeNoCache Code = "no-cache"
	CodeSyncing Code = "syncing"
	CodeRevoked Code = "revoked"
	// CodeReplaced is a connection replaced while the page was read: the one refusal
	// a caller sends again as it came.
	CodeReplaced Code = "replaced"
	// CodeUnauthorized is the cluster refusing to show a log.
	CodeUnauthorized Code = "unauthorized"
	// CodeFinished is a scope whose every container stopped, or whose Pod sources
	// are all gone.
	CodeFinished Code = "finished"
	// CodeReadFailed is a scope no container of which could be read.
	CodeReadFailed Code = "read-failed"
	// CodeTooLoud is a second of more than a mebibyte of log. It never refuses a
	// page: it reaches a caller inside a stream-skipped notice.
	CodeTooLoud Code = "too-loud"

	// CodeEmptyContext is padding asked past an open bound that found no line.
	CodeEmptyContext Code = "empty-context"
	// CodeContextShort is a counted Before that found fewer lines than it asked for.
	CodeContextShort Code = "context-short"
	// CodePageByteLimitExceeded is a page whose first line alone is over MaxPageBytes.
	CodePageByteLimitExceeded Code = "page-byte-limit-exceeded"
	// CodeStreamSkipped is a stream of the scope the page could not read.
	CodeStreamSkipped Code = "stream-skipped"
)

// Notice is something this page wants the caller to know: a Code, and a Message
// for people. It is about this page alone; a continuation carries none forward,
// and raises its own.
type Notice struct {
	Code    Code
	Message string
}

// FetchRequest is one page. A first page sets Scope, what is read, Window, when, and
// PageFrom; with PageFromFirst it may set Seen, the last line the caller has of each
// stream, so that stream reads past its line and every other from the window's
// start. A continuation sets PageCursor alone, one of a page's Prev, Next or
// Follow as it came: the cursor holds the scope and the window. PageSize is the
// lines a page holds, 1 to MaxPageSize, 0 for DefaultPageSize. MaxMessageBytes
// cuts each message, 0 for the service's own cap. MaxPageBytes caps the sum of a
// page's messages, 0 for MaxPageBytesCap. The three are read on every page.
type FetchRequest struct {
	Scope
	Window
	Seen       []string
	PageFrom   PageFrom
	PageCursor *string

	PageSize        int
	MaxMessageBytes int
	MaxPageBytes    int
}

// Page is the page's place in the window, each a page cursor, nil where there is
// none: Prev goes on toward older lines, Next toward newer, and Follow polls a
// live window's end for lines written since.
type Page struct {
	Prev   *string
	Next   *string
	Follow *string
}

// FetchResponse is one page of the window, oldest first. Request is the request as it
// ran: defaults filled, caps applied, and for a continuation the Scope and Window
// the page cursor holds. ResolvedAt is the first page's clock. Notices is never
// nil: empty with nothing to say.
type FetchResponse struct {
	Request    FetchRequest
	ResolvedAt time.Time
	Page       Page
	Records    []Record
	Notices    []Notice
}

// direction is which way a read goes: newer reads oldest first past its key, older
// newest first before it.
type direction string

const (
	newer direction = "newer"
	older direction = "older"
)

// back is the other way.
func (d direction) back() direction {
	if d == newer {
		return older
	}
	return newer
}

// pageQuery is what one direction's page reads: the scope and its compiled grep,
// the direction, the page cursor that says where each stream starts and which
// edge it stops at, nil to read every stream from its start or its end, and the
// limit.
type pageQuery struct {
	scope Scope
	grep  *regexp.Regexp
	mode  direction
	cur   *pageCursor
	limit int
}

// need is the lines a stream answers: one past the limit, so the merge can tell a
// page that was cut.
func (q pageQuery) need() int { return q.limit + 1 }

// full is whether a stream holding n lines, past of them past the range's end,
// holds what the page needs: need lines, or on a newer page of a window whose After
// is counted, what is left of the count past the end, since trim drops the rest.
func (q pageQuery) full(n, past int) bool {
	if n >= q.need() {
		return true
	}
	if q.cur == nil || q.mode != newer || !q.cur.counted(sideAfter) {
		return false
	}
	return past > 0 && past >= q.cur.lines(sideAfter)-q.cur.after
}

// pastEnd is whether p lies past the range's end, which only a counted After's
// lines do on a page full counts them for.
func (q pageQuery) pastEnd(p logstore.Pos) bool {
	if q.cur == nil {
		return false
	}
	end := q.cur.rangeEnd()
	return !end.at.isZero() && p.TS > end.at.Timestamp.UnixNano()
}

// key is where the page starts in ps.
func (q pageQuery) key(ps pageStream) logstore.Pos {
	if q.cur == nil {
		return streamBound(bound{}, ps.run(), q.mode)
	}
	return q.cur.keyFor(ps)
}

// far is where the page stops in ps, the first place past its range there: the
// window's edge ahead read as the bound of a page the other way. Nil with no edge.
func (q pageQuery) far(ps pageStream) *logstore.Pos {
	if q.cur == nil || q.cur.farEdge().at.isZero() {
		return nil
	}
	far := streamBound(q.cur.farEdge(), ps.run(), q.mode.back())
	return &far
}

// page is a page of a scope in progress, read one or more ways: one read, or a
// first page's range read and context read. It files the resolve's streams and the
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

// page is one merge per query of a scope on cluster id, every query over one resolve
// of it and one claim. The queries share their scope.
func (s *Service) page(ctx context.Context, id cluster.ClusterID, qs ...pageQuery) ([]merge, error) {
	t, err := resolve(ctx, s.clusters, id, qs[0].scope, false)
	if err != nil {
		return nil, err
	}
	file, err := s.files.OpenOrCreate(int64(t.cacheID))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	streams, release, err := pageStreams(ctx, file, t, qs[0].scope)
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
			out[i] = merge{records: []Record{}, skipped: []string{}}
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

// fill fills each stream the resolve handed out for each query, over one claim on
// the server the pods came from. A newer query fills first: an older one from the
// same bound, a first page's context read before Since or its range read before
// Until, links each stream's read back from the bound up to the newer fill's first
// line, so the two chunks join there.
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
	for _, mode := range []direction{newer, older} {
		for i, q := range qs {
			if q.mode != mode {
				continue
			}
			if fills[i], err = p.fillQuery(ctx, q, backends, seams); err != nil {
				return nil, err
			}
			if mode == newer {
				seams = p.seams(fills[i])
			}
		}
	}
	return fills, nil
}

// fillQuery fills each stream for q, all at once, each under the query's limits: an
// allowance of its own, and the pool they share. seams is each stream's line just past q's bound, nil for none. The
// first fill to fail ends the others, since its error refuses the page.
func (p *page) fillQuery(ctx context.Context, q pageQuery, backends []Backend, seams []*logstore.Pos) ([]streamFill, error) {
	fills := make([]streamFill, len(p.streams))
	var filled []int
	for i, ps := range p.streams {
		if !ps.exhausted() {
			filled = append(filled, i)
		}
	}
	limits := queryLimits(p.s.caps, max(len(filled), 1))
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	for n, i := range filled {
		ps := p.streams[i]
		f := &filler{
			file: p.file, clusters: p.s.clusters, id: p.id, save: p.save, ret: p.s.files.Retention(), ps: ps, backends: backends, names: p.backends,
			q: q, limits: limits[n], walk: newBudget(p.walkShare),
		}
		if seams != nil {
			f.seam = seams[i]
		}
		wg.Go(func() {
			var err error
			if fills[i], err = f.run(ctx, q.key(ps)); err != nil {
				cancel(err)
			}
			fills[i].downloaded = f.limits.spent
			// However early the fill ended, what it holds goes back for the streams
			// still reading.
			f.limits.ended()
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

// seams is each stream's line a newer query's fill proved nothing lies before past
// its bound, nil where it proved none: an older read back from the bound links up to it.
func (p *page) seams(fills []streamFill) []*logstore.Pos {
	out := make([]*logstore.Pos, len(fills))
	for i, f := range fills {
		out[i] = f.up
	}
	return out
}

// answer walks each stream of the file from q's key in its direction, in one read
// transaction, and merges the walks into the page.
func (p *page) answer(ctx context.Context, q pageQuery, fills []streamFill) (merge, error) {
	readings := make([]*streamRead, len(p.streams))
	reaches := make([]streamReach, len(p.streams))
	var read []logstore.Chunk
	left := false
	err := p.file.Read(ctx, func(r logstore.Reader) error {
		for i, ps := range p.streams {
			w := walker{
				r: r, ps: ps, names: p.backendsOf(ps), q: q, budget: newBudget(p.walkShare + fills[i].downloaded), fill: fills[i],
				look: p.s.files.Retention().LookBytes,
			}
			walked, err := w.walk(q.key(ps))
			if err != nil {
				return err
			}
			readings[i], reaches[i] = walked.reading, walked.reach
			read = append(read, walked.read...)
			left = left || walked.left
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
	m.skipped, m.reaches = []string{}, reaches
	// failed is the streams skipped for a failure: one stopped at a loud second cut
	// the page, and does not count toward refusing it.
	failed := 0
	for i, f := range fills {
		if f.err != nil {
			m.skipped = append(m.skipped, skippedLine(p.streams[i].key, f.err))
			if failureKind(f.err) != failLimit {
				failed++
			}
		}
	}
	if failed == len(p.t.streams) && len(m.records) == 0 {
		if err := everyStreamFailed(m.skipped); err != nil {
			return merge{}, err
		}
	}
	// A skipped stream does not cut the page, but the page reads to the end of its
	// direction only once every stream has nothing left that way: the next page goes
	// on from the farthest line this one answered. With none, the next page would be
	// this one, so the page is refused instead.
	if m.cut.isZero() && left {
		if len(m.records) == 0 {
			return merge{}, refuse(CodeReadFailed, "Kstack could not read "+string(q.mode)+" lines of this view: "+strings.Join(m.skipped, "; "))
		}
		m.cut = lineCursorOf(m.records[len(m.records)-1])
		if q.mode == older {
			m.cut = lineCursorOf(m.records[0])
		}
	}
	return m, nil
}

// MaxGrep is the longest grep a scope takes, in bytes. The wire takes any scope the
// webview sends, so the service checks it, and the LogsView tool's parse reads it.
const MaxGrep = 1_000

// compileGrep is a scope's grep, nil for none, refusing one past MaxGrep or one
// that does not compile.
func compileGrep(grep string) (*regexp.Regexp, error) {
	switch {
	case grep == "":
		return nil, nil
	case len(grep) > MaxGrep:
		return nil, refuse(CodeBadGrep, fmt.Sprintf("a grep is at most %d bytes", MaxGrep))
	}
	re, err := regexp.Compile(grep)
	if err != nil {
		return nil, refuse(CodeBadGrep, "the grep is not a regular expression")
	}
	return re, nil
}

// matches is whether message passes grep, nil passing every line. The file keeps a
// line's colours; grep reads it without them.
func matches(grep *regexp.Regexp, message string) bool {
	if grep == nil {
		return true
	}
	// A stored line's only control is a kept SGR sequence.
	if strings.Contains(message, "\x1b") {
		message = stripControl(message)
	}
	return grep.MatchString(message)
}
