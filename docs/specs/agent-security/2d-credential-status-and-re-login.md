---
title: Credential status, expiry and re-login
scope: sidecar, webview
status: Planned
---

# Credential status, expiry and re-login

**Needs:** step 1D, whose `Discover`, `Found()`, `State`, `Subscribe` and `excluded` argument
this step draws and wires, and step 1C, whose `Settings` holds the exclusion list. It lands in wave 2, before any proxy borrows. **Unblocks:** step 5C (the
per-cluster AWS profile it reads), step 6D (the monitor pauses on an expired credential), step
7A (onboarding shows what was found) and step 7B (OAuth connections sit in the same Settings
section).

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today step 1D's store knows what the user's tools hold and whether a borrow worked, and nothing
shows the user any of it. When the proxies land (steps 5C, 6B, 6C) an expired credential would
be a `403` the command reads and the model explains, with nothing to help the user sign in again.

After this step, the note's *Expiry and re-login* and the discovery half of *What the user
sees* are true of the code:

- **Discovery is on screen.** Step 1D's `Discover` runs at start and on *Refresh*, and the
  Settings section lists what it found.
- **Status is on screen.** Step 1D's `valid`, `expired`, `missing` — and this step's
  `excluded` — ride a gauge, `credentialsWatch`.
- **Re-login from chat.** An expired credential is a notice in the chat that met it — *AWS
  session for `dev` expired* — with a button that runs the tool's own login command on the
  host, outside every sandbox, which opens the browser as the tool does; the line reads
  *Renewed* once the next borrow succeeds. The proxies that raise it come later; this step
  builds the hook they call.
- **Exclusion.** A provider or profile the user excludes is never borrowed, and a request
  needing it is refused with a line that says so.
- **Settings: Credentials.** One row per credential with its status, *Exclude* / *Include*,
  *Sign in again*, and the AWS profile each cluster's commands use.

Nothing is copied from any tool's store: step 1D's no-disk test grows this step's cases.

## What is not in this step

- **No borrow, no expiry line.** The store, the borrows, the TTLs and each tool's expiry table
  are step 1D's; this step draws a status and reads no line.
- **No proxy.** Nothing raises a notice until step 5C lands; this step's notifier is tested
  with a fake caller.
- **No onboarding screen.** Step 7A draws discovery's answer on first launch; this step builds
  the Settings section it also draws.
- **No monitor.** Step 6D builds the session; this step names the hook it reads (§4).
- **No OAuth.** Step 7B adds *Connect* beside these rows.
- Nothing changes on Windows: `Discover` is not run, and the section is not drawn.

## Design

### 1. Discovery

`credentials.Discover(ctx) Found` is step 1D §4's: it runs `gh auth status`, `aws configure
list-profiles`, `gcloud config get-value account` and `project`, and `az account show`, on the
host and all at once, reading identities and never a secret, with the kube context names off
the kubeconfig service for the screen alone. This step:

- **Runs it** at start, after the login shell import, and on `credentialsRefresh`.
- **Joins the exclusion list**: `Found`'s `Identity` gains `Excluded bool`, read off
  `Settings.Credentials.Excluded` by the identity's `Key.String()` (`aws:dev`, `github`,
  `gcp`, `azure` — step 1D's one spelling, which Settings, the wire, the notice and the list
  use).

**Exclusion.** `securityconfig.Settings` (step 1C) gains:

```go
Credentials CredentialSettings `json:"credentials,omitzero"` // a struct, so omitzero (step 1C §2)

type CredentialSettings struct {
	Excluded []string                       `json:"excluded,omitempty"` // identity ids, Key.String()
	Profiles map[apimeta.ClusterID]string   `json:"profiles,omitempty"` // the AWS profile per cluster (§6)
}
```

`app` passes `NewStore`'s `excluded` argument (step 1D §1) as a read of that list through
`SecurityCfg.Get`, which is safe for concurrent use, so every borrow —
`AWS`, `GitHub`, `Google` and `Azure` — answers `ErrExcluded` for an excluded identity and runs
nothing, one check under every proxy to come. `credentials.ExcludedLine(key)` spells what a
proxy answers the command: *kstack: the GitHub credential is excluded in Kstack's settings*,
which the prompt already says is a refusal not to work around (step 3B §9). Steps 5C, 6B and 6C
print it.

### 2. Status

`Store.State(key) State` is step 1D §3's in-memory table: `valid`, `expired` or `missing`, when
it was entered, and the tool's one line as `Detail`. Which stderr line means expired is step
1D's, one table per provider beside its command; a `401` a proxy meets reaches the table
through step 1D's `MarkExpired`. This step adds one status:

| Status | When |
| --- | --- |
| `excluded` | on `Settings.Credentials.Excluded`, whatever the tool holds; set by the exclusion mutations and read back at start |

`credentials/exclude.go` adds `Excluded` to step 1D's `Status` and `Store.SetExcluded(key,
bool)`, which the exclusion mutations and the start call. Excluding sets `excluded` and drops the
identity's cache entries. Including clears the key's status, so the table has none for it, then
runs `Discover`, which fills a gap as it always does (step 1D §4): the identity reads `valid` if
its tool still holds it, `missing` if not, and the first borrow proves it.

and the line a proxy prints on `ErrExpired`, `credentials.ExpiredLine(key)`: *kstack: the AWS
session for profile `dev` has expired; the user can renew it with `aws sso login --profile
dev`*, the command off §3's table. The model reads it; the user gets the notice (§3).
`Store.Subscribe()` (step 1D) is what the gauge `credentialsWatch` serves and step 6D's monitor
reads.

### 3. Re-login from chat

A run's proxies know their session (step 2C), so an `ErrExpired` under a chat's command reaches
`chatsvc` the way a cluster write's request does: `tools.Runtime` gains `Credentials
CredentialNotifier`, set by the turn and a subagent to a function bound to the chat, which Bash
hands to the run's proxies with the grant. **No proxy calls it in this step**; steps 5C, 6B and
6C do. It writes one row:

```sql
CREATE TABLE credential_notices (
  id              TEXT    PRIMARY KEY,
  conversation_id TEXT    NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  credential_id   TEXT    NOT NULL,
  status          TEXT    NOT NULL CHECK (status IN ('expired', 'renewed')),
  created_at      INTEGER NOT NULL,
  notified_at     INTEGER
) STRICT, WITHOUT ROWID;
```

in `appdb/migrations/0001_init.sql`, under the
[pre-release schema policy](../../adr/2026-08-29-schema-edit-not-migration.md), once per chat
and credential while it stays expired. A row with no `notified_at` is a waiting notice, read by
`waitingNotices` beside the tasks' and told the model the same two ways (`sidecar/CLAUDE.md`,
*Notices*): riding the next send, or on a turn of its own, which `startsTurn` starts for an
`expired` row as for an exit, and for a `renewed` row, so the model picks the work back up.
`llm.TaskNotice` gains a kind:

```go
TaskCredential TaskKind = "credential"

// CredentialNotice is a credential notice's own fields: the identity's ID
// ("aws:dev"), Provider and Label, the fixed login Command, and Renewed.
type CredentialNotice struct{ ID, Provider, Label, Command string; Renewed bool }
```

carried on `TaskNotice.Credential`, sent by every dialect in the reference's
`<task-notification>` as the others are: *the AWS credential for `dev` has expired; the user has
been asked to run `aws sso login --profile dev`*, or *… has been renewed*. `noticesOf` in
`chats.tsx` reads the kind and the transcript's `Notices` draws one muted line: *AWS session for
`dev` expired* with a button *Run `aws sso login --profile dev`*, *GitHub login expired* with
*Run `gh auth login --web`*, or *AWS session for `dev` renewed*. The button is drawn off the
gauge, not the block: it shows while `credentialsWatch` says `expired`, is down while a renewal
runs, and gives way to *Renewed* once the gauge says `valid` with a `Since` after the notice,
so a stored message is never rewritten.

**`credentialRenew(id)`** runs the identity's fixed login command on the host, as the user,
outside every sandbox, with the sidecar's environment, stdin the null device, in its own
session, under a 5 minute bound, one at a time per identity. `credentials.RenewCommand(key)
[]string` is the table:

| Provider | Command |
| --- | --- |
| `aws:<profile>` | `aws sso login --profile <profile>` |
| `gcloud` | `gcloud auth login` |
| `azure` | `az login` |
| `github` | `gh auth login --web --hostname github.com` |

`aws`, `gcloud` and `az` open the browser themselves. `gh` does not with stdin the null device:
it prints a one-time code and the device URL on stderr and waits for the user to enter the code
there. So for GitHub alone the sidecar reads the code off stderr, matched to its fixed shape
(`XXXX-XXXX`), serves it on the notice beside *Enter this code at github.com/login/device*, and
opens `https://github.com/login/device` through the host's opener — a URL the sidecar names,
never one read from the output. Past that, the sidecar streams none of it to the model or the webview, logs
the exit status and never the output, and on exit 0 calls step 1D's `Recheck(ctx, key)`, which
borrows past the cache and the expiry backoff for every standing refusal; when it answers `valid`
it writes a `renewed` row for every chat holding an `expired` one for that identity. A
nonzero exit leaves `expired` with the tool's line as `Detail`. The command is the table's,
never the model's or the webview's: an id off the table is `KSTACK_VALIDATION_ERROR`, and
`<profile>` is one the store's last `Found()` lists, from `aws configure list-profiles`, passed as one argument, never through
a shell. The binaries are the store's `Binaries` (step 1D).

### 4. The monitor

Step 6D's monitor reads `Store.Subscribe()` and, before each check of a context, asks
`Store.Paused(key) (bool, State)` — this step's one hook, true for `expired` and `excluded` —
for the identity the context borrows (the exec entry's profile for AWS, the provider for the
rest). A paused identity pauses that context's checks and sets its status in the monitor's own
section; nothing is written to a chat and nobody is asked. The check runs again when the gauge
says `valid`.

### 5. Settings: Credentials

`credential-settings.tsx`, a Credentials section in the Settings dialog, over `useCredentials()`
in `src/lib/credentials.tsx`, the one reader of the query, the gauge and the mutations:

- **One row per identity**: the provider, the label, a tag off its status — *valid*, *expired*,
  *not signed in*, *excluded* — and `Since` as a relative time. An expired row draws **Sign in
  again** (`credentialRenew`); every present row **Exclude** or **Include**. A provider whose
  tool is absent draws one muted row, *`gcloud` is not installed*.
- **The kube contexts** as one line, *4 kube contexts*: each is a cluster with its own toggles.
- **The AWS profile per cluster** (§6): under the AWS rows, one line per cluster whose
  kubeconfig entry names AWS, with a select over the profiles found; *from the kubeconfig* while
  no override is set.
- A line under the list: *Kstack asks your tools for a credential when a command needs one and
  keeps it in memory until it expires. Nothing is copied from their own stores.*
- **Refresh** above the list runs `Discover` again.

The wire:

```graphql
enum CredentialStatus { Valid Expired Missing Excluded }
"One per step 1D Provider."
enum CredentialProvider { Aws GitHub Google Azure }
"detail is the tool's own line, when there is one; never a secret."
type CredentialState { id: ID!, provider: CredentialProvider!, label: String!, status: CredentialStatus!, since: Time!, detail: String!, toolMissing: Boolean!, "The one-time code a GitHub renewal waits on; empty otherwise." deviceCode: String! }
"The AWS profile a cluster's commands sign with; profile is empty while the kubeconfig's applies."
type ClusterCredentialProfile { clusterID: ClusterID!, profile: String!, fromKubeconfig: String! }

extend type Query { credentials: [CredentialState!]!, credentialProfiles: [ClusterCredentialProfile!]! }
"A gauge: the whole list, current on subscribe."
extend type Subscription { credentialsWatch: [CredentialState!]! }
extend type Mutation {
  credentialExclude(id: ID!): [CredentialState!]!
  credentialInclude(id: ID!): [CredentialState!]!
  "Runs the identity's fixed login command on the host. KSTACK_VALIDATION_ERROR for an id off the table, or while one runs."
  credentialRenew(id: ID!): [CredentialState!]!
  credentialsRefresh: [CredentialState!]!
  credentialProfileSet(clusterID: ClusterID!, profile: String!): [ClusterCredentialProfile!]!
  credentialProfileClear(clusterID: ClusterID!): [ClusterCredentialProfile!]!
}
```

`credentialsWatch` is a gauge like `clusterCacheHealthWatch`: current on subscribe, no
`Bookmark`, the list whole on each change. Each mutation answers the list, so the section
redraws from one result.

### 6. Per-cluster AWS profile

This step stores and draws the profile a cluster's commands sign with:
`Settings.Credentials.Profiles[clusterID]`, written by `credentialProfileSet` and cleared by
`credentialProfileClear`. A profile off the found list is `KSTACK_VALIDATION_ERROR`.
`fromKubeconfig` is the profile the cluster's kubeconfig `exec` entry names, read the way step
5C's `awsProfileFor` reads it, so the row can say what applies while no override is set. **The
reader is step 5C's**: `awsProfileFor` takes the override first, and an override naming a
profile that has since gone falls back to the kubeconfig's, which the row says. Until step 5C
lands the setting is stored and shown and nothing reads it.

## Decisions this step asks for

1. **An expiry notice starts a turn of its own, like an exit.** A line drawn with no answer
   would be a user message with no run, which the store has no shape for; and a short answer
   (*Sign in and I'll continue*) is what the user wants to read. The `renewed` row starts one
   too, so the model resumes unasked. Recommended.
2. **The identity id is step 1D's `Key.String()`**, one spelling everywhere, so the mutation
   takes an id and not two fields. Recommended.
3. **`valid` means found and not known expired.** Discovery does not borrow, so it cannot prove
   a credential works; the first borrow does. A fifth status would cost a state nothing draws
   differently. Recommended.
4. **A kube context is not a credential row.** The cluster proxy borrows the kubeconfig as
   today, and a cluster's toggles are its exclusion. Recommended.
5. **The section lands before any proxy borrows.** Until step 5C every row reads what
   `Discover` found and never flips to `expired` in a chat; the notice and the renewal are
   tested against a fake caller and exercised by hand once 5C lands. Recommended: the UI and
   the store's hooks are one assignment, and the proxies then each wire one call.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | The exclusion list, the `excluded` argument wired, `excluded` status and `SetExcluded`, `ExcludedLine`, `Discover` at start and on refresh | `securityconfig/`, `credentials/exclude.go`, `app/app.go`, their tests | — | Planned |
| 2 | `ExpiredLine`, `Paused` | `credentials/status.go`, its tests | 1 | Planned |
| 3 | `credential_notices`, the notifier, the notice kind, the turn | `appdb/migrations/0001_init.sql`, `chatsvc/notices.go`, `chatsvc/turn.go`, `tools/tool.go`, `llm/block.go`, their tests | 2 | Planned |
| 4 | `Renew`: `RenewCommand`, the check borrow, the renewed rows | `credentials/renew.go`, `chatsvc/`, their tests | 2, 3 | Planned |
| 5 | The per-cluster profile setting and its shape check | `securityconfig/`, its tests | 1 | Planned |
| 6 | The wire and the resolvers | `sidecar/graph/schema.graphqls`, `graph/`, `app/app.go`, generated code | 2, 4, 5 | Planned |
| 7 | Codegen, `useCredentials`, the section, the notice's line and button | `src/gql/`, `src/lib/credentials.tsx`, `src/lib/chats.tsx`, `src/components/widgets/credential-settings.tsx`, `chat-transcript.tsx`, `settings-dialog.tsx`, their tests | 6 | Planned |
| 8 | Docs, per *When it lands* | see there | 1–7 | Planned |

**Order:** 1, then 2, then 3, 4 and 5 at the same time, then 6, then 7, then 8.

## Tests

**`credentials`**

- `TestAnExcludedIdentityIsNeverBorrowed`: with `aws:dev` on the settings list, `AWS(ctx,
  "dev")` answers `ErrExcluded` and runs no `aws`, the status is `excluded`; `aws:prod` still
  borrows; `ExcludedLine` names the setting.
- `TestExpiredLineNamesTheCommand`: one case per provider, the profile in the AWS one.
- `TestPausedIsExpiredOrExcluded`, and false for `valid` and `missing`.
- `TestRenewRunsTheFixedCommandAndNothingElse`: for each provider the fake tool records exactly
  the table's argv; an id off the table is a validation error; a second renew while one runs is
  refused; a profile with a space rides as one argument; exit 0 followed by a borrow that
  succeeds sets `valid`, and one that fails leaves `expired` with the tool's line.
- `TestNoCredentialIsWrittenToDisk` (step 1D's) grows a case: a renewal and the exclusion list
  leave no token, profile file or kubeconfig bytes under any Kstack directory.

**`chatsvc`**

- `TestAnExpiredBorrowWritesOneNotice`: two calls of the notifier from one command, through a
  fake caller standing in for a proxy, write one row for the chat and identity, a subagent's
  included; the notice starts a turn of its own once the turn settles, with the reference's
  `<task-notification>` text naming the command.
- `TestARenewalWritesARenewedRow` for every chat holding an `expired` one, and it starts a turn.

**`securityconfig`**: `TestExclusionsAndProfilesPersist`, and the read-back check refuses a
profile or an exclusion id out of shape; it reads the value alone (step 1C §3), so it never asks
whether `Discover` found the profile. **`graph`**: `TestCredentialMutationsAnswerTheList`, each
refusal a `KSTACK_VALIDATION_ERROR`, a profile off the found list among them.

**Webview** (`credential-settings.test.tsx`, `credentials.test.tsx`, `chat-transcript.test.tsx`)

- Each status draws its tag; *Sign in again* on an expired row alone; *Exclude* and *Include*
  call their mutations and redraw from the answer; the profile select calls `credentialProfileSet`.
- The notice draws *AWS session for `dev` expired* with its button while the gauge says
  `expired`, the button down while a renewal runs, and *Renewed* once the gauge says `valid`
  with a later `Since`; a `renewed` block draws its line and no button.

## Security

**Moved.** The renewal runs a command on the host, outside the sandbox, on the user's click.
What holds it: the command is one of four fixed by provider, its one variable a profile name the
tool itself listed, passed as an argument; the model cannot call the mutation and the webview
cannot name the command; the browser handoff is the tool's own; nothing of the output reaches
the model, the webview or the log. This is a security record.

**Not moved.** No token is shown anywhere: the wire carries statuses and labels, `Detail` is
the tool's line through `safe.String`, and the notice names the command, never a credential.
Discovery is step 1D's, reading identities from the tools' own commands and never a file, so the
note's no-copy invariant keeps its test. **Narrowed:** an excluded identity is never borrowed,
in any session, once a proxy borrows at all.

**Residuals.** `credentialRenew` is a mutation, so a script in the webview can start a login
flow in the user's browser; it obtains no token from it, since the tool writes to its own store
and Kstack borrows through the tool. The browser tab is the tool's, and Kstack cannot close it.

The record, `docs/security/<date>-credential-status-and-re-login.md`, is short, and
`security-model.md` gains a row for the fixed renewal command and one for the exclusion list.

## When it lands

- **The security record** above, and **an ADR**: an expired credential is a notice and a fixed
  host-side command; exclusion is by identity id; the monitor pauses and never asks.
- **`security-model.md`**: the two rows above, and step 1D's no-disk row gains this step's cases.
- **`sidecar/CLAUDE.md`**: `credentials`' `excluded` status, `ExcludedLine`, `ExpiredLine`,
  `Paused`, `RenewCommand` and `Renew`; `credential_notices`, the notifier on the runtime, the
  notice kind and its turn; `securityconfig`'s `Credentials`. **Root `CLAUDE.md`**, *Chat* and
  the Settings dialog: the notice's line and button, `noticesOf`'s new kind, the Credentials
  section and `useCredentials`.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks.

By hand, `pnpm tauri dev` on Linux or macOS: Settings lists what your tools hold — *GitHub as
`@you`*, each AWS profile, the gcloud account and project, the Azure subscription — every row
*valid* (found, not yet tried), and *Refresh* re-reads after `gh auth logout` to show *not
signed in*. Exclude a profile and read the row *excluded*; set another profile for a cluster
and read the row leave *from the kubeconfig*. Once step 5C has landed: after `aws sso logout`,
ask for `aws sts get-caller-identity`, read the `403` in the answer and the line *AWS session
for `dev` expired* with its button; click it, sign in in the browser, and read the line flip to
*Renewed*, the model resume, and the row *valid*; ask again with the profile excluded and read
the refusal name the setting.
