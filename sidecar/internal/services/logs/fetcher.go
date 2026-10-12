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
	"sync/atomic"
	"time"

	"github.com/amorey/gochan/spsc"
)

// maxLine is the most of one line read off the wire; the rest of it is skipped.
// stripControl cuts what is kept to maxMessage.
const maxLine = 64 << 10

// timestampSpan is the longest RFC 3339 timestamp with nanoseconds, plus the space
// after it.
const timestampSpan = 36

// errShort is a read that ran out of budget or windows before the end of its range.
var errShort = errors.New("logs: the read stopped short")

// budget is the bytes a fetch's streams may still read between them, spent as each
// reads, so a busy stream can spend what a quiet one would have read. A nil budget
// is no bound.
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
	// budget is what a fetch's read spends.
	budget *budget
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

// readForward reads s's log oldest first, as kubetail's StreamForward does, over
// one GET of its log path.
func readForward(ctx context.Context, get getter, s stream, o readOpts) *streamRead {
	return start(ctx, get, s, o, (*reader).forward)
}

// start runs read on its own goroutine, its lines on the streamRead's channel, and
// records how it ended: a read that stopped short, at the reader's edge. A read
// that failed past its first line in range stopped short there too, so a page ends
// where its lines do and the next reads on from it.
func start(ctx context.Context, get getter, s stream, o readOpts, read func(*reader) error) *streamRead {
	out, lines := spsc.New[Record](0)
	r := &streamRead{key: s.key, lines: lines}
	go func() {
		defer out.Close()
		rd := &reader{ctx: ctx, get: get, key: s.key, place: s.place, o: o, out: out}
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

// reader is one stream's read: what it reads with, where its lines go, and
// its edge, the last line in range it read whole — forward the newest, backward
// the oldest taken, matched or not.
type reader struct {
	ctx   context.Context
	get   getter
	key   StreamKey
	place Place
	o     readOpts
	out   *spsc.Sender[Record]
	edge  position
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

// send puts rec on out when it matches the grep, and is false once the context
// has ended.
func (r *reader) send(rec Record) bool {
	return !r.o.matches(rec) || r.out.SendContext(r.ctx, rec) == nil
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

// lines reads one GET of the log, every line cleaned, stamped with its source and
// numbered within its moment, and hands each one in o's bounds to each, until the
// body ends, a line passes the upper bound, or each answers false, spending what
// it reads from o's budget. The bounds are applied here and nowhere else, so no
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
			rec.Stream, rec.Place = r.key, r.place
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
