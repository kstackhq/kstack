---
title: The settings file
scope: sidecar
status: Landed
---

# The settings file

**Needs:** nothing beyond `main`. **Unblocks:** steps 2D, 3A and 3B, which each add fields, and
through them 4C, 4D, 6A, 6D and 7A.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today the sandbox has no settings of its own. [The note](../../notes/sandbox-credentials-and-permissions.md)
gives the user a list of things to decide — the frozen `PATH`, the approval modes and rules, the
hosts, the granted folders, the registered tools, the excluded credentials — and each needs a file.

After this step, **`securityconfig`** is that file: `<data>/security.json`, a `Store` that opens
it, reads it, writes it under one lock and publishes each write. `Settings` has no fields yet.
Each later step adds its own, and this step fixes how every field is read back and checked, so
the steps can land in parallel without meeting in the struct. Nothing the user sees changes.

## What is not in this step

- **No field.** The table in §2 says which step adds which.
- **No settings on the wire.** The `sandboxSettings` query is step 7A's; `sandboxPath` and the
  `permission…`, `network…` and `credential…` names are their steps'. This step's one query is
  `securityRefused`, what the file lost (§3).
- **No Settings section.** Each step that adds a field draws it.

## Design

### 1. `securityconfig.Store`

`securityconfig/store.go`. The file is `<data>/security.json`, 0600, written through
`atomicjson`. It lives in the data directory, which no sandboxed command reads
(`bash.Paths.DeniedDirs`), and it is not synced: it describes this machine. The `Store` is
modelled on `cloud/prefs` without its envelope:

```go
// Settings is the security settings. Each field is added by the step that
// needs it; see the table in the spec.
type Settings struct{}

// Store keeps Settings in one JSON file and publishes each write. Safe for
// concurrent use.
type Store struct{ /* the path, a mutex, the current value, a watch.Hub */ }

// Open reads file. A missing file is empty Settings; one that is not a JSON
// object is an error naming the file, since a sandbox with no settings is one
// the user cannot see.
func Open(file string, opts ...Option) (*Store, error)

// Refused is what Open left out of the file, each value with its reason, for
// the store's life.
func (s *Store) Refused() []Refusal

// Get is a copy of the current Settings.
func (s *Store) Get() Settings

// Update applies fn to a copy under the lock and saves it before returning;
// an error from fn saves nothing, and a result equal to the current value
// writes and publishes nothing. fn must not call the store: it runs under
// the lock. fields names, by JSON key, each field fn sets (§3).
func (s *Store) Update(fn func(*Settings) error, fields ...string) error

// Subscribe is a current-on-subscribe receiver, as prefs has; close it when done.
func (s *Store) Subscribe() *watch.Receiver[Settings]
```

`Get` and each `Send` copy, so no caller reaches the store's value. The copy is a JSON round
trip (`clone` in `store.go`): later fields add slices and maps, and a copy that needs no line per
field cannot miss one. `Settings` holds only JSON-safe values, so the round trip cannot fail, and
`clone` panics if it does. An empty slice or map comes back nil, since `omitempty` leaves it out. `gochan/watch` hands one sent value to every receiver, so receivers share a
delivery: a receiver treats it as read-only, and `Get` is the way to a copy it can change.
**Equal means the same JSON**: `Update` marshals the result and compares the bytes with the
current value's. `cloud/prefs` compares with `reflect.DeepEqual`, which tells a nil slice from an
empty one and would make an `Update` that changes nothing write. A fresh file is written by the first `Update` that
changes something, not by `Open`. The receiver is `gochan/watch`'s, which holds the latest value:
a slow receiver sees the newest `Settings` and may skip one between.

`atomicjson.Load` answers a parse error without the path, so `Open` wraps it with the file's
name. `Option`'s one constructor, `WithChecks`, is the test seam §3 uses. It is exported because
`graph`'s test sits in another package; production passes none.

### 2. The fields, by step

| Field | Step | On a refused value |
| --- | --- | --- |
| `Path` | 3A | fails open |
| `DefaultMode`, `Modes`, `Rules` | 3B | fails closed |
| `Credentials` | 2D | fails closed |
| `Hosts` | 4C | fails closed |
| `Folders` | 4D | fails open |
| `Tools` | 6A | fails open |
| `Monitor` | 6D | fails open |
| `Onboarded` | 7A | fails open |

**A hand edit may cost a permission, never a restriction.** A field that only grants **fails
open**: a refused value is dropped, which grants less. A field that restricts **fails closed**: a
refused value makes the field answer its most restrictive state until the user fixes the file,
never its zero value — for example every credential excluded, the most restrictive mode, and no
host allowed but the built-ins. The step that adds a restricting field names that state and adds
one line to `strictest` (§3); a field not listed there fails open.

Every field is left out of the file when empty, so the file holds only what the user set: a
slice, a map or a string is `omitempty`; a struct, which `omitempty` never leaves out, is
`omitzero`. A file written before a step landed reads under it, since a missing key decodes to
the zero value.

**The file carries its layout's version.** Every write stamps `"schemaVersion"`: the store's
`schemaVersion`, 1 in this step, or the file's own when it is higher, since a newer Kstack wrote
it and this build cannot upgrade what it does not know. A step that changes a field's layout —
a rename, a split, a list of strings become a list of objects — bumps it and upgrades an older
file in `Open`, before decoding; a step that only adds a field does not.

### 3. Read-back

Every field follows one convention, which its step's tests pin. `securityconfig/check.go` holds
`checks`, a slice of `func(*Settings) []Refusal`, one per field, empty in this step; each field's
step adds its line. A check removes from the `Settings` every value it refuses and answers one
`Refusal` per value. A `Refusal` is the field, the value — what a row in that field's Settings
section would name it by (a rule's text, a folder's path), else its JSON — and the reason, in
the user's words. The store runs the slice it was given, `checks` in production and a test's own through an
unexported option, which is how this step's tests exercise the convention before any field has a
check.

**A check reads the value alone**: its shape, never the disk or another service. A rule that
needs either — step 4D's folder that must exist, step 2D's profile that must be one `Discover`
found — is its step's, run where the step says (the mutation, or a run's start), and shown by
that step beside what `Refused()` lists.

- **Decoding is per field, and a list per element.** `Open` reads the file as a
  `map[string]json.RawMessage` and decodes each key into the `Settings` field whose key is the one
  `encoding/json` writes it under: its JSON name, else its Go name, never for a field tagged `-`
  or unexported. A list decodes element by element, so one bad element is refused alone, as one
  `Refusal` with the element's raw JSON as its value, and its siblings load. Any other value that
  does not decode into its field's type — a string where a list belongs — is refused whole, with
  the field's raw JSON as its value; a struct field is one key, so one bad member refuses the
  whole struct. The other fields load either way. A key no field names is ignored and kept: every
  write puts it back as read, so a write by an older Kstack keeps a setting a newer one wrote.
  Only a file that is not a JSON object fails `Open`, `null` included. So a hand edit
  can cost a value, never the app.
- **A refused value of a restricting field fails closed.** `check.go` holds `strictest`, a map
  from a restricting field's JSON key to a `func(*Settings)` that sets it to its most restrictive
  state, empty in this step. After decoding and the checks, every field with a refusal and a line
  in `strictest` is set to that state, whatever else it loaded, and the store keeps the field's
  raw JSON from the file. A field with no line keeps what loaded, less what was refused.
- **On `Open`**, every refused value is logged as one line and kept on the store: `Refused()`
  lists them for the store's life, so a Settings section can show the value and its reason. A
  file refused in part still opens.
- **The next write keeps a refused restriction and drops the rest.** An `Update` writes each
  field's JSON, except that a restricting field whose raw JSON the store keeps is written as that
  raw JSON, so the restriction stays in the file until the user fixes it. An `Update` whose result
  changes that field, or that names it in `fields` — the user's fix through its Settings section —
  writes the new value, and the store stops keeping the raw one. The fix can be the strictest state
  the field already answers, which changes nothing in memory, so a Settings section's mutation
  names the field it writes. A refused value of a field that fails open is dropped by the
  next `Update` that writes, of any field; `Refused()` still lists it until the sidecar restarts.
  That loss is deliberate: the same check would refuse the value at every launch, dropping it
  grants less, and the log line and this launch's Settings section are where the user learns of
  it. Equality is the file's JSON: an `Update` that leaves it as it is writes and publishes
  nothing.
- **On the wire**, `securityRefused: [SecurityRefusal!]!` answers `Refused()`, with
  `type SecurityRefusal { field: String!, value: String!, reason: String! }`, `field` being the
  JSON key. It is a query, not a watch, since the list is fixed at `Open`. Each later step's
  Settings section reads it and draws the entries of its own field, so no step's own query
  carries refusals, and steps 2D, 3A and 3B, landing in either order, need nothing of each
  other to show them.
- **On `Update`**, the same check runs over the result, and a refusal is the error: nothing is
  written, and the caller answers `KSTACK_VALIDATION_ERROR` with the reason.

### 4. The app opens it

`app.paths` gains `SecurityFile`, `<data>/security.json`. `app.New` opens the store right before
`app.db`, on every platform, and closes nothing for it: the store holds no handle, so a failed
`Open` fails `New` with nothing to close.
`graph.Resolver` gains `SecurityCfg *securityconfig.Store`, never nil, like every resolver field
(named for what it holds, since the store is not a service);
its one resolver in this step is `securityRefused`. A resolver that answers *no sandbox* keys on
the machine's sandbox status (step 1B's `SandboxStatus`), not on the store. On native Windows the
store opens like anywhere else, since step 7A's `Onboarded` flag needs a home there.

## Decisions this step asks for

1. **A restricting field fails closed.** One bad value in a field that restricts — a rule, an
   exclusion, a mode, a host entry — could otherwise leave the whole field at its zero value, and
   the next unrelated write would delete the restriction from the file for good. So each field is
   marked in §2: one that only grants drops a refused value; one that restricts answers its most
   restrictive state while the file holds a refused value, and the store never rewrites that
   field's raw JSON until a write of the field itself replaces it. A list decodes per element, so
   one bad element of a field that grants costs that element alone. Recommended; the rule lets
   steps 2D, 3B and 4C say "fails closed" and add one `strictest` line each.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `securityconfig`: `Settings`, `Store`, per-field decoding, `check`, `Refusal` | `securityconfig/store.go`, `securityconfig/check.go`, their tests | — | Done |
| 2 | `SecurityFile`; `app.New` opens the store; `Resolver.SecurityCfg`; `securityRefused` | `app/paths.go`, `app/app.go`, `sidecar/graph/resolver.go`, `sidecar/graph/schema.graphqls`, generated code, their tests | 1 | Done |
| 3 | Docs, per *When it lands* | see there | 1, 2 | Done |

**Order:** 1, then 2, then 3.

## Tests

**`securityconfig`**

- `TestTheStorePersists`: what `Update` writes is what a reopen reads; a missing file opens empty.
- `TestTheFileIsOwnerOnly` (`store_unix_test.go`): the file lands 0600.
- `TestABadFileFailsOpen`: a file that is not a JSON object — `{`, `[]`, `null` — is an error
  naming the file.
- `TestAFieldOfTheWrongTypeIsRefusedAlone`: over a test's `Settings` with two fields, a file
  whose first holds the wrong JSON type opens with the second loaded and one `Refusal` for the
  first; an unknown key is ignored; a key spelled in another case is unknown.
- `TestAnEqualUpdateWritesNothing`: an `Update` that swaps a nil slice for an empty one writes
  and publishes nothing.
- `TestUpdateIsOneWrite`: one `Update` is one save, an error from `fn` saves nothing, an
  `Update` that changes nothing saves nothing, and of two concurrent `Update`s the later sees the
  earlier's result.
- `TestSubscribeSeesTheLatestWrite`: a receiver gets the current value on subscribe, then the
  value after an `Update`, a copy the test can change without changing the store's.
- `TestARefusedValueIsLeftOutAndListed`: over a test's check that refuses the first time it runs,
  on a field that fails open, `Open` logs the refusal and lists it in `Refused()`; an `Update`
  over a check that refuses is an error that writes nothing; the next `Update` that writes drops
  the value from the file, and `Refused()` still lists it. Each field's
  step adds its own case over its real check (step 3B's `TestABadRuleIsLeftOutWithItsReason`).
- `TestOneBadElementIsRefusedAlone`: a list with one element of the wrong type loads its other
  elements, and the one `Refusal` carries the bad element.
- `TestARefusedRestrictionFailsClosed`: over a test field in `strictest`, a bad element, a value
  of the wrong type and a value a check refuses each load the field as its most restrictive
  state.
- `TestAnUpdateKeepsARefusedRestrictionInTheFile`: an unrelated `Update` leaves the field's raw
  JSON on disk and the field at its strictest state; an `Update` of the field itself writes the
  new value.
- `TestAnUpdateNamingARefusedRestrictionWritesItsStrictestState`: an `Update` that sets the field
  to the strictest state it already answers keeps the raw JSON unless it names the field, and
  naming it writes and publishes that state.
- `TestAWriteStampsTheVersion`: a write stamps `schemaVersion` 1 into a new file and into one
  with no version, and keeps a newer file's higher one.
- `TestAWriteKeepsTheKeysNoFieldNames`: a key no field names, and one spelled in another case,
  is not refused and is written back as read.
- `TestDecodeReadsTheKeyASaveWrites`: a field with no JSON name decodes from its Go name, and a
  field tagged `-` or unexported decodes from nothing.
- `TestUpdateKeepsItsOwnCopy`: a slice the `Update` callback still holds cannot change the stored
  value afterwards.

**`graph`**

- `TestSecurityRefusedIsWhatOpenLeftOut`: over a store opened with a test's refusing check, the
  query answers each refusal's field, value and reason.

**`app`**

- `TestABadSecurityFileFailsNew`: `New` over a `SecurityFile` that is not a JSON object fails naming the
  file, before `app.db` opens. The store opens whatever the sandbox's probe finds, since
  nothing in `app` keys on it.

## Security

No boundary moves. The file lives in the data directory, which a sandboxed command cannot read,
so nothing a sandboxed command runs can read or change what the user decided. A command outside
the sandbox, and every command on native Windows, runs as the user and can, as it can `app.db`;
it asks first, and the store reads the file only at start, so a change made under it is seen at
the next launch or overwritten by the next `Update`. `security-model.md`'s
owner-only files row gains `security.json`, pinned by `TestTheFileIsOwnerOnly`. A malformed
value never loosens a restriction: a restricting field fails closed and keeps its raw JSON in the
file (§2, §3), pinned by `TestARefusedRestrictionFailsClosed` and
`TestAnUpdateKeepsARefusedRestrictionInTheFile`. An older Kstack does not apply a setting a newer
one wrote, but keeps it in the file, so going back and forward loses no restriction, pinned by
`TestAWriteKeepsTheKeysNoFieldNames`. No security record.

## When it lands

- **`security-model.md`**: `security.json` joins the owner-only files row.
- **`sidecar/CLAUDE.md`**: `securityconfig` under `internal/` (the file, the `Store`, the
  read-back convention, per-field decoding, `securityRefused`, and that each field's step names
  it); `SecurityFile` in `app.paths`'
  tree comment; `Resolver.SecurityCfg`.
- **The sequence's README**: this row's status.

## Verification

Run the [verification commands](../README.md#verification-commands), including the wire checks.

By hand, `pnpm tauri dev` on any platform: write `{` into `<data>/security.json` and relaunch,
and the sidecar fails to start naming the file; remove it and relaunch clean.
