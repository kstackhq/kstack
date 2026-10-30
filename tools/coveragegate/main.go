// Command coveragegate applies the coverage gate to Codecov's merged result of
// a commit, once every upload a CI run made has been processed:
//
//   - patch: every changed line a report measured is covered;
//   - project: the total is no more than -threshold points below the base.
//
// CI's Coverage · Gate job runs it after every coverage job:
//
//	go run ./coveragegate -sha <head> -pr <n> -run <run url> -flags go-linux,typescript
//
// -flags names the uploads this run made; each is waited for. -pr 0 compares
// the commit with its parent, as on a push to main. Codecov's public API needs
// no token, so a fork's pull request runs it too.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type config struct {
	sha       string
	pr        int
	run       string // the CI run's URL, as Codecov records it on each upload
	flags     []string
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
	cmp, err := wait(ctx, cfg, cc)
	if err != nil {
		return err
	}
	v := judge(cmp, cfg.threshold)
	v.report(out, summary)
	if v.failed {
		return errFailed
	}
	return nil
}

// Codecov's API, the parts read here. A comparison nests each side's value
// under head or base.

type upload struct {
	Flags    []string `json:"flags"`
	State    string   `json:"state"`
	BuildURL string   `json:"build_url"`
}

type commit struct {
	State  string `json:"state"`
	Parent string `json:"parent"`
}

type comparison struct {
	HeadCommit string `json:"head_commit"`
	BaseCommit string `json:"base_commit"`
	Totals     struct {
		Base *totals `json:"base"`
		Head *totals `json:"head"`
	} `json:"totals"`
	Files []file `json:"files"`
}

type totals struct {
	Lines int `json:"lines"`
	Hits  int `json:"hits"`
}

type file struct {
	Name  struct{ Head string } `json:"name"`
	Lines []line                `json:"lines"`
}

// A line of a compared file; Coverage is nil where no report measured it.
type line struct {
	Number   struct{ Head *int } `json:"number"`
	Coverage struct{ Head *int } `json:"coverage"`
	Added    bool                `json:"added"`
}

// Codecov's coverage of a line.
const (
	hit     = 0
	miss    = 1
	partial = 2
)

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
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// An uploadError is an upload Codecov failed to process. No wait changes it.
type uploadError struct{ flags string }

func (e *uploadError) Error() string {
	return "Codecov failed to process upload " + e.flags + "; re-run its coverage job"
}

// wait polls until fetch has the comparison, for at most cfg.timeout.
func wait(ctx context.Context, cfg config, cc *codecov) (*comparison, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	pending := errors.New("no request answered")
	for {
		cmp, err := fetch(ctx, cfg, cc)
		if err == nil {
			return cmp, nil
		}
		var bad *uploadError
		if errors.As(err, &bad) {
			return nil, err
		}
		// A request the deadline cut short says nothing about Codecov.
		if ctx.Err() == nil {
			pending = err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("Codecov did not finish processing %s within %s (%v). "+
				"This is a Codecov delay, not a coverage failure: re-run the job", cfg.sha, cfg.timeout, pending)
		case <-time.After(cfg.interval):
		}
	}
}

// fetch returns the comparison once every upload the run made is processed,
// the commit is complete and the comparison is of this commit. Until then its
// error says what Codecov is still working on.
func fetch(ctx context.Context, cfg config, cc *codecov) (*comparison, error) {
	if err := uploadsProcessed(ctx, cfg, cc); err != nil {
		return nil, err
	}

	var c commit
	if err := cc.get(ctx, "/commits/"+cfg.sha+"/", &c); err != nil {
		return nil, err
	}
	if c.State != "complete" {
		return nil, errors.New("commit is " + c.State)
	}

	q := url.Values{}
	if cfg.pr != 0 {
		q.Set("pullid", fmt.Sprint(cfg.pr))
	} else {
		q.Set("base", c.Parent)
		q.Set("head", cfg.sha)
	}
	var cmp comparison
	if err := cc.get(ctx, "/compare/?"+q.Encode(), &cmp); err != nil {
		return nil, err
	}
	// A pull request's comparison follows its head, which Codecov moves when it
	// sees the head's first upload.
	if cmp.HeadCommit != cfg.sha {
		return nil, errors.New("comparison is of " + cmp.HeadCommit)
	}
	return &cmp, nil
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

type uncovered struct {
	file  string
	line  int
	state string // "not covered" or "partially covered"
}

// A verdict is the gate's reading of a comparison, worded for the report.
type verdict struct {
	patch     string
	uncovered []uncovered
	project   string
	failed    bool
}

func judge(cmp *comparison, threshold float64) verdict {
	var v verdict

	measured := 0
	for _, f := range cmp.Files {
		for _, l := range f.Lines {
			if !l.Added || l.Number.Head == nil || l.Coverage.Head == nil {
				continue
			}
			measured++
			switch *l.Coverage.Head {
			case miss:
				v.uncovered = append(v.uncovered, uncovered{f.Name.Head, *l.Number.Head, "not covered"})
			case partial:
				v.uncovered = append(v.uncovered, uncovered{f.Name.Head, *l.Number.Head, "partially covered"})
			}
		}
	}
	v.patch = fmt.Sprintf("%d of %d changed lines covered", measured-len(v.uncovered), measured)
	v.failed = len(v.uncovered) > 0

	base, head := cmp.Totals.Base, cmp.Totals.Head
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
		percent(head), percent(base), cmp.BaseCommit, outcome)
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

	fmt.Fprintf(summary, "## Coverage · Gate\n\n**patch**: %s\n\n", v.patch)
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
