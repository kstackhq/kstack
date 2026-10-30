package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	sha    = "head"
	runURL = "https://github.com/kstackhq/kstack/actions/runs/1"
)

// A fake Codecov. Each request URI answers from its queue and the last answer
// repeats; a URI with no queue is a 404.
type fake struct {
	mu      sync.Mutex
	answers map[string][]any
	seen    []string
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	uri := r.URL.RequestURI()
	f.seen = append(f.seen, uri)
	queue, ok := f.answers[uri]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if len(queue) > 1 {
		f.answers[uri] = queue[1:]
	}
	json.NewEncoder(w).Encode(queue[0])
}

// gate runs the tool against f with cfg's pr, flags and timeout.
func gate(t *testing.T, cfg config, f *fake) (out, summary string, err error) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg.sha = sha
	cfg.run = runURL
	cfg.threshold = 0.1
	cfg.interval = time.Millisecond
	if cfg.timeout == 0 {
		cfg.timeout = time.Minute
	}
	var o, s bytes.Buffer
	cc := &codecov{api: srv.URL, client: srv.Client()}
	err = run(context.Background(), cfg, cc, &o, &s)
	return o.String(), s.String(), err
}

// ready is a Codecov that has processed go-linux and has cmp for the pull request.
func ready(cmp comparison) *fake {
	return &fake{answers: map[string][]any{
		"/commits/head/uploads/?page_size=100": {uploads(processed("go-linux"))},
		"/commits/head/":                       {commit{State: "complete", Parent: "parent"}},
		"/compare/?pullid=7":                   {cmp},
	}}
}

func uploads(us ...upload) any { return map[string]any{"results": us} }

func processed(flag string) upload {
	return upload{Flags: []string{flag}, State: "processed", BuildURL: runURL}
}

func ptr(n int) *int { return &n }

func measured(n int, coverage *int, added bool) line {
	var l line
	l.Number.Head, l.Coverage.Head, l.Added = ptr(n), coverage, added
	return l
}

var even = &totals{Lines: 1000, Hits: 900}

// compared is a comparison of sha whose one file adds a line per entry of
// coverage, numbered from 1; a nil entry is a line no report measured. Its
// line 100 is unchanged, measured and missed, so it is not the patch's.
func compared(coverage ...*int) comparison {
	cmp := comparison{HeadCommit: sha, BaseCommit: "base"}
	cmp.Totals.Base, cmp.Totals.Head = even, even
	var f file
	f.Name.Head = "src/a,b.ts"
	f.Lines = append(f.Lines, measured(100, ptr(miss), false))
	for i, c := range coverage {
		f.Lines = append(f.Lines, measured(i+1, c, true))
	}
	cmp.Files = []file{f}
	return cmp
}

// goLinux gates pull request 7 after one upload.
var goLinux = config{pr: 7, flags: []string{"go-linux"}}

func TestPassesWhenEveryChangedLineIsCovered(t *testing.T) {
	out, summary, err := gate(t, goLinux, ready(compared(ptr(hit), nil, ptr(hit))))
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !strings.Contains(out, "patch: 2 of 2 changed lines covered") {
		t.Errorf("out:\n%s", out)
	}
	if strings.Contains(out, "::error") {
		t.Errorf("annotated a covered change:\n%s", out)
	}
	if !strings.Contains(summary, "**patch**: 2 of 2") {
		t.Errorf("summary:\n%s", summary)
	}
}

func TestFailsOnAnUncoveredOrPartialChangedLine(t *testing.T) {
	out, summary, err := gate(t, goLinux, ready(compared(ptr(hit), ptr(miss), ptr(partial))))
	if !errors.Is(err, errFailed) {
		t.Fatalf("err = %v, want errFailed", err)
	}
	for _, want := range []string{
		"patch: 1 of 3 changed lines covered",
		"::error file=src/a%2Cb.ts,line=2,title=Uncovered line::This changed line is not covered.",
		"::error file=src/a%2Cb.ts,line=3,title=Uncovered line::This changed line is partially covered.",
		"  src/a,b.ts:2 not covered",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("out lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "line=100") {
		t.Errorf("annotated an unchanged line:\n%s", out)
	}
	if !strings.Contains(summary, "- `src/a,b.ts:3` partially covered") {
		t.Errorf("summary:\n%s", summary)
	}
}

func TestFailsWhenTheTotalDropsPastTheThreshold(t *testing.T) {
	// 90.00% to 89.85%: 0.15 points.
	cmp := compared(ptr(hit))
	cmp.Totals.Base, cmp.Totals.Head = &totals{Lines: 2000, Hits: 1800}, &totals{Lines: 2000, Hits: 1797}
	out, _, err := gate(t, goLinux, ready(cmp))
	if !errors.Is(err, errFailed) {
		t.Fatalf("err = %v, want errFailed\n%s", err, out)
	}
	if !strings.Contains(out, "project: 89.85% at the head, 90.00% at base base: FAILED") {
		t.Errorf("out:\n%s", out)
	}
}

func TestPassesADropWithinTheThreshold(t *testing.T) {
	// 90.00% to 89.95%: 0.05 points.
	cmp := compared(ptr(hit))
	cmp.Totals.Base, cmp.Totals.Head = &totals{Lines: 2000, Hits: 1800}, &totals{Lines: 2000, Hits: 1799}
	out, _, err := gate(t, goLinux, ready(cmp))
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !strings.Contains(out, "at base base: ok") {
		t.Errorf("out:\n%s", out)
	}
}

func TestDoesNotJudgeTheProjectWithoutABase(t *testing.T) {
	cmp := compared(ptr(hit))
	cmp.Totals.Base = nil
	out, summary, err := gate(t, goLinux, ready(cmp))
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !strings.Contains(out, "project: no base report") || !strings.Contains(summary, "not judged") {
		t.Errorf("out:\n%s\nsummary:\n%s", out, summary)
	}
}

func TestWaitsForEveryUploadTheCommitAndTheComparison(t *testing.T) {
	pending := processed("typescript")
	pending.State = "uploaded"
	anotherRun := upload{Flags: []string{"rust-linux"}, State: "uploaded", BuildURL: "https://github.com/kstackhq/kstack/actions/runs/0"}
	stale := compared(ptr(miss))
	stale.HeadCommit = "older"
	f := &fake{answers: map[string][]any{
		"/commits/head/uploads/?page_size=100": {
			uploads(),
			uploads(processed("go-linux")),
			uploads(processed("go-linux"), pending),
			// Another run's pending upload is not waited for.
			uploads(processed("go-linux"), processed("typescript"), anotherRun),
		},
		"/commits/head/":     {commit{State: "pending"}, commit{State: "complete"}},
		"/compare/?pullid=7": {stale, compared(ptr(hit))},
	}}
	out, _, err := gate(t, config{pr: 7, flags: []string{"go-linux", "typescript"}}, f)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	want := []string{
		"/commits/head/uploads/?page_size=100",
		"/commits/head/uploads/?page_size=100",
		"/commits/head/uploads/?page_size=100",
		"/commits/head/uploads/?page_size=100",
		"/commits/head/",
		"/commits/head/uploads/?page_size=100",
		"/commits/head/",
		"/compare/?pullid=7",
		"/commits/head/uploads/?page_size=100",
		"/commits/head/",
		"/compare/?pullid=7",
	}
	if got := strings.Join(f.seen, "\n"); got != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

func TestComparesAPushWithItsParent(t *testing.T) {
	f := ready(compared(ptr(hit)))
	f.answers["/compare/?base=parent&head=head"] = f.answers["/compare/?pullid=7"]
	delete(f.answers, "/compare/?pullid=7")
	out, _, err := gate(t, config{flags: []string{"go-linux"}}, f)
	if err != nil {
		t.Fatalf("err = %v\n%s\n%v", err, out, f.seen)
	}
}

func TestATimeoutIsNotACoverageFailure(t *testing.T) {
	cfg := goLinux
	cfg.timeout = 20 * time.Millisecond
	_, _, err := gate(t, cfg, &fake{})
	if err == nil || errors.Is(err, errFailed) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	for _, want := range []string{"did not finish processing head within 20ms", "not a coverage failure"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err lacks %q: %v", want, err)
		}
	}
}

func TestFailsOnAnUploadCodecovCouldNotProcess(t *testing.T) {
	broken := processed("go-linux")
	broken.State = "error"
	f := &fake{answers: map[string][]any{
		"/commits/head/uploads/?page_size=100": {uploads(broken)},
	}}
	_, _, err := gate(t, goLinux, f)
	if err == nil || errors.Is(err, errFailed) || !strings.Contains(err.Error(), "failed to process upload go-linux") {
		t.Fatalf("err = %v", err)
	}
}

func TestGatesNothingWhenNoJobUploaded(t *testing.T) {
	f := &fake{}
	out, _, err := gate(t, config{}, f)
	if err != nil || !strings.Contains(out, "nothing to gate") || len(f.seen) != 0 {
		t.Fatalf("err = %v, out = %q, requests = %v", err, out, f.seen)
	}
}

func TestEscapesAWorkflowCommandProperty(t *testing.T) {
	if got := escapeProperty("a:b,c%d\r\ne"); got != "a%3Ab%2Cc%25d%0D%0Ae" {
		t.Errorf("escapeProperty = %q", got)
	}
}
