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

//go:build !windows

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/testutil"
)

// access is what a run could do with a file: read its contents, write it.
type access struct{ read, write bool }

var (
	none      = access{}
	readOnly  = access{read: true}
	readWrite = access{read: true, write: true}
)

// accessOf runs files through s under a policy of System plus files and
// always, over base, and answers what the run could do with each file, named
// by its path under base. A read counts only when it returns the file's
// contents: on Linux a denied file reads as /dev/null and a denied folder
// lists as empty, where macOS answers an error.
func accessOf(t *testing.T, s *Sandbox, base string, files FilePolicy, always AlwaysPolicy, names ...string) map[string]access {
	t.Helper()
	home := mkdirs(t, base, "home")[0]
	var script strings.Builder
	for _, name := range names {
		path := filepath.Join(base, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("contents of "+name), 0o600))
		fmt.Fprintf(&script, "printf '%%s\\n' \"$(cat '%[1]s' 2>/dev/null)\"; echo x >> '%[1]s' 2>/dev/null; ", path)
	}
	script.WriteString("true")
	env := []string{"PATH=/usr/bin:/bin"}
	system := s.System(home, "/bin/sh").Files
	files.Read = append(system.Read, files.Read...)
	files.Deny = append(system.Deny, files.Deny...)
	always.Deny = append(s.Never(home), always.Deny...)
	r := Run{Shell: "/bin/sh", Args: []string{"-c", script.String()}, Dir: "/", Env: env, Policy: Policy{Files: files, Always: always}}

	out, err := command(t, s, t.Context(), r).Output()
	require.NoError(t, err, string(out))

	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	require.Len(t, lines, len(names), string(out))
	got := map[string]access{}
	for i, name := range names {
		written, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(name)))
		require.NoError(t, err)
		got[name] = access{read: lines[i] == "contents of "+name, write: strings.HasSuffix(string(written), "x\n")}
	}
	return got
}

// Every pair of rules answers as the rules say, on Linux and on macOS alike:
// the deepest rule wins, the narrower wins a tie, and inside a Kstack path
// only the run's own paths open anything.
func TestEveryPairOfRulesAnswersAlike(t *testing.T) {
	s := confining(t)
	for name, c := range map[string]struct {
		files  func(outer, inner string) FilePolicy
		inner  access
		beside access
	}{
		"Read over Read":  {func(o, i string) FilePolicy { return FilePolicy{Read: []string{o, i}} }, readOnly, readOnly},
		"Read over Write": {func(o, i string) FilePolicy { return FilePolicy{Read: []string{o}, Write: []string{i}} }, readWrite, readOnly},
		"Read over Deny":  {func(o, i string) FilePolicy { return FilePolicy{Read: []string{o}, Deny: []string{i}} }, none, readOnly},
		"Deny over Read":  {func(o, i string) FilePolicy { return FilePolicy{Deny: []string{o}, Read: []string{i}} }, readOnly, none},
		"Deny over Write": {func(o, i string) FilePolicy { return FilePolicy{Deny: []string{o}, Write: []string{i}} }, readWrite, none},
		"Deny over Deny":  {func(o, i string) FilePolicy { return FilePolicy{Deny: []string{o, i}} }, none, none},
	} {
		t.Run(name, func(t *testing.T) {
			base := resolved(t.TempDir())
			outer, inner := filepath.Join(base, "outer"), filepath.Join(base, "outer", "inner")

			got := accessOf(t, s, base, c.files(outer, inner), AlwaysPolicy{}, "outer/inner/f", "outer/beside/f")

			assert.Equal(t, map[string]access{"outer/inner/f": c.inner, "outer/beside/f": c.beside}, got)
		})
	}
	for name, c := range map[string]struct {
		files FilePolicy
		want  access
	}{
		"Write and Read": {FilePolicy{Write: []string{"p"}, Read: []string{"p"}}, readOnly},
		"Write and Deny": {FilePolicy{Write: []string{"p"}, Deny: []string{"p"}}, none},
		"Read and Deny":  {FilePolicy{Read: []string{"p"}, Deny: []string{"p"}}, none},
	} {
		t.Run("a tie of "+name, func(t *testing.T) {
			base := resolved(t.TempDir())
			on := func(rules []string) []string {
				if len(rules) == 0 {
					return nil
				}
				return []string{filepath.Join(base, "p")}
			}
			files := FilePolicy{Read: on(c.files.Read), Write: on(c.files.Write), Deny: on(c.files.Deny)}

			got := accessOf(t, s, base, files, AlwaysPolicy{}, "p/f")

			assert.Equal(t, map[string]access{"p/f": c.want}, got)
		})
	}
	t.Run("a Kstack path with the run's own inside", func(t *testing.T) {
		base := resolved(t.TempDir())
		k := filepath.Join(base, "k")
		always := AlwaysPolicy{Kstack: []string{k}, Read: []string{filepath.Join(k, "r")}, Write: []string{filepath.Join(k, "w")}}

		got := accessOf(t, s, base, FilePolicy{Read: []string{base}}, always, "beside/f", "k/other/f", "k/r/f", "k/w/f")

		assert.Equal(t, map[string]access{"beside/f": readOnly, "k/other/f": none, "k/r/f": readOnly, "k/w/f": readWrite}, got)
	})
}

// A Read on the home reads ~/.zshrc and not ~/.ssh, a denied-always path;
// and a Read inside ~/.ssh is refused by Check. Step 4D's grants rest on this.
func TestTheDeniedAlwaysListWinsOverARead(t *testing.T) {
	s := confining(t)
	base := resolved(t.TempDir())
	home := filepath.Join(base, "home")

	got := accessOf(t, s, base, FilePolicy{Read: []string{home}}, AlwaysPolicy{}, "home/.zshrc", "home/.ssh/id_ed25519")

	assert.Equal(t, map[string]access{"home/.zshrc": readOnly, "home/.ssh/id_ed25519": none}, got)
	p := Policy{Files: FilePolicy{Read: []string{filepath.Join(home, ".ssh", "keys")}}, Always: AlwaysPolicy{Deny: s.Never(home)}}
	assert.Error(t, p.Check())
}

// Every rule is compiled at its target: a Kstack directory given through a
// linked home, and a credential path that is itself a link, each lie where
// they resolve, inside the Read that holds them.
func TestARuleIsCompiledWhereItsLinkLeads(t *testing.T) {
	base := resolved(t.TempDir())
	home := mkdirs(t, base, "home")[0]
	linked := filepath.Join(base, "home-link")
	require.NoError(t, os.Symlink(home, linked))
	d := mkdirs(t, home, "tools/kstack", "tools/credentials")
	require.NoError(t, os.Symlink(d[1], filepath.Join(home, ".kube")))
	p := Policy{
		Files:  FilePolicy{Read: []string{filepath.Join(linked, "tools")}},
		Always: AlwaysPolicy{Deny: []string{filepath.Join(linked, ".kube")}, Kstack: []string{filepath.Join(linked, "tools", "kstack")}},
	}

	var at []string
	for _, r := range p.rules() {
		at = append(at, r.at)
	}

	assert.Equal(t, []string{filepath.Join(home, "tools"), d[1], d[0]}, at)
}

// A run's own path that is a link is refused, Read or Write, since the profile
// would open where the link leads. A link above the last component is not.
func TestARunsOwnPathThatIsALinkIsRefused(t *testing.T) {
	base := resolved(t.TempDir())
	d := mkdirs(t, base, "data/chats/c/workspace", "cache/tmp/1-a", "runtime/runs/1-a")
	link := filepath.Join(base, "data/chats/c/linked")
	require.NoError(t, os.Symlink(filepath.Join(base, "data"), link))
	above := filepath.Join(base, "cache/above")
	require.NoError(t, os.Symlink(filepath.Join(base, "data"), above))
	kstack := []string{filepath.Join(base, "data"), filepath.Join(base, "cache"), filepath.Join(base, "runtime")}
	policy := func(read, write []string) Policy {
		return Policy{Always: AlwaysPolicy{Kstack: kstack, Read: read, Write: write}}
	}
	require.NoError(t, policy([]string{d[2]}, []string{d[0], d[1]}).Check())

	assert.ErrorContains(t, policy([]string{d[2]}, []string{link, d[1]}).Check(), "is a link", "a Write path")
	assert.ErrorContains(t, policy([]string{link}, []string{d[0]}).Check(), "is a link", "a Read path")
	assert.NoError(t, policy(nil, []string{filepath.Join(above, "chats/c/workspace")}).Check(), "a link above the last component")
}

// A run's own path that cannot be looked at is refused, since it could be a
// link.
func TestARunsOwnPathThatCannotBeLookedAtIsRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root looks through any folder's mode")
	}
	base := resolved(t.TempDir())
	d := mkdirs(t, base, "data/chats/c/workspace")
	chat := filepath.Dir(d[0])
	require.NoError(t, os.Chmod(chat, 0))
	t.Cleanup(func() { _ = os.Chmod(chat, 0o700) })
	p := Policy{Always: AlwaysPolicy{Kstack: []string{filepath.Join(base, "data")}, Write: d}}

	assert.ErrorIs(t, p.Check(), os.ErrPermission)
}

func init() {
	// files opens /dev/null until it cannot, and prints how many it opened
	// and why it stopped.
	helpers["files"] = func() int {
		for n := 0; ; n++ {
			if _, err := os.Open("/dev/null"); err != nil {
				fmt.Println(n, errors.Is(err, syscall.EMFILE))
				return 0
			}
		}
	}
}

// limitedRunOf is a run of argv on s under the System policy over a fresh
// home, with a workspace it writes, the system's PATH and env, and limits l.
func limitedRunOf(t *testing.T, s *Sandbox, l Limits, env []string, argv ...string) Run {
	t.Helper()
	d := mkdirs(t, resolved(t.TempDir()), "home", "ws")
	env = append([]string{"PATH=/usr/bin:/bin"}, env...)
	files := s.System(d[0], "/bin/sh").Files
	files.Write = append(files.Write, d[1])
	return Run{
		Shell: argv[0], Args: argv[1:], Dir: d[1], Env: env,
		Policy: Policy{Files: files, Always: AlwaysPolicy{Deny: s.Never(d[0])}, Limits: l},
	}
}

// limitedRun runs limitedRunOf's run to its end and answers its exit code and
// stdout. A failsafe bounds it, since a limit that fails to hold leaves a
// spin running; a run that reaches it fails the test, so the kill is never
// read as the limit's.
func limitedRun(t *testing.T, s *Sandbox, l Limits, env []string, argv ...string) (int, string) {
	t.Helper()
	r := limitedRunOf(t, s, l, env, argv...)
	ctx, cancel := context.WithTimeout(t.Context(), 3*testutil.Timeout)
	defer cancel()
	cmd := command(t, s, ctx, r)
	out, err := cmd.Output()
	if cmd.ProcessState == nil {
		require.NoError(t, err)
	}
	require.NoError(t, ctx.Err(), "the run outlived its bound")
	return ExitCode(cmd.ProcessState), string(out)
}

// A process past its CPU time dies of SIGXCPU, which a shell reports as 152.
func TestACPUSpinEndsWithSIGXCPU(t *testing.T) {
	s := confining(t)

	code, _ := limitedRun(t, s, Limits{CPUSeconds: 1}, nil, "/bin/sh", "-c", "while :; do :; done")

	assert.Equal(t, 152, code)
}

func TestOpeningPastTheFileLimitFails(t *testing.T) {
	s := confining(t)

	code, out := limitedRun(t, s, Limits{OpenFiles: 64}, []string{"KSTACK_SANDBOX_TEST_HELPER=files"}, os.Args[0])

	require.Equal(t, 0, code, out)
	var n int
	var emfile bool
	_, err := fmt.Sscan(out, &n, &emfile)
	require.NoError(t, err, out)
	assert.Less(t, n, 64)
	assert.True(t, emfile, out)
}
