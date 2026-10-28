//go:build ignore

// Coverage gate for the frontend. Runs vitest with coverage, reads the line
// tally from coverage/coverage-summary.json, and fails below the percentage in
// scripts/coverage-threshold. Vitest itself only reports. Run from the repo root:
//
//	go run scripts/coverage-js.go                    # gate (make cover-js)
//	go run scripts/coverage-js.go -nogate            # measure, do not gate
//	go run scripts/coverage-js.go -summary p         # also copy the summary to p
//	go run scripts/coverage-js.go -spread s1 s2 ...  # how summaries vary, run no tests
//
// The build tag keeps this file out of every build; `go run` takes the path
// directly.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const summaryPath = "coverage/coverage-summary.json"

func main() {
	nogate := flag.Bool("nogate", false, "measure and do not gate")
	summary := flag.String("summary", "", "copy the coverage summary to `path`")
	spreadRuns := flag.Bool("spread", false, "report how the summaries given as arguments vary, run no tests")
	flag.Parse()

	var err error
	switch {
	case *spreadRuns:
		err = spread(flag.Args(), os.Stdout)
	case flag.NArg() > 0:
		fmt.Fprintln(os.Stderr, "coverage-js: only -spread takes arguments")
		flag.Usage()
		os.Exit(2)
	default:
		err = measure(*nogate, *summary)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// measure runs the suite and gates on scripts/coverage-threshold unless nogate.
// A non-empty copyTo gets a copy of the summary.
func measure(nogate bool, copyTo string) error {
	// Through node, not pnpm: pnpm is a .cmd shim on Windows and a script with
	// no shebang in some installs, and exec runs neither reliably.
	cmd := exec.Command("node", filepath.Join("node_modules", "vitest", "vitest.mjs"), "run", "--coverage",
		"--coverage.reporter=text", "--coverage.reporter=json-summary")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("vitest: %w", err)
	}
	if copyTo != "" {
		raw, err := os.ReadFile(summaryPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(copyTo, raw, 0o644); err != nil {
			return err
		}
	}
	l, err := readSummary(summaryPath)
	if err != nil {
		return err
	}
	if nogate {
		fmt.Printf("coverage: %.2f%% of %d lines (not gated)\n", l.floored(), l.total)
		return nil
	}
	want, err := threshold()
	if err != nil {
		return err
	}
	fmt.Printf("coverage: %.2f%% of %d lines (threshold %.2f%%)\n", l.floored(), l.total, want)
	if l.floored() < want {
		return errors.New("coverage below threshold; add tests")
	}
	return nil
}

// lines is the line tally from vitest's json-summary.
type lines struct{ covered, total int }

func (l lines) pct() float64 { return 100 * float64(l.covered) / float64(l.total) }

// floored is pct cut to two decimals, as istanbul's percent() cuts the number
// vitest gates on. Integer division keeps float error out of the cut, and the
// result is the same float the threshold file parses to, so == compares them.
func (l lines) floored() float64 { return float64(10000*l.covered/l.total) / 100 }

// readSummary reads total.lines from a coverage-summary.json. Its pct is
// rounded already, so the tally is the source of every percentage here.
func readSummary(path string) (lines, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return lines{}, err
	}
	var summary struct {
		Total struct {
			Lines struct{ Total, Covered int }
		}
	}
	if err := json.Unmarshal(raw, &summary); err != nil {
		return lines{}, fmt.Errorf("%s: %w", path, err)
	}
	l := lines{covered: summary.Total.Lines.Covered, total: summary.Total.Lines.Total}
	if l.total == 0 {
		return lines{}, fmt.Errorf("%s: no lines measured", path)
	}
	return l, nil
}

// spread prints each summary's exact percentage, then the min, median and max.
func spread(paths []string, w io.Writer) error {
	if len(paths) == 0 {
		return errors.New("coverage-js: -spread needs at least one summary")
	}
	var pcts []float64
	for _, path := range paths {
		l, err := readSummary(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s: %.3f%% (%d/%d)\n", filepath.Base(path), l.pct(), l.covered, l.total)
		pcts = append(pcts, l.pct())
	}
	min, median, max := stats(pcts)
	fmt.Fprintf(w, "coverage over %d runs: min %.3f%%, median %.3f%%, max %.3f%%\n", len(pcts), min, median, max)
	return nil
}

// stats returns the min, median and max of a non-empty list.
func stats(values []float64) (min, median, max float64) {
	sorted := slices.Sorted(slices.Values(values))
	n := len(sorted)
	median = sorted[n/2]
	if n%2 == 0 {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[0], median, sorted[n-1]
}

// threshold reads scripts/coverage-threshold.
func threshold() (float64, error) {
	raw, err := os.ReadFile(filepath.Join("scripts", "coverage-threshold"))
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
}
