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

package logswatch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/logsinput"
)

// webappIn is a watch of the webapp deployment in payments for /panic/, with limit
// and maxBytes.
func webappIn(limit, maxBytes int) input {
	return input{
		Input: logsinput.Input{
			Sources: []logsinput.Source{{Namespace: "payments", Kind: tools.LogsSourceDeployment, Name: "webapp"}},
			Grep:    "panic",
		},
		since: clock, limit: limit, maxBytes: maxBytes, until: clock.Add(time.Hour),
	}
}

// webappView is the view webappIn resolves to.
var webappView = tools.LogsViewInput{
	Sources: []tools.LogsViewSource{{Namespace: "payments", Kind: tools.LogsSourceDeployment, Name: "webapp", Containers: []string{"app"}}},
	Grep:    "panic",
}

// followed is a watch over a follow that sends frames, then holds until its
// context ends and returns err, or, as the service does, returns at once after an
// Ended frame; sent closes once every frame it could send is on its way. The
// clock reads ended at the end.
type followed struct {
	w    *watch
	sent chan struct{}
}

func follow(t *testing.T, in input, err error, frames ...logs.FollowFrame) followed {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sent := make(chan struct{})
	stream := cluster.NewStream(ctx, func(ctx context.Context, out chan<- logs.FollowFrame) error {
		for _, f := range frames {
			select {
			case out <- f:
			case <-ctx.Done():
			}
		}
		close(sent)
		if n := len(frames); n > 0 && frames[n-1].Type == logs.FollowEnded {
			return nil
		}
		<-ctx.Done()
		return err
	})
	end := clock.Add(10 * time.Minute)
	w := &watch{ctx: ctx, cancel: cancel, stream: stream, in: in, view: webappView, now: func() time.Time { return end }}
	return followed{w: w, sent: sent}
}

// expire ends the follow as its deadline does: the watch's context reads past
// its deadline, and the stream closes.
func (f followed) expire(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	t.Cleanup(cancel)
	f.w.ctx = ctx
	f.w.cancel()
}

// waited runs Wait to its end on a goroutine of its own, as the chat's watcher
// does, and answers when it returned.
func (f followed) waited(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		f.w.Wait()
		close(done)
	}()
	testutil.Wait(t, done, "the watch's end")
}

func line(rec logs.Record) logs.FollowFrame {
	return logs.FollowFrame{Type: logs.FollowLine, Record: &rec}
}

// The first line ends a watch with a limit of 1: it holds the line, cancels the
// follow, and its sample names one cursor, which reads both ways. A stop that
// lands as the stream drains does not make it stopped.
func TestTheFirstLineEndsAWatchOfOne(t *testing.T) {
	t.Parallel()
	a := record(time.Minute, 0, "panic: nil map")
	f := follow(t, webappIn(1, logsinput.SampleBudget), nil, line(a), line(record(2*time.Minute, 0, "panic: again")))
	f.waited(t)

	end := f.w.End(llm.TaskStoppedByUser)
	assert.Equal(t, tools.WatchMatched, end.Status)
	assert.Equal(t, "it reached its line limit", end.Result.EndedBecause)
	assert.Equal(t, []tools.LogsReadLine{logsinput.ReadLine(a)}, end.Result.Lines)
	c := logsinput.EncodeCursor(a)
	assert.Equal(t, "Watched 1 line of deployments/webapp in payments matching /panic/ from 2026-10-18T13:50:00Z until 2026-10-18T14:00:00Z, "+
		"ended because it reached its line limit. To read around it, call LogsRead with this cursor as until, with mode: tail, "+
		"for what came before, or as since for what followed: "+c+"\n"+
		"2026-10-18T13:51:00Z payments/webapp-a/app panic: nil map", end.Result.Text)
	assert.Equal(t, logsinput.ViewAround(webappView, a.Timestamp, clock.Add(10*time.Minute)), end.Result.ViewAround)
	assert.Error(t, f.w.ctx.Err(), "the follow is let go")
}

// The line that reaches the limit ends it; the lines are oldest first, checkpoints
// count for nothing, and the header names the first line's cursor to read back
// from and the last's to read on from.
func TestTheLimitthLineEndsIt(t *testing.T) {
	t.Parallel()
	a, b, c := record(time.Second, 0, "panic: a"), record(2*time.Second, 0, "panic: b"), record(3*time.Second, 0, "panic: c")
	checkpoint := "x"
	f := follow(t, webappIn(3, logsinput.SampleBudget), nil,
		line(a), logs.FollowFrame{Type: logs.FollowCheckpoint, Cursor: &checkpoint}, line(b), line(c), line(record(4*time.Second, 0, "panic: d")))
	f.waited(t)

	end := f.w.End("")
	assert.Equal(t, tools.WatchMatched, end.Status)
	require.Len(t, end.Result.Lines, 3)
	assert.Equal(t, "panic: c", end.Result.Lines[2].Message)
	header := strings.Split(end.Result.Text, "\n")[0]
	assert.True(t, strings.HasSuffix(header, "To read what came before, call LogsRead with mode: tail and until: "+logsinput.EncodeCursor(a)+
		"; what followed, with since: "+logsinput.EncodeCursor(c)), header)
	assert.True(t, strings.HasPrefix(header, "Watched 3 lines of deployments/webapp in payments"), header)
}

// Lines from another container that arrive later with an earlier moment are put
// in the service's order once the follow is done, lines at one moment by pod, so
// the sample is oldest first and the cursors and the view around are the first
// and last lines'.
func TestLinesAreOrderedAcrossContainers(t *testing.T) {
	t.Parallel()
	a, b, c := record(time.Second, 0, "panic: a"), record(2*time.Second, 0, "panic: b"), record(2*time.Second, 0, "panic: c")
	a.Stream.Pod, a.ContainerID = "webapp-b", "containerd://0b1c"
	c.Stream.Pod, c.ContainerID = "webapp-c", "containerd://0c1d"
	f := follow(t, webappIn(3, logsinput.SampleBudget), nil, line(c), line(b), line(a))
	f.waited(t)

	end := f.w.End("")
	assert.Equal(t, []tools.LogsReadLine{logsinput.ReadLine(a), logsinput.ReadLine(b), logsinput.ReadLine(c)}, end.Result.Lines)
	header := strings.Split(end.Result.Text, "\n")[0]
	assert.True(t, strings.HasSuffix(header, "until: "+logsinput.EncodeCursor(a)+"; what followed, with since: "+logsinput.EncodeCursor(c)), header)
	assert.Equal(t, a.Timestamp, *end.Result.ViewAround.Anchor.At)
}

// Lines that reach the byte limit end it before the line limit, keeping the line
// that reached it; each message is redacted, then cut.
func TestTheByteLimitEndsItOnTheLineThatReachesIt(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", logsinput.MaxSampleLine-8) + " sk-" + strings.Repeat("A", 30)
	f := follow(t, webappIn(100, 100), nil, line(record(time.Second, 0, "panic: a")), line(record(2*time.Second, 0, long)), line(record(3*time.Second, 0, "c")))
	f.waited(t)

	end := f.w.End("")
	assert.Equal(t, tools.WatchMatched, end.Status)
	assert.Equal(t, "it reached its byte limit", end.Result.EndedBecause)
	require.Len(t, end.Result.Lines, 2)
	cut := end.Result.Lines[1].Message
	assert.LessOrEqual(t, len(cut), logsinput.MaxSampleLine)
	assert.NotContains(t, cut, "sk-AAAA", "redacted before the cut")
}

// A deadline with lines held is matched, at its time; with none, expired, its
// sample naming the window. A stop, a fill and the deadline close the stream the
// same way, and the watch tells them apart: a stop that lands as the stream
// drains after either relabels nothing.
func TestTheDeadlineEndsItMatchedOrExpired(t *testing.T) {
	t.Parallel()
	held := follow(t, webappIn(3, logsinput.SampleBudget), nil, line(record(time.Second, 0, "panic: a")))
	testutil.Wait(t, held.sent, "the line")
	held.expire(t)
	held.waited(t)
	end := held.w.End(llm.TaskStoppedByUser)
	assert.Equal(t, tools.WatchMatched, end.Status)
	assert.Equal(t, "its time ran out", end.Result.EndedBecause)

	none := follow(t, webappIn(3, logsinput.SampleBudget), nil)
	none.expire(t)
	none.waited(t)
	end = none.w.End("")
	assert.Equal(t, tools.WatchExpired, end.Status)
	assert.Equal(t, []tools.LogsReadLine{}, end.Result.Lines)
	assert.Nil(t, end.Result.ViewAround)
	assert.Equal(t, "No line of deployments/webapp in payments matched /panic/ between 2026-10-18T13:50:00Z and 2026-10-18T14:00:00Z.", end.Result.Text)

	anyLine := webappIn(1, logsinput.SampleBudget)
	anyLine.Grep = ""
	quiet := follow(t, anyLine, nil)
	quiet.expire(t)
	quiet.waited(t)
	assert.Equal(t, "No line of deployments/webapp in payments was written between 2026-10-18T13:50:00Z and 2026-10-18T14:00:00Z.", quiet.w.End("").Result.Text)
}

// A stop ends a watch stopped, keeping the lines it held, in the words of who
// stopped it.
func TestAStopKeepsTheLines(t *testing.T) {
	t.Parallel()
	for by, because := range map[string]string{
		llm.TaskStoppedByUser: "the user stopped it",
		llm.TaskStoppedByApp:  "Kstack quit",
		"model":               "it was stopped with TaskStop",
	} {
		f := follow(t, webappIn(3, logsinput.SampleBudget), nil, line(record(time.Second, 0, "panic: a")))
		testutil.Wait(t, f.sent, "the line")
		f.w.Stop()
		f.w.Stop()
		f.waited(t)

		end := f.w.End(by)
		assert.Equal(t, tools.WatchStopped, end.Status, by)
		assert.Equal(t, because, end.Result.EndedBecause, by)
		assert.Len(t, end.Result.Lines, 1, by)
		assert.Contains(t, end.Result.Text, "ended because "+because+".", by)
	}

	f := follow(t, webappIn(3, logsinput.SampleBudget), nil)
	f.w.Stop()
	f.waited(t)
	end := f.w.End(llm.TaskStoppedByUser)
	assert.Equal(t, tools.WatchStopped, end.Status)
	assert.Equal(t, "No line of deployments/webapp in payments matched /panic/ between 2026-10-18T13:50:00Z and 2026-10-18T14:00:00Z; "+
		"the watch ended because the user stopped it.", end.Result.Text)
}

// The follow's own end ends the watch with its reason, stripped and redacted: with
// lines held it is matched, with none it failed.
func TestTheFollowsEndEndsIt(t *testing.T) {
	t.Parallel()
	ended := logs.FollowFrame{Type: logs.FollowEnded, Reason: "every container of this view has stopped \x1b[31m"}

	held := follow(t, webappIn(3, logsinput.SampleBudget), nil, line(record(time.Second, 0, "panic: a")), ended)
	held.waited(t)
	end := held.w.End("")
	assert.Equal(t, tools.WatchMatched, end.Status)
	assert.Equal(t, "every container of this view has stopped", end.Result.EndedBecause)

	none := follow(t, webappIn(3, logsinput.SampleBudget), nil, ended)
	none.waited(t)
	end = none.w.End("")
	assert.Equal(t, tools.WatchFailed, end.Status)
	assert.Equal(t, "every container of this view has stopped", end.Result.EndedBecause)
	assert.Contains(t, end.Result.Text, "the watch ended because every container of this view has stopped.")
}

// A follow that ends on an error ends the watch with none of its text: failed
// with no line held, matched with one, as the follow's own end does.
func TestAFollowErrorEndsItWithNoText(t *testing.T) {
	t.Parallel()
	a := record(time.Second, 0, "panic: a")
	for _, sent := range [][]logs.FollowFrame{nil, {line(a)}} {
		broken := cluster.NewStream(context.Background(), func(ctx context.Context, out chan<- logs.FollowFrame) error {
			for _, f := range sent {
				out <- f
			}
			return errors.New("the log file closed: /secret/path")
		})
		ctx, cancel := context.WithCancel(context.Background())
		w := &watch{ctx: ctx, cancel: cancel, stream: broken, in: webappIn(3, logsinput.SampleBudget), view: webappView, now: func() time.Time { return clock }}
		w.Wait()

		end := w.End("")
		assert.Equal(t, "the logs could not be read", end.Result.EndedBecause)
		assert.NotContains(t, end.Result.Text, "secret")
		assert.Contains(t, end.Result.Text, "ended because the logs could not be read.")
		assert.Len(t, end.Result.Lines, len(sent))
		if len(sent) == 0 {
			assert.Equal(t, tools.WatchFailed, end.Status)
		} else {
			assert.Equal(t, tools.WatchMatched, end.Status)
		}
	}
}

// No sample a watch holds passes the inline limit, however long its lines, and its
// cursors are printed after the redaction, which could change them.
func TestNoSamplePassesTheInlineLimit(t *testing.T) {
	t.Parallel()
	var frames []logs.FollowFrame
	for i := range maxLimit {
		rec := record(time.Duration(i)*time.Second+time.Nanosecond, 0, strings.Repeat("m", logsinput.MaxSampleLine*2))
		rec.Gap = &logs.Gap{After: rec.Timestamp.Add(-time.Second), Before: rec.Timestamp}
		frames = append(frames, line(rec))
	}
	in := webappIn(maxLimit, logsinput.SampleBudget)
	in.Grep = strings.Repeat("g", 1024)
	f := follow(t, in, nil, frames...)
	f.waited(t)

	end := f.w.End("")
	assert.Less(t, len(end.Result.Text), tools.InlineLimit)
	assert.Contains(t, end.Result.Text, logsinput.EncodeCursor(record(time.Nanosecond, 0, "")))
}

// The cursors are printed after the header's redaction, which would otherwise take
// them with a credential's line: a pattern that reads like one redacts the rest.
func TestTheCursorsArePrintedAfterTheRedaction(t *testing.T) {
	t.Parallel()
	in := webappIn(1, logsinput.SampleBudget)
	in.Grep = "api-key:"
	a := record(time.Second, 0, "panic")
	f := follow(t, in, nil, line(a))
	f.waited(t)

	header := strings.Split(f.w.End("").Result.Text, "\n")[0]
	assert.True(t, strings.HasSuffix(header, "or as since for what followed: "+logsinput.EncodeCursor(a)), header)
	assert.NotContains(t, header, "ended because", "the redaction took the rest of the header's line")
}
