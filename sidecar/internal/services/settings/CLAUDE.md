# internal/services/settings — the user's settings

**`<data>/settings.json` holds the user's `PATH`**; the rest are `app.db` rows. It is 0600
through `atomicjson`, in the data directory no sandboxed command reads, never synced, grouped by
area (`sandbox`), `schemaVersion` 1; the moved areas' keys are kept, unread. `Settings` mirrors
it; `Store` wraps
`jsonsettings.Store[Settings]` (`Get`, `Update`, `Subscribe` current-on-subscribe, `Held`,
`Refused`). **A field's key is dotted inside its group** (`sandbox.path`); every `Field*`
constant spells one, and `Held`, `Update`'s `fields`, a `Refusal` and the wire all use it. →
[ADR: settings are one file grouped by area](../../../../docs/adr/2026-10-07-settings-are-one-file-grouped-by-area.md).

**The store is `internal/lib/jsonsettings`**, which knows nothing of sandboxes or permissions.

- A field of struct type is a group, read field by field. A group or file that is not a JSON
  object fails `Open`. An empty group is left out of the file.
- Every value crosses a JSON copy (`clone`); receivers share one delivery, read-only.
- An `Update` that leaves the file's JSON unchanged writes and publishes nothing. `Open` writes
  nothing.
- A write is indented two spaces with a trailing newline (`atomicjson.SaveIndent`), keys sorted.
  The user's own formatting is not kept.
- Decoding is per field, and per element for a list. A `null` is refused. A bad list element is
  refused alone; any other bad value is refused whole and the other fields load. An unknown key is
  kept and written back, so an older Kstack keeps a newer one's setting.
- Every write stamps `schemaVersion` — the file's own when a newer Kstack wrote it. A field layout
  change bumps it and upgrades in `Open`.
- **A hand edit may cost a permission, never a restriction.** `strictest` (`check.go`) maps each
  restricting field to its most restrictive state. A refused value of it sets that state and the
  store keeps the raw JSON, written back until an `Update` names the field. **An `Update` that
  changes a held field without naming it is `ErrHeld`**, so a Settings mutation names the field it
  writes. A field that only grants drops a refused value. A group is never held whole.
- The read-back is `checks` (`check.go`), one per field, each answering a `Refusal` per value
  removed. On `Open` refusals are logged and kept in `Refused()` (`settingsRefused`); on `Update` a
  refusal is the error and nothing is written. `WithChecks` is the test seam.

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

**`Settings.Sandbox.Path` is the user's `PATH`, frozen** (`path.go`): a `PathEntry` per folder,
with `Dir`, `Target`, `State` (`adopted`, `pending`, `gone`), `Source` (`shell`/`user`) and
`Shared`. **A state holds for its exact `Target`, compared as text**: a folder in another
spelling is a new folder, and no stored `Target` is read off the disk. A removal the shell no
longer lists keeps its `Target`; one it lists holds for its `Dir` wherever that leads. → [ADR](../../../../docs/adr/2026-11-24-a-stored-path-decision-holds-for-its-exact-path.md). `filterPath` (`filter.go`) drops what
`sandbox.SearchFolder` refuses, `node_modules` and duplicates, and marks `Shared` a folder a
non-admin group can write.

**`Service` (`service.go`) is the store plus the sync**, over `Zones` (`Never`, `Open`, `Home`),
the refresh's resolver and the launch's fault; on a machine with no sandbox it syncs nothing and
its path methods answer `ErrNoSandbox`. An entry is **open** when its `Target` is under an `Open`
Read path and no Deny path, compared as files. `SyncPath`, serialized, reads the disk on a
goroutine bounded by `SyncTimeout` and diffs in one `Update` naming `sandbox.path`: a new
open, unshared entry is `adopted`, any other new one `pending`, a changed `Target` is new again,
and the shell's order wins. **While the field is held or `PathStrict` is set, every new entry is
`pending`.** A removal holds under every `Dir` whose `Target` it names. A sync that changed the list probes every listed
executable (`StartProbe`), after the write and never holding it; a read of the executables that
fails starts none and is a warning, not a startup error. `AdoptPath` (Include) refuses `ErrPathChanged` when a refresh moved the entry, and drops the
removals kept under other `Dir`s for its exact `Target`; `DropPath` (Remove) sets `gone`.
`RunCheck` (`checkrun.go`) applies the filter and adoption rule to a run's folder: an approval
holds when the folder resolves to exactly its `Target`. Its callers bound it. → [ADR: `PATH` is the login shell's, filtered and
frozen](../../../../docs/adr/2026-10-02-path-is-the-login-shells-filtered-and-frozen.md), [security
record](../../../../docs/security/2026-10-02-path-from-the-login-shell.md).

**A folder grant is checked against the zones** (`folders.go`): a `FolderGrant`, read or
read-write, on a folder. `CheckFolder` answers a `FolderRefusal`; the
rules are in the function and `TestCheckFolderRefusesEachRule`. **A grant is its canonical
path** (`realpath.Canonical`), **and is compared with the zones and `PATH` as files**
(`realpath.Under`, `realpath.Same`), never by text: macOS opens `/Users/ME` as `/Users/me`. **The
service holds the zones as a snapshot** taken by each `SyncPath`; checks re-resolve its paths but
never re-run the zones function. Every disk read goes through `readWithin`, bounded by
`SyncTimeout`. With no sandbox every folder is refused. → [ADR: a folder grant is
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
