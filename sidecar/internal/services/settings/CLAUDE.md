# internal/services/settings — the user's settings

**Every setting is an `app.db` row.** `<data>/settings.json` is still opened, through
`internal/lib/jsonsettings` (`Store` wraps `jsonsettings.Store[Settings]`), and read for no field:
`Settings` is empty and `checks` and `strictest` (`check.go`) hold nothing, so nothing writes the
file and its keys are kept, unread. `Refused()` answers `settingsRefused`, empty. →
[ADR: settings are one file grouped by area](../../../../docs/adr/2026-10-07-settings-are-one-file-grouped-by-area.md).

**The modes, the always rules and the always folder grants are `app.db` rows** with `chat_id`
NULL (`approval_modes`, `kube_rules`, `folder_grants`; `agent/chat` writes a chat's), in
`permissions.go`, `store.go` and `statements.go`. Each write is one
transaction and then notifies `appdb.KeyPermissions` (`WatchPermissions`). → [ADR](../../../../docs/adr/2026-11-24-a-folder-grant-is-its-own-row-and-a-mode-and-a-rule-belong-to-a-cluster.md),
[security record](../../../../docs/security/2026-11-24-permissions-in-app-db.md).

- `ModeFor(ctx, cluster)` is the cluster's row, else the default row, else `Ask`. `SetMode` refuses
  a cluster gone or marked (`ErrClusterGone`) or with no kube-context (`ErrNoKubeContext`).
- `CheckKubeRule`, which both writers call first, refuses with a `RuleRefusal` exactly what the
  table's `CHECK`s refuse, an `allow` naming no cluster among them. `AddKubeRule` refuses a
  cluster as `SetMode` does, and answers a rule already held. `KubeRuleColumns`, `KubeRules` and `KubeRuleValues` (and `FolderGrant*`) are the
  tables' one encoding, shared with `agent/chat`.
- `GrantFolder` upserts on the folder, keeping its id; its caller checks the folder first.
- A failed read is an error; its readers decide what it costs.

**The `PATH` is one string, `sandbox_settings.path`** (`path.go`), written as in a shell's
startup file, `$PATH` by default. `parsePath` splits it before expanding: each element is exactly
`$PATH`/`${PATH}` or a typed folder (absolute, `~/`, `$HOME/`, `${HOME}/`); `CheckPath` refuses the whole
value, naming the element, for `$PATH` mixed with text, any other `$`, a relative path, `..`, the
home itself, an empty element or a NUL. **`ExpandPath` splices the shell's `PATH` in already split**,
so nothing it lists is ever typed, and joins a typed folder to the canonical home. A `PathFolder`
carries its `Origin` (`OriginShell`, `OriginTyped`). `SetPath` updates the one row, never inserts
it, and notifies `appdb.KeySandboxPath` (`WatchSandboxPath`); a stored value the grammar refuses
is read with its `Refusal`. → [ADR](../../../../docs/adr/2026-11-24-the-sandbox-path-is-one-setting-the-user-writes.md),
[security record](../../../../docs/security/2026-11-24-the-path-setting.md).

**`Service` (`service.go`) holds the login shell's last `PATH`** in `shell_path`, a copy no row of
which is refused, over `Zones` (`Never`, `Open`, `Home`), the refresh's resolver, the launch's
fault and the platform's default folders (`$PATH` with no row). On a machine with no sandbox its
path methods answer `ErrNoSandbox`. **`SetShellPath`**, one at a time and bounded by
`DiskTimeout`, retakes the zones' snapshot, then stores the `PATH`, and pings `KeySandboxPath`
whether or not it changed; an empty one is `ErrEmptyShellPath`. A change logs a tracked tool's
move (`logToolMoves`, after the write). `RefreshPath` runs the shell, then `SetShellPath`; a read
that fails or answers no `PATH` keeps the last and sets the fault. **`RunPath` reads both rows in
one read transaction**: `Search`, the value expanded, and `NoWrite`, every shell folder whatever
the value says plus each typed one; a refused value is a `PathRefusal`, so no run starts.
`PathNoWrite` is `NoWrite` alone; `PathFolders` is the setting and each folder judged, for
Settings, bounded by `DiskTimeout`.

**`RunCheck.Folders` (`checkrun.go`) is the one rule** a run and Settings both read. Every folder
passes `filterDir` (`filter.go`: `sandbox.SearchFolder`, `node_modules`; `Shared` marks a folder
a non-admin group can write). A shell folder is searched only when open (under an `Open` Read
path and no Deny path, as files) and unshared; a typed one when `checkFolder` passes it for a
read, unstored, so a link or another spelling is left out naming the folder to type. A folder
already searched is not searched again, compared as files. `Addable` marks a shell folder left out
only for being closed or shared, that no typed folder covers, whose target could be typed.

**A folder grant is checked against the zones** (`folders.go`): a `FolderGrant`, read or
read-write, on a folder. `CheckFolder` answers a `FolderRefusal`; the
rules are in the function and `TestCheckFolderRefusesEachRule`. **A grant is its canonical
path** (`realpath.Canonical`), **and is compared with the zones and `PATH` as files**
(`realpath.Under`, `realpath.Same`), never by text: macOS opens `/Users/ME` as `/Users/me`. **The
service holds the zones as a snapshot** taken by each `SetShellPath`, or on first use; checks
re-resolve its paths but never re-run the zones function. They read `PathNoWrite` for the `PATH`;
a read that fails is an error, never a `FolderRefusal`. Every disk read goes through `readWithin`, bounded by
`DiskTimeout`. With no sandbox every folder is refused. → [ADR: a folder grant is
checked](../../../../docs/adr/2026-10-04-a-folder-grant-is-a-rule.md), [security
record](../../../../docs/security/2026-10-04-path-grants.md).

**The registered executables are `executables` rows** (`executables.go`), probed beside
`CuratedExecutables`. `CheckExecutable` checks a register and every read: a refused row is listed
with its `Refusal`, never probed, and removable. A stored name is `ErrExecutableListed` before any
check. A write notifies `appdb.KeyExecutables` (`WatchExecutables`).

**The cloud platform switches are `sandbox_settings`' one row** (`cloudplatform.go`), each on by
default ([ADR](../../../../docs/adr/2026-11-18-every-cloud-platform-is-on-until-the-user-turns-it-off.md)).
`CloudPlatformSwitches` reads the row; a missing one is `ErrNoSandboxSettings`. A failed read is
off everywhere but Settings, which answers the error. `SetCloudPlatformSwitch` never inserts the
row, which would turn the others on. Nothing watches the switches.
