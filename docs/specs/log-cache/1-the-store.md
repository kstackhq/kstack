---
title: The store
scope: sidecar
status: Built
---

# The store

**Needs:** nothing in this sequence. **Unblocks:** steps 3, 4 and 5, which read and write the
file this step creates.

Go paths are under `sidecar/internal/`. The design is the README's §1 and §2; this file says
what to build and how to know it is built.

## In short

A new package, `services/logs/logstore`, owns one SQLite file per cluster cache at
`<cache>/logs/<cache id>.db`. It has the three tables, a manager that opens, clears and removes
files, and the `Retention` knobs. The file is created on first open, emptied when the user
clears the cluster's cache, and removed when the cache is removed. Nothing reads or writes a
line yet: this step is the shelf, not what goes on it.

## What is not in this step

- **No reads from `Fetch` or `Follow`.** They are unchanged, and `logs.New` does not take the
  manager yet; step 3 does that when something reads it.
- **No janitor sweep.** `Retention` exists with every knob the README names and
  `DefaultRetention` holds its numbers, but nothing takes them in this step: step 5 adds the
  sweep, gives `NewManager` a `Retention` and wires `DefaultRetention` in `app`.
- **No statements for later steps.** This step prepares what it needs to open, check and
  remove a file. Each later step adds the statements its reads and writes need, in its own
  change, so no SQL is written for a design that may still move.
- **No `Source`.** Step 2.

## Design

### 1. The package

`services/logs/logstore` is a package under the logs service, as `kubestore` is under the
cluster service, so the service's resolve and reads stay in `services/logs` and the file's
mechanics stay out of them. It imports `sqlitepool`, `sqlitemigrate` and `sqlstmt`, and nothing
of the logs service, which will import it.

- **`Manager`**, shaped like `kubestore.Manager`: `NewManager(dir)`,
  `OpenOrCreate(cacheID) (*Store, error)` refcounted, `Clear(cacheID) error`,
  `Remove(cacheID) error` and `Close() error`. It is a `lifecycle.Part`; its `Start` does
  nothing in this step.
  - **`Clear`** is `kubestore.Clear`'s shape: the entry is retired and the file deleted, a handle
    still held answers `ErrClosed`, the next open creates a fresh file under a new entry, and
    releasing an old handle never changes the new entry's count. No tombstone: the cache goes on.
  - **`Remove`** tombstones the id for the manager's life, closes the entry if held, and deletes
    the file. It is idempotent.
  - **After `Close`**, `Clear` and `Remove` still delete the file and open nothing. `Remove` must,
    since the deleting arm clears the finalizer on its answer and nothing else would delete the
    file.
- **`Store`**, one open file: the writer connection and the reader pool from `sqlitepool`, and
  `Close`. The `sqlstmt.Set` and its id type come in step 3 with the first statement, so this
  step declares no id type with no values. Every write a later step makes goes through
  `set.InTx`, and a statement run both inside a fill's write transaction and alone is declared
  `OnBoth`.
- **The schema**, `migrations/0001_init.sql`, exactly the README's §2: `streams`, `chunks`,
  `lines` with its `AUTOINCREMENT` id and `lines_position`, every table `STRICT`. The id must
  never be reused, since the follow reads by it, and the test below pins that.
- **`Retention`**: `Bytes`, `StreamShare`, `ReadTTL`, `ChunkBytes`, `ChunkSpan`, `TailGrace`,
  `WriteBatch`, `WriteBatchBytes`, `WriteTick`, `WriteQueue`, `VacuumPages`, `Interval`, each
  with a comment saying what it bounds, and `DefaultRetention` with the README's §7 numbers. The
  queue's worst case is `WriteQueue` times `WriteBatchBytes`, 16 MiB, and the comment on
  `WriteQueue` says so, since log text is the cluster's to write. A zero `Interval` runs no
  janitor.

### 2. The file's lifetime

- **`CacheFileOwner`** is a small interface in `services/cluster`:
  `Clear(cacheID int64) error` and `Remove(cacheID int64) error`. One owner, passed to
  `cluster.New`; a list is for a second owner, when there is one. The cache pass's deleting arm
  calls `Remove` after `kubestoreMgr.Remove` and before it clears `cacheFinalizer`, and a failed
  removal fails the pass as the mirror's does. `clusterCacheClear` calls `Clear` first, outside
  `RunWithCacheSyncStopped`; a log clear that fails errors before the mirror is touched, and the
  user runs it again. A crash between the two removals needs no sweep:
  the finalizer is still set, so the arm runs again at the next start. `cluster.New` refuses a
  nil owner, as it refuses every nil dependency; a test that builds it passes a fake or the no-op
  `noCacheFiles` in `testutil_test.go`, which `newTestDepsOver` uses.
- **Order.** `app` builds the manager before `cluster.New`, hands it in, and appends it to
  `Runtime.parts` before the cluster service, so the reverse close order closes the service
  whose cache pass calls the manager before the manager itself. Build order is part order.
- **Paths.** `app/paths.go` gains `LogsDir`, `<cache>/logs`, in the struct, its tree comment and
  `paths_test.go`.

## Tasks

1. **The package.** `logstore/store.go`, `migrations/0001_init.sql`, `retention.go`. Tests
   under *Tests*.
2. **The manager.** `manager.go`: refcount, `Clear`, tombstone, `Remove` twice, calls on a closed
   manager, `Close` with files open.
3. **The lifetime.** `CacheFileOwner` in `services/cluster`, the two calls, the order in `app`,
   `LogsDir` in `paths.go`.

## Tests

Go, beside the files they cover, each over `t.TempDir()`.

- `store_test.go`: a file opens `0600` under the directory and reopens; `STRICT` refuses a text
  in an integer column; `lines_position` refuses a duplicate position and `OR IGNORE` skips one;
  **ids are never reused**: insert lines, delete the newest, insert again, and the new id is
  above every id ever handed out.
- `manager_test.go`: two opens share one store and the second close releases it; `Clear` retires
  the open store, whose handle answers `ErrClosed`, deletes the file, and the next open creates a
  fresh one; **releasing the old handle after a `Clear` leaves the new entry's count alone**, so
  the new store stays open while anyone holds it; `Remove` of an open file closes it first;
  `Remove` twice succeeds; a removed id cannot be opened again; `Clear` and `Remove` after
  `Close` delete the file, open nothing and panic on nothing.
- `services/cluster`: the deleting arm calls a fake owner's `Remove` after the mirror's, and a
  failing owner fails the pass with the finalizer intact; `clusterCacheClear` calls its `Clear`.
- `app/paths_test.go`: `LogsDir` is under the cache directory.

## Security

**Widens:** nothing yet. The file exists and holds no line until step 3. It lives under
`<cache>/`, which the bash tool's `DeniedDirs` already covers and `Read`, `Write` and `Edit`
fence, so when lines arrive the containment is already in place. A user's clear of the cache
empties it, so clearing means what it says once lines are there.

**Residual:** none in this step.

## When it lands

- `sidecar/CLAUDE.md`: the directory table gains `logs/<cache id>.db` under `<cache>/`, the
  *Tools* paragraph on `services/logs` names `logstore` as its file, and the `clusterCacheClear`
  sentence says the log file goes with the rows.
- This README's row reads Built, with the pull request.

## Verification

Per [Verification commands](../README.md#verification-commands): `bash scripts/sandbox-dev-setup.sh`
first; `cd sidecar && go test ./internal/services/logs/... ./internal/services/cluster/...
./internal/app/...`; `make lint-go`, `make vet-go`; `make test-changed` while working.
