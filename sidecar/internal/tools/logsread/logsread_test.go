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

package logsread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/services/cluster"
	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/logsinput"
	"github.com/kstackhq/kstack/sidecar/internal/tools/internal/logsinput/logsinputtest"
)

// record is a line of prod/webapp-a/app at offset past 09:14, the ordinal-th of its
// moment, saying msg.
func record(offset time.Duration, ordinal int, msg string) logs.Record {
	return logs.Record{
		Timestamp: time.Date(2026, 10, 17, 9, 14, 0, 0, time.UTC).Add(offset), Message: msg,
		Stream:      logs.StreamKey{Namespace: "prod", Pod: "webapp-a", Container: "app"},
		ContainerID: "containerd://3f9a", Ordinal: ordinal,
	}
}

// fakeLogs is the log service as the tool reads it: one page or error for every
// fetch, each fetch's view and query kept.
type fakeLogs struct {
	page    logs.Page
	err     error
	queries []logs.FetchQuery
	views   []logs.View
	// wait holds a fetch until its context ends.
	wait bool
}

func (f *fakeLogs) Fetch(ctx context.Context, _ cluster.ClusterID, view logs.View, q logs.FetchQuery) (logs.Page, error) {
	f.views, f.queries = append(f.views, view), append(f.queries, q)
	if f.wait {
		<-ctx.Done()
		return logs.Page{}, ctx.Err()
	}
	return f.page, f.err
}

// run calls the tool over the watching cluster and l with raw, for cluster 7, and
// answers what the model reads and the action.
func run(t *testing.T, l *fakeLogs, raw string) (string, bool, *tools.Action) {
	t.Helper()
	return runOver(t, logsinputtest.Watching(t), l, raw)
}

func runOver(t *testing.T, f *logsinputtest.Service, l *fakeLogs, raw string) (string, bool, *tools.Action) {
	t.Helper()
	return New(f, l, func() time.Time { return clock }).RunShown(t.Context(), tools.Runtime{ClusterID: "7"}, json.RawMessage(raw))
}

// readOf is the read a call answers, which must read.
func readOf(t *testing.T, l *fakeLogs, raw string) (string, tools.LogsReadAction) {
	t.Helper()
	text, isError, shown := run(t, l, raw)
	require.False(t, isError, text)
	require.NotNil(t, shown)
	require.NotNil(t, shown.LogsRead)
	return text, *shown.LogsRead
}

// pod is the webapp deployment's view in prod, one pod's app container read.
const webapp = `{"sources":[{"namespace":"prod","resource":"deployments/webapp","containers":["app"]}]`

// The model reads a header, then one line per record, oldest first; the action
// holds the same lines, and the service is asked for the range as the call named it.
func TestAHeadReadFromAMoment(t *testing.T) {
	t.Parallel()
	since := "2026-10-17T09:13:00Z"
	l := &fakeLogs{page: logs.Page{
		Records: []logs.Record{record(19*time.Second+870*time.Millisecond, 0, "error: upstream timed out"), record(20*time.Second+4*time.Millisecond, 0, "panic: nil map")},
		Before:  logs.PageEdge{Cursor: &since, More: true},
	}}

	text, read := readOf(t, l, webapp+`,"since":"`+since+`","grep":"error|panic"}`)

	assert.Equal(t, "Read 2 lines of deployments/webapp in prod from 2026-10-17T09:13:00Z, oldest first, matching /error|panic/. Container app. Older lines may exist.\n"+
		"2026-10-17T09:14:19.87Z prod/webapp-a/app error: upstream timed out\n"+
		"2026-10-17T09:14:20.004Z prod/webapp-a/app panic: nil map", text)
	assert.Equal(t, []logs.FetchQuery{{Mode: logs.FetchHead, Since: &since, Limit: 50}}, l.queries)
	assert.Equal(t, "error|panic", l.views[0].Grep)
	at := time.Date(2026, 10, 17, 9, 13, 0, 0, time.UTC)
	assert.Equal(t, &at, read.Since)
	assert.Nil(t, read.Until)
	assert.Equal(t, tools.LogsReadHead, read.Mode)
	assert.Equal(t, clock, read.ResolvedAt)
	assert.Equal(t, tools.LogsReadUnread, read.Before)
	assert.Equal(t, tools.LogsReadEnd, read.After)
	assert.Equal(t, []string{}, read.Skipped)
	assert.Equal(t, "webapp", read.Sources[0].Name)
}

// The action's lines are the output's record lines, in order, field for field.
func TestTheActionIsTheSample(t *testing.T) {
	t.Parallel()
	prev := record(2*time.Second, 1, "b")
	prev.Stream.Previous = true
	l := &fakeLogs{page: logs.Page{Records: []logs.Record{record(time.Second, 0, "a"), prev}}}

	text, read := readOf(t, l, webapp+`}`)

	lines := strings.Split(text, "\n")[1:]
	require.Len(t, read.Lines, len(lines))
	for i, line := range read.Lines {
		stream := line.Namespace + "/" + line.Pod + "/" + line.Container
		if line.Previous {
			stream += " (previous)"
		}
		assert.Equal(t, line.Timestamp.Format(time.RFC3339Nano)+" "+stream+" "+line.Message, lines[i])
	}
	assert.Equal(t, tools.LogsReadLine{Timestamp: prev.Timestamp, Namespace: "prod", Pod: "webapp-a", Container: "app", Previous: true, Message: "b"}, read.Lines[1])
}

// firstLine is text's header.
func firstLine(text string) string { return strings.Split(text, "\n")[0] }

// A read with more past its far end prints the cursor to read on from: a head read
// its last line's after since, a tail read its first line's after until. A read that
// reached its far bound prints none.
func TestAReadWithMorePrintsWhereToReadOn(t *testing.T) {
	t.Parallel()
	a, b := record(time.Second, 0, "a"), record(2*time.Second, 0, "b")
	more := logs.PageEdge{More: true}

	text, read := readOf(t, &fakeLogs{page: logs.Page{Records: []logs.Record{a, b}, After: more}}, webapp+`}`)
	assert.Equal(t, "Read 2 lines of deployments/webapp in prod, oldest first. Container app. More lines follow; to read on, since: "+logsinput.EncodeCursor(b), firstLine(text))
	assert.Equal(t, tools.LogsReadEnd, read.Before)
	assert.Equal(t, tools.LogsReadMore, read.After)

	text, read = readOf(t, &fakeLogs{page: logs.Page{Records: []logs.Record{a, b}, Before: more}}, webapp+`,"mode":"tail"}`)
	assert.Equal(t, "Read 2 lines of deployments/webapp in prod, the newest. Container app. Older lines exist; to read on, until: "+logsinput.EncodeCursor(a), firstLine(text))
	assert.Equal(t, tools.LogsReadMore, read.Before)
	assert.Equal(t, tools.LogsReadEnd, read.After)

	text, read = readOf(t, &fakeLogs{page: logs.Page{Records: []logs.Record{a, b}}}, webapp+`,"until":"2026-10-17T09:15:00Z"}`)
	assert.Equal(t, "Read 2 lines of deployments/webapp in prod up to 2026-10-17T09:15:00Z, oldest first. Container app. Newer lines may exist.", firstLine(text))
	assert.Equal(t, tools.LogsReadUnread, read.After)
}

// A read continued from a printed cursor starts past its line, and the side it was
// continued from has more: the lines the model just read. A moment on that side may
// have more, and the action records each end's moment.
func TestEachEndIsEndMoreOrUnread(t *testing.T) {
	t.Parallel()
	a := record(time.Second, 0, "a")
	cursor := logsinput.EncodeCursor(a)

	l := &fakeLogs{page: logs.Page{Records: []logs.Record{record(2*time.Second, 0, "b")}, Before: logs.PageEdge{More: true}}}
	text, read := readOf(t, l, webapp+`,"since":"`+cursor+`"}`)
	assert.Equal(t, a.Cursor(), *l.queries[0].Since)
	assert.Equal(t, "Read 1 line of deployments/webapp in prod after 2026-10-17T09:14:01Z, oldest first. Container app. Older lines exist.", firstLine(text))
	assert.Equal(t, tools.LogsReadMore, read.Before)
	assert.Equal(t, a.Timestamp, *read.Since)

	l = &fakeLogs{page: logs.Page{Records: []logs.Record{record(0, 0, "z")}, After: logs.PageEdge{More: true}}}
	text, read = readOf(t, l, webapp+`,"until":"`+cursor+`","since":"2026-10-17T09:00:00Z","mode":"tail"}`)
	assert.Equal(t, a.Cursor(), *l.queries[0].Until)
	assert.Equal(t, "Read 1 line of deployments/webapp in prod from 2026-10-17T09:00:00Z before 2026-10-17T09:14:01Z, the newest. Container app. More lines follow. Older lines may exist.", firstLine(text))
	assert.Equal(t, tools.LogsReadMore, read.After)
	assert.Equal(t, tools.LogsReadUnread, read.Before)
}

// An empty range is a true answer: no lines between its ends.
func TestAnEmptyRangeReadsNoLines(t *testing.T) {
	t.Parallel()
	text, read := readOf(t, &fakeLogs{page: logs.Page{}}, webapp+`,"since":"2026-10-17T09:00:00Z","until":"2026-10-17T09:05:00Z"}`)
	assert.Equal(t, "No lines of deployments/webapp in prod between 2026-10-17T09:00:00Z and 2026-10-17T09:05:00Z. Container app. Older lines may exist. Newer lines may exist.", text)
	assert.Equal(t, []tools.LogsReadLine{}, read.Lines)
	assert.Nil(t, read.ViewAround)
}

// The cursor is printed after the header's redaction, which would otherwise take it
// with the rest of a line that names a credential header.
func TestTheCursorIsPrintedAfterTheRedaction(t *testing.T) {
	t.Parallel()
	b := record(2*time.Second, 0, "b")
	text, _ := readOf(t, &fakeLogs{page: logs.Page{Records: []logs.Record{b}, After: logs.PageEdge{More: true}}}, webapp+`,"grep":"api-key:"}`)
	assert.True(t, strings.HasSuffix(firstLine(text), "; to read on, since: "+logsinput.EncodeCursor(b)), firstLine(text))
}

// A record's hole is a line of the sidecar's before it, and the action line's gap.
func TestAHoleIsALineBeforeItsRecord(t *testing.T) {
	t.Parallel()
	b := record(2*time.Second, 0, "b")
	b.Gap = &logs.Gap{After: record(time.Second, 0, "").Timestamp, Before: b.Timestamp}

	text, read := readOf(t, &fakeLogs{page: logs.Page{Records: []logs.Record{record(time.Second, 0, "a"), b}}}, webapp+`}`)

	assert.Equal(t, []string{
		"2026-10-17T09:14:01Z prod/webapp-a/app a",
		"[lines may be missing from prod/webapp-a/app between 2026-10-17T09:14:01Z and 2026-10-17T09:14:02Z]",
		"2026-10-17T09:14:02Z prod/webapp-a/app b",
	}, strings.Split(text, "\n")[1:])
	assert.Equal(t, b.Gap, read.Lines[1].Gap)
	assert.Nil(t, read.Lines[0].Gap)
}

// A message is stripped and redacted before it is cut, so a secret the cut would
// split is still redacted.
func TestAMessageIsRedactedBeforeItIsCut(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", logsinput.MaxSampleLine-8) + " sk-" + strings.Repeat("A", 30)
	text, read := readOf(t, &fakeLogs{page: logs.Page{Records: []logs.Record{record(time.Second, 0, long), record(2*time.Second, 0, "\x1b[31mred\x1b[0m")}}}, webapp+`}`)

	assert.NotContains(t, text, "sk-")
	assert.NotContains(t, text, "\x1b")
	assert.LessOrEqual(t, len(read.Lines[0].Message), logsinput.MaxSampleLine)
	assert.NotContains(t, read.Lines[1].Message, "\x1b")
	assert.LessOrEqual(t, len(logsinput.CutLine(strings.Repeat("é", logsinput.MaxSampleLine))), logsinput.MaxSampleLine)
	assert.True(t, strings.HasSuffix(logsinput.CutLine(strings.Repeat("é", logsinput.MaxSampleLine)), "é…"))
}

// A sample past its budget drops lines from the far end, counts them, says more
// lies that way, and reads on from the last line it kept.
func TestASampleDropsPastItsBudget(t *testing.T) {
	t.Parallel()
	var recs []logs.Record
	for i := range 100 {
		recs = append(recs, record(time.Duration(i)*time.Second, 0, strings.Repeat("m", 1000)))
	}

	text, read := readOf(t, &fakeLogs{page: logs.Page{Records: recs}}, webapp+`,"limit":100}`)
	kept := len(read.Lines)
	assert.Less(t, kept, 100)
	assert.Equal(t, 100-kept, read.Dropped)
	assert.Equal(t, recs[0].Timestamp, read.Lines[0].Timestamp)
	assert.Equal(t, tools.LogsReadMore, read.After)
	assert.Contains(t, firstLine(text), fmt.Sprintf(" %d more lines were left out to fit.", read.Dropped))
	assert.True(t, strings.HasSuffix(firstLine(text), "; to read on, since: "+logsinput.EncodeCursor(recs[kept-1])))
	assert.LessOrEqual(t, len(text)-len(firstLine(text)), logsinput.SampleBudget)

	text, read = readOf(t, &fakeLogs{page: logs.Page{Records: recs}}, webapp+`,"limit":100,"mode":"tail"}`)
	assert.Equal(t, recs[99].Timestamp, read.Lines[len(read.Lines)-1].Timestamp)
	assert.Equal(t, tools.LogsReadMore, read.Before)
	assert.True(t, strings.HasSuffix(firstLine(text), "; to read on, until: "+logsinput.EncodeCursor(recs[100-len(read.Lines)])))
}

// longName is a distinct object name of the longest length, 253 characters.
func longName(i int) string { return fmt.Sprintf("%s-%03d", strings.Repeat("a", 249), i) }

// missingSources is n pods the mirror does not hold, each named at length.
func missingSources(n int) string {
	var sources []string
	for i := range n {
		sources = append(sources, `{"namespace":"prod","resource":"pods/`+longName(i)+`"}`)
	}
	return `{"sources":[` + strings.Join(sources, ",") + `]`
}

// skippedStreams is n lines naming a stream the page could not read, each at length.
func skippedStreams(n int) []string {
	var out []string
	for i := range n {
		out = append(out, longName(i)+"/"+strings.Repeat("c", 63)+": the server refused this log: forbidden")
	}
	return out
}

// The header names skipped streams up to its budget and counts the rest; the
// action keeps every one.
func TestTheHeaderNamesSkippedStreamsUpToItsBudget(t *testing.T) {
	t.Parallel()
	text, read := readOf(t, &fakeLogs{page: logs.Page{Skipped: skippedStreams(100)}}, webapp+`}`)
	assert.Len(t, read.Skipped, 100)
	assert.Contains(t, text, " Could not read "+read.Skipped[0])
	assert.Regexp(t, `, and \d+ more streams\.`, text)
	assert.Less(t, len(text), skippedBudget+500)
}

// The header names each source's notes up to its budget and counts the sources
// left out; the action keeps every source.
func TestTheHeaderNamesNotesUpToItsBudget(t *testing.T) {
	t.Parallel()
	text, read := readOf(t, &fakeLogs{}, missingSources(100)+`}`)
	assert.Len(t, read.Sources, 100)
	assert.Contains(t, text, "pods/"+longName(0)+": not in Kstack's cache of the cluster yet")
	assert.Regexp(t, ` Notes on \d+ more sources left out\.`, text)
	assert.Less(t, len(text), notesBudget+1000)
}

// No output passes the inline limit: the longest lines at the most of them, with
// the most skipped streams and missing sources, all at length, and the longest grep.
func TestNoOutputPassesTheInlineLimit(t *testing.T) {
	t.Parallel()
	var recs []logs.Record
	for i := range 100 {
		rec := record(time.Duration(i)*time.Second+time.Nanosecond, 0, strings.Repeat("m", logsinput.MaxSampleLine*2))
		rec.Stream = logs.StreamKey{Namespace: strings.Repeat("n", 63), Pod: longName(i), Container: strings.Repeat("c", 63), Previous: true}
		rec.Gap = &logs.Gap{After: rec.Timestamp, Before: rec.Timestamp}
		recs = append(recs, rec)
	}
	page := logs.Page{Records: recs, After: logs.PageEdge{More: true}, Before: logs.PageEdge{More: true}, Skipped: skippedStreams(100)}
	grep := strings.Repeat("g", logs.MaxGrep)
	for _, mode := range []string{"head", "tail"} {
		text, _ := readOf(t, &fakeLogs{page: page}, missingSources(100)+`,"grep":"`+grep+`","limit":100,"mode":"`+mode+`","since":"PT1H","until":"2026-10-17T09:59:59.999999999Z"}`)
		assert.LessOrEqual(t, len(text), tools.InlineLimit, mode)
	}
}

// refusalOf is the refusal a call answers, which must refuse with no action.
func refusalOf(t *testing.T, f *logsinputtest.Service, l *fakeLogs, raw string) string {
	t.Helper()
	text, isError, shown := runOver(t, f, l, raw)
	require.True(t, isError, text)
	assert.Nil(t, shown)
	return text
}

// A view the mirror refuses is refused as LogsView refuses it, before any read.
func TestAViewTheMirrorRefusesIsRefused(t *testing.T) {
	t.Parallel()
	l := &fakeLogs{}
	assert.JSONEq(t, `{"error":"no-container","source":0,"containers":["app","istio-proxy","setup"]}`,
		refusalOf(t, logsinputtest.Watching(t), l, `{"sources":[{"namespace":"prod","resource":"pods/webapp-a","containers":["nope"]}]}`))
	assert.JSONEq(t, `{"error":"no-cache"}`, refusalOf(t, &logsinputtest.Service{ReadErr: cluster.ErrNotFound}, l, webapp+`}`))
	syncing := logsinputtest.Watching(t)
	syncing.CacheHealth.Reason = "Connecting"
	assert.JSONEq(t, `{"error":"syncing"}`, refusalOf(t, syncing, l, webapp+`}`))
	assert.Empty(t, l.queries)
}

// A read the service refuses answers its words, as a line is read; any other
// failure carries none of its text.
func TestAFailedReadIsRefused(t *testing.T) {
	t.Parallel()
	l := &fakeLogs{err: logs.Refusal{Reason: "no container of this view could be read: webapp-a/app: \x1b[31mdenied sk-" + strings.Repeat("A", 30)}}
	text := refusalOf(t, logsinputtest.Watching(t), l, webapp+`}`)
	assert.Contains(t, text, `"error":"refused"`)
	assert.Contains(t, text, "no container of this view could be read: webapp-a/app")
	assert.NotContains(t, text, "sk-")
	assert.NotContains(t, text, "\x1b")

	l = &fakeLogs{err: errors.New("open /home/user/cache: disk I/O error")}
	assert.JSONEq(t, `{"error":"read-failed"}`, refusalOf(t, logsinputtest.Watching(t), l, webapp+`}`))
}

// A read past its own deadline answers timed-out itself, inside the loop's bound on
// the call.
func TestASlowReadTimesOut(t *testing.T) {
	t.Parallel()
	assert.Less(t, readTimeout, New(nil, nil, time.Now).CallTimeout(nil))
	tool := New(logsinputtest.Watching(t), &fakeLogs{wait: true}, func() time.Time { return clock })
	// The fetch waits out this deadline, so the test's wait is the read's own.
	tool.readTimeout = time.Millisecond
	text, isError, shown := tool.RunShown(t.Context(), tools.Runtime{ClusterID: "7"}, json.RawMessage(webapp+`}`))
	assert.True(t, isError)
	assert.Nil(t, shown)
	assert.JSONEq(t, `{"error":"timed-out"}`, text)
}

// View around this opens the read's view at its first line's moment, unpinned, in
// either mode; a read of no line has none.
func TestViewAroundIsTheViewAtTheFirstLine(t *testing.T) {
	t.Parallel()
	a, b := record(time.Second, 0, "a"), record(2*time.Second, 0, "b")
	for _, mode := range []string{"head", "tail"} {
		_, read := readOf(t, &fakeLogs{page: logs.Page{Records: []logs.Record{a, b}}}, webapp+`,"grep":"a|b","mode":"`+mode+`"}`)
		require.NotNil(t, read.ViewAround, mode)
		assert.Equal(t, tools.LogsViewAction{
			LogsViewInput: read.LogsViewInput,
			Anchor:        tools.LogsViewAnchor{Kind: tools.LogsAnchorAt, At: &a.Timestamp},
			ResolvedAt:    clock,
		}, *read.ViewAround, mode)
		assert.Equal(t, "a|b", read.ViewAround.Grep)
	}
}

func TestTheToolIsAShownBoundedRead(t *testing.T) {
	t.Parallel()
	tool := New(nil, nil, time.Now)
	def := tool.Definition()
	assert.Equal(t, Name, def.Name)
	assert.Equal(t, description, def.Description)
	assert.JSONEq(t, string(inputSchema), string(def.InputSchema))
	assert.Equal(t, tools.ActionLogsRead, tool.ActionKind())
	assert.Equal(t, prompt, tool.Prompt())
	_, err := tool.Action(json.RawMessage(webapp+`}`), "", false)
	assert.ErrorIs(t, err, errNotShown)
}

// Run is RunShown with the action dropped, and the action carries the description.
func TestRunAnswersTheSample(t *testing.T) {
	t.Parallel()
	_, _, shown := run(t, &fakeLogs{}, `{"description":"Why checkout failed","sources":[{"namespace":"prod","resource":"deployments/webapp"}]}`)
	require.NotNil(t, shown)
	assert.Equal(t, "Why checkout failed", shown.Description)

	text, isError := New(logsinputtest.Watching(t), &fakeLogs{}, func() time.Time { return clock }).Run(t.Context(), tools.Runtime{ClusterID: "7"}, json.RawMessage(webapp+`}`))
	assert.False(t, isError)
	assert.True(t, strings.HasPrefix(text, "No lines of deployments/webapp in prod."), text)
}
