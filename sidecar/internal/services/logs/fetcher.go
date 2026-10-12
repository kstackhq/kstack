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
)

// maxLine is the most of one line read off the wire; the rest of it is skipped.
// stripControl cuts what is kept to maxMessage.
const maxLine = 64 << 10

// A fetch's bounds: the bytes read across its streams, shared evenly so no stream
// is starved by the others, and the GETs each stream may send. An ordinary page
// never reaches either; a rare grep read backward does.
const (
	maxBudget  = 32 << 20
	maxWindows = 16
)

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
	// follow keeps reading as the log grows. A follow always has a since: a GET from
	// the end of a log numbers its first moment from wherever it joined it, which a
	// read from that moment's second would number differently.
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

// reading is one container's read in progress: its lines in the order read, then,
// once lines closes, how it ended.
type reading struct {
	src   RecordSource
	lines <-chan Record
	// err is nil when the read ended clean.
	err error
	// short is whether the read stopped before the end of its range — out of budget
	// or windows, cut by the connection's retirement (err errRetired), or failed
	// after reading a line in range (err its failure) — and stop the edge of what it
	// read whole: forward, it read every line in range up to stop; backward, every
	// one from stop on. Zero when it read no line in range.
	short bool
	stop  position
}

// readForward reads src's log oldest first, as kubetail's StreamForward does, over
// one GET of its log path.
func readForward(ctx context.Context, get getter, src RecordSource, o readOpts) *reading {
	return start(ctx, get, src, o, (*reader).forward)
}

// readBackward reads src's log newest first. It finds the log's first line, then,
// as kubetail's StreamBackward does, reads ever larger windows off the end with
// tailLines, each reaching further back than the last, until one reaches the first
// line. Under until it reads windows of time instead (backwardUntil). The fetch's
// budget and the stream's window cap end it early.
func readBackward(ctx context.Context, get getter, src RecordSource, o readOpts) *reading {
	return start(ctx, get, src, o, (*reader).backward)
}

// start runs read on its own goroutine, its lines on the reading's channel, and
// records how it ended: a read that stopped short, at the reader's edge. A read
// that failed past its first line in range stopped short there too, so a page ends
// where its lines do and the next reads on from it.
func start(ctx context.Context, get getter, src RecordSource, o readOpts, read func(*reader) error) *reading {
	out := make(chan Record)
	r := &reading{src: src, lines: out}
	go func() {
		defer close(out)
		rd := &reader{ctx: ctx, get: get, src: src, o: o, out: out}
		err := read(rd)
		switch {
		case errors.Is(err, errShort), errors.Is(err, errRetired):
			r.short, r.stop = true, rd.edge
		case err != nil && fatal(err) == nil && !rd.edge.isZero():
			r.short, r.stop = true, rd.edge
		}
		if !errors.Is(err, errShort) {
			r.err = err
		}
	}()
	return r
}

// reader is one container's read: what it reads with, where its lines go, and
// its edge, the last line in range it read whole — forward the newest, backward
// the oldest taken, matched or not.
type reader struct {
	ctx  context.Context
	get  getter
	src  RecordSource
	o    readOpts
	out  chan<- Record
	edge position
}

// forward reads the log oldest first over one GET.
func (r *reader) forward() error {
	q := r.query(r.o.since.at.Timestamp)
	if r.o.follow {
		q.Set("follow", "true")
	}
	read, err := r.lines(q, r.o, r.send)
	r.edge = read.last
	return err
}

// backward reads the log newest first in growing tailLines windows.
func (r *reader) backward() error {
	first, err := r.firstTimestamp()
	switch {
	case err != nil || first.IsZero():
		return err
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
		case w.oldest.IsZero():
			// Emptied under the read.
			return nil
		case r.inSinceMoment(w.oldest):
			// The window began in since's moment, so every later moment is whole in
			// it and every earlier one out of range. Its lines at that moment are
			// numbered from the window's top, which since's line cannot be judged by.
			if !r.takeBack(lines, w.oldest) {
				return nil
			}
			return r.wholeMoment(w.oldest)
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
				return r.wholeMoment(first)
			}
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

// inSinceMoment is whether t is the moment of the line since names. A since that
// names a moment alone bounds by time, which a window's numbering cannot mislead.
func (r *reader) inSinceMoment(t time.Time) bool {
	return !r.o.since.at.moment() && t.Equal(r.o.since.at.Timestamp)
}

// wholeMoment ends a backward read whose window began in moment t, the log's
// first or since's, with every line of that moment in range. The window cannot
// tell whether it began at the moment's top, so the moment is read forward, whole.
func (r *reader) wholeMoment(t time.Time) error {
	lines, err := r.span(t, bound{position{Timestamp: t}, true})
	if err != nil {
		return err
	}
	r.takeBack(lines, time.Time{})
	return nil
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
		if !r.takeBack(lines, time.Time{}) || start.Equal(first) {
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
	return !r.o.matches(rec) || send(r.ctx, r.out, rec)
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
// an empty log.
func (r *reader) firstTimestamp() (time.Time, error) {
	q := r.query(r.o.since.at.Timestamp)
	q.Set("limitBytes", strconv.Itoa(firstBytes))
	body, err := r.get(r.ctx, logPath(r.src), q)
	if err != nil {
		return time.Time{}, err
	}
	defer body.Close()
	buf, err := io.ReadAll(counted{body, r.o.budget})
	if err != nil {
		return time.Time{}, err
	}
	rec, _ := parseLine(buf)
	return rec.Timestamp, nil
}

// query is the query every read sends: with timestamps, the container, the
// previous instance when asked, and from since's second when since is set, since
// the log endpoint takes sinceTime to the second.
func (r *reader) query(since time.Time) url.Values {
	q := url.Values{"timestamps": {"true"}, "container": {r.src.Container}}
	if r.src.Previous {
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

// lines reads one GET of the log, every line cleaned, stamped with its source and
// numbered within its moment, and hands each one in o's bounds to each, until the
// body ends, a line passes the upper bound, or each answers false, spending what
// it reads from o's budget. The bounds are applied here and nowhere else, so no
// read can miss one. A read its context ended is a clean one.
func (r *reader) lines(q url.Values, o readOpts, each func(Record) bool) (span, error) {
	var read span
	body, err := r.get(r.ctx, logPath(r.src), q)
	if err != nil {
		return read, err
	}
	defer body.Close()
	br := bufio.NewReader(counted{body, o.budget})
	var moment time.Time
	ordinal := 0
	for {
		line, err := readLine(br)
		if rec, ok := parseLine(line); ok {
			rec.Source = r.src
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
				read.last = positionOf(rec)
				if !each(rec) {
					return read, nil
				}
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

// send puts v on out, and is false once ctx has ended.
func send[T any](ctx context.Context, out chan<- T, v T) bool {
	select {
	case out <- v:
		return true
	case <-ctx.Done():
		return false
	}
}

// logPath is src's pod's log path.
func logPath(src RecordSource) string {
	return "/api/v1/namespaces/" + url.PathEscape(src.Namespace) + "/pods/" + url.PathEscape(src.Pod) + "/log"
}

// readLine is the next whole line without its newline, at most maxLine of it. A
// last line with no newline is not a line, as kubetail's reader has it: a follow
// can end inside one.
func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		line = append(line, chunk[:min(len(chunk), maxLine-len(line))]...)
		switch {
		case err == nil:
			return bytes.TrimSuffix(line, []byte{'\n'}), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
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
