# internal/services/settings — the user's settings file

**`<data>/settings.json` is the user's settings**: 0600 through `atomicjson`, in the data
directory no sandboxed command reads, never synced. Keys are grouped by area (`sandbox`,
`permissions`, then `executables`), `schemaVersion` 1. `Settings` mirrors it; `Store` wraps
`jsonsettings.Store[Settings]` (`Get`, `Update`, `Subscribe` current-on-subscribe, `Held`,
`Refused`). **A field's key is dotted inside its group** (`permissions.rules`); every `Field*`
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
- **Approval modes and always rules** (`permissions.go`): `ModeFor(context)` is read-only while
  `permissions.modes` is held, else the first matching entry, else `DefaultMode()` (`Ask` when
  unset). `Rules()` drops every `Allow` while `permissions.rules` is held, and adds
  `permissions.Refused`/`RefusedSecrets`. The checks (`folderRuleRefusal`, `secretRuleRefusal`
  and the rest) enforce the rule shapes; **a list element with a key its type does not name is
  refused**, since a dropped narrowing field would widen the rule. → [the permissions
  engine](../../../../docs/security/2026-10-02-the-permissions-engine.md).

**`Settings.Sandbox.Path` is the user's `PATH`, frozen** (`path.go`): a `PathEntry` per folder
in the shell's order, with `Dir`, `Target`, `State` (`adopted`, `pending`, `gone`), `Source`
(`shell`/`user`) and `Shared`. A state holds for `Target` alone. `filterPath` (`filter.go`) drops
what `sandbox.SearchFolder` refuses, `node_modules` and duplicates, and marks `Shared` a folder
writable by a non-administrators' group.

**`Service` (`service.go`) is the store plus the sync**, over `Zones` (`Never`, `Open`, `Home`),
the refresh's resolver and the launch's fault; on a machine with no sandbox it syncs nothing and
its path methods answer `ErrNoSandbox`. An entry is **open** when its `Target` is under an `Open`
Read path and no Deny path, compared by text over resolved lists. `SyncPath` reads the disk on a
goroutine bounded by `SyncTimeout` (5s) and diffs in one `Update` naming `sandbox.path`: a new
open, unshared entry is `adopted`, any other new one `pending`, a changed `Target` is new again,
and the shell's order wins. **While the field is held or `PathStrict` is set, every new entry is
`pending`.** A sync that changed the list starts a probe of every listed executable
(`StartProbe`), after the write and never holding it; a probe that fails is a warning, not a
startup error. `AdoptPath`
(Include) refuses `ErrPathChanged` when a refresh moved the entry since the user saw it;
`DropPath` (Remove) sets `gone`. `RunCheck` (`checkrun.go`) applies the filter and adoption rule
to one folder for a run. → [ADR: `PATH` is the login shell's, filtered and
frozen](../../../../docs/adr/2026-10-02-path-is-the-login-shells-filtered-and-frozen.md), [security
record](../../../../docs/security/2026-10-02-path-from-the-login-shell.md).

**A folder grant is a rule checked against the zones** (`folders.go`): `permissions.Rule.Folder`,
class 1 read or 2 read-write, matching no action. `CheckFolder` answers a `FolderRefusal`; the
rules are in the function and `TestCheckFolderRefusesEachRule`. **Every path compared is
canonical** (`canonical`, `filter.go`: links followed, each component spelled as its directory
lists it), since the zones are compared by text and macOS opens `/Users/ME` as `/Users/me`. **The
service holds the zones as a snapshot** taken by each `SyncPath`; checks re-resolve its paths but
never re-run the zones function. Every disk read goes through `readWithin`, bounded by
`SyncTimeout`. With no sandbox every folder is refused. → [ADR: a folder grant is a
rule](../../../../docs/adr/2026-10-04-a-folder-grant-is-a-rule.md), [security
record](../../../../docs/security/2026-10-04-path-grants.md).

**`Executables` is the user's registered executables** (`executables.go`), probed beside
`CuratedExecutables`. `CheckExecutable` is the one shape check, on register and on read-back. An
executable only widens what is probed, so the read-back drops a refused one and holds nothing.
