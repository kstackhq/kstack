// Copyright 2026 The Kstack Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//     http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build unix

// Package loginshell runs the user's login shell. Launch runs one command in it,
// and the bash tool's profile snapshot uses it on every Unix. Import uses it to
// recover an allowlisted set of the environment the login shell builds, which a
// macOS GUI launch does not inherit from launchd: without it, kubeconfig `exec`
// credential plugins on the shell PATH are not found, and the ones that are run
// against the wrong identity. Only main calls Import, on darwin.
package loginshell

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// DefaultTimeout bounds the whole resolution — spawn, read, kill, reap. Long
// enough for a startup file that does real work, short enough that a stalled one
// does not hold up the sidecar's READY line.
const DefaultTimeout = 5 * time.Second

// marker delimits the PATH in the shell's stdout. It must begin with a letter:
// printf reads `\0` as the start of an octal escape, so a marker starting 0-7
// would be swallowed into it and vanish.
const marker = "kstack_loginshell_v1"

// kind says what a variable's value is, and is the whole of the per-variable
// knowledge: a path is resolved, a list is split on `:` first, a plain value is
// copied through.
type kind int

const (
	plain kind = iota
	path
	pathList
)

type variable struct {
	name string
	kind kind
}

// imported is the allowlist, and it is the security boundary. We spawn credential
// plugins as children, so every name here is one a startup file can set inside
// them: deny by default, no prefix wildcards, and adding a row is a security
// change. The two SSL_CERT_* rows serve those children alone — Go reads them in
// crypto/x509/root_unix.go, which is not built on darwin, the one platform this
// runs on.
var imported = []variable{
	{"PATH", pathList},                       // finds the plugin binary
	{"KUBECONFIG", pathList},                 // which kubeconfig files exist at all
	{"AWS_PROFILE", plain},                   // which identity the plugin assumes
	{"AWS_REGION", plain},                    // which region it authenticates against
	{"AWS_DEFAULT_REGION", plain},            // the same, as the SDK's own spelling
	{"AWS_CONFIG_FILE", path},                // where those profiles are defined
	{"AWS_SHARED_CREDENTIALS_FILE", path},    // where their credentials are
	{"AWS_SDK_LOAD_CONFIG", plain},           // whether the profile file is read at all
	{"CLOUDSDK_CONFIG", path},                // gcloud's config, holding the active account
	{"CLOUDSDK_CORE_PROJECT", plain},         // which project gke-gcloud-auth-plugin targets
	{"GOOGLE_APPLICATION_CREDENTIALS", path}, // a service-account key
	{"AZURE_CONFIG_DIR", path},               // the Azure CLI's token cache
	{"HTTP_PROXY", plain},                    // reaching the API server from a corporate network
	{"HTTPS_PROXY", plain},                   // the same, for TLS
	{"NO_PROXY", plain},                      // a list, but not of paths
	{"http_proxy", plain},                    // Go's ProxyFromEnvironment honours both spellings
	{"https_proxy", plain},                   // the same
	{"no_proxy", plain},                      // the same
	{"SSL_CERT_FILE", path},                  // a private CA, for the plugins we spawn
	{"SSL_CERT_DIR", pathList},               // the same, as a directory list
	{"OLLAMA_HOST", plain},                   // where the user's Ollama daemon is; an endpoint, not a credential
}

// command is the only thing the shell is asked to run. It is built from imported
// and constants and nothing else — no part of it comes from a kubeconfig, cluster
// data, or the socket. Both utilities are absolute because a builtin printf varies
// by shell, echo cannot emit a NUL, and looking either one up would search the
// very PATH we are here to replace. It parses unchanged under zsh, bash, and fish
// (whose `||` dates from 3.0).
var command = buildCommand()

// buildCommand frames the shell's own cwd and then one printenv per variable
// between markers. The cwd leads because a startup file may have changed it, and
// a relative value the shell reports means the directory it ended in. printenv
// exits 1 for an unset variable, so `|| true` stops a startup file's `set -e`
// from ending the run there.
func buildCommand() string {
	var b strings.Builder
	b.WriteString(`/usr/bin/printf '\000` + marker + `\000'; /bin/pwd || true; `)
	for _, v := range imported {
		b.WriteString(`/usr/bin/printf '\000` + marker + `\000'; `)
		b.WriteString(`/usr/bin/printenv ` + v.name + ` || true; `)
	}
	b.WriteString(`/usr/bin/printf '\000` + marker + `\000'`)
	return b.String()
}

// defaultShell is macOS's login shell, used when $SHELL says nothing usable. A
// variable so a test can stand on a machine without one.
var defaultShell = "/bin/zsh"

// maxOutputBytes caps each of the shell's two streams. Startup files are chatty,
// but not this chatty; past it we are reading something we do not understand.
const maxOutputBytes = 64 << 10

// The fixed fallback reasons. They are the whole vocabulary of the warning line —
// short and closed on purpose, because the log must never carry PATH, shell
// output, or any other environment value.
const (
	reasonNoShell     = "no shell"
	reasonShellExited = "shell exited"
	reasonBadOutput   = "bad output"
	reasonOutputLimit = "output limit"
	reasonTimeout     = "timeout"
)

// Fault is a resolution failure, carrying exactly what the caller may log.
type Fault struct {
	Reason string
	// ExitCode is -1 when the shell never ran, or never exited on its own.
	ExitCode int
}

func fault(reason string) *Fault { return &Fault{Reason: reason, ExitCode: -1} }

// answer is what the shell reported: the directory it ended in, and the raw value
// of every variable it had set.
type answer struct {
	dir string
	env map[string]string
}

// outcome is what one of the reader goroutines concluded. reason is empty on
// success, and is one of the fixed fallback reasons otherwise.
type outcome struct {
	out    []byte
	reason string
}

// Import asks the user's login shell for the variables in imported and returns
// the ones it had set, resolved. The deadline is ctx's; the caller installs the
// answer. Import never touches the environment itself, so a failure leaves the
// inherited one exactly as it was.
func Import(ctx context.Context) (map[string]string, *Fault) {
	shell, f := shellOrDefault()
	if f != nil {
		return nil, f
	}
	out, f := Launch(ctx, shell, command, maxOutputBytes, func(buf []byte, _ int) bool {
		_, ok := parse(buf)
		return ok
	})
	if f != nil {
		return nil, f
	}
	a, _ := parse(out)
	env, ok := resolveEnv(a.env, a.dir)
	if !ok {
		// The shell answered, but with a PATH that finds nothing.
		return nil, fault(reasonBadOutput)
	}
	return env, nil
}

// Launch runs command in shell as an interactive login shell and returns its
// stdout as read up to the moment done says it is complete. done is handed the
// whole read so far and where the latest read began. Each of the shell's two
// streams is capped at limit. The deadline is ctx's, and the shell's session is
// killed and reaped before Launch returns, whatever happened.
func Launch(ctx context.Context, shell, command string, limit int, done func(buf []byte, from int) bool) ([]byte, *Fault) {
	// Interactive login flags, passed separately because fish does not cluster
	// them, so the shell reads the startup files where a user sets PATH.
	cmd := exec.Command(shell, "-i", "-l", "-c", command)
	// nil stdin is /dev/null: a startup file that reads it gets EOF, not a hang.
	cmd.Stdin = nil
	cmd.SysProcAttr = shellProcAttr()
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	cmd.Env = append(os.Environ(), "DISABLE_AUTO_UPDATE=true")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fault(reasonNoShell)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fault(reasonNoShell)
	}
	if err := cmd.Start(); err != nil {
		return nil, fault(reasonNoShell)
	}

	// Buffered to each goroutine's send, so neither blocks after Launch returns.
	results := make(chan outcome, 2)
	go func() { results <- readMarked(stdout, limit, done) }()
	go func() {
		// Draining stderr at all is what keeps a chatty startup file from
		// filling the pipe and blocking the shell forever. The bytes are never
		// read back: shell output must not reach a log line.
		if overflowed(stderr, limit) {
			results <- outcome{reason: reasonOutputLimit}
		}
	}()

	var res outcome
	select {
	case res = <-results:
	case <-ctx.Done():
		res = outcome{reason: reasonTimeout}
	}
	if res.reason != "" {
		// An answer already in hand outranks a drain failure or a deadline
		// that raced it: it is complete either way.
		select {
		case other := <-results:
			if other.reason == "" {
				res = other
			}
		default:
		}
	}

	// Unconditional, so a shell still running — or a grandchild holding stdout
	// open — cannot outlive the call or extend it. A group that already exited
	// ignores this. Wait then closes our pipe ends, which unblocks the reader
	// goroutines even if a grandchild that left the group keeps the far ends open.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	code := exitCode(cmd.Wait())

	if res.reason == "" {
		return res.out, nil
	}
	// The shell's status gates only when nothing usable was captured, and a
	// nonzero one names the failure better than the empty output it caused.
	if res.reason == reasonBadOutput && code > 0 {
		res.reason = reasonShellExited
	}
	return nil, &Fault{Reason: res.reason, ExitCode: code}
}

// shellProcAttr puts the shell in a session of its own. One kill then reaches
// whatever the startup files spawned, and — a session leader starting with none —
// the shell has no controlling terminal to contend for. Sharing ours is what
// hangs: an interactive shell in a background process group is stopped by SIGTTIN
// the first time it touches the tty, and the answer never comes. A GUI launch has
// no terminal to inherit, so only a run from one is affected.
func shellProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// Find is $SHELL when it names an executable file by absolute path. A
// relative one is never looked up: resolving it would search the very PATH
// Import is here to replace.
func Find() (string, bool) {
	shell := os.Getenv("SHELL")
	if filepath.IsAbs(shell) && executable(shell) {
		return shell, true
	}
	return "", false
}

// shellOrDefault returns the shell to ask for a PATH: Find, else macOS's own.
func shellOrDefault() (string, *Fault) {
	if shell, ok := Find(); ok {
		return shell, nil
	}
	if executable(defaultShell) {
		return defaultShell, nil
	}
	return "", fault(reasonNoShell)
}

func executable(file string) bool {
	info, err := os.Stat(file)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// readMarked reads r until done says it holds a complete answer, it exceeds
// limit, or it ends. Stopping at done is the point: waiting for EOF would hand a
// startup file that backgrounds a daemon the power to stall every launch.
func readMarked(r io.Reader, limit int, done func([]byte, int) bool) outcome {
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			from := len(buf)
			buf = append(buf, chunk[:n]...)
			if done(buf, from) {
				return outcome{out: buf}
			}
			if len(buf) > limit {
				return outcome{reason: reasonOutputLimit}
			}
		}
		if err != nil {
			return outcome{reason: reasonBadOutput}
		}
	}
}

// parse returns what the shell reported: the cwd frame first, then the raw value
// of each variable it had set, keyed by name. It reports false while the last
// marker has not arrived yet, so a caller reading a stream can keep going; out is
// the bytes read so far.
func parse(out []byte) (answer, bool) {
	delimiter := []byte("\x00" + marker + "\x00")
	_, rest, ok := bytes.Cut(out, delimiter)
	if !ok {
		return answer{}, false
	}
	frames := make([][]byte, 0, len(imported)+1)
	for range len(imported) + 1 {
		var frame []byte
		frame, rest, ok = bytes.Cut(rest, delimiter)
		if !ok {
			return answer{}, false
		}
		// printenv's own trailing newline, and only that one: a value may hold
		// any byte but NUL, newlines included, so nothing further is trimmed and
		// nothing else is judged.
		frame = bytes.TrimSuffix(frame, []byte("\n"))
		// A NUL cannot come from either utility, so one here means the markers
		// framed something other than the payload we asked for.
		if bytes.IndexByte(frame, 0) >= 0 {
			return answer{}, false
		}
		frames = append(frames, frame)
	}

	a := answer{dir: string(frames[0]), env: make(map[string]string, len(imported))}
	for i, v := range imported {
		// printenv prints nothing for an unset variable and `|| true` swallows
		// its status, so an empty frame is the shell saying it has no value.
		if frame := frames[i+1]; len(frame) > 0 {
			a.env[v.name] = string(frame)
		}
	}
	return a, true
}

// resolveEnv resolves each raw value by its kind and drops the ones left empty.
// It reports false for a PATH that is missing or resolves to nothing: installing
// that would leave the process unable to find any command at all, which is worse
// than the PATH launchd handed us.
func resolveEnv(raw map[string]string, dir string) (map[string]string, bool) {
	// A cwd the shell could not report is no base at all, and a relative entry
	// then passes through as the shell gave it.
	if !filepath.IsAbs(dir) {
		dir = ""
	}
	env := make(map[string]string, len(raw))
	for _, v := range imported {
		value, ok := raw[v.name]
		if !ok {
			continue
		}
		switch v.kind {
		case path:
			value = resolvePath(value, dir)
		case pathList:
			value = resolveList(value, dir)
		}
		if value == "" {
			continue
		}
		env[v.name] = value
	}
	if env["PATH"] == "" {
		return nil, false
	}
	return env, true
}

// resolvePath makes one path the shell reported meaningful outside it: the shell
// ran in dir, so a relative entry means the same thing joined to dir. It returns
// "" for a dropped entry. A leading `~` is answered first, because IsAbs says it
// is relative and joining would invent a path the shell never produced.
func resolvePath(value, dir string) string {
	switch {
	case value == "":
		return ""
	case strings.HasPrefix(value, "~"):
		return value
	case filepath.IsAbs(value), dir == "":
		return value
	default:
		// Prefixed rather than joined: filepath.Join cleans, and cleaning
		// `link/..` away lexically names a different file than the kernel would
		// resolve whenever link is a symlink.
		return strings.TrimSuffix(dir, "/") + "/" + value
	}
}

// resolveList resolves each `:`-separated entry and rejoins the survivors.
// Splitting is the list's business alone: a single path may legally contain a
// colon, so only the kind knows whether one is a separator.
func resolveList(value, dir string) string {
	var out []string
	for _, entry := range filepath.SplitList(value) {
		if resolved := resolvePath(entry, dir); resolved != "" {
			out = append(out, resolved)
		}
	}
	return strings.Join(out, string(filepath.ListSeparator))
}

// overflowed discards r, reporting whether it ran past limit.
func overflowed(r io.Reader, limit int) bool {
	n, _ := io.Copy(io.Discard, io.LimitReader(r, int64(limit)+1))
	return n > int64(limit)
}

// exitCode returns the shell's status, or -1 when it never exited on its own —
// including the kill above, which is why a success never consults it.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}
