# Step 2: Sandbox settings

Moves the cloud platform switches into `app.db`. Part of [the sequence](README.md), whose shared
rules apply.

## What moves

| In `settings.json` today | Becomes |
| --- | --- |
| `sandbox.aws`, `sandbox.github`, `sandbox.gcp`, `sandbox.azure` | `sandbox_settings` |

## The table

Migration `0007_sandbox_settings.sql`:

```sql
-- sandbox_settings: the user's settings for every sandboxed command, in one
-- row. Each <platform>_enabled is whether a sandboxed command may reach that
-- cloud platform as the user.
CREATE TABLE sandbox_settings (
  id             INTEGER PRIMARY KEY CHECK (id = 1),
  aws_enabled    INTEGER NOT NULL DEFAULT 1 CHECK (aws_enabled IN (0, 1)),
  github_enabled INTEGER NOT NULL DEFAULT 1 CHECK (github_enabled IN (0, 1)),
  gcp_enabled    INTEGER NOT NULL DEFAULT 1 CHECK (gcp_enabled IN (0, 1)),
  azure_enabled  INTEGER NOT NULL DEFAULT 1 CHECK (azure_enabled IN (0, 1)),
  updated_at     INTEGER NOT NULL
) STRICT;

INSERT INTO sandbox_settings (id, updated_at) VALUES (1, 0);
```

The table holds what the user chose for the sandbox as a whole. Today that is the four cloud
platform switches, each on by default. The columns are named `enabled`, as `tool_settings`'s is.

A new cloud platform is a new column, by a migration. So is a later sandbox setting that has one
value for every command. What Kstack measures on the machine (step 4's `shell_path`) stays out of
this table: it holds the user's choices alone.

The Go struct `SandboxSettings` keeps the file's `PATH` fields until step 4. It is not this
table, and no code of this step names the table by it.

## Reading and writing

**One read takes the whole row.** `CloudPlatformSwitches(ctx) (CloudPlatformSwitches, error)`
answers the four switches from one `SELECT`, so no read fails partway.
`CloudPlatformSwitches.On(target)` is one switch, false for a target with none.

**`SetCloudPlatformSwitch(ctx, target, on)` updates the row; it never inserts one.** Statements
are prepared, so a column cannot be a parameter: there is one `UPDATE` per platform, chosen by
`target`. An update that changes no row is `ErrNoSandboxSettings`. Inserting would write the
other three columns at their default, on, which turns on platforms that read off.

**The switches are read on every use, not cached.** A cloud platform's grant reads its switch on
every request, so a switch turned off refuses the run's next request. Nothing watches the
switches: Settings reads them with the `sandboxCloudAccess` query, and a chat's Sandbox block
reads them at each send. So the store notifies no key after a write
([During the sequence](README.md#during-the-sequence)).

**Only an authenticated request reads the switch.** The door no longer reads it. `runproxy.Grant`
reads it in `Enter`, which each platform's proxy calls after `Holds` has checked the run's token.
So a request without the token is refused before it reaches `app.db`, and every read is one of
the run's own requests, which it can send only through its grant. An `Enter` refused by the
switch answers `401`, never the grant's `407` challenge: the token was good, and a challenge
would only have the client send it again.

## A row Kstack cannot use, and a read that fails

No row can be refused: every column has a `CHECK`.

The migration writes the one row, so a read always finds it. **A missing row is a failed read**
(`ErrNoSandboxSettings`), not a default.

Each reader answers a failed read its own way:

| Reader | A failed read |
| --- | --- |
| `sandboxCloudAccess` and `sandboxCloudAccessSet` | answer the error |
| A grant, per request (`Enter`) | the platform off: the request gets `401` |
| A run's start (`platformsFor`) | every platform off, logged once per run |
| A chat's Sandbox block | no platform named |

Off is the strictest answer, so the run still starts. The log line says the switches could not
be read, so a command that fails for want of its credentials has a cause in the log.

## Code

Paths are relative to `sidecar/internal/` unless rooted.

- **`services/settings`**: `cloud.go` becomes `cloudplatform.go`, which reads and writes `app.db`
  through `CloudPlatformSwitches` and `SetCloudPlatformSwitch`, with their statements in
  `statements.go`. `ErrNoCloudSwitch` becomes `ErrNoCloudPlatformSwitch`. `CloudOn`,
  `cloudSwitch` and the four `Field*` constants are deleted. The switches leave
  `SandboxSettings`, and their entries leave `strictest`.
- **`run/runproxy`**: `NewGrant` takes the platform's switch, `func(context.Context) bool`.
  `Enter` reads it with the request's context, and refuses the request when it is off.
- **`run/awsproxy`, `azureproxy`, `gcpproxy`, `githubproxy`**: a refused `Enter` answers `401`.
- **`tools/bash`**: `Cloud.On` becomes `Cloud.Switches func(context.Context)
  (settings.CloudPlatformSwitches, error)`. `platformsFor` reads it with the run's context, and each
  platform's mount hands its grant the switch, off on a failed read. The door reads no switch.
- **`agent/chat`**: `cloudTargetsOn` reads `CloudPlatformSwitches` once and names no platform on an
  error.
- **`graph`**: `cloudAccess` takes a context and answers the read's error.
- **`app`**: `runtime.go` wires the store's `CloudPlatformSwitches` into `bash.Cloud`.

The identifiers this step adds say "cloud platform" in full, as the docs do: bare "Cloud" also
names Kstack Cloud. The existing `bash.Cloud`, `CloudRun`, `CloudMount`, `CloudRequest` and the
wire's `CloudAccess` keep their names here.

## The wire

The schema's shapes do not change: `sandboxCloudAccess` and `sandboxCloudAccessSet` keep them.
Each now answers an error when the row cannot be read, where it answered the file's values.
`sandboxCloudAccess`'s description says "On by default", not "Off by default".

The webview follows in `src/lib/sandbox-cloud-access.tsx` and the Settings dialog's
`sandbox-network.tsx`. It is done when these hold, each with a test:

- `useSandboxCloudAccess` reports the query's error apart from its loading.
- On an error, the *Cloud platforms* row reads "Couldn't load cloud access."
- A read with every switch off still reads "None. Every cloud platform is off.", tested apart
  from the error.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | `appdb`: migration `0007` and its test, which builds the schema before it, seeds a row in each earlier table, migrates, and checks that every seed survives, the one row with every switch on, each `CHECK`, and that a second row is refused. | Done |
| 2 | `services/settings`, `run/runproxy`, the four cloud platform proxies, `tools/bash`, `agent/chat`, `graph`, `app`: the switches on `app.db`, with each reader's failed read as above, tested with a failed query and a missing row. `SetCloudPlatformSwitch` on a missing row inserts nothing. A request without the token never reads the switch, and one with it is refused with `401` once the switch is off. | Done |
| 3 | `src/`: the webview's error state, and the schema's description. | Done |

## When it lands

- [`persisted-data.md`](../../persisted-data.md): add a row for `sandbox_settings`; take the
  switches out of the *Sidecar settings* row.
- [`security-model.md`](../../security-model.md): the cloud platform switches' row names the
  table, and replaces `TestASwitchTheFileTurnsOffIsOff` and `TestARefusedSwitchIsOff` with the
  tests that pin a failed read and a missing row as off at a grant and at a run's start. Its
  "off is a hand edit" becomes an edit of `app.db`, since the file is no longer read.
- The `door.ServeHTTP` comment, and the background command row of `security-model.md` that cites
  `TestSwitchingACloudPlatformOffClosesItsRoute`: the grant, not the door, reads the switch.
- `sidecar/internal/services/settings/CLAUDE.md`: the cloud switches paragraph.
- `graph/schema.resolvers.go`: the `coverage:ignore` on `SandboxCloudAccessSet`'s write goes,
  since a failed write is tested.
