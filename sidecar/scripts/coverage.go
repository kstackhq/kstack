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
//	go run scripts/coverage.go -spread -dir d
//	                                    # how the union varies across runs
//
// The union is gated twice: everything against scripts/coverage-threshold, and
// the files Linux does not compile against scripts/coverage-threshold-platform.
// Those are too few to move the first number.
//
// -spread reads coverage-<os>-<run>.out for each run that has all three OSes,
// as the coverage-spread workflow writes them, and reports each run, the min,
// median and max, and every block some runs covered and others did not.
//
// The build tag keeps this file out of `go build ./...` and out of the profile
// it measures; `go run` takes the path directly.
package main

import (
	"bufio"
	"cmp"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	spreadRuns := flag.Bool("spread", false, "report how the union varies across the runs in -dir, run no tests")
	dir := flag.String("dir", "", "directory of coverage-<os>-<run>.out profiles, for -spread")
	flag.Parse()

	switch {
	case *spreadRuns:
		if *dir == "" {
			usage("coverage: -spread needs -dir")
		}
		if *report || *out != "" || *linux != "" || *darwin != "" || *windows != "" {
			usage("coverage: -spread takes -dir alone")
		}
		if err := spread(*dir, os.Stdout); err != nil {
			fail(err)
		}
	case *dir != "":
		usage("coverage: -dir is for -spread")
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
	p := runSuites()
	files := p.files()
	all := sum(files, nil)
	if report {
		printReport(files, nil)
	}

	want := threshold("coverage-threshold")
	fmt.Printf("coverage: %.2f%% of %d statements (threshold %.2f%%)\n", all.pct(), all.total, want)
	if all.pct()+1e-9 < want {
		fmt.Fprintln(os.Stderr, "coverage below threshold; add tests or lower scripts/coverage-threshold deliberately")
		os.Exit(1)
	}
}

// gate unions the three OSes' profiles and gates on both thresholds.
func gate(linux, darwin, windows string, report bool) {
	u, err := union(linux, darwin, windows)
	if err != nil {
		fail(err)
	}
	all, platform := u.totals()
	if report {
		printReport(u.files(), func(f string) string {
			if u.outsideLinux[f] {
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
		fmt.Fprintln(os.Stderr, "coverage below threshold; add tests or lower scripts/coverage-threshold deliberately")
		failed = true
	}
	if platform.pct()+1e-9 < wantPlatform {
		fmt.Fprintln(os.Stderr, "coverage outside linux below threshold; add tests or lower scripts/coverage-threshold-platform deliberately")
		failed = true
	}
	if failed {
		os.Exit(1)
	}
}

// measure runs the suite on this OS and writes the union to path for gate.
// It does not gate: this OS alone is not the number CI holds to a threshold.
func measure(path string, report bool) {
	p := runSuites()
	if err := p.write(path); err != nil {
		fail(err)
	}
	files := p.files()
	if report {
		printReport(files, nil)
	}
	all := sum(files, nil)
	fmt.Printf("coverage: %.2f%% of %d statements (this OS alone)\n", all.pct(), all.total)
}

// spread reports how coverage varies across the runs in dir: each run's union
// of the three OSes, the min, median and max of both numbers, and every block
// some runs covered and others did not. It gates nothing.
func spread(dir string, w io.Writer) error {
	runs, err := runsIn(dir)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return fmt.Errorf("coverage: no run in %s has all of coverage-{linux,darwin,windows}-<run>.out", dir)
	}
	var unions []osUnion
	var pcts, platformPcts []float64
	for _, run := range runs {
		u, err := union(runProfile(dir, "linux", run), runProfile(dir, "darwin", run), runProfile(dir, "windows", run))
		if err != nil {
			return err
		}
		all, platform := u.totals()
		fmt.Fprintf(w, "run %s: coverage %.2f%% (%d/%d), outside linux %.2f%% (%d/%d)\n",
			run, all.pct(), all.hit, all.total, platform.pct(), platform.hit, platform.total)
		unions = append(unions, u)
		pcts = append(pcts, all.pct())
		platformPcts = append(platformPcts, platform.pct())
	}
	printStats(w, "coverage", pcts)
	printStats(w, "coverage outside linux", platformPcts)

	fmt.Fprintln(w, "blocks covered in some runs and not others:")
	hits := movers(unions)
	for _, block := range slices.Sorted(maps.Keys(hits)) {
		fmt.Fprintf(w, "  %d/%d  %s\n", hits[block], len(runs), block)
	}
	return nil
}

// runsIn lists the runs in dir that have all three OSes' profiles. Numeric
// labels sort numerically, so run 10 follows run 2.
func runsIn(dir string) ([]string, error) {
	paths, err := filepath.Glob(runProfile(dir, "linux", "*"))
	if err != nil {
		return nil, err
	}
	var runs []string
	for _, path := range paths {
		run := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "coverage-linux-"), ".out")
		if exists(runProfile(dir, "darwin", run)) && exists(runProfile(dir, "windows", run)) {
			runs = append(runs, run)
		}
	}
	slices.SortFunc(runs, func(a, b string) int {
		x, errX := strconv.Atoi(a)
		y, errY := strconv.Atoi(b)
		if errX == nil && errY == nil {
			return cmp.Compare(x, y)
		}
		return strings.Compare(a, b)
	})
	return runs, nil
}

func runProfile(dir, goos, run string) string {
	return filepath.Join(dir, "coverage-"+goos+"-"+run+".out")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// movers counts, for each block some runs covered and others did not, the
// runs that covered it. A block a run's profiles lack is uncovered in that run.
func movers(runs []osUnion) map[string]int {
	hits := map[string]int{}
	for _, u := range runs {
		for block, ok := range u.covered {
			if ok {
				hits[block]++
			}
		}
	}
	maps.DeleteFunc(hits, func(_ string, n int) bool { return n == len(runs) })
	return hits
}

func printStats(w io.Writer, name string, pcts []float64) {
	sorted := slices.Sorted(slices.Values(pcts))
	n := len(sorted)
	median := sorted[n/2]
	if n%2 == 0 {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	}
	fmt.Fprintf(w, "%s over %d runs: min %.3f%%, median %.3f%%, max %.3f%%\n", name, n, sorted[0], median, sorted[n-1])
}

// osUnion is the three OSes' profiles merged, with the files Linux does not
// compile picked out so they can be gated on their own.
type osUnion struct {
	profile
	outsideLinux map[string]bool // by file
}

func union(linux, darwin, windows string) (osUnion, error) {
	u := osUnion{profile: newProfile(), outsideLinux: map[string]bool{}}
	if err := u.merge(linux); err != nil {
		return osUnion{}, err
	}
	// With -coverpkg=./... a profile holds every block its OS compiles, so a
	// block the other two add is in a file Linux does not compile. Build
	// constraints are per file, so a file is wholly on one side.
	onLinux := maps.Clone(u.stmts)
	for _, path := range []string{darwin, windows} {
		if err := u.merge(path); err != nil {
			return osUnion{}, err
		}
	}
	for block := range u.stmts {
		if _, ok := onLinux[block]; !ok {
			u.outsideLinux[fileOf(block)] = true
		}
	}
	// Some file always builds off Linux, so none means a profile is missing.
	if len(u.outsideLinux) == 0 {
		return osUnion{}, fmt.Errorf("coverage: no statements outside linux in %s and %s", darwin, windows)
	}
	return u, nil
}

// totals tallies every file, and the files Linux does not compile.
func (u osUnion) totals() (all, platform tally) {
	files := u.files()
	return sum(files, nil), sum(files, func(f string) bool { return u.outsideLinux[f] })
}

// profile is the union of cover profiles: one entry per block, covered if any
// profile reached it. A block is "<file>:<range>" with modulePath cut off.
type profile struct {
	stmts   map[string]int
	covered map[string]bool
}

func newProfile() profile {
	return profile{stmts: map[string]int{}, covered: map[string]bool{}}
}

// runSuites runs the suite untagged and with -tags debug and unions the two:
// -tags debug compiles code the untagged run cannot see, and vice versa.
func runSuites() profile {
	p := newProfile()
	for _, tags := range []string{"", "debug"} {
		path, err := runSuite(tags)
		if err != nil {
			fail(err)
		}
		err = p.merge(path)
		os.Remove(path)
		if err != nil {
			fail(err)
		}
	}
	if len(p.stmts) == 0 {
		fail(fmt.Errorf("coverage: no statements measured"))
	}
	return p
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

// merge folds one cover profile in. With -coverpkg every package's test binary
// reports every block, so the same block arrives many times and only the OR
// of the counts is meaningful. Generated files and empty blocks are dropped.
func (p profile) merge(path string) error {
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
		if n == 0 {
			continue
		}
		p.stmts[block] = n
		p.covered[block] = p.covered[block] || count > 0
	}
	return sc.Err()
}

// write writes the union as a cover profile, blocks sorted so two runs write
// the same file. modulePath goes back on so go tool cover can read it.
func (p profile) write(path string) error {
	var b strings.Builder
	b.WriteString("mode: atomic\n")
	for _, block := range slices.Sorted(maps.Keys(p.stmts)) {
		count := 0
		if p.covered[block] {
			count = 1
		}
		fmt.Fprintf(&b, "%s%s %d %d\n", modulePath, block, p.stmts[block], count)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// tally counts statements: all of them, and those some profile covered.
type tally struct{ hit, total int }

func (t tally) pct() float64 { return 100 * float64(t.hit) / float64(t.total) }

// files groups the statements by file.
func (p profile) files() map[string]tally {
	files := map[string]tally{}
	for block, n := range p.stmts {
		t := files[fileOf(block)]
		t.total += n
		if p.covered[block] {
			t.hit += n
		}
		files[fileOf(block)] = t
	}
	return files
}

func fileOf(block string) string {
	return block[:strings.Index(block, ":")]
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
	slices.SortFunc(gaps, func(a, b string) int { return cmp.Compare(missing(b), missing(a)) })
	for _, f := range gaps {
		suffix := ""
		if mark != nil {
			suffix = mark(f)
		}
		fmt.Printf("%4d uncovered  %5.1f%%  %s%s\n", missing(f), files[f].pct(), f, suffix)
	}
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
