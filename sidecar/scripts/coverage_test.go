//go:build ignore

// Run from sidecar/: go test scripts/coverage.go scripts/coverage_test.go
package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	mode = "mode: atomic\n"
	pkg  = "github.com/kstackhq/kstack/sidecar/"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunsInKeepsCompleteRunsInNumericOrder(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"coverage-linux-10.out":   "",
		"coverage-darwin-10.out":  "",
		"coverage-windows-10.out": "",
		"coverage-linux-2.out":    "",
		"coverage-darwin-2.out":   "",
		"coverage-windows-2.out":  "",
		// No Windows profile, so run 3 is incomplete.
		"coverage-linux-3.out":  "",
		"coverage-darwin-3.out": "",
	})

	runs, err := runsIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"2", "10"}; !reflect.DeepEqual(runs, want) {
		t.Fatalf("runs = %v, want %v", runs, want)
	}
}

func TestUnionCountsABlockAnyOSCoveredAndSplitsOutTheFilesLinuxDoesNotCompile(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"l.out": mode + pkg + "a.go:1.1,2.2 2 0\n" + pkg + "a.go:3.1,4.2 3 1\n",
		"d.out": mode + pkg + "a.go:1.1,2.2 2 1\n" + pkg + "a.go:3.1,4.2 3 0\n" + pkg + "b_darwin.go:1.1,2.2 4 0\n",
		"w.out": mode + pkg + "a.go:1.1,2.2 2 0\n" + pkg + "c_windows.go:1.1,2.2 1 1\n",
	})

	u, err := union(filepath.Join(dir, "l.out"), filepath.Join(dir, "d.out"), filepath.Join(dir, "w.out"))
	if err != nil {
		t.Fatal(err)
	}
	all, platform := u.totals()
	if want := (tally{hit: 6, total: 10}); all != want {
		t.Errorf("all = %+v, want %+v", all, want)
	}
	if want := (tally{hit: 1, total: 5}); platform != want {
		t.Errorf("platform = %+v, want %+v", platform, want)
	}
}

func TestUnionFailsWhenNothingBuildsOffLinux(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"same.out": mode + pkg + "a.go:1.1,2.2 2 1\n"})

	same := filepath.Join(dir, "same.out")
	if _, err := union(same, same, same); err == nil {
		t.Fatal("union of one OS's profile three times succeeded")
	}
}

func TestPrintStatsTakesTheMiddleOrTheMeanOfTheTwoMiddles(t *testing.T) {
	for _, tc := range []struct {
		in   []float64
		want string
	}{
		{[]float64{3, 1, 2}, "coverage over 3 runs: min 1.000%, median 2.000%, max 3.000%\n"},
		{[]float64{4, 1, 3, 2}, "coverage over 4 runs: min 1.000%, median 2.500%, max 4.000%\n"},
	} {
		var out strings.Builder
		printStats(&out, "coverage", tc.in)
		if out.String() != tc.want {
			t.Errorf("printStats(%v) printed %q, want %q", tc.in, out.String(), tc.want)
		}
	}
}

func TestMoversCountsTheRunsThatCoveredEachBlockSomeRunMissed(t *testing.T) {
	run := func(covered ...string) osUnion {
		u := osUnion{profile: newProfile()}
		for _, block := range []string{"a.go:1.1,2.2", "a.go:3.1,4.2", "b.go:1.1,2.2", "b.go:3.1,4.2"} {
			u.stmts[block] = 1
		}
		u.covered["b.go:1.1,2.2"] = true
		for _, block := range covered {
			u.covered[block] = true
		}
		return u
	}
	runs := []osUnion{
		run("b.go:3.1,4.2"),
		run("b.go:3.1,4.2", "a.go:1.1,2.2"),
		run("a.go:3.1,4.2"),
	}

	want := map[string]int{"a.go:1.1,2.2": 1, "a.go:3.1,4.2": 1, "b.go:3.1,4.2": 2}
	if got := movers(runs); !reflect.DeepEqual(got, want) {
		t.Fatalf("movers = %v, want %v", got, want)
	}
}

func TestSpreadPrintsEachRunTheStatsAndTheMovers(t *testing.T) {
	dir := t.TempDir()
	darwin := mode + pkg + "b_darwin.go:1.1,2.2 4 1\n"
	windows := mode + pkg + "c_windows.go:1.1,2.2 1 0\n"
	writeFiles(t, dir, map[string]string{
		"coverage-linux-1.out":   mode + pkg + "a.go:1.1,2.2 2 1\n" + pkg + "a.go:3.1,4.2 3 0\n",
		"coverage-darwin-1.out":  darwin,
		"coverage-windows-1.out": windows,
		"coverage-linux-2.out":   mode + pkg + "a.go:1.1,2.2 2 1\n" + pkg + "a.go:3.1,4.2 3 1\n",
		"coverage-darwin-2.out":  darwin,
		"coverage-windows-2.out": windows,
	})

	var out strings.Builder
	if err := spread(dir, &out); err != nil {
		t.Fatal(err)
	}
	want := `run 1: coverage 60.00% (6/10), outside linux 80.00% (4/5)
run 2: coverage 90.00% (9/10), outside linux 80.00% (4/5)
coverage over 2 runs: min 60.000%, median 75.000%, max 90.000%
coverage outside linux over 2 runs: min 80.000%, median 80.000%, max 80.000%
blocks covered in some runs and not others:
  1/2  a.go:3.1,4.2
`
	if out.String() != want {
		t.Fatalf("spread printed\n%s\nwant\n%s", out.String(), want)
	}
}

func TestSpreadFailsWithNoCompleteRun(t *testing.T) {
	if err := spread(t.TempDir(), io.Discard); err == nil {
		t.Fatal("spread of an empty directory succeeded")
	}
}
