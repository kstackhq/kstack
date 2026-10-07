---
title: Keep the user's settings in one file, grouped by area
date: 2026-10-07
scope: sidecar
status: Accepted
---

# Keep the user's settings in one file, grouped by area

## Context

The sidecar kept its user settings in `<data>/security.json`, owned by
`internal/services/securityconfig`: the frozen `PATH`, the permission modes and rules (folder
grants among them), the registered executables and the onboarding flag. The keys were flat, one
per field. Neither word of the name fit: the onboarding flag gates nothing, the executables are a
probe list, and the package is a service with operations and a watch, not a config read once.

The store under it refuses and holds a value per field and per list element: a value it cannot
read is left out, and for a field that restricts, the field answers its strictest state while the
file keeps the raw value ([the permissions engine](2026-10-02-permissions-are-classes-modes-and-rules-decided-at-the-proxy.md)).
That store knew nothing of sandboxes or permissions.

Nothing had shipped, so the file's name and layout could change without a migration.

## Decision

The file is `<data>/settings.json`, owned by `internal/services/settings`. Its keys are grouped
by area, one object per area:

```json
{
  "schemaVersion": 1,
  "sandbox": { "path": [], "pathResolved": false, "pathStrict": false },
  "permissions": { "defaultMode": "", "modes": [], "rules": [] },
  "executables": [],
  "onboarded": false
}
```

The generic store moves to `internal/lib/jsonsettings`. A field of struct type is a group: an
object in the file whose fields are decoded, refused and held one by one, each under its dotted
key (`permissions.rules`). `Held`, `Update`'s `fields` and a `Refusal`'s `Field` name that key, and
so does the wire (`settingsRefused`, `PermissionSettings.held`, `permissionDiscardRefused`). A
group that is not a JSON object fails `Open`, as a file that is not one does. `schemaVersion`
stays 1.

The settings stay a JSON file, not rows in `app.db`. Per-record state that must commit beside a
row, such as a chat's grants, is already in `app.db`.

## Alternatives considered

**Hold a group as one key.** The decode would stay flat, but one bad rule would hold the modes
beside it, so a hand edit to a rule would make every context read-only.

**Rows in `app.db`.** Rows bring a watch, transactions and one schema. But the file is the
user's security policy: it is meant to be read, diffed and edited by hand without Kstack, and
refuse-and-hold exists for exactly that. A row has no hand edit to refuse.

**Keep the flat layout and rename later.** Two breaks of an unshipped file instead of one.

## Consequences

A new field joins its area's group, or a new group, and keeps per-field refusal without any code
in the store. A key a group does not name is kept on write, as a top-level one is, so an older
Kstack keeps a newer one's setting inside a group too.

A Go field of struct type can no longer be a single value of the file: the store reads it as a
group. A setting that is one structured value has to be a list element or a group of its own.

## Revisit when

Settings sync, or a setting that must commit in one transaction with a row in `app.db`.
