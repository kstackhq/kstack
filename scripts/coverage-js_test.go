//go:build ignore

// Run from the repo root: go test scripts/coverage-js.go scripts/coverage-js_test.go
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeSummary(t *testing.T, dir, name string, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadSummaryTakesTheTotalLines(t *testing.T) {
	path := writeSummary(t, t.TempDir(), "coverage-summary.json", `{
		"total": {"lines": {"total": 300, "covered": 297, "skipped": 0, "pct": 99}},
		"/src/a.ts": {"lines": {"total": 10, "covered": 1, "skipped": 0, "pct": 10}}
	}`)

	got, err := readSummary(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := (lines{covered: 297, total: 300}); got != want {
		t.Fatalf("readSummary = %+v, want %+v", got, want)
	}
}

func TestReadSummaryRefusesASummaryWithNoLines(t *testing.T) {
	for _, body := range []string{`not json`, `{}`, `{"total": {"lines": {"total": 0, "covered": 0}}}`} {
		path := writeSummary(t, t.TempDir(), "coverage-summary.json", body)
		if _, err := readSummary(path); err == nil {
			t.Errorf("readSummary(%s) succeeded", body)
		}
	}
}

// As istanbul's percent(), which vitest's threshold compares against.
func TestFlooredDropsEverythingPastTwoDecimals(t *testing.T) {
	for _, tc := range []struct {
		l    lines
		want string
	}{
		{lines{2, 3}, "66.66"},
		{lines{99999, 100000}, "99.99"},
		{lines{9791, 10001}, "97.90"},
		{lines{297, 300}, "99"},
		{lines{1, 1}, "100"},
	} {
		// The gate compares with ==, so the float must be the one the
		// threshold file parses to.
		want, _ := strconv.ParseFloat(tc.want, 64)
		if got := tc.l.floored(); got != want {
			t.Errorf("%+v.floored() = %v, want %v", tc.l, got, want)
		}
	}
}

func TestSpreadPrintsEachRunAndTheStats(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i, covered := range []int{297, 298, 300} {
		body := fmt.Sprintf(`{"total": {"lines": {"total": 300, "covered": %d}}}`, covered)
		paths = append(paths, writeSummary(t, dir, fmt.Sprintf("coverage-js-%d.json", i+1), body))
	}

	var out strings.Builder
	if err := spread(paths, &out); err != nil {
		t.Fatal(err)
	}
	want := `coverage-js-1.json: 99.000% (297/300)
coverage-js-2.json: 99.333% (298/300)
coverage-js-3.json: 100.000% (300/300)
coverage over 3 runs: min 99.000%, median 99.333%, max 100.000%
`
	if out.String() != want {
		t.Fatalf("spread printed\n%s\nwant\n%s", out.String(), want)
	}
}

func TestSpreadFailsWithNoSummary(t *testing.T) {
	if err := spread(nil, io.Discard); err == nil {
		t.Fatal("spread of no summaries succeeded")
	}
}
