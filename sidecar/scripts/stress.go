//go:build ignore

// Reads the nightly stress run's `go test -json` output and writes the failures
// to failures.json beside it, for the workflow's report job to file as issues.
//
//	go run scripts/stress.go -cpu 1,4 -count 3 -race=true -runner ubuntu-24.04 stress/stress.json
//
// It also prints them to the log and to $GITHUB_STEP_SUMMARY. It exits 0
// whatever it finds: the workflow fails on go test's own status.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const modulePath = "github.com/kstackhq/kstack/sidecar"

// The test2json fields this reads.
type event struct {
	Action  string
	Package string
	Test    string
	Output  string
}

// One test failing under one -cpu value on this runner.
type failure struct {
	Package  string `json:"package"`
	Test     string `json:"test"`
	CPU      int    `json:"cpu"`
	Seed     string `json:"seed"`
	TimedOut bool   `json:"timedOut"`
	Command  string `json:"command"`
}

type report struct {
	Runner   string    `json:"runner"`
	Failures []failure `json:"failures"`
	// Packages that failed with no test to blame: a build failure, a panic in
	// TestMain, a race found after the last test.
	Outside []string `json:"outside"`
}

type pkgState struct {
	seed     string
	timedOut bool
	failed   bool
	runs     map[string]int // top-level test → runs started
	running  map[string]int // top-level test → -cpu of its unfinished run
	failures []failure
}

func main() {
	cpuFlag := flag.String("cpu", "", "the -cpu list go test ran with")
	count := flag.Int("count", 1, "the -count go test ran with")
	race := flag.Bool("race", true, "whether go test ran with -race")
	runner := flag.String("runner", "", "the runner's name")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/stress.go [flags] stress.json")
		os.Exit(2)
	}
	cpus, err := parseCPUs(*cpuFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	in, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer in.Close()
	r, err := read(in, cpus, *count, *race)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	r.Runner = *runner

	out := filepath.Join(filepath.Dir(flag.Arg(0)), "failures.json")
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	summary := markdown(r)
	fmt.Print(summary)
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		if _, err := f.WriteString(summary); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func parseCPUs(list string) ([]int, error) {
	var cpus []int
	for s := range strings.SplitSeq(list, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 {
			return nil, fmt.Errorf("-cpu: bad value %q", s)
		}
		cpus = append(cpus, n)
	}
	return cpus, nil
}

// read folds the event stream into failures. Test names carry no -cpu
// suffix, so a run's -cpu comes from its round: go test runs every test once
// per round, -count rounds per -cpu value, in the order the list gives them.
func read(in io.Reader, cpus []int, count int, race bool) (report, error) {
	pkgs := map[string]*pkgState{}
	var order []string
	dec := json.NewDecoder(bufio.NewReader(in))
	for {
		var e event
		if err := dec.Decode(&e); err == io.EOF {
			break
		} else if err != nil {
			return report{}, err
		}
		if e.Package == "" {
			continue
		}
		p := pkgs[e.Package]
		if p == nil {
			p = &pkgState{runs: map[string]int{}, running: map[string]int{}}
			pkgs[e.Package] = p
			order = append(order, e.Package)
		}

		if e.Test == "" {
			switch e.Action {
			case "output":
				if seed, ok := strings.CutPrefix(e.Output, "-test.shuffle "); ok {
					p.seed = strings.TrimSpace(seed)
				}
			case "fail":
				p.failed = true
			}
			continue
		}
		if e.Action == "output" && strings.HasPrefix(e.Output, "panic: test timed out after ") {
			p.timedOut = true
		}
		// A subtest's failure fails its parent too, and the issue is the parent's.
		if strings.Contains(e.Test, "/") {
			continue
		}
		switch e.Action {
		case "run":
			round := p.runs[e.Test] / count
			p.runs[e.Test]++
			cpu := 0
			if round < len(cpus) {
				cpu = cpus[round]
			}
			p.running[e.Test] = cpu
		case "fail":
			p.failures = addFailure(p.failures, failure{Package: e.Package, Test: e.Test, CPU: p.running[e.Test]})
			delete(p.running, e.Test)
		case "pass", "skip":
			delete(p.running, e.Test)
		}
	}

	r := report{Failures: []failure{}, Outside: []string{}}
	for _, name := range order {
		p := pkgs[name]
		if !p.failed {
			continue
		}
		// The alarm names the tests running when it fired, and test2json
		// closes none of them.
		if p.timedOut {
			for test, cpu := range p.running {
				p.failures = addFailure(p.failures, failure{Package: name, Test: test, CPU: cpu, TimedOut: true})
			}
		}
		if len(p.failures) == 0 {
			r.Outside = append(r.Outside, name)
			continue
		}
		for _, f := range p.failures {
			f.Seed = p.seed
			f.Command = command(f, count, race)
			r.Failures = append(r.Failures, f)
		}
	}
	return r, nil
}

// addFailure keeps one entry per test and -cpu value, however many rounds
// failed it.
func addFailure(fs []failure, f failure) []failure {
	for i, g := range fs {
		if g.Test == f.Test && g.CPU == f.CPU {
			fs[i].TimedOut = g.TimedOut || f.TimedOut
			return fs
		}
	}
	return append(fs, f)
}

// command reproduces a failure from sidecar/. The seed orders the whole
// package, so it runs the whole package.
func command(f failure, count int, race bool) string {
	dir := "."
	if rel, ok := strings.CutPrefix(f.Package, modulePath+"/"); ok {
		dir = "./" + rel
	}
	var b strings.Builder
	b.WriteString("go test")
	if race {
		b.WriteString(" -race")
	}
	fmt.Fprintf(&b, " -cpu %d -shuffle=%s -count=%d %s", f.CPU, f.Seed, count, dir)
	return b.String()
}

func markdown(r report) string {
	if len(r.Failures) == 0 && len(r.Outside) == 0 {
		return "No test failed.\n"
	}
	var b strings.Builder
	if len(r.Failures) > 0 {
		b.WriteString("| Package | Test | -cpu | Seed | Reproduce |\n|---|---|---|---|---|\n")
		for _, f := range r.Failures {
			test := f.Test
			if f.TimedOut {
				test += " (timed out)"
			}
			fmt.Fprintf(&b, "| %s | %s | %d | %s | `%s` |\n", f.Package, test, f.CPU, f.Seed, f.Command)
		}
	}
	if len(r.Outside) > 0 {
		b.WriteString("\nFailed outside a test:\n\n")
		for _, p := range r.Outside {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}
	return b.String()
}
