---
title: The sandbox's own environment
scope: sidecar
status: Planned
---

# The sandbox's own environment

**Needs:** nothing beyond `main`: the `sandbox` package's `Policy` and `Lists`, which
`sidecar/CLAUDE.md` describes. **Unblocks:** steps 3A and 4A.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a sandboxed run takes three things from the sidecar and the user's shell:

- `PATH`, which also decides which folders the sandbox can read: `pathTrees` opens the tree of
  every `PATH` entry under the home and the resolved path of every entry outside the system
  folders, and `pathLinks` recreates the links on the way;
- the locale, `LANG` and every `LC_*`;
- the shell snapshot, which every sandboxed command sources first, so the user's functions and
  aliases run inside the sandbox, and which every sandboxed run waits for. Its last line is
  `export PATH=…` from the user's interactive login shell, so the `PATH` a sandboxed command
  runs with is the snapshot's, not the sidecar's.

After this step:

- **The zones are lists.** `Lists` says what every run reads by default (`System`), which
  folders under the home hold the user's tools (`Toolchain`), what no run reads whatever else is
  granted (`Never`), and what stays shut under a grant of a folder above it (`Closed`). A `PATH`
  entry opens nothing. Step 3A decides which entries the sandbox gets.
- **The environment is built from one table and nothing else.** One list in `sandbox` names
  what may never pass, the bash tool's builder never copies it, and both compilers refuse a run
  that holds it.
- **A tool that writes under the home writes a folder of the chat's**, through its own
  variables. `HOME` is the workspace, so the rest lands there, as today. The kubectl cache stays
  per cluster.
- **Sandboxed commands neither source nor wait for the snapshot.** On a machine with a sandbox
  the snapshot is taken the first time a command runs outside it.

Commands outside the sandbox are unchanged: they run as the user, with the user's environment
and the snapshot.

## What is not in this step

- **`PATH` itself.** Until step 3A the sandbox's `PATH` is the sidecar's, unfiltered. On macOS
  that is the login shell's already: the sidecar imports it at start (`main_darwin.go`), so
  nothing changes there. On Linux the sidecar's is the desktop session's, so the entries a
  `.bashrc` or `.zshrc` adds leave the sandbox's `PATH` with the snapshot. This step also stops
  `PATH` from opening folders, so an entry that no list names finds nothing. The chat's switch
  is the way out, and step 3A fixes both (*Decisions*, 9).
- **No user folders.** Step 4D adds grants. `Closed` and the other homes on `Never` matter only
  once a grant reaches them.
- **No limits.** Step 2B adds `Policy.Limits`.
- **No change on Windows**, which has no sandbox. `sandbox_windows.go`'s `System` and `Never`
  keep answering nothing.

## Design

### 1. The lists

`Lists` (`sandbox/lists.go`) gains `Toolchain` and `Closed`:

```go
type Lists struct {
	System    []string   // readable by default
	Toolchain []Location // readable under the home by default, when present
	Never     []string   // denied always
	Closed    []string   // denied, but a grant inside one opens what it names
}

// Location is a folder the user's tools live in. A path starting with ~/ is
// under the user's home. Env is set only when the first Read folder exists.
// With no home, Toolchain and Closed add nothing, as Never's ~/ paths do not.
type Location struct {
	Name string            // shown in Settings, like "asdf"
	Read []string          // folders every run can read
	Env  map[string]string // variables that point the tool at them, since HOME is the workspace
}
```

**`System`** is today's lists plus the folders that `PATH` entries outside the home commonly
resolve through. Today `pathTrees` opens any such entry. After this step, an entry outside
every list finds nothing until step 3A (*Decisions*, 9).

- Linux: `/usr`, `/bin`, `/sbin`, `/lib`, `/lib32`, `/lib64`, `/libx32`, `/etc`, `/opt`,
  `/nix/store`, `/home/linuxbrew/.linuxbrew`, and new, `/snap`, `/nix/var/nix/profiles` and
  NixOS's `/run/current-system`.
- macOS: `/usr`, `/bin`, `/sbin`, `/System`, `/Library`, `/Applications`, `/private/etc`,
  `/opt`, and new, `/nix/store`, `/nix/var/nix/profiles` and nix-darwin's
  `/run/current-system`.

`/nix/var/nix/profiles` holds multi-user Nix's profile links, which `PATH` names as
`/nix/var/nix/profiles/default/bin`. It holds links into `/nix/store` and no data.
`/run/current-system` is a link into `/nix/store`, which NixOS's `PATH` names as
`/run/current-system/sw/bin`; on Linux the rule binds nothing new and recreates the link (§3).

`/dev` and `/proc` are on no list. Each compiler supplies them: bwrap's `--dev /dev` and
`--proc /proc` (`args` in `sandbox_linux.go`), whose paths `Command` refuses a rule on
(`overFixedMount`), and the fixed rules of `profile_darwin.sb`, which already allow `/dev/null`,
`/dev/zero`, `/dev/random`, `/dev/urandom`, `/dev/fd`, the timezone database and the dyld cache.

`/etc` is read whole, with its secret files on `Never`: `/etc/ssh`, `/etc/sudoers`,
`/etc/sudoers.d`, `/etc/shadow`, `/etc/gshadow`, `/etc/krb5.conf`, `/etc/krb5.keytab`. On macOS
they are named once, under `/private/etc` where `System` reads them, with
`/private/etc/master.passwd`; rules compile resolved, so `/etc/...` needs no twin. This is decision 12 of the
note's [*Where this meets the code*](../../notes/sandbox-credentials-and-permissions.md#where-this-meets-the-code).

**`Toolchain`**, each with the variable that points its tool at it:

| Name | Read | Env |
| --- | --- | --- |
| user binaries | `~/.local/bin`, `~/bin`, `~/go/bin`, `~/.cargo/bin` | — |
| asdf | `~/.asdf` | `ASDF_DATA_DIR`, `ASDF_DIR`, and the global versions (§4) |
| mise | `~/.local/share/mise`, `~/.config/mise` | `MISE_DATA_DIR`, `MISE_CONFIG_DIR` |
| nvm | `~/.nvm` | `NVM_DIR` |
| pyenv | `~/.pyenv` | `PYENV_ROOT` |
| rbenv | `~/.rbenv` | `RBENV_ROOT` |
| volta | `~/.volta` | `VOLTA_HOME` |
| SDKMAN | `~/.sdkman/candidates` | — |
| tfenv | `~/.tfenv` | — |
| aqua | `~/.local/share/aquaproj-aqua`, `~/.config/aquaproj-aqua` | `AQUA_ROOT_DIR`, `AQUA_GLOBAL_CONFIG` (its `aqua.yaml`) |
| bun | `~/.bun/bin` | — |
| deno | `~/.deno/bin` | — |
| pipx and uv | `~/.local/share/pipx`, `~/.local/share/uv` | — |
| krew | `~/.krew` | `KREW_ROOT` |
| helm plugins | `~/.local/share/helm/plugins` | `HELM_PLUGINS` |
| Google Cloud SDK | `~/google-cloud-sdk` | — |
| Nix | `~/.nix-profile` | — |
| Rancher Desktop | `~/.rd/bin` | — |
| OrbStack | `~/.orbstack/bin` | — |
| Linkerd | `~/.linkerd2/bin` | — |
| istioctl | `~/.istioctl/bin` | — |

A location is a set of Read rules only where its folders exist, and its `Env` is set only then.
Rancher Desktop and OrbStack are read by their `bin` alone, since the folder above holds the
Docker socket. Their programs are links into `/Applications` or `/opt`, which `System` reads.

**A location joins the list** when its tool installs under the home by default, the folders
named hold programs or the runtime behind them and no credential, and Task 1's check shows a
program from it running on both platforms. A credential the tool keeps beside its programs goes
on `Never`, as `~/.local/share/uv/credentials` does. **A location leaves the list** when its tool does not
run with its Read and `Env` alone; the spec's residuals then name it.

**`Never`**, on top of today's (`~/.kube`, `~/.aws`, `~/.azure`, `~/.config/gcloud`, `~/.ssh`,
`~/.gnupg`, `~/.config/gh`, `~/.docker`, `~/.netrc`, `~/.git-credentials`,
`~/.cargo/credentials`, `~/.cargo/credentials.toml`, `~/.pulumi/credentials.json`,
`~/.fly/config.yml`, and per platform `~/Library/Keychains` or `~/.local/share/keyrings`):

- `~/.helm`, `~/.terraform.d`, `~/.npmrc`, `~/.pypirc`, `~/.gem/credentials`,
  `~/.config/git/credentials`;
- `~/.local/share/uv/credentials`, where `uv auth login` can keep a plaintext credential inside
  the uv location's tree;
- `~/.bash_history`, `~/.zsh_history`, `~/.python_history`, `~/.node_repl_history`,
  `~/.psql_history`, `~/.mysql_history`, `~/.lesshst`, `~/.local/share/fish/fish_history`;
- the `/etc` files above;
- the container sockets: `/run/containerd`, `/var/run/docker.sock`, `/run/docker.sock`,
  `/run/podman`, `~/.rd/docker.sock`, `~/.orbstack/run`, `~/.colima`, `~/.lima`, and on
  Linux the rootless sockets in the user's runtime folder (§2). Docker Desktop's
  `~/.docker/run` is inside `~/.docker` already;
- `/root`, and the other users' homes (§2);
- macOS: `~/Library/Cookies`, `~/Library/Application Support/Google/Chrome`,
  `~/Library/Application Support/Firefox`, `~/Library/Safari`;
- Linux: `~/.config/google-chrome`, `~/.config/chromium`, `~/.mozilla`.

A path on `Never` that does not exist is a Deny that covers nothing. A glob has no rule to
compile to, so the history files are named one by one.

**`Closed`**: `~/Documents`, `~/Desktop`, `~/Downloads`. They are Files Deny rules of the policy
`System` answers, not Always rules. A Deny is left out of the compiled rules until a Read or
Write reaches it (`Policy.rules`), so they cost nothing today. Under step 4D, a grant of `~`
leaves them shut, since the deepest rule wins, and a grant of a folder inside one opens that
folder (*Decisions*, 3).

**Homebrew's `var`** is a Deny of `System`'s policy on both platforms: `/opt/homebrew/var` and
`/usr/local/var` on macOS (`brewVar`, as today), and `/home/linuxbrew/.linuxbrew/var` on Linux.

### 2. `Never` at run time

`Sandbox.Never(home)` answers the `Never` list with `~/` expanded, plus two things it reads at
call time:

- **The other users' homes.** Each entry of `/home` on Linux and `/Users` on macOS, compared
  resolved, other than the user's home, `/home/linuxbrew` and `/Users/Shared`. A parent that
  cannot be listed adds nothing. No rule reaches another home today, since the home zone is
  closed and no `System` folder holds one, so these Deny rules bind only once a grant does.
- **`/root`.**
- **On Linux, the rootless container sockets**: `docker.sock` and `podman` in
  `/run/user/<uid>`, the user's runtime folder. Kstack's own runtime folder sits beside them,
  not inside.

It leaves out any path on or above the user's home, compared resolved, so a home of `/root`
does not deny itself. The parent of the other homes is a package variable, so a test can stand
one in.

If one of Kstack's directories lies inside a `Never` path (an `XDG_DATA_HOME` set inside
`~/.docker`, say), `Policy.Check` refuses every sandboxed run, since a run's own paths may not
lie inside the denied-always list. That is the intended failure: the run never reads the
credential, and the user's way out is the chat's switch.

**The listing runs once per run, and always under a bound.** A listing of `/home` can hang on a
network mount. `Never` is called on `sandboxedRunFor`'s policy goroutine, which already exists
for that reason (`bash.go`). `System` no longer calls `Never`: `systemFiles` drops what lies on
the lists' own `Never` paths (`neverPaths`), and no `System` rule reaches another home (§3). The
probe calls `Never` through `probePolicy`. macOS already builds that policy on a goroutine
abandoned when the probe's bound ends. Linux's `try` does the same, through a package variable
a test can replace, as `buildProfile` is on macOS.

### 3. `System`

`Sandbox.System(home, shell string) System` loses its `env` argument and reads no `PATH`. It
answers the policy and the environment the toolchain needs, from one look at the home:

```go
// System is what every sandboxed run starts from.
type System struct {
	Files FilePolicy // the rules below
	Env   []string   // each found location's Env, as NAME=value with ~ expanded
	Asdf  bool       // whether the asdf location was found
}
```

Its Read rules:

- the `System` list;
- each `Toolchain` folder that exists;
- the shell's folder. If that folder is named `bin`, lies outside the home, and its parent is
  not `/` and does not hold the home, the parent instead, so a shell installed under a prefix
  such as `/opt/zsh` reaches its own `share`. Under the home the folder is read alone, since a
  parent there can hold every app's data: `~/.local` holds `~/.local/share` and
  `~/.local/state`. `/bin/zsh` opens `/bin`, and a shell in `~/.local/bin`, `~/opt/zsh/bin` or
  `/home/bin` opens that `bin` alone. A shell's folder in another user's home, under the parent
  §2 lists, adds nothing;
- Kstack's own executable, at its resolved path.

Its Deny rules are Homebrew's `var` and `Closed`. As today it holds no Read on or inside a
`Never` path (`systemFiles`' `Outside`), and on Linux none `overFixedMount` refuses. With no
home it holds no `Toolchain` or `Closed` rule and no `Env`.

`System` stats folders under the home, so it runs where `Never` does: on the policy goroutine
in `sandboxedRunFor`, and in `probePolicy`. `sandboxedRunFor` builds the environment from its
answer on that goroutine too.

**What recreates a link.** On Linux, `mounter.mount` binds a Read rule at its resolved path, and
`mounter.link` recreates the path as written as a link to it (`sandbox_linux.go`). That covers a
toolchain folder that is itself a link: `~/.nix-profile` is bound, or found readable under
`/nix/store` already, and recreated as a link, so a `PATH` entry of `~/.nix-profile/bin` resolves
through it. `args` no longer adds `pathLinks`' links. On macOS, Seatbelt checks resolved paths and
the profile already lets a run `lstat` each rule's ancestors, so nothing changes there.

**Deleted:** `pathTrees`, `pathLinks`, `linkTargets`, `maxHops`, `homesOf`, `firstBelow`, the
package function `readable`, `inRoot`, `pathOf`, the `link` type, and macOS's
`sharedHomeDirs`. `mounter.readable` and `mounter.link` stay. `paths.go` keeps `resolvedAll`,
`inAny`, `within` and `resolved`, which the policy and both compilers use.

The `sandboxer` interface (`tools/bash/bash.go`) declares `System(home, shell string)
sandbox.System`. `probePolicy(shell, dir, home)` loses its `env` argument and uses the answer's
`Files`. `sandbox_windows.go`'s `System` answers the zero `System`.

### 4. The environment

`sandboxedRunEnv` in `tools/bash/env.go` builds the whole environment. It copies `PATH`, `LANG`
and `TZ` from the sidecar's, and nothing else. It takes `System`'s `Env` and the asdf versions
as arguments, so its test needs no real home:

| Variable | Value |
| --- | --- |
| `PATH` | the sidecar's, until step 3A replaces it with the frozen list |
| `HOME` | the workspace |
| `PWD` | the folder the command starts in |
| `TMPDIR` | the run's own |
| `ZDOTDIR` | the run's folder, so zsh sources no startup file |
| `KUBECONFIG`, `KUBECACHEDIR` | the run's own and the cluster's kubectl cache, with a cluster, as today |
| `LANG` | the sidecar's when set; else `en_US.UTF-8` on macOS, and on Linux `C.UTF-8` when `/usr/lib/locale/C.utf8` or `/usr/lib/locale/C.UTF-8` exists, else `C` |
| `TZ` | the sidecar's, when set |
| `TERM` | `dumb` |
| the tool home's variables (§6) | into the chat's tool home |
| `System`'s `Env` | each location found |
| `ASDF_<TOOL>_VERSION` | one per line of the user's `~/.tool-versions`, below |
| `KSTACK`, `KSTACK_SIDECAR_PID`, `KSTACK_HOST_PID` | as today |

**`LC_*` no longer passes.** A GUI app's `LC_*` is whatever launched it, and `LANG` alone is the
note's. `LC_ALL` set in a terminal Kstack was started from no longer overrides the run's locale.

**asdf's global versions.** asdf reads them from `$HOME/.tool-versions`, and `HOME` is the
workspace. When `System` answers `Asdf`, the sidecar reads the user's `~/.tool-versions`
(a plain file, at most 64 KiB) and sets `ASDF_<TOOL>_VERSION` for each line whose tool matches
`^[a-z0-9][a-z0-9_-]*$` and whose first version matches `^[A-Za-z0-9._+-]+$`. The tool name is
upper-cased with `-` spelled `_`, as asdf spells it. Any other line is skipped. The file is read
each run, on the policy goroutine, since it lies under the home. It is not readable in the
sandbox.

Step 4C adds `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`. Each is added to this table and to the
test of it.

### 5. What may never pass

The list lives in `sandbox/env.go`, as data, so the bash tool's builder, its test, and both
compilers read one list and the sandbox names no tool:

```go
// EnvRule matches an environment variable no sandboxed run may hold: by its
// name, or every name starting with Prefix but those in Except.
type EnvRule struct {
	Name   string
	Prefix string
	Except []string
}

// NeverEnv is the rules, a copy each call.
func NeverEnv() []EnvRule

// Unpassable reports whether name matches a rule.
func Unpassable(name string) bool
```

The rules: `SSH_AUTH_SOCK`, `GITHUB_TOKEN`, `GH_TOKEN`, `DOCKER_HOST`, `DYLD_LIBRARY_PATH`,
`DYLD_INSERT_LIBRARIES`, `DYLD_FRAMEWORK_PATH`, `LD_LIBRARY_PATH`, `LD_PRELOAD`, `LD_AUDIT`,
`BASH_ENV`, `ENV`, `PROMPT_COMMAND`, and the prefix `AWS_` with no exception. A later step that
sets one of these names adds it to the prefix's `Except` or drops the named rule, in its own
change, with its test. A step that adds a name, as 4C adds `ALL_PROXY`, adds a rule.

`Run.check` in `sandbox/sandbox.go` is `Policy.Check` plus a refusal of any `Env` entry that
`Unpassable` matches. Both platforms' `Command` call it in place of `r.Policy.Check()`, so no
later caller can pass one by hand. An exception cannot tell a placeholder from a real key: the
builder never copies a value from the sidecar's environment, and the excepting step's test pins
the value it sets.

### 6. The tool home, one per chat

The note's write policy points each tool that writes under `$HOME` elsewhere with its own
variable. Today those writes land in the workspace, which is per chat. This step gives them a
folder of their own beside it, the **tool home**, `tools.ToolHomePath(dir)` =
`<chat dir>/toolhome/`:

| Folder | Variables |
| --- | --- |
| `xdg/cache`, `xdg/config`, `xdg/data` | `XDG_CACHE_HOME`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME` |
| `helm/cache`, `helm/config`, `helm/data` | `HELM_CACHE_HOME`, `HELM_CONFIG_HOME`, `HELM_DATA_HOME` |
| `npm` | `NPM_CONFIG_CACHE` |
| `pip` | `PIP_CACHE_DIR` |
| `go/build`, `go/mod` | `GOCACHE`, `GOMODCACHE` |

`HELM_PLUGINS` points at the user's `~/.local/share/helm/plugins` (§1), since `HELM_DATA_HOME`
would otherwise hide it.

The bash tool makes the tool home and its folders before each run through an `os.Root` on the
chat's directory, as `tools.OpenWorkspace` makes the workspace. `rootdir.Open` takes one name,
so the folders are made one level at a time, each opened through the last, and a link a command
swaps in at any level is refused. The tool home is one Always Write rule of the run's
policy, beside the workspace, so it passes `Policy.Check`'s link and Kstack-directory checks. It
is removed with the chat's directory. A subagent shares it, as it shares the workspace.

The kubectl cache stays `<cache>/kubectl/<cluster id>/<server>/` (`kubectlcache.go`,
`bash.Paths.KubectlDir` set in `app/paths.go`), per cluster and swept at start once the cluster
is gone, as today.

### 7. No snapshot in the sandbox

`runCall` (`bash.go`) and `runTask` (`task.go`) call `snapshotFor` only when `sandboxerFor`
answers nil. A sandboxed run neither waits for it nor reads it. Its wrapper is
`wrapper(kind, "", command)`, and `sandboxedRunFor` loses its `snapshot` argument and the Read
rule for it.

`StartSnapshot` takes the snapshot at start only on a machine with no sandbox. On one with a
sandbox, it starts nothing and returns the same stop function. `snapshotFor` starts the
snapshot on the first run outside the sandbox, through a `sync.Once`, so it starts once. Every
run outside the sandbox waits for it as runs wait today, concurrent first runs included, since
each waits on the one `ready` channel the `Once` makes. The stop function cancels a snapshot
started this way and waits for its reap under the drain context, as it does for one started at
start; with none started it returns at once. So a machine where every chat stays sandboxed never
runs the user's startup files to snapshot them.

**A stop is final.** The snapshot runs under the context `StartSnapshot` made, which the stop
cancels, and the stop marks the tool stopped under the lock the `Once` start takes. A first run
outside the sandbox after the stop starts no shell: its `Once` closes `ready` with no snapshot,
and the run goes on without one, as a run does today when the snapshot was not taken. So no login
shell starts once the stop has returned. Step 3A's `PATH` resolution runs the login shell for its own
reason, and step 4A confines both.

The snapshot's `kill` and `pkill` shims are not needed in the sandbox: on Linux the run has its
own process namespace, and on macOS the profile lets a run signal only its own processes.

### 8. The prompt

`prompts/sandbox.md` says that the sandbox has the user's tools and none of their shell's
functions, aliases or variables; that `HOME` is the workspace; and that a tool which cannot find
its own files under the home needs a folder the user grants (step 4D), or the user can switch the
chat to run outside the sandbox. It never says the model can leave the sandbox.
`prompts/description.md`'s "the shell is initialized from the user's profile" is said of a
command outside the sandbox only.

## Decisions this step asks for

1. **The zones are Kstack's lists, not the user's `PATH`.** A startup file can put anything on
   `PATH`, so `PATH` choosing what the sandbox reads lets the user's shell widen it unseen.
2. **`/etc` is read whole, its secret files on `Never`** (the note's decision 12). The loader and
   libc read files the note's short list cannot name: `ld.so.cache`, `ld.so.conf.d`,
   `nsswitch.conf`, `/etc/ssl` whole.
3. **`~/Documents`, `~/Desktop` and `~/Downloads` are `Closed`, not `Never`.** The note lists them
   as denied always so that a broad grant cannot cover them by accident. A Files Deny does that:
   a grant of `~` leaves them shut. Unlike `Never`, it lets step 4D grant a project under
   `~/Documents`, where many macOS users keep code. A grant of the folder itself, such as
   `~/Documents`, ties with the Deny and the Deny wins, so a grant must name a folder inside it.
4. **The other homes are read from `/home` and `/Users` each run**, not globbed. A policy has no
   glob, and a new user's home appears without a restart.
5. **A `Never` path on or above the user's home is dropped**, so a home of `/root` works. A Kstack
   directory inside a `Never` path fails closed rather than dropping the Deny.
6. **The tool home is per chat, and only the kubectl cache is per cluster.** The note prefers a
   per-cluster cache for helm indexes and the like, to save a download. A shared config or build
   cache (`XDG_CONFIG_HOME`, `GOCACHE`, `GOMODCACHE`, helm's chart and index cache) lets one
   chat plant what a later chat executes. Today every such write lands in the per-chat workspace,
   so per chat costs nothing that works now.
7. **The snapshot is never sourced in the sandbox, and is taken only when needed.** The user's
   functions run in a sandboxed command only if the sandbox runs the user's startup files, which
   is what this sequence takes away.
8. **`LANG` is the sidecar's or a default, and `LC_*` goes.** The note names `LANG` alone.
9. **The loss of unlisted `PATH` entries is accepted until step 3A.** Without `pathTrees`, an
   entry that no list in §1 covers, under the home or outside it, and a program link into an
   unlisted folder, find nothing in the sandbox. Flatpak's `/var/lib/flatpak/exports/bin` is
   one. Without the snapshot, a Linux sandbox's `PATH` is the desktop session's, so the entries
   a `.bashrc` or `.zshrc` adds are gone too; macOS's is the login shell's already. §1's lists
   cover the common Kubernetes and toolchain folders. The chat's switch is the way out. Step 3A
   gives the sandbox the login shell's `PATH` and each frozen entry's own folder, and step 4D
   grants the rest. The loss is the main branch's alone: this step and step 3A reach users in
   the same release, so no release ships it.
10. **asdf's global versions ride as `ASDF_<TOOL>_VERSION`**, read from `~/.tool-versions` on the
    host, rather than a readable `~/.tool-versions` that `HOME` no longer points at.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The lists, shared and per platform: `System`'s additions, `Toolchain`, `Closed`, the `Never` additions, Linuxbrew's `var`; the `Lists` comment | `sandbox/lists.go`, `sandbox/lists_unix.go`, `sandbox/lists_darwin.go`, `sandbox/lists_linux.go`, their tests | — | Planned |
| 2 | `Never` at run time, listed once per run and under a bound; `System(home, shell)` and its answer; delete `pathTrees` and its helpers, and `pathLinks` from `args`; the new signatures in `probe.go`, `sandbox_windows.go` and the `sandboxer` interface; Linux's probe policy on a bounded goroutine; the existing tests below; regenerate the goldens | `sandbox/`, `sandbox/testdata/args_linux_*.golden`, `sandbox/testdata/profile_darwin_*.golden`, `tools/bash/bash.go`, their tests | 1 | Planned |
| 3 | `NeverEnv`, `Unpassable`, `Run.check` in both `Command`s; the environment table, built on the policy goroutine, the locale, asdf's versions | `sandbox/env.go`, `sandbox/sandbox.go`, `sandbox/sandbox_linux.go`, `sandbox/sandbox_darwin.go`, `tools/bash/bash.go`, `tools/bash/env.go`, their tests | 2 | Planned |
| 4 | The tool home | `tools/workspace.go`, `tools/bash/bash.go`, `tools/bash/env.go`, their tests | — | Planned |
| 5 | No snapshot in the sandbox, and the snapshot on first need through a `sync.Once` the stop function drains; the prompt | `tools/bash/bash.go`, `tools/bash/task.go`, `tools/bash/snapshot.go`, `tools/bash/prompts/`, their tests | 3 | Planned |
| 6 | Docs, per *When it lands* | see there | 1–5 | Planned |

**Order:** 1 and 4 at the same time, then 2, then 3, then 5, then 6.

**Existing tests.**

- **Deleted with `pathTrees`:**
  - every test in `paths_test.go`: `TestThePathTrees`,
    `TestAnEntryUnderASharedDirectoryIsNotWidened`, `TestATreeInsideAnotherIsDropped`,
    `TestThePathIsTheEnvironmentsLast` and `TestLocalItsShareAndConfigCountAsHomes`;
  - every test in `paths_unix_test.go` but `TestAMissingPathIsResolvedThroughItsDeepestFolder`,
    which tests `resolved` and stays;
  - in `sandbox_linux_test.go`, `TestALinkedLocalShareOpensOnlyTheToolsTree`,
    `TestAProgramLinkChainRuns`, `TestAProgramLinkThroughALinkedHomeRuns` and
    `TestAProgramLinkInARootRuns`, whose program links lead into folders no list names, which
    `TestAFolderOnThePathIsNotRead` now pins shut;
  - in `sandbox_darwin_test.go`, `TestAnEntryUnderASharedDirectoryOpensOnlyItself`.
- **Helpers.** `mkdirs` and `machine` move from `paths_test.go` to `testutil_test.go`, since
  other files use them, and `paths_test.go` goes.
- **Rewritten:**
  - `TestTheSandboxedEnvironmentIsBuiltWhole` (`env_unix_test.go`) becomes
    `TestTheSandboxedEnvironmentIsFixed`.
  - `TestTheCredentialsBesideAToolsProgramsAreListed` (`lists_test.go`) pins that each credential
    inside a `Toolchain` location's tree is on `Never`: `~/.local/share/uv/credentials`. The
    entries outside every location (`~/.cargo/credentials` and the rest) stay on `Never` as
    credentials.
  - `TestNoPathIsInTwoLists` covers the four lists.
  - `TestNeverWithNoHomeIsTheAbsolutePaths` stands in an empty parent for the other homes and
    checks containment, not an exact list or length, since the absolute entries grow.
  - `TestSystemLeavesOutTheNeverPaths` puts a stand-in `Toolchain` location inside `~/.docker`,
    not a `PATH` entry.
  - `TestSystemIsTheListsAndTheTrees` (both platforms) becomes `TestSystemReadsTheZones`.
  - `TestSystemLeavesOutTheFixedMounts` uses a shell in `/tmp` and under `/proc`, not `PATH`.
  - `TestAPathEntryThroughALinkRuns`: its `~/.nix-profile` half becomes
    `TestAToolchainLinkIsRecreated`, and its other half goes.
  - The real-sandbox tests that open a folder by putting it on `PATH` (`withPath` in
    `sandbox_linux_test.go`, `m.Env[0] = "PATH=…"` in `sandbox_darwin_test.go`) open it through a
    stand-in `Toolchain` or `System` entry instead: `TestKstacksDirectoriesAreUnreadableThroughALinkedHome`,
    `TestACredentialPathInsideAReadableTreeIsUnreadable` (both platforms),
    `TestALinkedCredentialPathIsUnreadable` and `TestHomebrewsVarIsDenied`. A test whose `PATH`
    names a folder a list already reads, such as `TestARootThatIsALinkRuns`, is kept.
  - Each `System(…, env)` call drops `env` and reads `.Files`: `probe_unix_test.go`,
    `policy_unix_test.go`, `golden_unix_test.go`, `sandbox_darwin_test.go` and
    `sandbox_linux_test.go`'s run helper.
- **Kept:** `TestEveryListPathIsAbsoluteOrInTheHome`, `TestADenyIsCompiledOnlyWhereAReadOrWriteReachesIt`,
  `TestTheDeniedAlwaysListWinsOverARead`.

**The seams.** With step 2B, both change the compiled arguments, the profile and the goldens
(`golden_unix_test.go`), and both change `workspacePolicy`. This step changes the Read and Deny
rules and drops `pathLinks`' links; 2B adds `Policy.Limits` and its arguments and profile lines.
Whichever lands second rebases onto the other and regenerates the goldens, keeping the first's
lines. If 2B has landed, `workspacePolicy` keeps its `Limits`. If this step has landed, 2B adds
to the policy as this step leaves it. With step 2C, both change `sandboxedRunFor`. This step
reads the workspace and the chat's directory from wherever the runtime holds them when it lands.

## Tests

**`sandbox`**

- `TestEachToolchainLocationRunsAProgram`: for each location, a stand-in home laid out as that
  tool lays it out, links and all, and a program in it that runs through the real sandbox with
  the location's `Env` set.
- `TestSystemReadsTheZones`: `System`'s `Files` is the platform's `System` list, each
  `Toolchain` folder that exists, the shell's folder, and the executable, with Homebrew's `var`
  and `Closed` as Deny; its `Env` is each found location's, and `Asdf` is set when asdf's is
  found. A missing location adds nothing and sets no variable.
- `TestWithNoHomeSystemHoldsNoHomeRule`: `System("", shell)` holds no `Toolchain` or `Closed`
  rule and no `Env`, and a probe policy built from it passes `Check`.
- `TestTheShellsPrefixIsReadButNeverTheHome`: a shell in a stand-in `<prefix>/zsh/bin` outside
  the home opens `<prefix>/zsh`; one in `<home>/.local/bin`, `<home>/opt/zsh/bin` or
  `<home>/bin` opens that `bin` alone; one in a stand-in `<parent>/bin`, where `<parent>` holds
  the home, opens that `bin` alone; `/bin/zsh` and `/bin/bash` open `/bin` and never `/`.
- `TestAFolderOnThePathIsNotRead`: with `<home>/notes/bin` on the sidecar's `PATH`,
  `<home>/notes` cannot be read.
- `TestAToolchainLinkIsRecreated` (Linux): a stand-in `~/.nix-profile` linked into a readable
  store, and a `PATH` entry through it, runs its program.
- `TestEtcSecretsStayHidden`: through the real sandbox, with a stand-in `etc` on `System` and
  its `ssh` folder and `shadow` file on `Never`, both world-readable on disk, neither can be
  read and the stand-in's `hosts` can. A second case reads the real `/etc/hosts`.
- `TestTheOtherHomesAreNever`: over a stand-in parent (§2's package variable) holding `a`, `b`,
  `linuxbrew` and the user's home, `Never` holds `a` and `b` alone, and `System` holds neither.
- `TestANeverPathAboveTheHomeIsDropped`: `Never("/root")` does not hold `/root`, and `Never`
  with a home under a stand-in `Never` folder does not hold that folder. `Never` reads nothing
  under the home, so no root is needed.
- `TestTheProbePolicyIsBounded` (Linux): with a probe policy that never answers, `try` answers
  its bound's error. The policy is built through a package variable, as `buildProfile` is on
  macOS.
- `TestAGrantOfTheHomeLeavesClosedFoldersShut`: a policy reading the home cannot read
  `~/Documents`, and one that also reads `~/Documents/project` reads it, through the real
  sandbox.
- `TestARunWithAnUnpassableVariableIsRefused`: `Command` answers an error for a `Run` whose
  `Env` holds `LD_PRELOAD`, and for one holding `AWS_SESSION_TOKEN`.
- `TestUnpassableMatchesNamesPrefixesAndExceptions`: over a table built for the test.
- `TestNoPathIsInTwoLists`, extended to the four lists.

**`bash`**

- `TestTheSandboxedEnvironmentIsFixed`: with the sidecar's environment holding every name
  `NeverEnv` matches and every `LC_*`, the run's environment is exactly §4's table, and
  `Unpassable` matches none of it. This is the note's environment invariant.
- `TestTheLocale`: the sidecar's `LANG` passes; with none, the platform default.
- `TestAsdfVersionsComeFromToolVersions`: valid lines become variables; a line with a bad name or
  version is skipped; with no asdf location found, none is set.
- `TestASandboxedRunSourcesNoSnapshot`: a function the snapshot defines is not there.
- `TestASandboxedRunDoesNotWaitForTheSnapshot`: with the snapshot never ready, a sandboxed call
  and a sandboxed background call both run.
- `TestTheSnapshotIsTakenOnTheFirstRunOutside`: on a machine with a sandbox, no snapshot is
  taken at start; two concurrent first runs outside the sandbox take it once and both wait for
  it.
- `TestTheStopReapsASnapshotStartedLate`: the stop function cancels a snapshot the first run
  outside started, and returns once it is reaped; with none started it returns at once.
- `TestNoSnapshotStartsAfterTheStop`: with the stop returned and no snapshot started, a first
  run outside the sandbox runs with none, and the fake launcher is never called.
- `TestTheEnvironmentIsBuiltOnThePolicyGoroutine`: over the fake sandboxer, with a `System`
  that never answers, a sandboxed call ends when its context does.
- `TestTheToolHomeIsPerChat`: two chats on one cluster get different tool homes; a run cannot
  write another chat's; a link planted at any level is refused.
- `TestHelmWritesItsCache`, through the real sandbox with `helm` where CI has it: `helm env`
  names the tool home, and a write there lands.

## Security

**Narrows.**

- A `PATH` entry opens nothing. Before this step, every entry under the home opened a tree, every
  entry outside the lists opened its folder, and a startup file chose the entries.
- Sandboxed commands source no snapshot, so no startup file runs inside the sandbox. On a machine
  with a sandbox, the user's startup files run only when a command first runs outside it.
- The environment is built from one table, and both compilers refuse what may never pass.
  `LC_*` no longer passes.
- `Never` gains the note's list, the container sockets, uv's credentials, `/root` and the other
  homes, so step 4D's broad grants cannot reach them.

**Widens.**

- The toolchain folders are readable whether or not `PATH` names them. `/snap` (Linux),
  `/nix/store` (macOS), `/nix/var/nix/profiles` and `/run/current-system` (both) join
  `System`.
- The tool home is writable. It is per chat, and today's writes already land in the per-chat
  workspace, so no chat reads what another wrote, except the kubectl cache, as today.

**What holds it.** `TestTheSandboxedEnvironmentIsFixed` and
`TestARunWithAnUnpassableVariableIsRefused` hold the environment; `TestAFolderOnThePathIsNotRead`,
`TestTheShellsPrefixIsReadButNeverTheHome`, `TestEtcSecretsStayHidden`, `TestTheOtherHomesAreNever` and
`TestAGrantOfTheHomeLeavesClosedFoldersShut` hold the files; `TestASandboxedRunSourcesNoSnapshot`
holds the snapshot; `TestTheToolHomeIsPerChat` holds the tool home.

**Residuals.**

- A toolchain folder can hold a secret Kstack does not know of. mise's `config.toml` may carry
  `[env]` values, and nvm's per-version global `npmrc` (`~/.nvm/versions/node/*/etc/npmrc`) may
  carry a registry token; a run reads both. Settings will show the list (step 7A).
- The kubectl discovery cache is shared by every chat on a cluster, as today: one chat can plant
  a discovery document a later chat's `kubectl` reads.
- An exception to the never-list cannot tell a placeholder from a real value. The builder never
  copies one, and each excepting step pins what it sets.
- A Kstack directory inside a `Never` path fails every sandboxed run. The chat's switch is the way
  out.
- Until step 3A, a `PATH` entry that §1 does not cover finds nothing, under the home or outside
  it, flatpak's `/var/lib/flatpak/exports/bin` among them.
- Until step 3A, a Linux sandbox's `PATH` is the desktop session's, without the entries a
  `.bashrc` or `.zshrc` adds. The chat's switch is the way out.

The record, `docs/security/<date>-the-sandboxs-own-environment.md`, argues this.

## When it lands

- **The security record** above.
- **An ADR**: the sandbox's zones are Kstack's lists, not the user's `PATH`; `Closed` beside
  `Never`; the tool home is per chat and the kubectl cache per cluster; the sandbox sources no
  snapshot, and the snapshot is taken on first need.
- **`security-model.md`**: the sandbox's rows, the environment row with its tests, the snapshot
  row, and a row for the tool home.
- **`sidecar/CLAUDE.md`**: the four lists, `Never` at run time, `System` and its answer, the
  environment table, `NeverEnv` and `Run.check`, the tool home, no snapshot in the sandbox.
- **The note's *Where this meets the code***: decisions 3, 6, 8, 9 and 10 above as decisions the
  specs added, and one more for the variables the environment holds beyond the note's
  pass-through list: `PWD`, `ZDOTDIR`, `KUBECACHEDIR`, the `KSTACK` variables, each toolchain
  location's `Env` and `ASDF_<TOOL>_VERSION`.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands).

By hand, for a person with a Mac and a Linux machine, a build of Kstack on each. On Linux, start
it from the desktop, not from a terminal: a terminal hands it the shell's `PATH`, which hides
the loss decision 9 accepts.

- With Homebrew's or the distribution's `kubectl`, a krew plugin, a pipx tool, an asdf-managed
  tool with a global version, and on macOS Rancher Desktop's or OrbStack's `kubectl`: a sandboxed
  `command -v kubectl kubectl-<plugin> <tool>` finds each, and the asdf tool runs at the global
  version. On Linux, an entry only a `.bashrc` adds is not found, and is found once the chat is
  switched outside the sandbox.
- Each `Toolchain` location installed on the machine runs a program in the sandbox; a location
  that does not is dropped from §1 and named in the residuals.
- `env` prints only §4's variables.
- With `~/notes/bin` on your shell's `PATH`, `ls ~/notes` fails. `ls ~/Documents` and
  `ls /home/<another user>` (or `/Users/<another user>`) fail.
- `helm repo add` then `helm repo update` writes under the chat's tool home, and a second chat
  does not see that repo.
- `type <a function from your profile>` finds nothing, and the log shows no snapshot taken until
  a chat switched outside the sandbox runs a command.
