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
	"sync/atomic"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs/logstore"
)

// tailer is one stream's Tail read, writing what it reads into the file through the
// file's writer. Its fields past done are guarded by the registry's mu, but err, which
// the read's end sets before done closes.
type tailer struct {
	r      *registry
	st     stream
	row    int64
	cancel context.CancelFunc
	// ready closes once the start has run, read or failed, and done once the read has
	// ended, err then saying how.
	ready chan struct{}
	done  chan struct{}
	err   error

	refs     int
	stopping bool
	grace    *time.Timer
	// start is where the read reads on from: every gap past it is the tailer's. A walk
	// reads it holding one of the file's reader connections, so it takes no lock.
	start atomic.Pointer[logstore.Pos]
	// current is the file's tail the read writes into, nil until its first line.
	current atomic.Pointer[logstore.Tail]
}

// started is whether t's start has run; a tailer not started is being started.
func (t *tailer) started() bool {
	select {
	case <-t.ready:
		return true
	default:
		return false
	}
}

// join holds t for one more follow, and keeps it past its grace. It is called with
// mu held.
func (t *tailer) join() {
	t.refs++
	if t.grace != nil {
		t.grace.Stop()
		t.grace = nil
	}
}

// tailRead is one Tail read and the moment its GET's first line came at, which is
// set before that line goes out.
type tailRead struct {
	read   *streamRead
	oldest time.Time
}

// read opens a Tail read of the stream past start.
func (t *tailer) read(ctx context.Context, backend Backend, start logstore.Pos) *tailRead {
	r := &tailRead{}
	r.read = backend.Tail(ctx, t.st, readOpts{
		since:    bound{positionAt(t.st.instance(), start), false},
		onOldest: func(ts time.Time) { r.oldest = ts },
	})
	return r
}

// stop ends the tailer. It is called with mu held.
func (t *tailer) stop() {
	t.stopping = true
	t.cancel()
}

// run reads the stream until the read ends or the tailer stops, reading again from
// now whenever the writer falls behind, and records how the read ended.
func (t *tailer) run(ctx context.Context, file *logstore.Store, backend Backend, read *tailRead, prev *logstore.Pos) {
	defer func() {
		t.r.ended(t)
		close(t.done)
	}()
	defer t.cancel()
	for {
		behind, err := t.tail(ctx, file, read, prev)
		if !behind {
			t.err = err
			return
		}
		// The lines it could not hand over are a live gap below the new start, which a
		// follow or a page that reaches them fills.
		start := logstore.Pos{TS: t.r.s.now().UnixNano(), Ord: -1}
		t.start.Store(&start)
		read, prev = t.read(ctx, backend, start), nil
	}
}

// tail takes tr's lines into the file, continuing the chunk ending at prev when it is
// set, and hands them to the writer in batches: whatever has arrived, up to
// WriteBatch lines or WriteBatchBytes. It answers whether it stopped because the
// writer could not take a batch within WriteWait, and otherwise how the read ended.
func (t *tailer) tail(ctx context.Context, file *logstore.Store, tr *tailRead, prev *logstore.Pos) (bool, error) {
	ret := t.r.s.files.Retention()
	read := tr.read
	defer drainReading(read)
	defer read.cancel()
	var tail *logstore.Tail
	var failed <-chan struct{}
	ch := read.lines.Chan()
	for {
		var rec Record
		var ok bool
		select {
		case rec, ok = <-ch:
		case <-failed:
			// What the writer lost is a gap a fill reads once this tailer has ended.
			return false, t.close(tail, nil, tail.Err())
		}
		if !ok {
			return false, t.close(tail, nil, read.err)
		}
		if tail == nil {
			// The first line has come, so the moment the read began at is known.
			var err error
			if tail, err = file.OpenTail(t.row, t.r.s.backends[0].name, prev, returnedWhole(tr.oldest, prev)); err != nil {
				return false, err
			}
			t.current.Store(tail)
			failed = tail.Failed()
		}
		lines, open := gather(ch, ret, lineOf(rec))
		if !open {
			return false, t.close(tail, lines, read.err)
		}
		wctx, cancel := context.WithTimeout(ctx, ret.WriteWait)
		err := tail.Write(wctx, lines)
		cancel()
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return false, t.close(tail, nil, nil)
		case errors.Is(err, context.DeadlineExceeded):
			// Stop reading before waiting on the writer to close the chunk.
			read.cancel()
			return true, t.close(tail, nil, nil)
		default:
			return false, t.close(tail, nil, err)
		}
	}
}

// gather is first and whatever else has arrived on ch, up to WriteBatch lines or
// WriteBatchBytes, and whether ch is still open.
func gather(ch <-chan Record, ret logstore.Retention, first logstore.Line) ([]logstore.Line, bool) {
	lines, bytes := []logstore.Line{first}, int64(len(first.Message))
	for len(lines) < ret.WriteBatch && bytes < ret.WriteBatchBytes {
		select {
		case rec, ok := <-ch:
			if !ok {
				return lines, false
			}
			l := lineOf(rec)
			lines, bytes = append(lines, l), bytes+int64(len(l.Message))
		default:
			return lines, true
		}
	}
	return lines, true
}

// lineOf is rec as the file stores it.
func lineOf(rec Record) logstore.Line {
	return logstore.Line{Pos: logstore.Pos{TS: rec.Timestamp.UnixNano(), Ord: rec.Ordinal}, Message: rec.Message}
}

// close closes tail with its last lines, when it was opened, and answers the read's
// failure, else the close's.
func (t *tailer) close(tail *logstore.Tail, lines []logstore.Line, readErr error) error {
	if tail == nil {
		return readErr
	}
	// The tailer's own context may have ended; its chunk still has to close.
	err := tail.Close(context.Background(), lines)
	if readErr != nil {
		return readErr
	}
	return err
}
