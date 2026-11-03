// Command coverdiff lists the changed lines a local coverage run measured but
// no test reached, as Coverage · Gate would flag them. scripts/cover-changed.sh
// runs it:
//
//	go run ./coverdiff -root .. -diff changes.diff -go merged.out -lcov lcov.info
//
// -diff is a unified diff (`git diff -U0`) whose added lines are the changed
// lines. -go is a Go cover profile of the module in -godir, whose go.mod maps
// its import paths back to files. Each -lcov is a report whose SF paths are
// relative to -root or absolute. Every path printed is relative to -root.
//
// It prints `file:line` and the line's state for each one, and exits 1 when it
// lists any. A line no report measured is not listed: the gate skips it too.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
)

func main() {
	var cfg config
	flag.StringVar(&cfg.root, "root", ".", "the repository the paths are relative to")
	flag.StringVar(&cfg.diff, "diff", "", "unified diff of the changes")
	flag.StringVar(&cfg.goDir, "godir", "sidecar", "the Go module the -go profile measured, relative to -root")
	flag.StringVar(&cfg.goProfile, "go", "", "a Go cover profile")
	flag.Func("lcov", "an lcov report (repeatable)", func(path string) error {
		cfg.lcov = append(cfg.lcov, path)
		return nil
	})
	flag.Parse()
	if cfg.diff == "" || flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: coverdiff -root <repo> -diff <diff> [-godir <dir> -go <profile>] [-lcov <report>]...")
		os.Exit(2)
	}
	err := run(cfg, os.Stdout)
	if errors.Is(err, errUncovered) {
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "coverdiff:", err)
		os.Exit(1)
	}
}

type config struct {
	root      string
	diff      string
	goDir     string
	goProfile string
	lcov      []string
}

// errUncovered is the verdict; the lines that earned it are already printed.
var errUncovered = errors.New("changed lines are not covered")

// state sums every measure of a line: each Go block spanning it, each lcov
// line or branch record on it. One that both ran and missed is partial.
type state struct {
	hit, missed bool
}

// verdict is how the gate lists the line, or empty when it is covered.
func (s state) verdict() string {
	switch {
	case !s.hit:
		return "not covered"
	case s.missed:
		return "partially covered"
	}
	return ""
}

// coverage holds each measured line's state, by file relative to the root.
type coverage map[string]map[int]state

// add records one measure of the line.
func (c coverage) add(file string, line int, ran bool) {
	lines := c[file]
	if lines == nil {
		lines = map[int]state{}
		c[file] = lines
	}
	s := lines[line]
	s.hit = s.hit || ran
	s.missed = s.missed || !ran
	lines[line] = s
}

func run(cfg config, out io.Writer) error {
	changed, err := readDiff(cfg.diff)
	if err != nil {
		return err
	}
	cov := coverage{}
	if cfg.goProfile != "" {
		if err := readGo(cov, cfg.root, cfg.goDir, cfg.goProfile); err != nil {
			return err
		}
	}
	for _, path := range cfg.lcov {
		if err := readLcov(cov, cfg.root, path); err != nil {
			return err
		}
	}

	files := make([]string, 0, len(changed))
	for file := range changed {
		files = append(files, file)
	}
	sort.Strings(files)
	listed := 0
	for _, file := range files {
		for _, line := range changed[file] {
			s, measured := cov[file][line]
			if v := s.verdict(); measured && v != "" {
				fmt.Fprintf(out, "%s:%d %s\n", file, line, v)
				listed++
			}
		}
	}
	if listed > 0 {
		return errUncovered
	}
	return nil
}
