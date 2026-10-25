//go:build ignore

// Coverage gate for the sidecar. Runs the suite twice — untagged and with
// `-tags debug`, the only build that compiles the environment overrides — merges
// the two profiles, drops generated files, and fails below the threshold in
// scripts/coverage-threshold. That measures one OS; CI measures each OS with
// -out and gates the union of the three profiles.
//
//	go run scripts/coverage.go          # check this OS against the threshold
//	go run scripts/coverage.go -report  # also list every file with a gap
//	go run scripts/coverage.go -out p   # write this OS's profile to p, no gate
//	go run scripts/coverage.go -linux l -darwin d -windows w
//	                                    # gate the union, run no tests
//
// The union is gated twice: everything against scripts/coverage-threshold, and
// the files Linux does not compile against scripts/coverage-threshold-platform.
// Those are too few to move the first number.
//
// The build tag keeps this file out of `go build ./...` and out of the profile
// it measures; `go run` takes the path directly.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Generated files nobody hand-writes. graph/schema.resolvers.go carries a
// "Code generated" header too but its bodies are ours, so the list is explicit
// rather than derived from the header.
var generated = []string{
	"graph/generated.go",
	"graph/model/models_gen.go",
	"grpc/authpb/",
	"grpc/pokepb/",
}

const modulePath = "github.com/kstackhq/kstack/sidecar/"

func main() {
	report := flag.Bool("report", false, "list every file with uncovered statements")
	out := flag.String("out", "", "write this OS's merged profile to `path` and do not gate")
	linux := flag.String("linux", "", "gate the union of this Linux profile, -darwin's and -windows'")
	darwin := flag.String("darwin", "", "macOS profile, for -linux")
	windows := flag.String("windows", "", "Windows profile, for -linux")
	flag.Parse()

	switch {
	case *linux != "" || *darwin != "" || *windows != "":
		if *out != "" {
			usage("coverage: -out does not combine with -linux, -darwin or -windows")
		}
		if *linux == "" || *darwin == "" || *windows == "" {
			usage("coverage: -linux, -darwin and -windows go together")
		}
		gate(*linux, *darwin, *windows, *report)
	case *out != "":
		measure(*out, *report)
	default:
		check(*report)
	}
}

func usage(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	flag.Usage()
	os.Exit(2)
}

// check runs the suite on this OS and gates on scripts/coverage-threshold.
func check(report bool) {
	stmts, covered := runSuites()
	files := tallyFiles(stmts, covered)
	all := sum(files, nil)
	if all.total == 0 {
		fmt.Fprintln(os.Stderr, "coverage: no statements measured")
		os.Exit(1)
	}
	if report {
		printReport(files, nil)
	}

	want := threshold("coverage-threshold")
	fmt.Printf("coverage: %.2f%% of %d statements (threshold %.2f%%)\n", all.pct(), all.total, want)
	if all.pct()+1e-9 < want {
		fmt.Fprintf(os.Stderr, "coverage below threshold; add tests or lower scripts/coverage-threshold deliberately\n")
		os.Exit(1)
	}
}

// gate unions the three OSes' profiles and gates on both thresholds.
func gate(linux, darwin, windows string, report bool) {
	stmts := map[string]int{}
	covered := map[string]bool{}
	// A block absent from the Linux profile is in a file Linux does not
	// compile: with -coverpkg=./... a profile holds every compiled block.
	onLinux := map[string]bool{}
	if err := mergeProfile(linux, stmts, covered, onLinux); err != nil {
		fail(err)
	}
	for _, path := range []string{darwin, windows} {
		if err := mergeProfile(path, stmts, covered, nil); err != nil {
			fail(err)
		}
	}
	files := tallyFiles(stmts, covered)
	// Build constraints are per file, so a file is wholly on one side.
	outsideLinux := map[string]bool{}
	for block := range stmts {
		if !onLinux[block] {
			outsideLinux[block[:strings.Index(block, ":")]] = true
		}
	}
	all := sum(files, nil)
	platform := sum(files, func(f string) bool { return outsideLinux[f] })
	if all.total == 0 {
		fmt.Fprintln(os.Stderr, "coverage: no statements measured")
		os.Exit(1)
	}
	// Some file always builds off Linux, so none means a profile is missing.
	if platform.total == 0 {
		fmt.Fprintln(os.Stderr, "coverage: no statements outside linux measured")
		os.Exit(1)
	}
	if report {
		printReport(files, func(f string) string {
			if outsideLinux[f] {
				return " (outside linux)"
			}
			return ""
		})
	}

	want := threshold("coverage-threshold")
	wantPlatform := threshold("coverage-threshold-platform")
	fmt.Printf("coverage: %.2f%% of %d statements (threshold %.2f%%)\n", all.pct(), all.total, want)
	fmt.Printf("coverage outside linux: %.2f%% of %d statements (threshold %.2f%%)\n", platform.pct(), platform.total, wantPlatform)
	failed := false
	if all.pct()+1e-9 < want {
		fmt.Fprintf(os.Stderr, "coverage below threshold; add tests or lower scripts/coverage-threshold deliberately\n")
		failed = true
	}
	if platform.pct()+1e-9 < wantPlatform {
		fmt.Fprintf(os.Stderr, "coverage outside linux below threshold; add tests or lower scripts/coverage-threshold-platform deliberately\n")
		failed = true
	}
	if failed {
		os.Exit(1)
	}
}

// measure runs the suite on this OS and writes the union to path for gate.
// It does not gate: this OS alone is not the number CI holds to a threshold.
func measure(path string, report bool) {
	stmts, covered := runSuites()
	files := tallyFiles(stmts, covered)
	all := sum(files, nil)
	if all.total == 0 {
		fmt.Fprintln(os.Stderr, "coverage: no statements measured")
		os.Exit(1)
	}
	if err := writeProfile(path, stmts, covered); err != nil {
		fail(err)
	}
	if report {
		printReport(files, nil)
	}
	fmt.Printf("coverage: %.2f%% of %d statements (this OS alone)\n", all.pct(), all.total)
}

// writeProfile writes the union as a cover profile, blocks sorted so two runs
// write the same file. modulePath goes back on so go tool cover can read it.
func writeProfile(path string, stmts map[string]int, covered map[string]bool) error {
	blocks := make([]string, 0, len(stmts))
	for block, n := range stmts {
		if n > 0 {
			blocks = append(blocks, block)
		}
	}
	sort.Strings(blocks)

	var b strings.Builder
	b.WriteString("mode: atomic\n")
	for _, block := range blocks {
		count := 0
		if covered[block] {
			count = 1
		}
		fmt.Fprintf(&b, "%s%s %d %d\n", modulePath, block, stmts[block], count)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// runSuites runs the suite untagged and with -tags debug and returns the union.
// One block per source range. A block is covered if any run reached it:
// -tags debug compiles code the untagged run cannot see, and vice versa.
func runSuites() (stmts map[string]int, covered map[string]bool) {
	stmts = map[string]int{}
	covered = map[string]bool{}
	for _, tags := range []string{"", "debug"} {
		profile, err := runSuite(tags)
		if err != nil {
			fail(err)
		}
		err = mergeProfile(profile, stmts, covered, nil)
		os.Remove(profile)
		if err != nil {
			fail(err)
		}
	}
	return stmts, covered
}

// tally counts statements: all of them, and those some profile covered.
type tally struct{ hit, total int }

func (t tally) pct() float64 { return 100 * float64(t.hit) / float64(t.total) }

// tallyFiles groups the union's statements by file.
func tallyFiles(stmts map[string]int, covered map[string]bool) map[string]tally {
	files := map[string]tally{}
	for block, n := range stmts {
		if n == 0 {
			continue
		}
		file := block[:strings.Index(block, ":")]
		t := files[file]
		t.total += n
		if covered[block] {
			t.hit += n
		}
		files[file] = t
	}
	return files
}

// sum adds up the files keep accepts, or every file when keep is nil.
func sum(files map[string]tally, keep func(file string) bool) tally {
	var all tally
	for f, t := range files {
		if keep == nil || keep(f) {
			all.hit += t.hit
			all.total += t.total
		}
	}
	return all
}

// printReport lists every file with uncovered statements, most uncovered
// first, each followed by mark(file) when mark is non-nil.
func printReport(files map[string]tally, mark func(file string) string) {
	var gaps []string
	for f, t := range files {
		if t.hit < t.total {
			gaps = append(gaps, f)
		}
	}
	missing := func(f string) int { return files[f].total - files[f].hit }
	sort.Slice(gaps, func(i, j int) bool { return missing(gaps[i]) > missing(gaps[j]) })
	for _, f := range gaps {
		suffix := ""
		if mark != nil {
			suffix = mark(f)
		}
		fmt.Printf("%4d uncovered  %5.1f%%  %s%s\n", missing(f), files[f].pct(), f, suffix)
	}
}

// runSuite runs the whole module with cross-package coverage and returns the
// profile path. -coverpkg=./... is what makes a helper exercised only from
// another package's test count.
func runSuite(tags string) (string, error) {
	out, err := os.CreateTemp("", "sidecar-cover-*.out")
	if err != nil {
		return "", err
	}
	out.Close()
	// The slowest run of the suite: no -short, every package instrumented.
	// Go's 10-minute default per test binary is too tight for it on Windows.
	args := []string{"test", "./...", "-timeout", "30m", "-coverpkg=./...", "-covermode=atomic", "-coverprofile=" + out.Name()}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	cmd := exec.Command("go", args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go test (tags=%q): %w", tags, err)
	}
	return out.Name(), nil
}

// mergeProfile folds one profile into the running union. With -coverpkg every
// package's test binary reports every block, so the same block arrives many
// times and only the OR of the counts is meaningful. A non-nil seen records
// every block the profile holds.
func mergeProfile(path string, stmts map[string]int, covered, seen map[string]bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		// "<file>:<range> <numStmts> <count>"
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return fmt.Errorf("%s: malformed profile line %q", path, line)
		}
		block := strings.TrimPrefix(fields[0], modulePath)
		if isGenerated(block) {
			continue
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		stmts[block] = n
		covered[block] = covered[block] || count > 0
		if seen != nil {
			seen[block] = true
		}
	}
	return sc.Err()
}

func isGenerated(block string) bool {
	for _, g := range generated {
		if strings.HasPrefix(block, g) {
			return true
		}
	}
	return false
}

// threshold reads one of the scripts/coverage-threshold* files.
func threshold(name string) float64 {
	raw, err := os.ReadFile(filepath.Join("scripts", name))
	if err != nil {
		fail(err)
	}
	want, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	if err != nil {
		fail(err)
	}
	return want
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
