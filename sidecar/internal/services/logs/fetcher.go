// Copyright 2026 The Kstack Authors
// Copyright 2024 The Kubetail Authors
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
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/amorey/gochan/spsc"
)

// maxLine is the most of one line read off the wire; the rest of it is skipped.
// stripControl cuts what is kept to maxMessage.
const maxLine = 64 << 10

// minBatch is the fewest lines a backward window asks for: a smaller one spends a
// GET on nearly nothing.
const minBatch = 10

// firstBytes is how much of a log the read that finds its first line asks for: more
// than the longest timestamp and the space after it.
const firstBytes = 100

// timestampSpan is the longest RFC 3339 timestamp with nanoseconds, plus the space
// after it.
const timestampSpan = 36

// errShort is a read that ran out of budget before the end of its range.
var errShort = errors.New("logs: the read stopped short")

// budget is the bytes a stream of a fetch may still read. A nil budget is no bound.
type budget struct{ left atomic.Int64 }

func newBudget(n int64) *budget {
	b := &budget{}
	b.left.Store(n)
	return b
}

// spend takes n bytes, and is false once there were not n left.
func (b *budget) spend(n int) bool { return b == nil || b.left.Add(-int64(n)) >= 0 }

// spent is whether the budget has run out.
func (b *budget) spent() bool { return b != nil && b.left.Load() < 0 }

// remaining is the bytes left to spend, none once spent, and as many as count with
// no bound.
func (b *budget) remaining() int64 {
	if b == nil {
		return math.MaxInt64
	}
	return max(b.left.Load(), 0)
}

// counted is a body whose bytes are spent from a budget.
type counted struct {
	io.Reader
	budget *budget
}

func (c counted) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	if n > 0 && !c.budget.spend(n) {
		return n, errShort
	}
	return n, err
}

// getter is one GET on the claimed server: a claim's get, or a test's.
type getter func(ctx context.Context, path string, query url.Values) (io.ReadCloser, error)

// readOpts is what one container's read asks for.
type readOpts struct {
	// since and until bound the lines, either zero for no bound.
	since bound
	until bound
	// follow keeps reading as the log grows. A follow reads from since or the log's
	// start, never from its end: a GET from the end numbers its first moment from
	// wherever it joined it, which a read from that moment's second would number
	// differently.
	follow bool
	// A fetch's bounds: the budget a read spends, and for a backward read the kept
	// lines it reads toward, which a tailLines read's first window asks for.
	budget *budget
	batch  int64
	// grep is the view's, which a backward read counts its windows' kept lines by,
	// and near the rate next to a backward read's point, nil with none.
	grep *regexp.Regexp
	near *rate
	// onOldest, when set, is told the moment of a GET's first line, in range or not,
	// before any line of it goes out: a tail decides from it whether its first read
	// returned its start's moment whole, and reads on.
	onOldest func(time.Time)
	// more, when set, is asked at each window's end before a backward read sends its
	// next GET, with the read's context, and ends the read clean when false.
	more func(context.Context) bool
}

// readItem is what a read hands over: a line, or with windowEnd, the end of a window,
// after its last line, or with windowCut, the end of a window stopped part way, which
// is an island: the lines between it and the point were never read. A backward read
// is a run of windows; a forward read is one, which its close ends, so it sends none.
type readItem struct {
	Record
	windowEnd bool
	windowCut bool
}

// streamRead is one stream's read in progress: its lines in the order read, then,
// once lines closes, how it ended.
type streamRead struct {
	key   StreamKey
	lines *spsc.Receiver[readItem]
	// cancel ends the read, which then closes lines.
	cancel context.CancelFunc
	// err is nil when the read ended clean.
	err error
	// short is whether the read stopped before the end of its range — out of
	// budget, cut by the connection's retirement (err errRetired), or failed
	// after reading a line in range (err its failure) — and stop the edge of what it
	// read whole: forward, it read every line in range up to stop; backward, every
	// one from stop on. Zero when it read no line in range.
	short bool
	stop  position
	// head is whether the read reached the oldest line the backend holds: a backward
	// read with no lower bound back to the log's first line, or a forward read with
	// none, which starts there.
	head bool
	// atEnd is whether a forward read reached the last line of a finished instance,
	// whose log never grows again.
	atEnd bool
	// oldest is the moment of the first line a forward read's GET returned, in range
	// or not, zero with none: a read from a second whose oldest line is at or before
	// a moment returned that moment whole.
	oldest time.Time
}

// readForward reads s's log oldest first, as kubetail's StreamForward does, over
// one GET of its log path.
func readForward(ctx context.Context, get getter, s stream, o readOpts) *streamRead {
	return start(ctx, get, s, o, (*reader).forward)
}

// readBackward reads s's log in windows, newest window first, each oldest first and
// sent as its GET reads it. It finds the log's first line, then, as kubetail's
// StreamBackward does, reads ever larger windows off the end with tailLines, each
// asking for twice the lines of the last, until one reaches the first line. Under
// until it reads windows of time instead (backwardUntil). The fetch's budget and
// o.more end it early.
func readBackward(ctx context.Context, get getter, s stream, o readOpts) *streamRead {
	return start(ctx, get, s, o, (*reader).backward)
}

// start runs read on its own goroutine, its lines on the streamRead's channel, and
// records how it ended: a read that stopped short, at the reader's edge. A read
// that failed past its first line in range stopped short there too, so a page ends
// where its lines do and the next reads on from it.
func start(ctx context.Context, get getter, s stream, o readOpts, read func(*reader) error) *streamRead {
	ctx, cancel := context.WithCancel(ctx)
	out, lines := spsc.New[readItem](0)
	r := &streamRead{key: s.key, lines: lines, cancel: cancel}
	go func() {
		defer out.Close()
		defer cancel()
		rd := &reader{ctx: ctx, get: get, key: s.key, place: s.place, instance: s.gen.instance, finished: s.finished(), o: o, out: out}
		err := read(rd)
		switch {
		case errors.Is(err, errShort), errors.Is(err, errRetired):
			r.short, r.stop = true, rd.edge
		case err != nil && failureKind(err) != failFatal && !rd.edge.isZero():
			r.short, r.stop = true, rd.edge
		}
		if !errors.Is(err, errShort) {
			r.err = err
		}
		r.head, r.atEnd, r.oldest = rd.head, rd.atEnd, rd.oldest
	}()
	return r
}

// reader is one stream's read: what it reads with, the instance it reads and
// whether it has finished, where its lines go, and its edge, the last line in
// range it read whole: forward the newest, backward the oldest of the last window
// to complete. windowOldest is the oldest line the window arriving has sent.
type reader struct {
	ctx          context.Context
	get          getter
	key          StreamKey
	place        Place
	instance     string
	finished     bool
	o            readOpts
	out          *spsc.Sender[readItem]
	edge         position
	windowOldest position
	head         bool
	atEnd        bool
	oldest       time.Time
}

// forward reads the log oldest first over one GET.
func (r *reader) forward() error {
	q := r.query(r.o.since.at.Timestamp)
	if r.o.follow {
		q.Set("follow", "true")
	}
	read, err := r.lines(q, r.o, func(rec Record, _ int) bool { return r.send(rec) })
	r.edge, r.oldest = read.last, read.oldest
	// With no lower bound the read starts at the oldest line, so its first line is
	// the backend's oldest, and a log it read clean to the end with none is empty.
	r.head = r.o.since.at.isZero() && (!read.first.isZero() || err == nil)
	r.atEnd = r.finished && r.o.until.at.isZero() && !r.o.follow && err == nil && r.ctx.Err() == nil
	return err
}

// backward reads the log in doubling tailLines windows, newest window first.
func (r *reader) backward() error {
	first, err := r.firstTimestamp()
	switch {
	case err != nil:
		return err
	case first.IsZero():
		r.reachedFirst(first)
		return nil
	case !r.o.until.at.isZero():
		return r.backwardUntil(first)
	}
	tail := max(r.o.batch, minBatch)
	// sent is the moment the last window started. Every line after it is sent; the
	// lines at it are left to the next window, since a window can start inside a
	// moment. Each window asks for twice the lines of the last, so a sparse read
	// reaches a long log's start in a few GETs.
	var sent time.Time
	for {
		w, err := r.window(tail, sent)
		start := w.first.Timestamp
		switch {
		case err != nil:
			// A window cut short went out without its end.
			return err
		case r.ctx.Err() != nil:
			return nil
		case w.oldest.IsZero() && sent.IsZero():
			// Emptied under the read.
			return nil
		case w.oldest.IsZero():
			// Emptied under the read after a window was sent, so the lines before
			// that window were never read.
			return errShort
		case r.inSinceMoment(w.oldest):
			// The window began in since's moment, so every later moment is whole in
			// it and every earlier one out of range. Its lines at that moment are
			// numbered from the window's top, which since's line cannot be judged by.
			if !r.endWindow() || !r.more() {
				return nil
			}
			_, err := r.wholeMoment(w.oldest)
			return err
		case !start.IsZero() && w.oldest.Before(start):
			// The window began below since, so it holds every line in range, and
			// whole: its first moment in range is not the one it began in.
			r.endWindow()
			return nil
		case start.IsZero() && sent.IsZero():
			// No line in range: the newest window holds the newest lines, so none
			// older is in range either.
			return nil
		case !w.oldest.After(first):
			// The window reached the log's first line, so every moment after the
			// first is whole in it. The first moment may not be.
			if !r.endWindow() {
				return nil
			}
			if start.Equal(first) {
				if !r.more() {
					return nil
				}
				// The first moment is the backend's oldest only once it has all gone out:
				// a read cancelled inside it leaves older lines of it unread.
				whole, err := r.wholeMoment(first)
				if !whole || err != nil {
					return err
				}
			}
			r.reachedFirst(first)
			return nil
		case !sent.IsZero() && (start.IsZero() || !start.Before(sent)):
			// The log grew by the whole window under the read, or one moment fills
			// it. Read further back before ending a window.
			tail *= 2
			continue
		}
		if !r.endWindow() || !r.more() {
			return nil
		}
		sent = start
		if r.o.budget.spent() {
			return errShort
		}
		tail *= 2
	}
}

// reachedFirst marks a backward read that took every line back to first, the one
// firstTimestamp found, zero for an empty log. That is the backend's oldest with no
// since. With since, it is when since's line is gone, as rotation drops the oldest
// lines first: first is past since's moment, or the log is empty from since's second.
func (r *reader) reachedFirst(first time.Time) {
	since := r.o.since.at
	r.head = since.isZero() || first.After(since.Timestamp) || first.IsZero() && r.ctx.Err() == nil
}

// inSinceMoment is whether t is the moment of the line since names. A since that
// names a moment alone bounds by time, which a window's numbering cannot mislead.
func (r *reader) inSinceMoment(t time.Time) bool {
	return !r.o.since.at.moment() && t.Equal(r.o.since.at.Timestamp)
}

// wholeMoment ends a backward read whose window began in moment t, the log's
// first or since's, with every line of that moment in range. The window cannot
// tell whether it began at the moment's top, so the moment is read forward, whole,
// as a window of its own: its lines are older than the window that found it, and
// arrive after it. It is false once the context has ended before the window's end
// went out.
func (r *reader) wholeMoment(t time.Time) (bool, error) {
	if err := r.span(t, bound{position{Timestamp: t}, true}, func(rec Record, _ int) bool { return r.take(rec) }); err != nil {
		return false, err
	}
	return r.ctx.Err() == nil && r.endWindow(), nil
}

// backwardUntil is backward under until, which a tailLines window reaches only
// by reading every line after it: windows of time ending at until instead, each
// read forward from the top of a second and stopped where the last one began. The
// first is sized from the rate near the point; each window that leaves the read
// short is followed by an older one sized from its own. A window far busier than it
// was sized for is stopped part way and replaced by a narrower one ending where it
// would have.
func (r *reader) backwardUntil(first time.Time) error {
	until := r.o.until.at.Timestamp
	if first.After(until) {
		// No line is in range, and none older than first is left.
		r.reachedFirst(first)
		return nil
	}
	reach := defaultReach
	if r.o.near != nil {
		reach = windowFor(r.o.batch, *r.o.near, r.target())
	}
	end, endAt := r.o.until, until
	start := startOf(endAt, reach, first)
	// kept is the kept lines of the windows read whole, so each window is sized for
	// the lines the read still misses.
	var kept int64
	for {
		w, cut, err := r.timeWindow(start, end, endAt)
		switch {
		case err != nil:
			return err
		case r.ctx.Err() != nil:
			return nil
		case cut != nil:
			if !r.cutWindow() {
				return nil
			}
			start = r.narrow(start, endAt, *cut, kept, first)
			continue
		}
		if !r.endWindow() {
			return nil
		}
		if start.Equal(first) {
			r.reachedFirst(first)
			return nil
		}
		if !r.more() {
			return nil
		}
		if r.o.budget.spent() {
			return errShort
		}
		kept += w.kept
		reach := r.widen(w, endAt.Sub(start), kept)
		end, endAt = bound{position{Timestamp: start}, false}, start
		start = startOf(endAt, reach, first)
	}
}

// startOf is the start of a window reaching back reach from endAt: the top of a
// second, and never before first.
func startOf(endAt time.Time, reach time.Duration, first time.Time) time.Time {
	if reach >= endAt.Sub(first) {
		return first
	}
	if start := endAt.Add(-reach).Truncate(time.Second); start.After(first) {
		return start
	}
	return first
}

// narrow is the start of the window that replaces one from start, ending at endAt,
// stopped with its newest second at cut: sized for the kept lines the read still
// misses, and at least a second later than start, so narrowing reaches the smallest
// window in the end.
func (r *reader) narrow(start, endAt time.Time, cut rate, kept int64, first time.Time) time.Time {
	narrowed := startOf(endAt, windowFor(max(r.o.batch-kept, 1), cut, r.target()), first)
	if narrowed.After(start) {
		return narrowed
	}
	return start.Truncate(time.Second).Add(time.Second)
}

// widen is the reach of the window older than w, which covered d and left the read
// with kept lines: sized for the ones still missing at w's rate, or eight times d
// after a window with no line, whose silence measures no rate.
func (r *reader) widen(w sample, d time.Duration, kept int64) time.Duration {
	if w.lines == 0 {
		if d > maxReach/8 {
			return maxReach
		}
		return 8 * d
	}
	return windowFor(max(r.o.batch-kept, 1), w.per(d), r.target())
}

// target is the most bytes a window is sized to hold: byteTarget, and under half of
// what the stream has left, since every window is rounded up to a whole second.
func (r *reader) target() int64 {
	return min(byteTarget, max(r.o.budget.remaining()/2, 1))
}

// timeWindow streams the lines in range from the moment from up to end, which lies
// at endAt, oldest first, over one forward GET from from's second, and answers what
// it read. A window larger than the smallest one before endAt is checked as it goes,
// and stopped once it is far busier than planned: cut is then the rate of its newest
// second, which sizes the window that replaces it.
func (r *reader) timeWindow(from time.Time, end bound, endAt time.Time) (sample, *rate, error) {
	checked := from.Before(endAt.Add(-1).Truncate(time.Second))
	p := plan{need: r.o.batch, target: r.target(), left: r.o.budget.remaining()}
	var w sample
	q := recent{from: from}
	var cut *rate
	err := r.span(from, end, func(rec Record, size int) bool {
		kept := matches(r.o.grep, rec.Message)
		// What the body carried, which the budget spends: the message is cleaned and
		// cut by now.
		bytes := int64(size)
		w.add(rec.Timestamp, bytes, kept)
		q.add(rec.Timestamp, bytes, kept)
		if !r.take(rec) {
			return false
		}
		if checked && p.tooBusy(w, q.rate(), endAt.Sub(rec.Timestamp)) {
			cut = new(q.rate())
			return false
		}
		return true
	})
	return w, cut, err
}

// cutWindow puts the end of a window stopped part way on out, and is false once the
// context has ended. The window was not read to the point, so its oldest line never
// becomes the edge.
func (r *reader) cutWindow() bool {
	r.windowOldest = position{}
	return r.out.SendContext(r.ctx, readItem{windowCut: true}) == nil
}

// send puts rec on out, and is false once the context has ended.
func (r *reader) send(rec Record) bool {
	return r.out.SendContext(r.ctx, readItem{Record: rec}) == nil
}

// endWindow puts the end of a window on out, and is false once the context has
// ended, when it goes unsent. A window is read whole only once its end goes out, so
// only then does its oldest line become the edge.
func (r *reader) endWindow() bool {
	if r.out.SendContext(r.ctx, readItem{windowEnd: true}) != nil {
		return false
	}
	if !r.windowOldest.isZero() {
		r.edge, r.windowOldest = r.windowOldest, position{}
	}
	return true
}

// more is whether the read may send another GET, as o.more says.
func (r *reader) more() bool {
	return r.o.more == nil || r.o.more(r.ctx)
}

// take sends rec, a line of the window arriving, and is false once the context has
// ended.
func (r *reader) take(rec Record) bool {
	if r.windowOldest.isZero() {
		r.windowOldest = positionOf(rec)
	}
	return r.send(rec)
}

// window streams the last tail lines of the log from since up to the moment sent,
// which an earlier window sent already, oldest first, and answers what the GET
// read. The lines at the moment the GET began in are left to the next window: the
// GET may have begun inside that moment, and the next window reads it from its top.
func (r *reader) window(tail int64, sent time.Time) (span, error) {
	q := r.query(r.o.since.at.Timestamp)
	q.Set("tailLines", strconv.FormatInt(tail, 10))
	o := r.o
	if !sent.IsZero() {
		o.until = bound{position{Timestamp: sent}, true}
	}
	var began time.Time
	o.onOldest = func(t time.Time) { began = t }
	return r.lines(q, o, func(rec Record, _ int) bool {
		return !rec.Timestamp.After(began) || r.take(rec)
	})
}

// span hands each the lines in range from the moment from up to end, oldest first,
// each with the bytes the body carried it in, over one forward GET from from's
// second.
func (r *reader) span(from time.Time, end bound, each func(rec Record, size int) bool) error {
	o := r.o
	if o.since.at.Timestamp.Before(from) {
		o.since = bound{position{Timestamp: from}, true}
	}
	o.until = end
	_, err := r.lines(r.query(from), o, each)
	return err
}

// firstTimestamp is the time of the log's first line from since's second, zero for
// an empty log or a read its context ended.
func (r *reader) firstTimestamp() (time.Time, error) {
	q := r.query(r.o.since.at.Timestamp)
	q.Set("limitBytes", strconv.Itoa(firstBytes))
	body, err := r.get(r.ctx, logPath(r.key), q)
	if err != nil {
		return time.Time{}, r.cleanEnd(err)
	}
	defer body.Close()
	buf, err := io.ReadAll(io.LimitReader(counted{body, r.o.budget}, firstBytes))
	if err != nil {
		return time.Time{}, r.cleanEnd(err)
	}
	rec, _ := parseLine(buf)
	return rec.Timestamp, nil
}

// cleanEnd is err, or nil once the read's context has ended: a read its context
// ended is a clean one. Running out of budget is never clean.
func (r *reader) cleanEnd(err error) error {
	if r.ctx.Err() != nil && !errors.Is(err, errShort) {
		return nil
	}
	return err
}

// query is the query every read sends: with timestamps, the container, the
// previous instance when asked, and from since's second when since is set, since
// the log endpoint takes sinceTime to the second.
func (r *reader) query(since time.Time) url.Values {
	q := url.Values{"timestamps": {"true"}, "container": {r.key.Container}}
	if r.key.Previous {
		q.Set("previous", "true")
	}
	if !since.IsZero() {
		q.Set("sinceTime", since.UTC().Truncate(time.Second).Format(time.RFC3339))
	}
	return q
}

// span is what one GET read whole: the first and the last line in range, zero
// with none, and the moment of the oldest line at all.
type span struct {
	first, last position
	oldest      time.Time
}

// lines reads one GET of the log, every line cleaned, stamped with its stream
// and instance and numbered within its moment, and hands each one in o's bounds
// to each with the bytes the body carried it in, before cleaning, until the body
// ends, a line passes the upper bound, or each answers false, spending what it
// reads from o's budget. The bounds are applied here and nowhere else, so no read
// can miss one. A read its context ended is a clean one.
func (r *reader) lines(q url.Values, o readOpts, each func(rec Record, size int) bool) (span, error) {
	var read span
	body, err := r.get(r.ctx, logPath(r.key), q)
	switch {
	case err == nil:
	case r.ctx.Err() != nil:
		return read, nil
	default:
		return read, err
	}
	defer body.Close()
	br := bufio.NewReader(counted{body, o.budget})
	var moment time.Time
	ordinal := 0
	for {
		line, size, err := readLine(br)
		if rec, ok := parseLine(line); ok {
			rec.Stream, rec.Place, rec.ContainerID = r.key, r.place, r.instance
			if rec.Timestamp.Equal(moment) {
				ordinal++
			} else {
				moment, ordinal = rec.Timestamp, 0
			}
			rec.Ordinal = ordinal
			if read.oldest.IsZero() {
				read.oldest = rec.Timestamp
				if o.onOldest != nil {
					o.onOldest(rec.Timestamp)
				}
			}
			switch {
			case o.since.below(rec):
			case o.until.above(rec):
				return read, nil
			default:
				if read.first.isZero() {
					read.first = positionOf(rec)
				}
				if !each(rec, size) {
					return read, nil
				}
				read.last = positionOf(rec)
			}
		}
		switch {
		case err == nil:
			continue
		case errors.Is(err, io.EOF):
			return read, nil
		case errors.Is(err, errShort):
			return read, err
		case r.ctx.Err() != nil:
			return read, nil
		default:
			return read, err
		}
	}
}

// logPath is key's pod's log path.
func logPath(key StreamKey) string {
	return "/api/v1/namespaces/" + url.PathEscape(key.Namespace) + "/pods/" + url.PathEscape(key.Pod) + "/log"
}

// readLine is the next whole line without its newline, at most maxLine of it,
// valid until br's next read, and the bytes of the body it took, its newline and
// the part past maxLine included. A last line with no newline is not a line, as
// kubetail's reader has it: a follow can end inside one.
func readLine(br *bufio.Reader) ([]byte, int, error) {
	var line []byte
	size := 0
	for {
		chunk, err := br.ReadSlice('\n')
		size += len(chunk)
		chunk = chunk[:min(len(chunk), maxLine-len(line))]
		switch {
		case err == nil && line == nil:
			// A line whole in one chunk is the buffer's own bytes, not a copy.
			return bytes.TrimSuffix(chunk, []byte{'\n'}), size, nil
		case err == nil:
			return bytes.TrimSuffix(append(line, chunk...), []byte{'\n'}), size, nil
		case errors.Is(err, bufio.ErrBufferFull):
			line = append(line, chunk...)
		default:
			return nil, size, err
		}
	}
}

// parseLine is a line as the log endpoint writes it with timestamps: an RFC 3339
// time, a space, the message. A line with no time is not a record.
func parseLine(line []byte) (Record, bool) {
	pos := bytes.IndexByte(line[:min(len(line), timestampSpan)], ' ')
	if pos < 0 {
		return Record{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, string(line[:pos]))
	if err != nil {
		return Record{}, false
	}
	return Record{Timestamp: ts, Message: stripControl(string(line[pos+1:]))}, true
}
