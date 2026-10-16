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
	"net/url"
	"regexp"
	"slices"
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

// errShort is a read that ran out of budget or windows before the end of its range.
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
	// grep is what each cleaned message must match, nil for every line.
	grep *regexp.Regexp
	// A fetch's bounds: the budget a read spends, and for a backward read, the
	// lines its first window asks for and the GETs it may send.
	budget  *budget
	batch   int64
	windows int
}

// matches is whether rec's message passes o's grep.
func (o readOpts) matches(rec Record) bool {
	return o.grep == nil || o.grep.MatchString(rec.Message)
}

// streamRead is one stream's read in progress: its lines in the order read, then,
// once lines closes, how it ended.
type streamRead struct {
	key   StreamKey
	lines *spsc.Receiver[Record]
	// cancel ends the read, which then closes lines.
	cancel context.CancelFunc
	// err is nil when the read ended clean.
	err error
	// short is whether the read stopped before the end of its range — out of budget
	// or windows, cut by the connection's retirement (err errRetired), or failed
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

// readBackward reads s's log newest first. It finds the log's first line, then,
// as kubetail's StreamBackward does, reads ever larger windows off the end with
// tailLines, each reaching further back than the last, until one reaches the first
// line. Under until it reads windows of time instead (backwardUntil). The fetch's
// budget and the stream's window cap end it early.
func readBackward(ctx context.Context, get getter, s stream, o readOpts) *streamRead {
	return start(ctx, get, s, o, (*reader).backward)
}

// start runs read on its own goroutine, its lines on the streamRead's channel, and
// records how it ended: a read that stopped short, at the reader's edge. A read
// that failed past its first line in range stopped short there too, so a page ends
// where its lines do and the next reads on from it.
func start(ctx context.Context, get getter, s stream, o readOpts, read func(*reader) error) *streamRead {
	ctx, cancel := context.WithCancel(ctx)
	out, lines := spsc.New[Record](0)
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
// range it read whole — forward the newest, backward the oldest taken, matched or
// not.
type reader struct {
	ctx      context.Context
	get      getter
	key      StreamKey
	place    Place
	instance string
	finished bool
	o        readOpts
	out      *spsc.Sender[Record]
	edge     position
	head     bool
	atEnd    bool
	oldest   time.Time
}

// forward reads the log oldest first over one GET.
func (r *reader) forward() error {
	q := r.query(r.o.since.at.Timestamp)
	if r.o.follow {
		q.Set("follow", "true")
	}
	read, err := r.lines(q, r.o, r.send)
	r.edge, r.oldest = read.last, read.oldest
	// With no lower bound the read starts at the oldest line, so its first line is
	// the backend's oldest, and a log it read clean to the end with none is empty.
	r.head = r.o.since.at.isZero() && (!read.first.isZero() || err == nil)
	r.atEnd = r.finished && r.o.until.at.isZero() && !r.o.follow && err == nil && r.ctx.Err() == nil
	return err
}

// backward reads the log newest first in growing tailLines windows.
func (r *reader) backward() error {
	first, err := r.firstTimestamp()
	switch {
	case err != nil:
		return err
	case first.IsZero():
		r.reachedFirst()
		return nil
	case !r.o.until.at.isZero():
		return r.backwardUntil(first)
	}
	batch := max(r.o.batch, minBatch)
	tail := batch
	// sent is the moment the last window started. Every line after it is sent; the
	// lines at it are held for the next window, since a window can start inside a
	// moment. The first line's read and the first moment's each take one GET.
	var sent time.Time
	for windows := 2; windows < r.o.windows; windows++ {
		lines, w, err := r.window(tail, sent)
		start := w.first.Timestamp
		switch {
		case err != nil:
			// A window cut short stays unsent.
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
			if !r.takeBack(lines, w.oldest) {
				return nil
			}
			_, err := r.wholeMoment(w.oldest)
			return err
		case !start.IsZero() && w.oldest.Before(start):
			// The window began below since, so it holds every line in range, and
			// whole: its first moment in range is not the one it began in.
			r.takeBack(lines, time.Time{})
			return nil
		case start.IsZero() && sent.IsZero():
			// No line in range: the newest window holds the newest lines, so none
			// older is in range either.
			return nil
		case !w.oldest.After(first):
			// The window reached the log's first line, so every moment after the
			// first is whole in it. The first moment may not be.
			if !r.takeBack(lines, first) {
				return nil
			}
			if start.Equal(first) {
				// The first moment is the backend's oldest only once it has all gone out:
				// a read cancelled inside it leaves older lines of it unread.
				sent, err := r.wholeMoment(first)
				if !sent || err != nil {
					return err
				}
			}
			r.reachedFirst()
			return nil
		case !sent.IsZero() && (start.IsZero() || !start.Before(sent)):
			// The log grew by a whole batch under the read, or one moment fills the
			// window. Grow faster.
			batch *= 2
			tail += batch
			continue
		}
		if !r.takeBack(lines, start) {
			return nil
		}
		sent = start
		if r.o.budget.spent() {
			break
		}
		tail += batch
	}
	return errShort
}

// reachedFirst marks a backward read that took every line back to the first one
// firstTimestamp found, which is the backend's oldest only with no since.
func (r *reader) reachedFirst() {
	r.head = r.o.since.at.isZero()
}

// inSinceMoment is whether t is the moment of the line since names. A since that
// names a moment alone bounds by time, which a window's numbering cannot mislead.
func (r *reader) inSinceMoment(t time.Time) bool {
	return !r.o.since.at.moment() && t.Equal(r.o.since.at.Timestamp)
}

// wholeMoment ends a backward read whose window began in moment t, the log's
// first or since's, with every line of that moment in range. The window cannot
// tell whether it began at the moment's top, so the moment is read forward, whole.
// It is false once the context has ended before every line went out.
func (r *reader) wholeMoment(t time.Time) (bool, error) {
	lines, err := r.span(t, bound{position{Timestamp: t}, true})
	if err != nil {
		return false, err
	}
	return r.takeBack(lines, time.Time{}), nil
}

// backwardUntil is backward under until, which a tailLines window reaches only
// by reading every line after it: windows of time ending at until instead, each
// read forward from its start and stopped at the last one's, each reaching twice
// as far back as the last. The first reaches back far enough that the last window
// the cap allows starts at the first line: the range halved once per doubling,
// rounded up, since a reach rounded down falls short of the first line.
func (r *reader) backwardUntil(first time.Time) error {
	until := r.o.until.at.Timestamp
	if first.After(until) {
		return nil
	}
	doublings := max(r.o.windows-2, 0)
	reach := max((until.Sub(first)+(1<<doublings)-1)>>doublings, time.Second)
	end := r.o.until
	for windows := 1; windows < r.o.windows; windows++ {
		start := until.Add(-reach)
		if start.Before(first) {
			start = first
		}
		lines, err := r.span(start, end)
		switch {
		case err != nil:
			return err
		case r.ctx.Err() != nil:
			return nil
		}
		if !r.takeBack(lines, time.Time{}) {
			return nil
		}
		if start.Equal(first) {
			r.reachedFirst()
			return nil
		}
		end = bound{position{Timestamp: start}, false}
		if r.o.budget.spent() {
			break
		}
		reach *= 2
	}
	return errShort
}

// send puts rec on out when it matches the grep, and is false once the context
// has ended.
func (r *reader) send(rec Record) bool {
	return !r.o.matches(rec) || r.out.SendContext(r.ctx, rec) == nil
}

// takeBack sends the lines after the moment after, newest first, every one in
// range moving the edge whether or not the grep keeps it, and is false once the
// context has ended.
func (r *reader) takeBack(lines []Record, after time.Time) bool {
	for _, rec := range slices.Backward(lines) {
		if !rec.Timestamp.After(after) {
			continue
		}
		r.edge = positionOf(rec)
		if !r.send(rec) {
			return false
		}
	}
	return true
}

// window reads the last tail lines of the log from since, stopping at the first
// line after the moment sent, which an earlier window sent already.
func (r *reader) window(tail int64, sent time.Time) ([]Record, span, error) {
	q := r.query(r.o.since.at.Timestamp)
	q.Set("tailLines", strconv.FormatInt(tail, 10))
	o := r.o
	if !sent.IsZero() {
		o.until = bound{position{Timestamp: sent}, true}
	}
	return r.collect(q, o)
}

// span is the lines in range from the moment from up to end, oldest first, over
// one forward GET from from's second.
func (r *reader) span(from time.Time, end bound) ([]Record, error) {
	o := r.o
	if o.since.at.Timestamp.Before(from) {
		o.since = bound{position{Timestamp: from}, true}
	}
	o.until = end
	lines, _, err := r.collect(r.query(from), o)
	return lines, err
}

// collect is every line in range one GET reads, with what it read.
func (r *reader) collect(q url.Values, o readOpts) ([]Record, span, error) {
	var lines []Record
	read, err := r.lines(q, o, func(rec Record) bool {
		lines = append(lines, rec)
		return true
	})
	return lines, read, err
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
// to each, until the body ends, a line passes the upper bound, or each answers
// false, spending what it reads from o's budget. The bounds are applied here and nowhere else, so no
// read can miss one. A read its context ended is a clean one.
func (r *reader) lines(q url.Values, o readOpts, each func(Record) bool) (span, error) {
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
		line, err := readLine(br)
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
			}
			switch {
			case o.since.below(rec):
			case o.until.above(rec):
				return read, nil
			default:
				if read.first.isZero() {
					read.first = positionOf(rec)
				}
				if !each(rec) {
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
// valid until br's next read. A last line with no newline is not a line, as
// kubetail's reader has it: a follow can end inside one.
func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		chunk = chunk[:min(len(chunk), maxLine-len(line))]
		switch {
		case err == nil && line == nil:
			// A line whole in one chunk is the buffer's own bytes, not a copy.
			return bytes.TrimSuffix(chunk, []byte{'\n'}), nil
		case err == nil:
			return bytes.TrimSuffix(append(line, chunk...), []byte{'\n'}), nil
		case errors.Is(err, bufio.ErrBufferFull):
			line = append(line, chunk...)
		default:
			return nil, err
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
