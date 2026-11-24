# Step 3: Cleanup

Removes the handling that existed because a folder nested in another by name could lead
elsewhere. Part of [the sequence](README.md), whose shared rules apply. It needs steps 1 and 2.

Paths are relative to `sidecar/internal/` unless rooted.

## What goes

After step 1 the fence is built from resolved paths, so a folder's name says where it is.

- **`run/sandbox`'s rule compile (`Policy.rules`)** denies a Kstack folder inside another with
  the one around it, by canonical path (`outermost`). On Linux the logs inside the state add no
  rule. `pathsOf` keeps every resolved folder in `bash.Paths.DeniedDirs`: the file tools and the
  grant zones compare folders as files, and a second name of a nested folder, such as a bind
  mount's source, matches only that folder's own entry. A `logs` linked to `/mnt/logs` is fenced
  at its target.
- **`Layout.Dirs` (`app/layout/layout.go`)** keeps dropping only an exact duplicate, and its
  comment stops giving links as the reason. `make clean-data` still reads it on the unresolved
  layout.
- **`holderOf` (`app/layout/make.go`)** is read on the resolved layout alone, and its comment
  says so.
- Every comment that explains a choice by "a link can lead it elsewhere" is rewritten to state
  the present design. Find them with `grep -rn -i 'link' sidecar/internal/app sidecar/config.go`.

The drop is the unsafe direction: an entry dropped by mistake is a folder left unfenced. So it
happens only where the deny is by path, in one policy whose canonical paths name no folder two
ways.

## What stays

- `run/sandbox`'s `Policy` still accepts a Kstack path nested in another
  (`TestANestedKstackPathIsAcceptedAndDenied`).
- `make clean-data` and `topDirs` (`sidecar/scripts/clean-dev-data.go`), which work by name.

## Tests

- The fence of each platform's layout holds every resolved folder, on real folders.
- A linked-out `logs` is in the fence, as its target.
- A policy compiles one deny for a Kstack folder inside another.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | `run/sandbox`: the drop in `Policy.rules`; `app`, `app/layout`: the comments. | Built |
| 2 | The docs below. | Built |

## When it lands

- **[`security-model.md`](../../security-model.md)**: the row for Kstack's directories no longer
  says each folder is kept when nested in another by name, and names this step's tests.
- **[`TODO.md`](../../TODO.md#security)**: this sequence's line goes.
- This is the sequence's last step. The `wip/folder-links` pull request deletes
  `docs/specs/folder-links/` when it lands.
