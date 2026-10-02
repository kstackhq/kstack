// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package bash is the tool that runs a command the model asks for on the user's
// machine, once the user approves it: in the user's shell, after a snapshot of
// their login profile, in the directory the call names or else the chat's
// workspace. It knows the shell's path and its own directories (Paths), and of
// the chat's cluster only its record and its kubectl cache, which a sandboxed
// run reads.
package bash

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kstackhq/kstack/sidecar/internal/clustersvc"
	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/safe"
	"github.com/kstackhq/kstack/sidecar/internal/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

var (
	_ tools.Gated   = (*Tool)(nil)
	_ tools.Bounded = (*Tool)(nil)
)

//go:embed prompts/bash.md
var prompt string

// sandboxPrompt opens the section, under its heading, where there is a sandbox.
//
//go:embed prompts/sandbox.md
var sandboxPrompt string

// sandboxLinuxPrompt follows sandboxPrompt on Linux, whose sandbox refuses
// what a snap needs to start.
//
//go:embed prompts/sandbox_linux.md
var sandboxLinuxPrompt string

// description is the tool description the model reads with the schema: the
// reference's, adapted to what is true here.
//
//go:embed prompts/description.md
var description string

// inputSchema is the reference's, less dangerouslyDisableSandbox, plus workdir:
// whether a command leaves the sandbox is the user's switch, never the model's.
//
//go:embed prompts/schema.json
var inputSchema []byte

const (
	// DefaultTimeout is a call's bound when it names none, and MaxTimeout the
	// most it may ask for.
	DefaultTimeout = 120 * time.Second
	MaxTimeout     = 600 * time.Second
	// killGrace is how long a timed-out group has between SIGTERM and SIGKILL.
	killGrace = 5 * time.Second
	// callMargin puts the loop's bound on a call above the command's own and its
	// grace, covering pipeGrace and scheduling, so a timed-out command answers with
	// its own result rather than the loop's timeout refusal.
	callMargin = 5 * time.Second
)

// Name is the name bash is offered under.
const Name = "Bash"

var _ interface {
	tools.Custom
	tools.Gated
	tools.Bounded
} = (*Tool)(nil)

// sandboxer runs a command sandboxed: *sandbox.Sandbox, or a test's fake.
// Command's contract is sandbox.Sandbox.Command's, exec.CommandContext included.
type sandboxer interface {
	Command(ctx context.Context, r sandbox.Run) (*exec.Cmd, error)
	System(home, shell string) sandbox.System
	Never(home string) []string
	Confines() bool
	Port() (int, error)
}

// Tool is a shell found on the machine. It is the tools.Gated a turn is offered.
type Tool struct {
	Reader
	// sandboxer runs a call in the machine's sandbox, nil where it has none. A
	// call runs through it unless the user switched its chat outside it.
	sandboxer sandboxer
	shell     string
	// kind is zsh or bash: which shell the wrapper and the prompt are written for.
	kind string
	// home is the user's home: what ~ in the workdir of a call outside the
	// sandbox names.
	home string
	// scripts holds the snapshot, and on Windows each command's script.
	scripts string
	// version is bash's major.minor, named in the prompt; "" for zsh, or when it
	// could not be read.
	version  string
	platform string // runtime.GOOS, the prompt's Platform line
	// env is what Kstack adds to a command's environment.
	env []string
	// clusterSvc is the cluster service, which a sandboxed run reads the chat's
	// cluster through.
	clusterSvc clustersvc.Service
	// runsDir and tmpDir are where a sandboxed run's own directory and its
	// TMPDIR are made, and kubectlDir where each cluster's kubectl cache is.
	runsDir, tmpDir, kubectlDir string
	// denied is Kstack's own directories, which a sandboxed run cannot read.
	denied []string

	// The profile snapshot every command outside the sandbox sources: its
	// path, "" while there is none, and ready, closed once it is written or
	// given up — nil for a tool whose snapshot was never started. snapTimeout
	// and snapLimit bound taking it.
	snapshot string
	ready    chan struct{}
	// snapCtx is the context StartSnapshot made, which its stop cancels;
	// snapMu guards it and the start that sets ready.
	snapMu      sync.Mutex
	snapCtx     context.Context
	snapTimeout time.Duration
	snapLimit   int
	// launch runs the login shell: launchDump, or a test's stand-in.
	launch func(ctx context.Context) (out []byte, reason string, code int)
	// onSnapshotWait runs as a call begins to wait on the snapshot: a test's
	// hook, nil in production.
	onSnapshotWait func()
	// statDir reads a command's directory before it starts: os.Stat, or a
	// test's stand-in.
	statDir func(name string) (os.FileInfo, error)
	// extraWritable is more a sandboxed run may write: a test's seam, nil in
	// production.
	extraWritable []string
}

// Paths is where the tool keeps its files. ShellDir holds the snapshot, and on
// Windows each command's script; RunsDir each sandboxed run's own directory,
// TmpDir each one's TMPDIR, and KubectlDir each cluster's kubectl cache.
// DeniedDirs are Kstack's own directories, which a sandboxed run cannot read
// but for what it is given of them.
type Paths struct {
	ShellDir, RunsDir, TmpDir, KubectlDir string
	DeniedDirs                            []string
}

// New finds the shell — on Unix the login shell when it is zsh or bash, else
// bash on PATH; on Windows only Git for Windows', through Git's own install
// record, never off PATH — and the user's home. ok false offers no tool. What a
// crash left behind in paths.ShellDir is swept here. On a goroutine of its
// own, New sweeps the run directories a sidecar that is gone left in
// paths.RunsDir or paths.TmpDir, and the kubectl cache of every cluster
// clusterSvc no longer holds. hostPID is the host's process, 0 when the sidecar was
// not told it. boxer is the machine's sandbox, nil for none, and clusterSvc the cluster
// service, which a sandboxed run reads the chat's cluster through.
func New(paths Paths, hostPID int, boxer *sandbox.Sandbox, clusterSvc clustersvc.Service) (t *Tool, ok bool) {
	shell, kind, found := findShell()
	if !found {
		return nil, false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, false
	}
	scripts := paths.ShellDir
	_ = os.RemoveAll(scripts)
	// New runs before the sidecar is ready, and a crash can leave a TMPDIR
	// holding a whole build. This sidecar's own runs are never taken: it holds
	// its lock before it makes one.
	go func() {
		sweepRunDirs(paths.RunsDir)
		sweepRunDirs(paths.TmpDir)
		// A tool built with no cluster service, as in a test, has no clusters
		// to sweep against.
		if clusterSvc != nil {
			sweepKubectlCache(context.Background(), paths.KubectlDir, clusterSvc)
		}
	}()
	t = &Tool{
		shell: shell, kind: kind, home: home, scripts: scripts, platform: runtime.GOOS,
		env:         kstackEnv(hostPID),
		clusterSvc:  clusterSvc,
		runsDir:     paths.RunsDir,
		tmpDir:      paths.TmpDir,
		kubectlDir:  paths.KubectlDir,
		denied:      paths.DeniedDirs,
		snapTimeout: snapshotTimeout,
		snapLimit:   snapshotLimit,
	}
	// A nil pointer in an interface is not a nil interface.
	if boxer != nil {
		t.sandboxer = boxer
	}
	t.launch = t.launchDump
	t.statDir = os.Stat
	if kind == "bash" {
		t.version = readVersion(shell)
	}
	return t, true
}

// kstackEnv is what Kstack adds to a command's environment: that it runs under
// Kstack, and the processes the kill shims refuse to stop.
func kstackEnv(hostPID int) []string {
	env := []string{"KSTACK=1", "KSTACK_SIDECAR_PID=" + strconv.Itoa(os.Getpid())}
	if hostPID > 0 {
		env = append(env, "KSTACK_HOST_PID="+strconv.Itoa(hostPID))
	}
	return env
}

// versionTimeout bounds the one bash --version New runs.
const versionTimeout = 2 * time.Second

// readVersion asks bash for its version with --version, which exits before any
// startup file is read — -c would source BASH_ENV first. "" on any failure.
func readVersion(bash string) string {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bash, "--version")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return parseVersion(string(out))
}

// versionRE reads major.minor off the first line of bash --version.
var versionRE = regexp.MustCompile(`^GNU bash, version (\d+\.\d+)\.`)

// parseVersion is bash's major.minor, or "" when out is not bash's own first line.
func parseVersion(out string) string {
	first, _, _ := strings.Cut(out, "\n")
	if m := versionRE.FindStringSubmatch(first); m != nil {
		return m[1]
	}
	return ""
}

// errInput is an input parse refuses.
var errInput = errors.New(`bash input is not {"command": <non-empty string>, "description"?: <string>, "timeout"?: <positive number>, "workdir"?: <directory>, "run_in_background"?: <boolean>}`)

// errOutsideWorkspace is a sandboxed call whose workdir resolves outside the
// chat's workspace.
var errOutsideWorkspace = errors.New("bash: a sandboxed command starts in its workspace")

// maxWorkdir is the longest workdir parse reads, in bytes: a PATH_MAX.
const maxWorkdir = 4096

// input is one call's arguments.
type input struct {
	Command     string
	Description string
	Timeout     time.Duration // DefaultTimeout when the call names none
	Workdir     string        // "" when the call names none
	Background  bool          // the call's run_in_background
}

// parse reads an object whose keys are command, description, timeout, workdir
// and run_in_background, each spelled exactly and at most once, with nothing after it, and a
// command that is not empty. It walks the tokens itself because a struct decode
// matches a key without regard to case and lets a duplicate win, and the command
// that runs must be the one approved. Each value's type is checked off its token, since a typed
// decode takes null as the zero value. An empty command is refused because it
// would still start bash, which sources BASH_ENV first.
func parse(raw json.RawMessage) (input, error) {
	in := input{Timeout: DefaultTimeout}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return input{}, errInput
	}
	seen := map[string]bool{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return input{}, errInput
		}
		name, _ := key.(string)
		if seen[name] {
			return input{}, errInput
		}
		seen[name] = true
		val, err := dec.Token()
		if err != nil {
			return input{}, errInput
		}
		var ok bool
		switch name {
		case "command":
			in.Command, ok = val.(string)
		case "description":
			in.Description, ok = val.(string)
		case "timeout":
			if n, isNumber := val.(json.Number); isNumber {
				in.Timeout, ok = readTimeout(n)
			}
		case "workdir":
			in.Workdir, ok = val.(string)
			ok = ok && validWorkdir(in.Workdir)
		case "run_in_background":
			in.Background, ok = val.(bool)
		}
		if !ok {
			return input{}, errInput
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return input{}, errInput
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return input{}, errInput
	}
	if in.Command == "" {
		return input{}, errInput
	}
	return in, nil
}

// validWorkdir is whether a workdir can be drawn as one readable line on the
// approval request: not empty, at most maxWorkdir bytes, no control character.
func validWorkdir(workdir string) bool {
	if workdir == "" || len(workdir) > maxWorkdir {
		return false
	}
	return !strings.ContainsFunc(workdir, unicode.IsControl)
}

// readTimeout is a timeout in milliseconds as a duration, capped at MaxTimeout
// and rounded up to a whole millisecond. ok is false for anything but a finite
// number above zero. The cap comes before the conversion, so a huge value cannot
// overflow a Duration.
func readTimeout(n json.Number) (time.Duration, bool) {
	ms, err := strconv.ParseFloat(string(n), 64)
	if err != nil || ms <= 0 {
		return 0, false
	}
	ms = min(ms, float64(MaxTimeout/time.Millisecond))
	return time.Duration(math.Ceil(ms)) * time.Millisecond, true
}

// CallTimeout is the loop's bound on one call: the command's own, its grace, a
// margin, and the longest it can wait on the snapshot.
func (t *Tool) CallTimeout(raw json.RawMessage) time.Duration {
	in, _ := parse(raw)
	return in.Timeout + killGrace + callMargin + t.snapTimeout
}

// Definition is the offer: a function named Bash, run by the sidecar.
func (t *Tool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        Name,
		Description: description,
		InputSchema: json.RawMessage(inputSchema),
	}
}

// Prompt is the tool's section of the system prompt: Kstack's own lines, opened
// by the sandbox's where there is one, then the platform and the shell that runs
// the command. A model writes bash 4 or later
// unless told otherwise, and macOS ships 3.2, so an older one comes with what it
// lacks.
func (t *Tool) Prompt() string {
	shell := t.kind
	if t.version != "" {
		shell += " " + t.version
	}
	if t.platform == "windows" {
		shell += " (Git for Windows)"
	}
	var b strings.Builder
	if t.sandboxer != nil {
		// sandbox.md says what waits for the user in place of bash.md's first
		// paragraph, which says every command does.
		heading, body, _ := strings.Cut(prompt, "\n\n")
		_, shared, _ := strings.Cut(body, "\n\n")
		b.WriteString(heading)
		b.WriteString("\n\n")
		b.WriteString(sandboxPrompt)
		if t.platform == "linux" {
			b.WriteString(sandboxLinuxPrompt)
		}
		b.WriteString("\n")
		b.WriteString(shared)
	} else {
		b.WriteString(prompt)
	}
	b.WriteString("\n- Platform: ")
	b.WriteString(t.platform)
	b.WriteString("\n- Shell: ")
	b.WriteString(shell)
	b.WriteString("\n")
	major, _, _ := strings.Cut(t.version, ".")
	if n, err := strconv.Atoi(major); err == nil && n < 4 {
		b.WriteString("\nThe shell is older than bash 4: no associative arrays, `mapfile` or `readarray`, " +
			"`${var,,}` or `${var^^}`, `**` globs, `|&` or `&>>`.\n")
	}
	if t.platform == "windows" {
		b.WriteString("\nPaths are Git Bash's: `C:\\Users\\ana` is `/c/Users/ana`, and a Windows program " +
			"given a path may need the `C:\\` form. `workdir` takes either form of a drive path, " +
			"and no other Git Bash path such as `/tmp`.\n")
	}
	return b.String()
}

// Approval is the directory the command starts in, resolved against the chat's
// workspace and the home the tool has now, and whether a sandbox confines it.
// Run resolves both the same way. A sandboxed call whose workdir leaves the
// workspace is refused in words the model reads. A call a sandbox confines runs
// unasked; every other call asks.
func (t *Tool) Approval(_ context.Context, rt tools.Runtime, raw json.RawMessage) (tools.Approval, error) {
	in, err := parse(raw)
	if err != nil {
		return tools.Approval{}, err
	}
	cwd, err := t.startDir(in, rt)
	if errors.Is(err, errOutsideWorkspace) {
		return tools.Approval{}, &tools.Refusal{Result: outsideWorkspace(tools.WorkspacePath(rt.Dir))}
	}
	if err != nil {
		return tools.Approval{}, err
	}
	boxer := t.sandboxerFor(rt)
	sandboxed := boxer != nil && boxer.Confines()
	return tools.Approval{Cwd: cwd, Sandboxed: sandboxed, Skip: sandboxed}, nil
}

// startDir is the directory a call starts in. A call is sandboxed when it runs
// through the tool's sandbox, whether or not that confines it: that is what the
// model was told.
func (t *Tool) startDir(in input, rt tools.Runtime) (string, error) {
	return resolveWorkdir(t.home, tools.WorkspacePath(rt.Dir), in.Workdir, t.sandboxerFor(rt) != nil)
}

// outsideWorkspace is the refusal of a sandboxed call whose workdir leaves the
// workspace ws.
func outsideWorkspace(ws string) string {
	return "A sandboxed command starts in its workspace, " + ws + ", or a directory under it. Name one there, or ask the user to run this chat outside the sandbox."
}

// makeWorkspace makes the chat's workspace, so a call has somewhere to start.
func makeWorkspace(dir tools.ChatDir) error {
	root, err := tools.OpenWorkspace(dir, true)
	if err != nil {
		return errors.New("the workspace " + tools.WorkspacePath(dir) + " could not be made: " + err.Error())
	}
	return root.Close()
}

// sandboxerFor is the sandboxer a call of rt's chat runs through: the tool's,
// unless the user switched the chat outside it; nil for none.
func (t *Tool) sandboxerFor(rt tools.Runtime) sandboxer {
	if rt.OutsideSandbox {
		return nil
	}
	return t.sandboxer
}

// Reader reads bash's rows: the tool's own reading, and alone on a machine that
// offers no shell.
type Reader struct{}

func (Reader) Name() string { return Name }

func (Reader) ActionKind() tools.ActionKind { return tools.ActionCommand }

func (Reader) Action(raw json.RawMessage, cwd string, sandboxed bool) (tools.Action, error) {
	return ActionOf(raw, cwd, sandboxed)
}

// ActionOf is the command a call runs, checked as Run checks its input, so
// every input Run refuses as bad input, ActionOf refuses, and what is shown is
// what runs. cwd and sandboxed are the row's, never resolved again.
func ActionOf(raw json.RawMessage, cwd string, sandboxed bool) (tools.Action, error) {
	in, err := parse(raw)
	if err != nil {
		return tools.Action{}, err
	}
	if err := checkWorkdir(in.Workdir); err != nil {
		return tools.Action{}, err
	}
	return tools.Action{
		Description: in.Description,
		Command: &tools.CommandAction{
			Text: in.Command, Cwd: cwd, Background: in.Background,
			Sandboxed: sandboxed,
		},
	}, nil
}

// Run runs one command, saving output too large to come back whole in the
// chat's results. A background command is started as the chat's task
// instead, and the call answers at once.
func (t *Tool) Run(ctx context.Context, rt tools.Runtime, raw json.RawMessage) (string, bool) {
	in, err := parse(raw)
	if err != nil {
		return badInput, true
	}
	if in.Background {
		return t.runTask(ctx, rt, in)
	}
	return t.runCall(ctx, in, rt)
}

// badInput answers a call whose input parse or startDir refused. The loop
// asks Approval first, so a gated call never gets this far with one.
const badInput = `{"error":"bad-input"}`

// runCall runs one command and answers with its output as resultText renders it,
// saving it in results when it is too large to come back whole.
func (t *Tool) runCall(ctx context.Context, in input, rt tools.Runtime) (string, bool) {
	dir := rt.Dir
	cwd, err := t.startDir(in, rt)
	if err != nil {
		return badInput, true
	}
	if err := makeWorkspace(dir); err != nil {
		return resultText(result{Error: err.Error()}, in.Timeout, nil, false), true
	}
	// Checked here, not at the approval, since the user can take minutes to
	// decide; and before the snapshot wait, which can take seconds.
	if err := t.checkDir(ctx, cwd); err != nil {
		return resultText(result{Error: err.Error()}, in.Timeout, nil, false), true
	}
	boxer := t.sandboxerFor(rt)
	snapshot, err := t.snapshotFor(ctx, boxer != nil)
	if err != nil {
		return resultText(result{Error: err.Error()}, in.Timeout, nil, false), true
	}
	s := spec{
		shell: t.shell, dir: cwd, scripts: t.scripts, env: t.env,
		command: wrapper(t.kind, snapshot, in.Command),
		capture: tools.FileLimit, timeout: in.Timeout, killGrace: killGrace, pipeGrace: pipeGrace,
	}
	if boxer != nil {
		sandboxedRun, err := t.sandboxedRunFor(ctx, boxer, rt, cwd, false)
		if err != nil {
			return resultText(result{Error: err.Error()}, in.Timeout, nil, false), true
		}
		// After the reap: nothing of the run's is left to use it.
		defer sandboxedRun.end()
		s.sandboxedRun = sandboxedRun
	}
	r := run(ctx, s)
	return resultText(r, in.Timeout, tools.SaveTo(dir), boxer != nil && boxer.Confines()), r.failed()
}

// sandboxedRun is what a sandboxed run carries beyond an unconfined one: the
// sandboxer, for a run with a cluster its claim and its proxy, the run's own
// directory, and the Run the sandboxer is handed, less what shellCmd fills in.
type sandboxedRun struct {
	boxer sandboxer
	claim claimed
	proxy *runProxy
	dir   *runDir
	run   sandbox.Run
}

// end ends whatever of the run was made: the proxy, which cancels what is in
// flight and closes the socket, then the claim, then the run's directory.
func (r *sandboxedRun) end() {
	if r.proxy != nil {
		r.proxy.end()
	}
	if r.claim != nil {
		r.claim.Close()
	}
	if r.dir != nil {
		r.dir.removeLogged()
	}
}

// sandboxedRunFor makes a sandboxed run's directory and builds its environment,
// for a call of rt's chat starting in cwd: with the chat's cluster, its claim,
// its proxy, whose writes go as writesFor says, its kubeconfig and its kubectl
// cache. A cluster that is gone fails it before anything is made, since a
// sandboxed kubectl aimed at nothing would read as the cluster being down. The
// caller calls end when the run ends.
func (t *Tool) sandboxedRunFor(ctx context.Context, boxer sandboxer, rt tools.Runtime, cwd string, background bool) (*sandboxedRun, error) {
	r := &sandboxedRun{boxer: boxer}
	var cluster *target
	var port int
	if rt.ClusterID != "" {
		tg, err := t.target(ctx, rt.ClusterID)
		if err != nil {
			return nil, err
		}
		cluster = &tg
		// Only a run with a cluster dials its port.
		if port, err = boxer.Port(); err != nil {
			return nil, err
		}
		if r.claim, err = t.claim(ctx, rt.ClusterID, tg.serverUID); err != nil {
			return nil, err
		}
	}
	var err error
	if r.dir, err = newRunDir(t.runsDir, t.tmpDir, os.Getpid()); err != nil {
		r.end()
		return nil, err
	}
	reads := []string{r.dir.path}
	if err := makeToolHome(rt.Dir); err != nil {
		r.end()
		return nil, errors.New("the tool home " + tools.ToolHomePath(rt.Dir) + " could not be made: " + err.Error())
	}
	ws, toolHome := tools.WorkspacePath(rt.Dir), tools.ToolHomePath(rt.Dir)
	writes := []string{ws, toolHome, r.dir.tmp}
	var relays []sandbox.Relay
	if cluster != nil {
		socket := r.dir.socket()
		asker, refusal := writesFor(rt, background)
		if r.proxy, err = startProxy(r.claim, socket, asker, refusal); err != nil {
			r.end()
			return nil, err
		}
		if err := writeKubeconfig(r.dir.kubeconfig(), cluster.context, port, r.proxy.grant.Token()); err != nil {
			r.end()
			return nil, err
		}
		writes = append(writes, cluster.cacheDir)
		relays = []sandbox.Relay{{Port: port, Socket: socket}}
	}
	// System stats folders under the home, Never lists the other homes and
	// toolVersions reads a file under the home, any of which can hang on a
	// network mount, so the run is built on a goroutine abandoned if ctx ends
	// first.
	built := make(chan sandbox.Run, 1)
	go func() {
		sys := boxer.System(t.home, t.shell)
		toolchain := sys.Env
		if sys.Asdf {
			toolchain = slices.Concat(sys.Env, toolVersions(t.home))
		}
		env := sandboxedRunEnv(os.Environ(), t.env, ws, cwd, r.dir, cluster, toolHome, toolchain)
		built <- sandbox.Run{Env: env, Policy: t.workspacePolicy(boxer, sys.Files, reads, writes, relays)}
	}()
	select {
	case r.run = <-built:
		return r, nil
	case <-ctx.Done():
		r.end()
		return nil, ctx.Err()
	}
}

// workspacePolicy is a sandboxed run's policy: system, the sandbox's System,
// less what lies in Kstack's directories, and the extra writable, which lies
// in none; the Never paths and Kstack's directories denied but for the run's
// own reads and writes, which lie inside them; and relays.
func (t *Tool) workspacePolicy(boxer sandboxer, system sandbox.FilePolicy, reads, writes []string, relays []sandbox.Relay) sandbox.Policy {
	files := system.Outside(t.denied...)
	files.Write = append(files.Write, t.extraWritable...)
	return sandbox.Policy{
		Files:   files,
		Always:  sandbox.AlwaysPolicy{Deny: boxer.Never(t.home), Kstack: t.denied, Read: reads, Write: writes},
		Network: sandbox.NetworkPolicy{Relays: relays},
	}
}

// checkDir is nil when dir is a directory, else why the command cannot start
// there: it is not one, or ctx ended first. A stat on a dead network mount can
// block in the kernel past any cancel, so it runs on a goroutine of its own,
// abandoned if ctx ends and gone whenever the filesystem answers. It is the
// cancellable part of starting in dir: the start itself cannot be cancelled,
// so a mount that dies between the two still holds the call.
func (t *Tool) checkDir(ctx context.Context, dir string) error {
	isDir := make(chan bool, 1)
	go func() {
		info, err := t.statDir(dir)
		isDir <- err == nil && info.IsDir()
	}()
	select {
	case ok := <-isDir:
		if !ok {
			return errors.New(dir + " is not a directory")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// spec is one run: what it carries when sandboxed, nil for a run outside the
// sandbox, the shell, where it starts, the command, what its environment adds
// outside the sandbox, and its bounds.
type spec struct {
	sandboxedRun                 *sandboxedRun
	shell, dir, scripts, command string
	env                          []string
	capture                      int
	timeout                      time.Duration // the call's own deadline
	killGrace                    time.Duration // a timed-out group's time between SIGTERM and SIGKILL
	pipeGrace                    time.Duration // the wait for the pipe once bash is gone
	hooks                        hooks
}

// stopFor is why the runner is stopping bash: the loop's context ending is a
// cancel, and the call's own deadline is a timeout.
func stopFor(parent context.Context) stop {
	if parent.Err() != nil {
		return stopCancel
	}
	return stopTimeout
}

// guard is a run's stop and whether bash has been reaped, under one lock: a stop
// is recorded only before the reap, so a reaped exit is never relabelled.
type guard struct {
	hooks  hooks
	mu     sync.Mutex
	stop   stop
	reaped bool
	// done is closed at the reap, for the escalation and the Windows watcher to
	// select on.
	done chan struct{}
}

func newGuard(h hooks) *guard {
	return &guard{hooks: h, done: make(chan struct{})}
}

// record notes why the runner is about to signal bash, or answers false once
// bash is reaped, when nothing may be signalled.
func (g *guard) record(why stop) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reaped {
		return false
	}
	g.stop = why
	if g.hooks.onStop != nil {
		g.hooks.onStop(why)
	}
	return true
}

// reap marks bash reaped and answers the stop recorded before it.
func (g *guard) reap() stop {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reaped = true
	close(g.done)
	return g.stop
}

// escalate runs kill once the grace is out, or at once if parent ends during it.
// The reap disarms it: a late wake must not signal a group id that may belong to
// another process by then.
func (g *guard) escalate(parent context.Context, grace time.Duration, kill func()) {
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-t.C:
	case <-parent.Done():
	case <-g.done:
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.reaped {
		kill()
	}
}

// deadline derives the call's own context from parent: it ends when the timer
// fires, and the runner reads parent to tell that from a cancel.
func (h hooks) deadline(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	fire := time.After(timeout)
	if h.after != nil {
		fire = h.after(timeout)
	}
	go func() {
		select {
		case <-fire:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (h hooks) waitBeforeReap(ctx context.Context) {
	if h.beforeReap != nil {
		h.beforeReap(ctx)
	}
}

func (h hooks) waitAfterReap(ctx context.Context) {
	if h.afterReap != nil {
		h.afterReap(ctx)
	}
}

// copyOutput copies r to dst on a goroutine of its own, and answers the channel
// closed when the copy ends. A copy awaitCopy cuts short still takes what the
// pipe holds.
func copyOutput(dst io.Writer, r *os.File) <-chan struct{} {
	read := make(chan struct{})
	go func() {
		defer close(read)
		_, err := io.Copy(dst, r)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			drainPipe(dst, r)
		}
	}()
	return read
}

// awaitCopy waits for a copyOutput to end, or cuts it short once grace is out
// and waits then. bash is gone by now; whatever still holds the write end left
// its group, and the wait must end without it. The cut is a read deadline, so
// the copy still takes what bash wrote before it exited; a pipe that takes no
// deadline is closed instead.
func awaitCopy(read <-chan struct{}, r *os.File, grace time.Duration) {
	select {
	case <-read:
	case <-time.After(grace):
		if r.SetReadDeadline(time.Now()) != nil {
			_ = r.Close()
		}
		<-read
	}
}

// captureWriter keeps the first limit bytes written to it and counts the rest.
type captureWriter struct {
	limit     int
	buf       []byte
	discarded int
}

func (w *captureWriter) Write(p []byte) (int, error) {
	keep := min(len(p), max(w.limit-len(w.buf), 0))
	w.buf = append(w.buf, p[:keep]...)
	w.discarded += len(p) - keep
	return len(p), nil
}

// stop is why the runner signalled bash, if it did.
type stop int

const (
	stopNone    stop = iota
	stopTimeout      // the call's own deadline
	stopCancel       // the loop's context: the user's Cancel, a deleted chat, shutdown
)

// result is what one command produced.
type result struct {
	// Output is stdout and stderr together, in arrival order, up to the capture.
	Output string
	// Discarded is how many bytes the command wrote past the capture.
	Discarded int
	// ExitCode is how bash ended, as exitCode reads it.
	ExitCode int
	// Stop is why the runner signalled bash; stopNone when it did not.
	Stop stop
	// CodeUnknown is set when bash ran but how it ended could not be read.
	CodeUnknown bool
	// Error is why bash could not be started, and empty once it ran.
	Error string
}

// failed is whether the model is answered with an error. A stopped command is
// one whatever it exited with, so a cancel is never kept as a success.
func (r result) failed() bool {
	return r.Error != "" || r.CodeUnknown || r.ExitCode != 0 || r.Stop != stopNone
}

// resultText is what the model reads. An error result's first line is the
// sidecar's, saying what happened; the output follows on the next, redacted
// whole. Output that fits the inline limit with the header comes back as it is;
// past it, it is saved, or cut when the save fails. A bash that never started is
// the reason alone, so the model can tell a command that never began from one
// that failed. A confined run that failed on its own ends with sandboxLine, so
// the model knows the sandbox may be why.
func resultText(r result, timeout time.Duration, save tools.Saver, confined bool) string {
	if r.Error != "" {
		return tools.Cut("could not start: "+safe.String(r.Error), 0, tools.InlineLimit, "")
	}
	trailer := ""
	if confined && r.Stop == stopNone && (r.ExitCode != 0 || r.CodeUnknown) {
		trailer = sandboxLine
	}
	return tools.Fit(save, headerOf(r, timeout), safe.Redact(r.Output), trailer, r.Discarded, "output")
}

// sandboxLine follows the output of a confined run that failed.
const sandboxLine = "\n(Ran in the sandbox: no network, no files outside the workspace and the system, cluster changes only once the user approves each one.)"

// headerOf is the sidecar's first line on a result, newline included, or "" for
// a command that exited 0 unstopped.
func headerOf(r result, timeout time.Duration) string {
	code := strconv.Itoa(r.ExitCode)
	switch {
	case r.Stop == stopTimeout:
		return "Command timed out after " + formatTimeout(timeout) + " (exit code " + code + ")\n"
	case r.Stop == stopCancel:
		return "Command cancelled (exit code " + code + ")\n"
	case r.CodeUnknown:
		return "Command ended; its exit code could not be read\n"
	case r.ExitCode != 0:
		return "Exit code " + code + "\n"
	}
	return ""
}

// formatTimeout is whole seconds when d is a whole number of them, else
// milliseconds, so a subsecond timeout never reads 0s.
func formatTimeout(d time.Duration) string {
	if d%time.Second == 0 {
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
	return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
}
