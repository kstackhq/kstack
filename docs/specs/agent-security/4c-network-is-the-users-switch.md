---
title: Network is the user's switch
scope: sidecar, webview
status: Planned
---

# Network is the user's switch

**Needs:** step 2B, whose forwarder is every run's first process; step 2C, whose session carries
the switch to every tool and to a subagent; step 3B, whose cluster proxy stays the one way to the
cluster with a credential. It follows step 1B's switch end to end: the column, the mutation, the
send's check and its refusal, the composer's switch and the context block's section.
**Unblocks:** step 6B.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a sandboxed command reaches one thing past its loopback: the cluster proxy, over the run's
socket, when the chat has a cluster. `helm repo update`, `gh`, `curl` and `git clone` all fail
there, and the user's only way out is to switch the chat outside the sandbox (step 1B).

After this step, on macOS and Linux, the user can give a sandboxed command the internet:

- **For the chat**: a switch beside the sandbox switch in the composer, off by default. Turning it
  on opens a confirm dialog; turning it off does not.
- **For one message**: a toggle in the composer, *Network for this message*. Every command of
  that answer has network, its subagents' included, and the toggle resets after the send.
- **For one command**: Bash takes a `network` argument. With network off for the chat and the
  turn, a call that sets it asks: *Run this command with network access?* The request shows the
  command, as every request does, and the user approves that command once.

A command has network when any of the three says so. "Network" is the internet with nothing
filtered by host, minus the machine's own loopback. Every other part of the sandbox holds: the
command holds no credential, the denied-always list stays shut, writes stay in the workspace and
the granted folders, and the cluster is still reached with a credential only through the cluster
proxy, which classifies every write.

Each call records whether it had network and why, and its disclosure in the transcript says
`with network`.

## What is not in this step

- **No per-host allowlist.** A command with network reaches every host. The proxy-based allowlist
  needed the trust daemon refused on macOS, which breaks TLS for every Go tool, or allowed, which
  let any command send data around the proxy (§5). A host allowlist can return later as a third
  setting for proxy-aware tools; nothing here blocks it.
- **No network for a command outside the sandbox to grant.** A chat switched outside the sandbox
  (step 1B) already runs every command with the machine's network, each asking. Its network switch
  and toggle are hidden, and a call's `network` argument changes nothing.
- **No monitor.** Step 6B's session has no network, ever; this step makes a session with no
  `Network` function read none.
- **Nothing on Windows**: commands there run outside the sandbox and ask.

## Design

### 1. The chat's switch

`chats` in `appdb/migrations/0001_init.sql`, under the
[pre-release schema policy](../../adr/2026-08-29-schema-edit-not-migration.md):

```sql
network_enabled INTEGER NOT NULL DEFAULT 0 CHECK (network_enabled IN (0, 1)),
```

It is built as `sandbox_disabled` is: `Chat.NetworkEnabled` in `chatsvc/record.go`, read by
`chatColumns` and `scanChat`, written by `setNetworkEnabled` through a new
`stmtSetNetworkEnabled`, and `service.SetNetworkEnabled` beside `SetSandboxDisabled`.

```graphql
type Chat {
  # as landed, then:
  "The user's switch: the chat's sandboxed commands reach the internet. False for a new chat."
  networkEnabled: Boolean!
}

extend type Mutation {
  chatNetworkEnabledSet(id: ChatID!, enabled: Boolean!): Chat!
}
```

**The send says what its sender saw.** `chatSend` gains two arguments:

```graphql
chatSend(
  # as landed, then:
  "The chat's network switch as the sender saw it; false for a chat the send creates. A send whose switch differs from the chat's is refused with KSTACK_CHAT_NETWORK_CHANGED, and a replay ignores it."
  networkEnabled: Boolean!
  "Network for every command of this turn alone, its subagents' included."
  networkThisTurn: Boolean!
): ChatMessage!
```

`checkChat` answers the network switch beside the sandbox switch, inside the transaction that
reserves the turn, and a mismatch is `ErrChatNetworkChanged`, mapped in `graph/util.go` to the new
`ErrChatNetworkChanged` (`KSTACK_CHAT_NETWORK_CHANGED`, *Chat's network switch changed*). The turn
pins `networkThisTurn` beside `outsideSandbox` (`chatsvc/turn.go`).

**Where network is unavailable (§4) nothing turns it on.** `chatNetworkEnabledSet(id, true)` and a
send with `networkThisTurn` set are refused with `KSTACK_VALIDATION_ERROR`, as
`chatSandboxDisabledSet` is on a machine with no sandbox; turning the switch off is always
accepted. A switch turned on while network was available stays on, and §3 gives its calls none.

### 2. The session

`session.Session` gains one field:

```go
// Network is why a sandboxed command starting now reaches the internet:
// "chat" for the chat's switch, read live, "turn" for the turn's toggle, ""
// for neither. Nil is a session that never has it.
Network func(context.Context) string
```

`sessionFor(chatID, outside, thisTurn)` sets it: `"chat"` while the chat's `network_enabled` is
set, read at each call, else `"turn"` when `thisTurn`, else `""`. A read that fails answers as an
unset switch, so a broken read gives no network. It is the one builder; a session made any other
way has a nil `Network`. `Narrow` copies it, so a subagent spawned in a turn with the toggle keeps
the toggle for its whole life, as its background commands do. A switch turned off mid-turn
applies to the next command, and so does one turned on.

### 3. The call

Bash's `input` gains `Network bool`, the call's `network` key, parsed as `run_in_background` is,
and `schema.json` and `prompts/description.md` say what it is for: a command that needs the
internet, in a chat whose question says it has none.

`tools.Approval` gains one field:

```go
// Network is why a sandboxed call reaches the internet: "chat", "turn" or
// "approved"; "" for none. "approved" holds only once the user approves.
Network string
```

`Bash.Approval` decides it, for a sandboxed call alone:

| The session's `Network` | `in.Network` | Approval |
| --- | --- | --- |
| any, on a machine where network is unavailable (§4) | false | `Skip`, `Network: ""`: the call runs with none |
| any, on a machine where network is unavailable (§4) | true | a `*tools.Refusal`: *This machine cannot give a sandboxed command the internet: <reason>.* |
| nil (a monitor) | false | `Skip`, `Network: ""` |
| nil (a monitor) | true | a `*tools.Refusal`: *This session never has network.* |
| `"chat"` | either | `Skip`, `Network: "chat"` |
| `"turn"` | either | `Skip`, `Network: "turn"` |
| `""` | false | `Skip`, `Network: ""` |
| `""` | true | asks, `Network: "approved"`, which holds once the user approves |

The first matching row decides, so a monitor's `network: true` call on a machine with no network
reads the machine's refusal, not the monitor's. Bash reads whether network is available from its
sandbox (`Sandbox.NetworkStatus`, §4) before it reads the session, so a chat whose switch was turned
on while `pasta` was there keeps running its commands, without network, once it is gone. A call
outside the sandbox records `""`: its network is not this switch's.

**The run takes the approval.** `Run` must start the command with the network its approval
decided, never read the session again: a switch turned off between the two would otherwise run an
approved call with none, and one turned on would give an unasked call network nobody saw. So Bash
implements `tools.ApprovedRunner` (`RunApproved(ctx, rt, input, a Approval)`), which the agent loop
calls with the gate's `Approval`. **Step 4D adds the same interface**: whichever of the two lands
first adds it to `tools/tool.go` and `agent/run.go`, and the other implements it.
`sandboxedRunFor` sets `Policy.Network.Internet` from `a.Network != ""`.

**The record.** `tool_calls` gains:

```sql
network TEXT CHECK (network IN ('chat', 'turn', 'approved')),
```

the network the call ran with, NULL for none. It is written on the row that marks the call
running, with `started_at`, and NULL before it: a call's first row can be written while it waits
on the user, before anyone knows it will run, so a call that asked and was denied, or never
answered, keeps NULL. Two changes make that so: `ToolCallStarted` sets the row's network from
its `approval` on both paths, the row `Approve` opened included, since today it reads the approval
only for a row it mints; and `stmtUpsertToolCall`'s `DO UPDATE SET` gains `network`, since it
updates neither `cwd` nor `sandboxed` and would keep the waiting row's NULL. On the wire:

```graphql
"Why a sandboxed call reached the internet."
enum ToolCallNetwork { Chat Turn Approved }

type ToolCall {
  # as landed, then:
  "Null when the call had no network of its own: none, or a call outside the sandbox."
  network: ToolCallNetwork
}

type CommandAction {
  # as landed, then:
  "Whether the call asked for the internet (its `network` argument). Outside the sandbox it changes nothing."
  network: Boolean!
}
```

### 4. The policy

`sandbox.NetworkPolicy` gains one field:

```go
// Internet lets the run reach the internet. The host's loopback stays shut.
Internet bool
// Resolver is a resolv.conf the caller wrote for a run with Internet, bound
// over the system's on Linux; "" on macOS and for a run without Internet.
Resolver string
```

The cluster relay is unchanged and still the one relay: a run with the cluster and the internet
has both. `Policy.Check` accepts any combination of the relay and `Internet`, and refuses a
`Resolver` without `Internet`, and one that is not absolute, is a link, or lies outside Kstack's
directories, as it refuses a run's own path. Linux's `Command` refuses `Internet` with no
`Resolver`, and macOS's a `Resolver`.

**macOS.** With `Internet`, `profile` fills `;; NETWORK` with, beside any relay rules:

```scheme
(allow network-outbound (remote ip "*:*"))
(deny network-outbound (remote ip "localhost:*"))
(allow network-outbound (literal "/private/var/run/mDNSResponder"))
(allow mach-lookup
  (global-name "com.apple.dnssd.service")
  (global-name "com.apple.trustd.agent"))
```

The deny follows the allow, since Seatbelt reads a later rule over an earlier one. The relay's
`localhost:<port>` allow comes after both, so the cluster stays reachable. Seatbelt's `remote ip`
names `localhost` or `*` and no other host, so the local network cannot be refused (Security).
`refusedServices` keeps both services: a run without `Internet` still refuses them, and
`TestTheProfileNamesNoRefusedService` gains the one exception, a profile with `Internet`.

**Linux.** The run keeps a network namespace of its own; with `Internet`, `pasta` (from passt)
makes it and gives it the internet, and bwrap runs inside it without `--unshare-net`:

```
pasta --config-net --quiet --no-map-gw -t none -u none -T none -U none \
      --dns-forward 169.254.1.53 -- \
      bwrap --unshare-user --uid <uid> --gid <gid> --cap-drop ALL \
            <the run's arguments, less --unshare-net and --unshare-user-try> ...
```

- `pasta` with a command makes a new user and network namespace and runs the command in it,
  as uid 0 of that namespace.
- **The run holds no capability.** bwrap started as uid 0 keeps every capability by default,
  and in pasta's namespace that would let the command unmount the tmpfs over `~/.ssh` and read
  what lies under it. So bwrap makes a user namespace of its own (`--unshare-user`), runs the
  command as the user's own uid and gid there, and drops every capability (`--cap-drop ALL`):
  the command is the user, holding nothing, as in a run without `Internet`.
  `TestANetworkRunHoldsNoCapability` and `TestANetworkRunCannotUnmountANeverPath` pin it.
- **No run mounts anything.** `sandbox-shell`'s seccomp filter refuses `mount`, `umount2`,
  `pivot_root`, `open_tree`, `move_mount`, `fsopen`, `fsconfig`, `fsmount` and `mount_setattr`
  on every run, with or without `Internet`, so a capability that slipped through still unmounts
  nothing. bwrap has made every mount before the filter is installed.
- **The limits hold under pasta.** Step 2B's process count is counted against the run's user
  namespace on Linux 5.14 and later. The probe's `pasta` run records its own answer for
  `ownUserNS`, which a run with `Internet` reads, and 2B's limit tests gain an `Internet` case.
- `--no-map-gw` maps no address to the host's loopback; `-T none -U none` forward no port from the
  namespace to the host's loopback; `-t none -u none` forward none inward.
- `--dns-forward 169.254.1.53` sends queries to that address on to the host's first resolver,
  so a host whose resolver is on loopback (`127.0.0.53`) still resolves.
- **The run's `resolv.conf`.** The system's may name a loopback resolver the run cannot reach,
  so on Linux Bash's `sandboxedRunFor` writes `resolv.conf`, `nameserver 169.254.1.53`, into the
  run's directory and names it as `Policy.Network.Resolver`; on macOS it writes none and leaves
  `Resolver` empty. Which one is the sandboxer's answer (`NeedsResolver`), never a `GOOS` check in
  `bash`. The compiler binds it read-only
  (`--ro-bind`) at `/etc/resolv.conf` as resolved on the host when the run is built: on a
  systemd-resolved host the link to `/run/systemd/resolve/stub-resolv.conf` is followed and the
  bind lies over that file. The file exists on the host, so the bind lands whether a Read rule
  already put it in the run or bwrap makes its parents in the run's own root, and the link the
  run reads at `/etc/resolv.conf` leads to it. The bind comes after the policy's rules and the
  fixed mounts, so it lies over whichever of them holds the path, and before the closing
  `--remount-ro`. A host with no `/etc/resolv.conf` gets the bind at `/etc/resolv.conf` itself.
  A resolved path under a mount every run has, or under a Deny, refuses the run with that
  reason.
- **pasta copies the host's address into the namespace**: the address of the interface holding
  the default route. A connection to that address stays in the namespace. A host service on any
  other address the host holds — a Docker bridge, a second interface — is reachable, as the
  internet is.
- The namespace's own loopback is the run's, as today: the forwarder's relay port is there.
- **Stop and the timeout reach the whole run.** With a command, pasta stays in the foreground, in
  the process group `shellCmd` starts, and bwrap joins it. The group's SIGTERM or SIGKILL ends
  pasta, bwrap and the command, and pasta killed alone takes bwrap and the command with it. A group
  SIGTERM leaves pasta exiting 0 rather than by the signal, so the call's stop is the guard's
  record, as it already is. That 0 is pasta's, not the command's, so `headerOf` leaves the exit code
  out of a stopped network run's header: *Command timed out after 30s*, *Command cancelled*.
  Measured with passt 2026-01-20 on Linux 7.0; `TestStopEndsANetworkRun`,
  `TestATimeoutEndsANetworkRun` and `TestKillingPastaEndsTheRun` pin it.
- **An offline run fails, never runs quietly without network.** With no route, pasta either
  refuses to start, and the run fails before its command starts with pasta's message as its
  output, or starts with no route, and the command's first connection fails with its own error.
  Either way the user reads why. Which one a passt version does is not relied on.
- **The invocation is confirmed on CI** (`TestTheInternetReachesNoHostLoopback`,
  `TestTheInternetResolves`, `TestANetworkRunHoldsNoCapability`, `TestANetworkRunKeepsTheExitCode`,
  `TestTheProbeNeedsNoRoute` and the three stop tests), across the passt versions the CI images
  carry; a flag a version lacks, or an exit code pasta does not pass through, is a finding for this
  spec, not a quiet fallback. The images include Ubuntu 24.04 with its default AppArmor restriction
  on unprivileged user namespaces, which may refuse bwrap's namespace inside pasta's; there the
  probe answers network unavailable, with the reason.

`pasta` is found as bwrap is (`systemPastas`: `/usr/bin/pasta`, `/bin/pasta`,
`/usr/local/bin/pasta`, `/run/current-system/sw/bin/pasta`, never off `PATH`), by the probe,
which runs it once as it runs bwrap. seccomp's socket rules are unchanged: they allow `AF_INET`
and `AF_INET6` already, and the namespace is what fenced them.

**The probe needs no route, and is bounded.** It runs once, at startup, under `probeBound` (5
seconds) as each bwrap's try is, so it adds at most that to the time before the sidecar reports
ready, after bwrap's own tries; a probe that runs out answers network unavailable, with the
reason. It runs once, and its answer holds for the sidecar's
life, so it checks only what holds for that long: that `pasta` is there, makes its namespace,
and runs bwrap with the user's ids and no capability inside it. It passes every flag a run
passes but `--config-net`, so a passt version lacking one fails the probe rather than every run,
and it neither reads nor needs the host's routes, so a Kstack started offline, or at login before
the network is up, still offers network once the machine is online. `TestTheProbeNeedsNoRoute`
runs the probe under `unshare -rn`, a namespace with loopback alone, and it passes; a passt
version that refuses that is a finding for this spec.

**Where network-on is unavailable.** `SandboxStatus` gains:

```graphql
type SandboxStatus {
  # as landed, then:
  "Whether a sandboxed command can be given the internet on this machine."
  networkAvailable: Boolean!
  "Why not, in the sidecar's words; empty when it can."
  networkReason: String!
}
```

On macOS it is true wherever the sandbox is. On Linux it is false without a `pasta` that passes the
probe, with the reason (*pasta not found*).

**The answer rides `sandbox.Status`.** It gains `NetworkAvailable` and `NetworkReason`, filled by
`Probe`, and every reader already holds a `Status`: `app` hands it to `chatsvc.New`, which reads it
for §1's refusals and §7's `unavailable on this machine`, and to `graph.Resolver`, whose `sandbox`
query returns it as `SandboxStatus`. `sandboxStatusOf` in `app/app.go` clears both when it reports
the sandbox unavailable (a sandbox with no shell), so no reader sees network offered where commands
are not sandboxed. Bash holds the sandbox rather than the status, so `Sandbox.NetworkStatus()`
answers the same pair; the `sandboxer` interface gains it and `NeedsResolver`. The switch and the
toggle are then disabled with the reason under them, their mutation and the send refuse to turn
network on (§1), a switch already on gives its calls none, and a `network: true` call is refused
(§3).

### 5. Why the trust daemon follows the switch

Go programs and Security.framework verify a certificate through `com.apple.trustd.agent`. The
experiment on macOS 27.0.1 measured what allowing it does: with the service allowed and no
network, a sandboxed program that verifies a certificate it built makes the daemon fetch the
certificate's AIA URL, and with revocation asked for, its OCSP URL, from outside the sandbox — any
host, any path. With it refused, nothing is fetched. So the daemon is the network by another door,
and it opens with the network: allowed in a run with `Internet`, refused in every other. The test
is kept as `TestTrustFetchesNothingWithoutTheInternet`, so a macOS that changes the answer fails
it.

The daemon fetches from outside the sandbox, so the profile's loopback deny does not hold it.
With `Internet`, a command can make it send a GET, to a path the command chooses, to the host's
loopback and the local network. The command never reads the response, only whether the
certificate verified. This is a residual (Security).

### 6. The webview

- **`NetworkSwitch`** (`network-switch.tsx`), beside `SandboxSwitch` and drawn when it is, but
  hidden while the chat is outside the sandbox, where it changes nothing: *No network*, or *Network
  on* once switched, disabled until the list delivers the chat, while a switch is in flight, and
  with the reason while `networkAvailable` is false. Turning it on opens a confirm `Dialog`:
  *Commands in this chat can reach any server on the internet, and send it what they read: cluster
  data, your workspace, the folders you granted. They still hold no credential and cannot read your
  private files.* Only its confirm calls `chatNetworkEnabledSet(id, true)`; off calls it with
  `false` at once. It draws the watch's value, never its own. `useNetworkSwitch(chatID, watched)`
  (`src/lib/network-switch.tsx`) holds the in-flight wait as `useSandboxSwitch` does, under a
  provider at `AppLayout` beside `SandboxSwitchProvider`.
- **The toggle**: a *Network for this message* button in the composer, pressed or not, on an open
  chat and a chat not yet started alike, hidden while the chat is outside the sandbox or its
  switch is on, disabled with the reason while `networkAvailable` is false. It is the outbox
  entry's (`networkThisTurn`), so it survives the composer unmounting, and is cleared when a send
  is accepted.
- **The send.** `submit` and `askAgain` pass the chat's switch as the list watch has it and the
  entry's toggle; Send and Ask again wait for the list to deliver the chat and for any switch in
  flight, as they do for the sandbox switch. A held send keeps both and Retry sends them as held.
  Ask again sends the toggle as it stands, never the failed turn's: the toggle cleared when that
  turn's send was accepted, so the user presses it again before Ask again.
  `KSTACK_CHAT_NETWORK_CHANGED` is remembered as `refusal: { kind: 'network-changed' }` and drawn
  as *This chat's network switch changed. Check it, then send again.*
- **The request.** `commandHeading` reads `command.sandboxed` before `command.network`: a call
  outside the sandbox keeps its *outside the sandbox* heading whatever its `network` says, and a
  sandboxed call that asks is a network request, *Run this command with network access?* or *Run
  this command in the background, with network access?* The rest of the request is a command's,
  Approve and Deny alike.
- **The tag.** A call's disclosure adds `with network` after `sandboxed` in its `in <dir>` line,
  muted, off `ToolCall.network`, which is null for a call outside the sandbox.

### 7. The context and the prompt

The question's Sandbox section (`withSandbox`) gains a `network` key: `off`, `on for this chat`
or `on for this message`, and `unavailable on this machine` where it is. **Step 4D also changes
the section** (`withSection` takes a `map[string]any`, and `withSandboxReplaced` finds the stale
section by its heading); whichever lands first makes that change, and the other adds its key.
`withSandboxReplaced` re-sends the section whenever what it says differs from the newest block's:
the chat's switch changed, or the turn's toggle did, so a question sent with *on for this
message* is followed by one whose section says `off`.

`sandboxLine`, the trailer under a confined run that failed (`tools/bash/bash.go`), follows the
run: *no network* for a run without `Internet`, *with network* for one with it, so a network run
that failed never tells the model it had none. The rewrite drops *cluster changes only once the
user approves each one*, which step 3B's modes and rules made untrue: the line says cluster
changes go as the user's permissions decide.

`tools/bash/prompts/sandbox.md` and `sandbox_linux.md` replace *The sandbox reaches no network*:
the sandbox reaches the internet only when the question's context says so; a command that needs
it otherwise can set `network: true`, which asks the user for that command; a refused request is
the user's decision; with network a command still holds no credential, so `gh`, `aws` and a
private registry have no login in the sandbox; the cluster is reached through its proxy either
way.

## Decisions this step asks for

1. **A switch, not a per-host allowlist.** The allowlist reached only proxy-aware tools, and on
   macOS it needed the trust daemon, which fetches any URL a command writes into a certificate
   (§5). Recommended: the user decides whether a chat, a message or a command may talk to the
   internet, and every tool works when it may.
2. **Three scopes, any of which turns network on.** A chat that needs the internet throughout, a
   message that does once, and a command the model asks for. Recommended: the narrowest scope is
   always one click away, and the request shows the exact command.
3. **The per-command request is the model's ask, not its switch.** Step 1B took
   `dangerouslyDisableSandbox` away because a chain of approvals left the sandbox. Here the
   command stays sandboxed, and each request names the one command it lets reach the internet.
   Recommended.
4. **Network is decided when the command starts.** A background command keeps what it started
   with; a switch turned off or on reaches the next command, in a running turn too. The send's
   check covers the turn's start: the question's context says what the sender saw. Recommended:
   a sandbox is per command, and stopping a running command is what Stop is for.
5. **The host's loopback stays shut to the command.** Local services — Docker's API, Ollama, a
   dev database — are not the internet the user turned on. Recommended. On macOS the local
   network cannot be shut (§4), and the trust daemon can still send a GET to loopback for a
   command with network (§5); on Linux the local network can be reached through the host's
   routes, as the internet is.
6. **Linux network-on needs `pasta`.** Sharing the host's namespace would open its loopback.
   Recommended: a machine without `pasta` says so where the switch is, and its commands keep no
   network.
7. **Network on is offered for the session even while offline.** The probe checks pasta and the
   namespaces, never a route, and an offline run fails with pasta's message or its command's own
   connection error. Recommended: a Kstack started before the network came up would otherwise
   offer no network until it restarts.
8. **A switch left on where network became unavailable gives no network and stops nothing.**
   Recommended: the chat's commands keep running, without network, and the context says
   `unavailable on this machine`.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The chat's switch: the column, `SetNetworkEnabled`, the send's two arguments, `ErrChatNetworkChanged`; the turn pins the toggle; `Session.Network` | `appdb/migrations/0001_init.sql`, `chatsvc/record.go`, `chatsvc/store.go`, `chatsvc/statements.go`, `chatsvc/service.go`, `chatsvc/turn.go`, `chatsvc/grants.go`, `session/session.go`, their tests | — | Planned |
| 2 | The policy: `NetworkPolicy.Internet` and `Resolver`; the macOS rules; `pasta` found, probed with no route and composed with bwrap, the user's ids and no capability; the mount syscalls refused; `Status.NetworkAvailable` and `NetworkReason`, `Sandbox.NetworkStatus` and `NeedsResolver`, `sandboxStatusOf`; the probe's bound; the lookup mode and the `--dns` seam | `app/app.go`, `sandbox/policy.go`, `sandbox/sandbox_darwin.go`, `sandbox/profile_darwin.sb`, `sandbox/sandbox_linux.go`, `sandbox/seccomp_linux.go`, `sandbox/sandbox.go`, `sandbox/testdata/*.golden`, their tests | — | Planned |
| 3 | The call: `network` parsed; `Approval.Network`, none where network is unavailable; `ApprovedRunner` unless step 4D added it; `sandboxedRunFor` sets `Internet` and writes the run's `resolv.conf`; `tool_calls.network` written when the call runs; `sandboxLine` and a stopped network run's header; the mutation and the send refused where network is unavailable | `tools/bash/bash.go`, `tools/bash/schema.json`, `tools/bash/prompts/description.md`, `tools/tool.go`, `agent/run.go`, `chatsvc/` (the record), `appdb/migrations/0001_init.sql`, their tests | 1, 2 | Planned |
| 4 | The wire: `Chat.networkEnabled`, `chatNetworkEnabledSet`, `chatSend`'s arguments, `ToolCallNetwork`, `CommandAction.network`, `SandboxStatus`'s two fields, the error; codegen | `sidecar/graph/schema.graphqls`, `graph/`, `graph/errors/errors.go`, `graph/util.go`, generated code, `src/gql/` | 1, 3 | Planned |
| 5 | The webview: `NetworkSwitch`, hidden outside the sandbox, `useNetworkSwitch` and its provider, the toggle in the outbox, the send's refusal, the request's heading, the tag | `src/components/widgets/network-switch.tsx`, `src/lib/network-switch.tsx`, `src/lib/chat-outbox.tsx`, `src/components/widgets/chat-composer.tsx`, `src/layouts/app-layout.tsx`, `src/components/widgets/chat-transcript.tsx`, `src/lib/chats.tsx`, `src/lib/sandbox.tsx`, their tests | 4 | Planned |
| 6 | The context section and the prompts | `chatsvc/workspace.go`, `chatsvc/notices.go`, `tools/bash/prompts/sandbox.md`, `tools/bash/prompts/sandbox_linux.md`, their tests | 1, 3 | Planned |
| 7 | Docs, per *When it lands* | see there | 1–6 | Planned |

**Order:** 1 and 2 at the same time, then 3, then 4 and 6 at the same time, then 5, then 7.

## Tests

**`sandbox`** (through the real sandbox, each platform's file)

- `TestWithoutTheInternetNothingIsReached`: a run with no `Internet` reaches neither a listener on
  a non-loopback address of the host nor a public address; the existing
  `TestOnlyTheRunsPortAndSocketAreReached` and `TestNoListenerOutsideIsReachable` still hold.
- `TestTheInternetReachesOut`: with `Internet`, a run connects to a listener on a non-loopback
  address of the host (standing in for the internet, since CI's egress is not ours to rely on):
  on macOS the host's own address; on Linux an address other than the one pasta copies, such as
  the CI image's Docker bridge. A host that holds no such address fails the test, never skips
  it, so the one positive Linux check cannot go quiet.
- `TestTheInternetReachesNoHostLoopback`: with `Internet`, a listener on the host's `127.0.0.1`
  and `::1` is refused, and the run's relay port still reaches the forwarder.
- `TestTheInternetResolves`: with `Internet`, the run's command is the test binary in a lookup
  mode (re-run through `TestMain`, as the forwarder is), which calls `net.LookupHost` and prints
  the address, or whether the error is `IsNotFound`.
  - **Linux**, in a namespace the test makes with `unshare -rn`: there it is root of its own
    network, so it gives the namespace a dummy interface holding the default route, which pasta
    needs as its template, and serves DNS on `127.0.0.1:53` with one record. pasta's upstream
    resolver is a test seam (`--dns`, a `Sandbox` field production leaves unset), pointed at
    that server. The run, through its `resolv.conf`, answers the record's address.
  - **macOS**: the test controls no resolver, so it looks up a name under `.invalid`, which
    never resolves (RFC 6761). With `Internet` the answer is `IsNotFound`, the resolver's own
    answer, which only a lookup that reached mDNSResponder gets; without `Internet` the lookup
    fails before any answer, and the test asserts that too.
- `TestTrustFetchesNothingWithoutTheInternet` (`sandbox_darwin_test.go`): the experiment's
  variants — a leaf whose issuer is missing, and one verified with `-R ocsp` — make a listener
  outside see no request from a run without `Internet`; with `Internet`, a chain the test captured
  verifies at the time it was captured (`x509.VerifyOptions.CurrentTime`), so the fixture never
  expires. It compiles each policy's profile, never editing the profile text at run time, and
  dials nothing past the machine. It replaces the experiment's logging scaffold
  (`trustfetch_darwin_test.go`), which is not kept.
- `TestNoPastaNoNetwork` (Linux): the probe with no `pasta` answers `networkAvailable` false with
  its reason, and a run with `Internet` is refused by `Command`.
- `TestANetworkRunHoldsNoCapability` (Linux): with `Internet`, the run's `/proc/self/status`
  reads `CapEff` 0, and `id -u` and `id -g` answer the user's own ids.
- `TestANetworkRunCannotUnmountANeverPath` (Linux): with `Internet`, `umount` of the tmpfs over a
  never path fails, and a file under it stays unread.
- `TestTheMountSyscallsAreRefused` (Linux): each syscall the filter refuses fails with `EPERM`
  in a run with and without `Internet`.
- `TestTheLimitsHoldWithTheInternet` (Linux): step 2B's process count and memory limit hold in a
  run with `Internet`.
- `TestANetworkRunKeepsTheExitCode` (Linux): `exit 7` with `Internet` answers 7, and a command
  killed by a signal (`kill -SEGV $$`) answers what the same command answers without `Internet`.
- `TestTheProbeIsBounded` (Linux): over a fake `pasta` that never exits, the probe answers network
  unavailable at the shrunk bound.
- `TestTheResolverBindsWhereTheHostsLinkLeads` (Linux): the args golden puts the bind after every
  rule and fixed mount and before `--remount-ro`, at the link's target for a linked
  `/etc/resolv.conf` and at `/etc/resolv.conf` when the host has none; a macOS run names no
  `Resolver`.
- `TestTheProbeNeedsNoRoute` (Linux): the probe run under `unshare -rn` answers network
  available, and the probe's `pasta` arguments are a run's less `--config-net`. The user there is
  uid 0, so the test reads the ids it expects inside that namespace, never the host's.
- `TestStopEndsANetworkRun` and `TestATimeoutEndsANetworkRun` (Linux): with `Internet`, a
  foreground `sleep` stopped or timed out ends, and no process of the run is left — pasta,
  bwrap or the command; a background task's Stop does the same.
- `TestKillingPastaEndsTheRun` (Linux): pasta killed alone takes bwrap and the command with it.
- `TestAPastaFailureFailsTheRun` (Linux): over a fake `pasta` that prints a line and exits 1, the
  run fails with that line, and its command never ran.
- `TestTheResolverIsChecked`: `Check` refuses a `Resolver` without `Internet`, a relative one, a
  link and one outside Kstack's directories; Linux's `Command` refuses `Internet` with no
  `Resolver`, and one whose `/etc/resolv.conf` resolves under `/tmp`.
- `TestTheCompiledProfileMatchesTheGolden` and the Linux args goldens gain an `Internet` case.

**`chatsvc`**

- `TestTheNetworkSwitchIsTheChats`: set, read back on the list watch, false for a new chat.
- `TestASendWithAStaleNetworkSwitchIsRefused`: `KSTACK_CHAT_NETWORK_CHANGED`; a replay ignores it.
- `TestTheTurnsToggleReachesItsCommandsAndItsSubagents`: the session's `Network` answers `turn` for
  the turn and a subagent `Narrow`ed from it, and `""` for the next turn.
- `TestTheSwitchIsReadLive`: turned on mid-turn, the next command's session answers `chat`; a
  read that fails answers `""`.
- `TestTheContextSaysTheNetwork`: the Sandbox section's `network` key for each scope, re-sent
  when the switch changed and on the question after one sent with the toggle.
- `TestNoShellOffersNoNetwork` (`app`): `sandboxStatusOf` with no shell clears
  `NetworkAvailable` and `NetworkReason` as it clears `Available`.
- `TestNothingTurnsNetworkOnWhereItIsUnavailable`: `chatNetworkEnabledSet(id, true)` and a send
  with `networkThisTurn` are `KSTACK_VALIDATION_ERROR`; turning the switch off is accepted.

**`bash`**

- `TestTheApprovalNamesTheNetwork`: §3's table, row by row and in its order, over a fake session.
- `TestANetworkCallAsks`: with network off, `network: true` asks, and on approval the run's
  policy has `Internet` and the record says `approved`; denied, nothing starts and the record's
  `network` is NULL.
- `TestTheRunTakesTheApprovalsNetwork`: the switch turned off between `Approval` and the run
  still runs an approved call with network, and one turned on gives an unasked call none.
- `TestABackgroundCommandKeepsItsNetwork`: started with network, its policy keeps `Internet`
  after the switch goes off.
- `TestAMonitorNeverHasNetwork`: a session with a nil `Network` gives no `Internet`, and its
  `network: true` call is refused without asking (step 6B keeps a test of its own).
- `TestOutsideTheSandboxTheArgumentIsIgnored`: a chat outside the sandbox records no network,
  and a `network: true` call there asks as any outside call does.
- `TestNetworkUnavailableRefusesTheCall`, and `TestASwitchLeftOnWhereNetworkIsUnavailableGivesNone`:
  a session answering `chat` on a sandbox whose `NetworkStatus` is unavailable skips with
  `Network: ""`, and the run's policy has no `Internet`.
- `TestTheRecordSaysTheNetworkOnceTheCallRuns`: a `network: true` call's row reads NULL while it
  waits on the user, `approved` once it runs, and NULL when denied.
- `TestTheTrailerSaysTheRunsNetwork`: a failed confined run's trailer says *no network* without
  `Internet` and *with network* with it, and never that every cluster change is approved.
- `TestAStoppedNetworkRunsHeaderHasNoExitCode`: a network run timed out or cancelled reads
  *Command timed out after …* or *Command cancelled*, with no exit code.
- `TestTheRunWritesItsResolver` (Linux): a run with `Internet` names a `resolv.conf` in its
  directory holding `nameserver 169.254.1.53`.

**Webview** (`network-switch.test.tsx`, `chat-composer.test.tsx`, `chat-outbox.test.tsx`,
`chat-transcript.test.tsx`): the switch's confirm on, no confirm off, disabled while in flight and
while unavailable; the toggle reaching the send and clearing on acceptance, kept by a held send; the
refusal's line; Ask again sending the toggle as it stands; the switch hidden for a chat outside the
sandbox; the request's network heading, foreground and background, and the outside heading for an
outside call with `network` set; the `with network` tag.

## Security

**Widened.** A sandboxed command with network — in a chat whose switch is on, in a message the
user toggled, or approved on its own request — can send what it read to any server: cluster data
short of Secret values, the workspace, the folders the user granted. On macOS it can also reach
the local network, since Seatbelt cannot refuse it by address. A background command keeps the
network it started with until it exits, and a subagent started in a turn with the toggle keeps
it for its whole life, past the turn.

**What still holds.** The command holds no credential: the cluster's stay in the cluster proxy, and
the denied-always list keeps `~/.ssh`, `~/.kube` and the rest shut. Every cluster write still goes
through the cluster proxy and its classes, modes and rules; a command can reach the API server
directly, with nothing to log in with. Secret values stay redacted (step 5A). Writes stay in the
workspace and the granted folders. On Linux a run with network holds no capability and runs as the
user, and no run can mount or unmount, so pasta's namespace opens nothing the sandbox shut. The
host's loopback stays shut to the command, so it cannot connect to a service listening there alone —
Ollama, a database left at its default, Kstack's forwarders. Network is off by default and turned on
only by the user: the chat's switch behind a confirm, the toggle by hand, a command by a request
that shows it. The monitor never has it. Without network the trust daemon is refused, so the
measured fetch path (§5) is closed.

**Residuals.** With network on, a hijacked command can exfiltrate what it read; the dialog says
so, and this is the trade the user makes. On macOS the local network is reachable, the host's
own address included. On Linux a host service listening on any address but loopback and the
one pasta copies is reachable, a Docker bridge's among them. A port Docker publishes listens on
every address by default, so a container's published port is reachable on both platforms. A
listed allowlist no longer limits where the data goes.

**Residual: the trust daemon reaches the loopback.** On macOS, with network on, a command can
write a URL into a certificate's AIA, OCSP or CRL field and have `trustd` fetch it from outside
the sandbox (§5). That fetch is a GET to any host and path, the host's loopback and local network
included. The command never sees the response, only whether the certificate verified, so it
cannot read a local service this way, but it can trigger a GET that has side effects. Refusing
the daemon would break TLS for every Go tool, and Seatbelt cannot limit where the daemon
connects. Without network the daemon is refused, so this is open only while the user has turned
network on.

The record, `docs/security/<date>-network-is-the-users-switch.md`, argues this and the trust daemon
measurement, names the three scopes, and states that the per-command request is a call's own
approval under [the bash tool record](../../security/2026-09-18-bash-tool.md), never a way out of
the sandbox ([outside the sandbox is the user's
choice](../../security/2026-09-30-outside-the-sandbox-is-the-users-choice.md)).

## When it lands

- **The security record** above, and an ADR: network is a per-chat switch, a per-message toggle
  and a per-command request, chosen over a per-host allowlist; the trust daemon follows the
  network; the host's loopback stays shut; Linux needs `pasta`.
- **`security-model.md`**: the network row says no network unless the user turned it on, with
  the tests; the seccomp row gains the mount syscalls and a network run's capabilities; a **By
  decision** row for what a command with network can send, for the macOS local network, and for
  the trust daemon's GET to the loopback under network; the Mach services row says the trust
  daemon and DNS are allowed with the internet alone, citing
  `TestTrustFetchesNothingWithoutTheInternet`.
- **`sidecar/CLAUDE.md`**: the network answer on `sandbox.Status`, `chats.network_enabled`, the
  send's check and the refusals where network is unavailable, `Session.Network`, `Approval.Network`,
  `ApprovedRunner`, `tool_calls.network` written when the call runs, the probe needing no route,
  `sandboxLine` and a stopped network run's header, `NetworkPolicy.Internet` and `Resolver`, the
  macOS rules, `pasta` with the user's ids and no capability, the mount syscalls refused, the run's
  `resolv.conf`, `Sandbox.NetworkStatus` and `SandboxStatus.networkAvailable`, the context key.
- **Root `CLAUDE.md`**, *Chat* and the security invariants: `NetworkSwitch`, the toggle, the
  network request's heading, the tag, the refusal.
- **README** (setup): `pasta` (the `passt` package) for network on Linux.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks,
with the sandbox's tests on Linux and in CI's macOS job.

By hand, `pnpm tauri dev` on macOS and on Linux: ask for `curl -sI https://example.com` and read
it fail with no request; ask again and read the model set `network: true` and the request *Run
this command with network access?*; approve it and read the headers and `with network` on the
call. Toggle *Network for this message*, ask for `helm repo add bitnami
https://charts.bitnami.com/bitnami && helm repo update`, read both run with no request, and read
the next message's `curl` fail. Turn the chat's switch on, read the confirm, and read `gh api
/zen` verify TLS and answer with no token. Ask for `curl http://127.0.0.1:11434` with network on
and read it refused. On Linux without `pasta`, read the switch disabled with *pasta not found*.
