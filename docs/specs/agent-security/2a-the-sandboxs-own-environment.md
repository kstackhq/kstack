---
title: The sandbox's own environment
scope: sidecar
status: Planned
---

# The sandbox's own environment

**Needs:** step 1A, whose `Policy` and `Lists` this step extends. **Unblocks:** steps 3A and
4A.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a sandboxed run takes three things from the sidecar's own environment: `PATH`, which also
decides which folders under the home the sandbox can read (`pathTrees` opens the folder of every
`PATH` entry), the locale, and the shell snapshot, which every sandboxed command sources first,
so the user's functions and aliases run inside the sandbox.

After this step:

- **The three zones are lists.** `Lists` says what every run reads by default (`System`), which
  folders under the home hold the user's tools (`Toolchain`), and what no run reads whatever
  else is granted (`Never`), as the note tables them. A `PATH` entry opens nothing by itself:
  step 3A decides which entries the sandbox gets, and this step reads the toolchain folders
  whether or not `PATH` names them.
- **The environment is built from the note's list and nothing else.** A test pins that the
  never-list stays out, so a later pass-through cannot let a credential or a loader variable in.
- **Tools that write under the home write a cache Kstack owns**, one per cluster, through their
  own variables. `HOME` is the workspace, so the rest lands there.
- **Sandboxed commands do not source the snapshot.** The user's shell shapes nothing inside the
  sandbox.

Commands outside the sandbox are unchanged: they run as the user, with the user's environment
and the snapshot.

## What is not in this step

- **`PATH` itself.** Until step 3A the sandbox's `PATH` is still the sidecar's, unfiltered, as
  today. This step only stops `PATH` from opening folders.
- **No user folders.** Step 4D adds grants.
- **No change on Windows**, which has no sandbox.

## Design

### 1. The zones as lists

`Lists` (`sandbox/lists.go`) gains `Toolchain`, and the note's zones fill the three:

```go
type Lists struct {
	System    []string   // readable by default
	Toolchain []Location // readable under the home by default, when present
	Never     []string   // denied always
}

// Location is a folder the user's tools live in. A path starting with ~/ is
// under the user's home. Env is set only when the folder exists.
type Location struct {
	Name string            // shown in Settings, like "asdf"
	Read []string          // folders every run can read
	Env  map[string]string // variables that point the tool at them, since HOME is the workspace
}
```

**`System`**, the note's first table. Shared: `/usr`, `/bin`, `/sbin`, `/opt`, `/usr/local`,
`/nix/store`, `/etc`, `/dev/null`, `/dev/urandom`, `/dev/zero`. macOS: `/System`, `/Library`,
`/Applications`, `/private/etc`, `/private/var/db/timezone`, and the dyld cache paths. Linux:
`/lib`, `/lib32`, `/lib64`, `/libx32`, `/snap`, `/home/linuxbrew/.linuxbrew`, `/proc/self`,
`/proc/sys/kernel/random`. These are today's roots plus the note's additions; nothing today's
roots open is closed.

`/etc` is read whole, with `/etc/ssh`, `/etc/sudoers`, `/etc/sudoers.d`, `/etc/shadow`,
`/etc/gshadow`, `/etc/krb5.conf`, `/etc/krb5.keytab` and, on macOS, `/etc/master.passwd` and
`/private/etc/` twins on `Never`. The note narrows `/etc` to a few named files; that list cannot
name what the loader and libc read (`ld.so.cache`, `ld.so.conf.d`, `nsswitch.conf`, `profile`
for a login shell, `/etc/ssl` and `/etc/ca-certificates` whole), so this step keeps `/etc` open
and closes the files that hold secrets. That is the documented deviation the note allows.

**`Toolchain`**, the note's second table, each with the variable that points its tool at it:

| Name | Read | Env |
| --- | --- | --- |
| user binaries | `~/.local/bin`, `~/bin`, `~/go/bin`, `~/.cargo/bin` | — |
| asdf | `~/.asdf` | `ASDF_DATA_DIR`, `ASDF_DIR` |
| mise | `~/.local/share/mise`, `~/.config/mise` | `MISE_DATA_DIR`, `MISE_CONFIG_DIR` |
| nvm | `~/.nvm` | `NVM_DIR` |
| pyenv | `~/.pyenv` | `PYENV_ROOT` |
| rbenv | `~/.rbenv` | `RBENV_ROOT` |
| volta | `~/.volta` | `VOLTA_HOME` |
| pipx and uv | `~/.local/share/pipx`, `~/.local/share/uv` | — |
| krew | `~/.krew` | `KREW_ROOT` |
| helm plugins | `~/.local/share/helm/plugins` | `HELM_PLUGINS` |
| Google Cloud SDK | `~/google-cloud-sdk` | — |
| Nix | `~/.nix-profile` | — |

A location is a Read rule only when its folder exists, and its `Env` is set only then. Task 1
checks each on a real Mac and a real Linux machine and drops one whose tool does not run.

**`Never`**, the note's third table, on top of today's: `~/.helm`, `~/.terraform.d`,
`~/.npmrc`, `~/.pypirc`, `~/.gem/credentials`, `~/.config/git/credentials`, every `~/.*_history`
and `~/.bash_history`, `~/Documents`, `~/Desktop`, `~/Downloads`, `/run/containerd`,
`/var/run/docker.sock`, `~/.docker/run/docker.sock`, `/root`, and the other users' homes
(`/Users/*` and `/home/*` but the user's own, resolved at run time). macOS adds
`~/Library/Cookies`, `~/Library/Application Support/Google/Chrome`, `~/Library/Application
Support/Firefox`, `~/Library/Safari`; Linux adds `~/.config/google-chrome`, `~/.config/chromium`,
`~/.mozilla`. A path on `Never` that does not exist is a Deny that covers nothing.

`Sandbox.System(home)` loses its `env` argument and no longer reads `PATH`: its Read
is `System`, each `Toolchain` folder that exists, and Kstack's own executable; its Deny is
Homebrew's `var` on either platform. `Sandbox.Never(home)` is unchanged. A new
`Sandbox.ToolchainEnv(home)` answers the `Env` of each location found.

**Deleted:** `pathTrees`, `pathLinks`, `linkTargets`, `homesOf`, `firstBelow`, `readable`,
`inRoot`, `pathOf` and macOS's `sharedHomeDirs`, with their tests. On Linux `rootArgs` keeps
recreating the links a system folder is (`/bin` on a merged `/usr`), and `pathLinks`' one job,
`~/.nix-profile/bin` reached as written, is done by `~/.nix-profile` being a Read rule whose
target is under `/nix/store`, also readable.

### 2. The environment

`sandboxedRunEnv` in `tools/bash/env.go` builds the whole environment and copies nothing from the
sidecar's but `PATH`, until step 3A, and `TZ` when set:

| Variable | Value |
| --- | --- |
| `PATH` | the sidecar's, until step 3A replaces it with the frozen list |
| `HOME` | the workspace |
| `PWD` | the folder the command starts in |
| `TMPDIR` | the run's own |
| `ZDOTDIR` | the run's folder, so zsh sources no startup file |
| `KUBECONFIG` | the run's own, with a cluster, as today |
| `LANG` | `C.UTF-8` on Linux, `en_US.UTF-8` on macOS |
| `TZ` | the sidecar's, when set |
| `TERM` | `dumb` |
| the cache redirects (§3) | into the run's tool cache |
| each toolchain location's `Env` | the value in §1's table, `~` expanded to the user's home |
| `KSTACK`, `KSTACK_SIDECAR_PID`, `KSTACK_HOST_PID` | as today |

Step 4C adds `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`; step 5D the CA bundle variables; step 5C
`AWS_ENDPOINT_URL` and the placeholder keys; steps 6B and 6C each tool's own. This step's test of
the list is where each is added.

**The never-list.** `neverPassed` in `env.go` is the note's list: `SSH_AUTH_SOCK`,
`GITHUB_TOKEN`, `GH_TOKEN`, `AWS_PROFILE`, every `AWS_*` but the ones step 5C sets,
`DOCKER_HOST`, `DYLD_LIBRARY_PATH`, `DYLD_INSERT_LIBRARIES`, `DYLD_FRAMEWORK_PATH`,
`LD_LIBRARY_PATH`, `LD_PRELOAD`, `LD_AUDIT`, `BASH_ENV`, `ENV`, `PROMPT_COMMAND`. It exists for
one test: with the sidecar's environment holding every name on it, none reaches the run. The
sandbox's compilers also refuse a `Run` whose `Env` holds one, so no later caller can pass one
by hand.

Task 2 checks that each `LANG` value exists on the platform (`locale -a`).

### 3. The tool cache, one per cluster

The note's write policy: a tool that insists on writing under `$HOME` is pointed elsewhere with
its own variable, and the cache persists per cluster so sessions do not re-download. Today's
kubectl cache, `<cache>/kubectl/<id>/<server>`, becomes one folder of a **tool cache**,
`<cache>/tools/<id>/<server>/`, owned as `kubectlcache.go` owns it today (level by level through
`rootdir.Open`, swept at start once the cluster is gone, `serverKey` unchanged):

| Folder | Variables |
| --- | --- |
| `kubectl` | `KUBECACHEDIR` |
| `helm/cache`, `helm/config`, `helm/data` | `HELM_CACHE_HOME`, `HELM_CONFIG_HOME`, `HELM_DATA_HOME` |
| `xdg/cache`, `xdg/config`, `xdg/data` | `XDG_CACHE_HOME`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME` |
| `npm` | `NPM_CONFIG_CACHE` |
| `pip` | `PIP_CACHE_DIR` |
| `go/build`, `go/mod` | `GOCACHE`, `GOMODCACHE` |

`HELM_PLUGINS` points at the user's `~/.local/share/helm/plugins` (§1), since `HELM_DATA_HOME`
would otherwise hide them. The whole tool cache is one Always Write rule of the run's policy. A
run with no cluster gets the same folders under its `TMPDIR`, so the variables are always set.

The file `kubectlcache.go` becomes `toolcache.go`, and `Paths.KubectlDir` becomes
`Paths.ToolsDir`.

### 4. No snapshot in the sandbox

A sandboxed command's wrapper sources no snapshot: `wrapper(kind, "", command)`. The snapshot
leaves the Workspace policy's Always Read rules. The snapshot's `kill` and `pkill` shims are not
needed there: on Linux the run has its own process namespace, and on macOS the profile lets a
run signal only its own processes.

### 5. The prompt

`prompts/sandbox.md` says the sandbox has the user's tools and none of their shell's functions,
aliases or variables, that `HOME` is the workspace, and that a tool that cannot find its own
files under the home is one the user can grant a folder for (step 4D) or run outside the sandbox
(step 1B). `prompts/description.md` says the shell is initialized from the user's profile only
outside the sandbox.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The three lists, shared and per platform; check each toolchain entry on macOS and Linux | `sandbox/lists.go`, `sandbox/lists_darwin.go`, `sandbox/lists_linux.go`, their tests | — | Planned |
| 2 | `System(home)`, `ToolchainEnv`; delete `pathTrees` and its helpers; the environment and the never-list | `sandbox/`, `tools/bash/env.go`, their tests | 1 | Planned |
| 3 | The tool cache | `tools/bash/toolcache.go`, `tools/bash/bash.go`, `app/paths.go`, their tests | — | Planned |
| 4 | No snapshot in the sandbox; the prompt | `tools/bash/bash.go`, `tools/bash/prompts/`, their tests | 2 | Planned |
| 5 | Docs, per *When it lands* | see there | 1–4 | Planned |

**Order:** 1 and 3 at the same time, then 2, then 4, then 5.

## Tests

**`sandbox`**

- `TestEachToolchainLocationRunsAProgram`: for each location, a stand-in home laid out as that
  tool lays it out (links and all), and a program in it that runs through the real sandbox with
  the location's `Env` set.
- `TestSystemReadsTheZones`: `System` is the platform's `System` list, each `Toolchain` folder
  that exists, and the executable; a missing location adds nothing and sets no variable.
- `TestNoPathIsInTwoLists`, extended to the three lists.
- `TestAFolderOnTheUsersPathIsNotRead`: with `~/Documents/bin` on the sidecar's `PATH`,
  `~/Documents` cannot be read.
- `TestEtcSecretsStayHidden`: `/etc/ssh` and `/etc/sudoers` cannot be read, and `/etc/hosts` can.
- `TestARunWithANeverVariableIsRefused`: `Command` answers an error for a `Run` whose `Env` holds
  `LD_PRELOAD`.

**`bash`**

- `TestTheSandboxedEnvironmentIsFixed`: with the sidecar's environment full of variables, the
  run's environment is exactly the table in §2 plus the cache redirects, and none of
  `neverPassed` is in it. This is the note's third invariant.
- `TestASandboxedRunSourcesNoSnapshot`: a function the snapshot defines is not there.
- `TestTheToolCacheIsPerCluster`: two chats on one cluster share it, another cluster's differs,
  a run cannot write another cluster's, and a run with no cluster writes under its `TMPDIR`.
- `TestHelmWritesItsCache`, through the real sandbox with `helm` where CI has it: `helm env`
  names the cache, and a write there lands.

## Security

This step narrows what the sandbox reads by default: before it, every folder on the user's
`PATH` opened a folder in the home, and a startup file could choose which; after it, the sandbox
reads the listed zones and nothing the user's shell does changes that. It widens two things: the
toolchain folders are readable whether or not `PATH` names them, and the tool cache is writable,
which later runs on the same cluster read, as the kubectl cache is today. Both are named in the
record. The environment's never-list is the note's third invariant, pinned by one test.

The record, `docs/security/<date>-the-sandboxs-own-environment.md`, argues this.

## When it lands

- **The security record** above.
- **An ADR**: the sandbox's zones are Kstack's lists, not the user's `PATH`; a tool's cache is
  per cluster and Kstack's; the sandbox sources no snapshot.
- **`security-model.md`**: the sandbox's rows, the environment row with its test, and a row for
  the tool cache.
- **`sidecar/CLAUDE.md`**: the three lists, `System` and `ToolchainEnv`, the environment table,
  the never-list, the tool cache, no snapshot in the sandbox.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands).

By hand, `pnpm tauri dev` on macOS and on Linux, on a machine with Homebrew's `kubectl`, a krew
plugin and a pipx tool: a sandboxed `command -v kubectl kubectl-<plugin> <tool>` finds each;
`env` prints only §2's variables; `ls ~/Documents` fails even with `~/Documents/bin` on your
shell's `PATH`; `helm repo add` then `helm repo update` twice reads its index the second time
from the cache; `type <a function from your profile>` finds nothing.
