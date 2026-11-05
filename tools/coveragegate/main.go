// Command coveragegate applies the coverage gate to Codecov's merged result of
// a commit, once every upload a CI run made has been processed:
//
//   - patch: every changed line a report measured is covered;
//   - project: the total is no more than -threshold points below the base.
//
// CI's Coverage · Gate job (.github/workflows/coverage.yml) runs it after every
// coverage job, in a checkout holding both commits' history:
//
//	go run ./coveragegate -sha <head> -pr <n> -base <sha> -run <run url> -flags go-linux,typescript
//
// -flags names the uploads this run made; each is waited for. The changed
// lines are those `git diff <base>...<head>` adds; -pr 0 takes the head's
// parent as the base, as on a push. Codecov supplies the head's line coverage
// and both commits' totals. A base Codecov has no report for fails at once,
// since no wait gives it one. Codecov's public API needs no token, so a fork's
// pull request runs it too.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/kstackhq/kstack/tools/internal/unidiff"
)

type config struct {
	sha       string
	pr        int
	base      string // the pull request's base commit; -pr 0 uses the head's parent
	run       string // the CI run's URL, as Codecov records it on each upload
	flags     []string
	dir       string // the git checkout the diff is taken in; empty is the working directory
	threshold float64
	timeout   time.Duration
	interval  time.Duration
}

func main() {
	var cfg config
	var api, flags, summaryPath string
	flag.StringVar(&api, "api", "https://api.codecov.io/api/v2/github/kstackhq/repos/kstack", "Codecov API root of the repository")
	flag.StringVar(&cfg.sha, "sha", "", "head commit")
	flag.IntVar(&cfg.pr, "pr", 0, "pull request number; 0 compares the commit with its parent")
	flag.StringVar(&cfg.base, "base", "", "the pull request's base commit")
	flag.StringVar(&cfg.run, "run", "", "URL of the CI run that made the uploads")
	flag.StringVar(&flags, "flags", "", "comma-separated flags the run uploaded")
	flag.Float64Var(&cfg.threshold, "threshold", 0.1, "points the total may fall below the base")
	flag.DurationVar(&cfg.timeout, "timeout", 15*time.Minute, "how long to wait for Codecov")
	flag.DurationVar(&cfg.interval, "interval", 15*time.Second, "time between polls")
	flag.StringVar(&summaryPath, "summary", os.Getenv("GITHUB_STEP_SUMMARY"), "file the Markdown summary is appended to")
	flag.Parse()
	if flags != "" {
		cfg.flags = strings.Split(flags, ",")
	}

	var summary io.Writer = io.Discard
	if summaryPath != "" {
		f, err := os.OpenFile(summaryPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "coveragegate:", err)
			os.Exit(1)
		}
		defer f.Close()
		summary = f
	}

	cc := &codecov{api: api, client: http.DefaultClient}
	if err := run(context.Background(), cfg, cc, os.Stdout, summary); err != nil {
		fmt.Fprintln(os.Stderr, "coveragegate:", err)
		os.Exit(1)
	}
}

// errFailed is the gate's verdict; the lines that earned it are already printed.
var errFailed = errors.New("coverage gate failed")

func run(ctx context.Context, cfg config, cc *codecov, out, summary io.Writer) error {
	if len(cfg.flags) == 0 {
		fmt.Fprintln(out, "No coverage job uploaded a report in this run; nothing to gate.")
		return nil
	}
	if cfg.pr != 0 && cfg.base == "" {
		return errors.New("-pr needs -base")
	}
	res, err := wait(ctx, cfg, cc, out)
	if err != nil {
		return err
	}
	changed, err := changedLines(ctx, cfg.dir, res.base, cfg.sha)
	if err != nil {
		return err
	}
	v := judge(res, changed, cfg.threshold)
	v.report(out, summary)
	if v.failed {
		return errFailed
	}
	return nil
}

// Codecov's API, the parts read here.

type upload struct {
	Flags    []string `json:"flags"`
	State    string   `json:"state"`
	BuildURL string   `json:"build_url"`
}

type commit struct {
	State  string  `json:"state"`
	Parent string  `json:"parent"`
	Totals *totals `json:"totals"` // nil while the commit has no report
}

type totals struct {
	Lines int `json:"lines"`
	Hits  int `json:"hits"`
}

// A commit's report: each file's measured lines as [line, coverage] pairs.
type report struct {
	Files []struct {
		Name         string   `json:"name"`
		LineCoverage [][2]int `json:"line_coverage"`
	} `json:"files"`
}

// Codecov's coverage of a line.
const (
	hit     = 0
	miss    = 1
	partial = 2
)

// A result is what the gate reads from Codecov once the head is processed.
type result struct {
	base       string
	head       *totals
	baseTotals *totals
	coverage   map[string]map[int]int // file, then line, to hit, miss or partial
}

// A codecov reads one repository from Codecov's API.
type codecov struct {
	api    string // the repository's root, without a trailing slash
	client *http.Client
}

func (c *codecov) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.api+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var body struct {
			Detail string `json:"detail"`
		}
		// A body that is not Codecov's JSON leaves the detail empty.
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body)
		return &statusError{path: path, code: resp.StatusCode, status: resp.Status, detail: body.Detail}
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

type statusError struct {
	path   string
	code   int
	status string
	detail string // Codecov's reason, when it gives one
}

func (e *statusError) Error() string {
	msg := "GET " + e.path + ": " + e.status
	if e.detail != "" {
		msg += ": " + e.detail
	}
	return msg
}

func notFound(err error) bool {
	var s *statusError
	return errors.As(err, &s) && s.code == http.StatusNotFound
}

// rateLimited reports a 403 Codecov answers when a limit it shares with every
// anonymous caller runs out.
func rateLimited(err error) bool {
	var s *statusError
	return errors.As(err, &s) && s.code == http.StatusForbidden &&
		strings.Contains(strings.ToLower(s.detail), "rate limit")
}

// An uploadError is an upload Codecov failed to process. No wait changes it.
type uploadError struct{ flags string }

func (e *uploadError) Error() string {
	return "Codecov failed to process upload " + e.flags + "; re-run its coverage job"
}

// A noBaseError is a base commit Codecov has no report for, so the comparison
// cannot exist. A CI run on the base's branch gives it one; waiting does not.
type noBaseError struct{ base string }

func (e *noBaseError) Error() string {
	return "Codecov has no report for base commit " + e.base + ", so it cannot compare this commit with it. " +
		"Only CI's push runs (main and wip/*) upload a base's coverage: " +
		"re-run this job once the base's run has uploaded"
}

// wait polls until fetch has the result, for at most cfg.timeout, and prints
// each new reason it is still waiting.
func wait(ctx context.Context, cfg config, cc *codecov, out io.Writer) (*result, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	pending := errors.New("no request answered")
	for {
		res, err := fetch(ctx, cfg, cc)
		if err == nil {
			return res, nil
		}
		var bad *uploadError
		var noBase *noBaseError
		if errors.As(err, &bad) || errors.As(err, &noBase) {
			return nil, err
		}
		if rateLimited(err) {
			return nil, fmt.Errorf("rate-limited by Codecov (%v); "+
				"this is not a coverage failure: re-run the job once the limit resets", err)
		}
		// A request the deadline cut short says nothing about Codecov.
		if ctx.Err() == nil {
			if err.Error() != pending.Error() {
				fmt.Fprintln(out, "waiting:", err)
			}
			pending = err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out: Codecov did not finish processing %s within %s (%v); "+
				"this is a Codecov delay, not a coverage failure: re-run the job", cfg.sha, cfg.timeout, pending)
		case <-time.After(cfg.interval):
		}
	}
}

// fetch returns the result once every upload the run made is processed and
// the commit is complete. Until then its error says what Codecov is still
// working on.
func fetch(ctx context.Context, cfg config, cc *codecov) (*result, error) {
	if err := uploadsProcessed(ctx, cfg, cc); err != nil {
		return nil, err
	}

	var head commit
	if err := cc.get(ctx, "/commits/"+cfg.sha+"/", &head); err != nil {
		return nil, err
	}
	if head.State != "complete" {
		return nil, errors.New("commit is " + head.State)
	}

	res := &result{base: cfg.base, head: head.Totals, coverage: map[string]map[int]int{}}
	if cfg.pr == 0 {
		if head.Parent == "" {
			return nil, errors.New("Codecov has no parent for " + cfg.sha)
		}
		res.base = head.Parent
	}
	var base commit
	if err := cc.get(ctx, "/commits/"+res.base+"/", &base); err != nil {
		if notFound(err) {
			return nil, &noBaseError{res.base}
		}
		return nil, err
	}
	res.baseTotals = base.Totals

	var rep report
	if err := cc.get(ctx, "/report/?"+url.Values{"sha": {cfg.sha}}.Encode(), &rep); err != nil {
		return nil, err
	}
	for _, f := range rep.Files {
		lines := map[int]int{}
		for _, lc := range f.LineCoverage {
			lines[lc[0]] = lc[1]
		}
		res.coverage[f.Name] = lines
	}
	return res, nil
}

// uploadsProcessed is nil once Codecov has processed every flag in cfg.flags
// from this run. Another run's uploads are not waited for.
func uploadsProcessed(ctx context.Context, cfg config, cc *codecov) error {
	var page struct {
		Results []upload `json:"results"`
	}
	if err := cc.get(ctx, "/commits/"+cfg.sha+"/uploads/?page_size=100", &page); err != nil {
		return err
	}
	processed := map[string]bool{}
	for _, u := range page.Results {
		if u.BuildURL != cfg.run {
			continue
		}
		name := strings.Join(u.Flags, ",")
		switch u.State {
		case "error":
			return &uploadError{name}
		case "uploaded", "started":
			return fmt.Errorf("upload %s is %s", name, u.State)
		}
		for _, f := range u.Flags {
			processed[f] = true
		}
	}
	for _, f := range cfg.flags {
		if !processed[f] {
			return errors.New("upload " + f + " is not listed")
		}
	}
	return nil
}

// changedLines returns the lines `git diff base...head` adds in dir, by file.
func changedLines(ctx context.Context, dir, base, head string) (map[string][]int, error) {
	cmd := exec.CommandContext(ctx, "git", "-c", "core.quotePath=false", "diff",
		"-U0", "--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/", base+"..."+head)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	diff, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s...%s: %v: %s", base, head, err, strings.TrimSpace(stderr.String()))
	}
	return unidiff.Added(bytes.NewReader(diff))
}

type uncovered struct {
	file  string
	line  int
	state string // "not covered" or "partially covered"
}

// A verdict is the gate's reading of a result, worded for the report.
type verdict struct {
	patch     string
	uncovered []uncovered
	project   string
	failed    bool
}

func judge(res *result, changed map[string][]int, threshold float64) verdict {
	var v verdict

	files := make([]string, 0, len(changed))
	for f := range changed {
		files = append(files, f)
	}
	sort.Strings(files)
	measured := 0
	for _, f := range files {
		for _, n := range changed[f] {
			c, ok := res.coverage[f][n]
			if !ok {
				continue
			}
			measured++
			switch c {
			case miss:
				v.uncovered = append(v.uncovered, uncovered{f, n, "not covered"})
			case partial:
				v.uncovered = append(v.uncovered, uncovered{f, n, "partially covered"})
			}
		}
	}
	v.patch = fmt.Sprintf("%d of %d changed lines covered", measured-len(v.uncovered), measured)
	v.failed = len(v.uncovered) > 0

	base, head := res.baseTotals, res.head
	if base == nil || head == nil {
		v.project = "no base report to compare against; not judged"
		return v
	}
	// Codecov's percentages are rounded; these are not.
	outcome := "ok"
	if percent(head) < percent(base)-threshold {
		outcome = fmt.Sprintf("FAILED: more than %.2f points below the base", threshold)
		v.failed = true
	}
	v.project = fmt.Sprintf("%.2f%% at the head, %.2f%% at base %.7s: %s",
		percent(head), percent(base), res.base, outcome)
	return v
}

func percent(t *totals) float64 {
	if t.Lines == 0 {
		return 0
	}
	return 100 * float64(t.Hits) / float64(t.Lines)
}

// report writes the log, with an annotation per uncovered line, and the
// Markdown step summary.
func (v verdict) report(out, summary io.Writer) {
	fmt.Fprintln(out, "patch:", v.patch)
	for _, u := range v.uncovered {
		fmt.Fprintf(out, "::error file=%s,line=%d,title=Uncovered line::This changed line is %s.\n",
			escapeProperty(u.file), u.line, u.state)
		fmt.Fprintf(out, "  %s:%d %s\n", u.file, u.line, u.state)
	}
	fmt.Fprintln(out, "project:", v.project)

	fmt.Fprintf(summary, "## Coverage gate\n\n**patch**: %s\n\n", v.patch)
	for _, u := range v.uncovered {
		fmt.Fprintf(summary, "- `%s:%d` %s\n", u.file, u.line, u.state)
	}
	if len(v.uncovered) > 0 {
		fmt.Fprintln(summary)
	}
	fmt.Fprintf(summary, "**project**: %s\n", v.project)
}

// escapeProperty escapes a workflow command's property as GitHub's toolkit does.
func escapeProperty(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}
