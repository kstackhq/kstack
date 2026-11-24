# Step 3: Cleanup

Removes the handling that existed because a folder nested in another by name could lead
elsewhere. Part of [the sequence](README.md), whose shared rules apply. It needs steps 1 and 2.

Paths are relative to `sidecar/internal/` unless rooted.

## What goes

After step 1 the fence is built from resolved paths, so a folder's name says where it is.

- **`pathsOf` (`app/paths.go`)** drops a folder from `bash.Paths.DeniedDirs` when its resolved
  path is inside another's (`realpath.Relation`). On Linux the logs inside the state add no entry.
  A `logs` linked to `/mnt/logs` keeps its own. Settings' *Never readable* list shrinks to match.
- **`Layout.Dirs` (`app/layout/layout.go`)** keeps dropping only an exact duplicate, and its
  comment stops giving links as the reason. `make clean-data` still reads it on the unresolved
  layout.
- **`holderOf` (`app/layout/make.go`)** is read on the resolved layout alone, and its comment
  says so.
- Every comment that explains a choice by "a link can lead it elsewhere" is rewritten to state
  the present design. Find them with `grep -rn -i 'link' sidecar/internal/app sidecar/config.go`.

The drop is the unsafe direction: an entry dropped by mistake is a folder left unfenced. So it
uses [the identity rule](README.md#one-folder-is-inside-another-by-identity), and only for two
folders that both exist. Anything else keeps its entry.

## What stays

- `run/sandbox`'s `Policy` still accepts a Kstack path nested in another
  (`TestANestedKstackPathIsAcceptedAndDenied`). It is no longer handed one by `pathsOf`, and a
  policy is not the place to refuse it.
- `make clean-data` and `topDirs` (`sidecar/scripts/clean-dev-data.go`), which work by name.

## Tests

- The fence of each platform's layout holds no folder inside another, on real folders.
- A linked-out `logs` is in the fence, as its target.
- On Windows, in a case-sensitive directory, logs under `state` keep their entry beside a state
  folder named `State`. The test takes the file identity as a parameter if CI's runner cannot
  make such a directory.
- A folder that does not exist yet keeps its entry.

## Tasks

All tasks start Planned.

| # | Task | Status |
| --- | --- | --- |
| 1 | `app`, `app/layout`: the drop in `pathsOf`, and the comments. | Planned |
| 2 | The docs below. | Planned |

## When it lands

- **[`security-model.md`](../../security-model.md)**: the row for Kstack's directories no longer
  says each folder is kept when nested in another by name, and names this step's tests.
- **`sidecar/CLAUDE.md`**, *Directories*: the fence holds each resolved folder once.
- **[`TODO.md`](../../TODO.md#security)**: this sequence's line goes.
- This is the sequence's last step. The `wip/folder-links` pull request deletes
  `docs/specs/folder-links/` when it lands.
