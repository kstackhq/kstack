# Spec: gate Go coverage on Linux, macOS and Windows

Answers [#415](https://github.com/kstackhq/kstack/issues/415). Read `docs/specs/README.md`
first, then root `CLAUDE.md` (testing conventions) and `sidecar/CLAUDE.md` (the "Coverage is
gated" paragraph). All paths below are rooted at the repository unless they start with `sidecar/`.

## Problem

`Go · Coverage` runs `sidecar/scripts/coverage.go` on `ubuntu-24.04` alone. A file that builds
only on macOS or Windows (`sandbox_darwin.go`, `bash_windows.go`, every `_windows.go` and
`_darwin.go` file, and the `!linux` fallbacks) is never compiled by the gate. It is not counted as
uncovered; it is absent. Its coverage can fall to zero without CI noticing.

Adding the files to one repo-wide number is not enough. They are about 2,170 of 77,000 source
lines (the gate counts statements, a smaller number, but the proportion is the same), roughly
the headroom above the current 97.02% threshold, so a macOS file at 50% moves the repo-wide
number by half a point. They need their own threshold.

## What this builds

1. `coverage.go` gains two modes: write one OS's merged profile to a file, and gate a set of
   profiles from several OSes.
2. CI runs the suite on three runners, one per OS, uploads each profile, and one job merges them
   and gates on two thresholds: repo-wide, and the blocks Linux does not compile.
3. The local `make cover-go` keeps its single-OS behaviour.

Nothing else changes. No test moves, no build tag changes, no new tooling.

## Facts the implementation relies on

Verify each before relying on it; they were true when this was written.

- **Every platform split in the sidecar is by OS, none by architecture.** Check with
  `grep -rh '^//go:build' --include='*.go' sidecar | sort | uniq -c`. You will see `windows`,
  `!windows`, `darwin`, `!darwin`, `linux`, `!linux`, `unix`, `unix && !darwin`, `debug`,
  `!debug`, `ignore`, `tools`. No `amd64` or `arm64`. So one runner per OS sees every file.
- **Go writes profile paths as import paths, with forward slashes, on every OS.** A line looks
  like `github.com/kstackhq/kstack/sidecar/internal/app/app.go:10.2,12.3 2 1` on Windows too.
  Profiles from three OSes merge by string equality.
- **With `-coverpkg=./...`, every package's test binary reports every block of every compiled
  file.** So the Linux profile holds every block of every file Linux compiles, covered or not. A
  block absent from the Linux profile is in a file Linux does not compile. That is the whole
  definition of the "outside Linux" set; no file list is kept.
- **`coverage.go` already unions two profiles** (untagged and `-tags debug`) per block: a block
  is covered if any run covered it. Adding more profiles to the union is the same loop.
- **The macOS runner label `xcode-27` and the Windows label `windows-2025-vs2026` already run the
  whole suite** in `Go · Build and Test` (`.github/workflows/ci.yml`, anchor `full-matrix`). The
  suite passes there. macOS 27 is chosen because it is what users run; nothing in the sidecar
  branches on the macOS version, so any macOS runner would produce the same set of blocks.
- **macOS needs `KSTACK_REQUIRE_SANDBOX=1`** or the Seatbelt tests skip and the macOS sandbox
  file, the main reason for this spec, reads as uncovered. `setup-sandbox: "true"` on
  `.github/actions/setup-environment` sets it on macOS and Linux and does nothing on Windows.
- **Repo-wide coverage may drop on the first run**, because the new files enter the
  denominator. That is expected. `sidecar/CLAUDE.md` says a lowered threshold must say why in
  the commit body; the reason is "the denominator now includes the macOS and Windows files".

## Task 1 — `sidecar/scripts/coverage.go`: two new modes

Status: Planned.

Keep the file a single `package main` under `//go:build ignore`, run with `go run`. Keep every
existing behaviour of the no-flag invocation byte for byte: it is what `make cover-go` runs.

### Flags

| Flag | Meaning |
| --- | --- |
| `-report` | Existing. List every file with uncovered statements. Works in every mode. |
| `-out <path>` | New. Run the suite (both tag sets), union them, write the union to `<path>` as a cover profile, print this OS's percentage, exit 0. **Do not gate.** |
| `-linux <path>`, `-darwin <path>`, `-windows <path>` | New. Run no tests. Read the three profiles, union them, gate on two thresholds. All three are required together; any one without the other two is a usage error (exit 2). |

`-out` together with any of `-linux`/`-darwin`/`-windows` is a usage error.

Mode is decided once at the top of `main`:

```go
switch {
case *linux != "" || *darwin != "" || *windows != "":
    gate(*linux, *darwin, *windows, *report)   // all three non-empty, else usage error
case *out != "":
    measure(*out)                              // run suites, write union, no gate
default:
    check(*report)                             // today's behaviour
}
```

Three small functions, one per mode, each calling shared helpers. Do not nest the modes in one
function with flags checked throughout.

### `runSuite`

Add `-timeout 30m` to the `go test` arguments. The default is 10 minutes per test binary, and
the coverage run is the slowest configuration of the suite (no `-short`, atomic mode, every
package instrumented, run twice). Nothing else in `runSuite` changes.

### Shared state

Keep the existing maps and add one:

```go
stmts   map[string]int   // block -> statement count, block is "<file>:<range>" with modulePath trimmed
covered map[string]bool  // block -> any profile covered it
onLinux map[string]bool  // block -> the Linux profile contains it (gate mode only)
```

### `mergeProfile`

Keep its contract: trim `modulePath`, skip generated files, OR the covered bit. Make two changes:

1. **Remove `defer os.Remove(path)` from `mergeProfile`.** The callers that produce a temporary
   file (the default mode and `-out`) remove it themselves right after merging. Gate mode must
   not delete the artifacts it was handed.
2. Add a parameter, or a second thin function, so gate mode can also record `onLinux[block] = true`
   for every block in the Linux profile. The simplest shape: `mergeProfile(path, stmts, covered,
   seen map[string]bool)` where `seen` may be nil, and when non-nil every block read is marked in
   it. Gate mode passes `onLinux` for the Linux profile and `nil` for the other two.

### Writing a profile (`-out`)

After the union of the two suite runs, write `<path>`:

- First line: `mode: atomic`.
- Then one line per block in `stmts` with `n > 0`, in sorted block order (sorted so two runs
  produce identical files): `modulePath + block`, a space, the statement count, a space, then
  `1` if `covered[block]` else `0`.

Prepending `modulePath` makes the file a valid profile `go tool cover -func` can read, and makes
gate mode's `mergeProfile` (which trims the prefix again) read it back unchanged. Generated files
are already dropped, so they are absent from the written file.

Print one line after writing: `coverage: %.2f%% of %d statements (this OS alone)`.

### Gating (`-linux -darwin -windows`)

1. Merge the Linux profile with `seen = onLinux`, then macOS and Windows with `seen = nil`.
2. Walk `stmts` as the current code does. Compute two tallies:
   - **all**: every block with `n > 0`.
   - **outside Linux**: blocks with `n > 0` and `!onLinux[block]`.
3. If the outside-Linux tally has zero statements, print `coverage: no statements outside linux
   measured` to stderr and exit 1. It means a profile is missing or empty.
4. Read two thresholds. Generalise the existing `threshold()` to `threshold(name string)` reading
   `filepath.Join("scripts", name)`:
   - `coverage-threshold` for all (the existing file).
   - `coverage-threshold-platform` for outside Linux (new file, same one-number format).
5. With `-report`, list uncovered files as today, and append ` (outside linux)` to a file whose
   blocks are all outside Linux. A file is entirely on one side or the other, because build
   constraints are per file.
6. Print exactly two lines to stdout, in this order. The numbers here are illustrative; the
   first run supplies the real ones (Task 5).

   ```
   coverage: 97.12% of 71234 statements (threshold 97.10%)
   coverage outside linux: 88.40% of 1801 statements (threshold 88.40%)
   ```

7. Exit 1 if either number is below its threshold, after printing both lines and one stderr line
   per failing gate. Keep the existing `pct+1e-9 < want` comparison. The stderr lines name the
   file each gate reads, as today's message does:

   ```
   coverage below threshold; add tests or lower scripts/coverage-threshold deliberately
   coverage outside linux below threshold; add tests or lower scripts/coverage-threshold-platform deliberately
   ```

### Manual check for gate mode

Write these three files in a scratch directory and run gate mode from `sidecar/` with the two
threshold files temporarily edited as described. Restore them afterwards.

`linux.out`:

```
mode: atomic
github.com/kstackhq/kstack/sidecar/a/a.go:1.1,2.2 2 1
github.com/kstackhq/kstack/sidecar/a/a.go:3.1,4.2 2 0
github.com/kstackhq/kstack/sidecar/a/a_linux.go:1.1,2.2 1 1
```

`darwin.out`:

```
mode: atomic
github.com/kstackhq/kstack/sidecar/a/a.go:1.1,2.2 2 0
github.com/kstackhq/kstack/sidecar/a/a.go:3.1,4.2 2 1
github.com/kstackhq/kstack/sidecar/a/a_darwin.go:1.1,2.2 4 1
github.com/kstackhq/kstack/sidecar/a/a_darwin.go:3.1,4.2 4 0
```

`windows.out`:

```
mode: atomic
github.com/kstackhq/kstack/sidecar/a/a.go:1.1,2.2 2 1
github.com/kstackhq/kstack/sidecar/a/a.go:3.1,4.2 2 0
github.com/kstackhq/kstack/sidecar/a/a_windows.go:1.1,2.2 3 1
```

Expected: the union covers both `a.go` blocks (one OS each), `a_linux.go` 1 of 1, `a_darwin.go`
4 of 8, `a_windows.go` 3 of 3.

| Tally | Hit / total | Printed |
| --- | --- | --- |
| all | 12 / 16 | `75.00%` |
| outside linux | 7 / 11 | `63.64%` |

- With `coverage-threshold` = `75.00` and `coverage-threshold-platform` = `63.63`: exit 0.
- With `coverage-threshold-platform` = `63.64`: exit 1. 7/11 is 63.6363…, below 63.64. This is
  why a threshold is always set to the printed number **rounded down** to two decimals.
- With `-report`: `a_darwin.go` is listed with ` (outside linux)`; `a.go` is not listed (fully
  covered in the union).
- Omit `-windows`: usage error, exit 2, nothing merged.
- The three input files still exist afterwards.

### Manual check for `-out`

From `sidecar/`, `go run scripts/coverage.go -out /tmp/x.out` (any writable path) runs both
suites, writes the file, prints one line, exits 0 even if the number is below the threshold.
`go tool cover -func=/tmp/x.out | tail -1` prints a total. The first line of the file is
`mode: atomic`. No line starts with `graph/generated.go` or contains `grpc/authpb/`.

### Manual check for the default mode

`make cover-go` and `make cover-go ARGS=-report` behave exactly as before this change.

## Task 2 — `sidecar/scripts/coverage-threshold-platform`

Status: Planned.

Create the file containing one line: `0`. It is set to the measured number in Task 5. Same
format as `sidecar/scripts/coverage-threshold`: one decimal number, trailing newline.

## Task 3 — `.github/workflows/ci.yml`: three measuring jobs and one gate

Status: Planned.

Replace the existing `go-coverage` job with two jobs. Nothing else in the workflow references
`go-coverage` (check with `grep -n go-coverage .github/workflows/*.yml`).

### `go-coverage` (matrix, measures)

```yaml
  go-coverage:
    name: Go · Coverage · ${{ matrix.os }}
    needs:
      - go-lint
      - go-vet
    strategy:
      fail-fast: false
      # One runner per OS: every platform split in the sidecar is by OS, none
      # by architecture, so three profiles hold every file. macOS 27 is the
      # release users run; nothing in the sidecar branches on the macOS version.
      matrix:
        include:
          - os: linux
            runner: ubuntu-24.04
          - os: darwin
            runner: xcode-27
          - os: windows
            runner: windows-2025-vs2026
    runs-on: ${{ matrix.runner }}
    # Twice the two suite runs' -timeout, so a hang fails the test binary with
    # a goroutine dump before the runner kills the job.
    timeout-minutes: 60
    steps:
      # The whole history, for the same reason as Go · Build and Test.
      - uses: actions/checkout@v6
        with:
          fetch-depth: 0
      - id: setup
        uses: ./.github/actions/setup-environment
        with:
          setup-go: "true"
          # macOS skips its Seatbelt tests without KSTACK_REQUIRE_SANDBOX=1.
          setup-sandbox: "true"
          go-cache-key: ${{ github.job }}-${{ matrix.runner }}
          go-cache-mode: ${{ env.CACHE_MODE }}
      - name: Date files by their last commit
        run: go run scripts/restore-mtime.go
      # Built as scripts/build-sidecar.go ships it, so the run tests the
      # pure-Go os/user and net resolver the release uses. A cached test
      # replays its coverage with it. SSH_AUTH_SOCK is pinned as in Go · Build
      # and Test, since its value keys the login shell's cached results.
      - name: Measure coverage
        working-directory: ./sidecar
        env:
          CGO_ENABLED: "0"
          SSH_AUTH_SOCK: ""
        run: go run scripts/coverage.go -out ${{ runner.temp }}/coverage-${{ matrix.os }}.out
      - name: Upload the profile
        uses: actions/upload-artifact@v7
        with:
          name: go-coverage-${{ matrix.os }}
          path: ${{ runner.temp }}/coverage-${{ matrix.os }}.out
          retention-days: 7
      # After the profile is written, so a failed job leaves the last cache the newest.
      - name: Save Go cache
        if: env.CACHE_MODE != 'use'
        uses: actions/cache/save@v6
        with:
          path: ${{ steps.setup.outputs.go-cache-path }}
          key: ${{ steps.setup.outputs.go-cache-key }}
```

Notes for the implementer:

- `restore-mtime.go` and `coverage.go` are Go, so they run on Windows as written. Do not add
  `shell: bash` to those steps; the default shell on each runner runs a plain `go run`.
- `runner.temp` is outside the module, so the profile never enters Go's test cache key and no
  test that lists `sidecar/` sees it.
- The go-test job already runs the suite under these labels, but with cgo. This is the first
  time macOS and Windows run the suite with `CGO_ENABLED=0`, the release configuration. If the
  coverage run fails on one OS, read that OS's log before touching anything. A test that passes
  with cgo and fails without it is a real finding about the release build, not flakiness or a
  workflow problem. File it as an issue; do not work around it in this spec.
- Go's default per-binary test timeout is 10 minutes. The coverage run passes no `-short`, uses
  `-covermode=atomic` with `-coverpkg=./...`, and runs the suite twice, so a slow package can hit
  that default on Windows where the stress workflow needs `-timeout 90m`. `runSuite` passes
  `-timeout 30m` (Task 1) and the job has `timeout-minutes: 60`, so a package that still times
  out is a hang, not the default limit.
- `SSH_AUTH_SOCK: ""` is new to the Linux leg, so its test-cache key changes and the first run
  replays nothing. Expect that run to be slow; later runs replay as before.

### `go-coverage-gate` (gates)

Name it `Go · Coverage`, the name the single job had, so the check list on a PR reads as before.
No ruleset on `main` requires a coverage check (they require DCO and the `CodeQL · *` checks
alone), so a red `Go · Coverage` does not block a merge. That is true today too; this spec does
not change it.

```yaml
  go-coverage-gate:
    name: Go · Coverage
    needs: go-coverage
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v6
      - uses: ./.github/actions/setup-environment
        with:
          setup-go: "true"
      - uses: actions/download-artifact@v8
        with:
          pattern: go-coverage-*
          path: ${{ runner.temp }}/coverage
          merge-multiple: true
      # One union over the three OSes: a block counts as covered if any OS
      # covered it. The second line gates the files Linux does not compile,
      # which are too few to move the first.
      - name: Check coverage against the thresholds
        working-directory: ./sidecar
        run: >-
          go run scripts/coverage.go -report
          -linux ${{ runner.temp }}/coverage/coverage-linux.out
          -darwin ${{ runner.temp }}/coverage/coverage-darwin.out
          -windows ${{ runner.temp }}/coverage/coverage-windows.out
```

This job needs no Go cache: it compiles one script and runs no tests.

## Task 4 — comments and docs

Status: Planned.

- **`Makefile`**, the comment above `cover-go`. Replace the two lines with: the target measures
  the OS it runs on, so its number is not CI's; CI measures Linux, macOS and Windows and gates
  their union in `Go · Coverage`. Keep the recipe unchanged.
- **`sidecar/CLAUDE.md`**, the "Coverage is gated" paragraph. State what is true after this
  spec: CI runs the suite on one runner per OS, merges the three profiles, and gates on two
  thresholds, `scripts/coverage-threshold` for everything and
  `scripts/coverage-threshold-platform` for files Linux does not compile. Keep the rule about
  raising and never silently lowering, and say it applies to both files. Remove the sentence that
  says a `_windows.go` file never enters the denominator. Keep the list of what stays uncovered.
- **Root `CLAUDE.md`**, testing conventions: the sentence "Raise a coverage threshold when CI
  reports the number rose" already covers both Go files. No change unless the wording reads as
  one file per language.
- **`.github/workflows/ci.yml`**: the comments are in the YAML above. Make sure no comment left
  elsewhere says coverage runs on one OS. `Go · Vet`'s comment "The Windows runners run tests
  alone, so this is the one place the _windows.go files are vetted" stays true (vet is not
  coverage).

Follow the writing standards in root `CLAUDE.md`: describe the present, no "used to", no
"formerly", no history of this change.

## Task 5 — set the thresholds from the first run

Status: Planned. Do this after Tasks 1–4 are on the PR and CI has run.

1. Open the `Go · Coverage` job log and copy the two printed lines.
2. Set `sidecar/scripts/coverage-threshold` to the first number **rounded down** to two decimals,
   and `sidecar/scripts/coverage-threshold-platform` to the second, rounded down the same way.
3. Commit with subject `test(coverage): set the thresholds from the first three-OS run`. If the
   repo-wide number is below the previous 97.02, the body says, in one sentence, that the
   denominator now includes the macOS and Windows files.
4. Push and confirm `Go · Coverage` is green.

Rounding down matters: the gate compares the exact ratio against the file, so a threshold equal
to the rounded-up print fails by a hundredth.

## Acceptance

All of these hold on the PR before it is marked ready:

- [ ] `make cover-go` on a developer machine behaves exactly as before.
- [ ] The manual checks in Task 1 pass, including the usage errors and the unchanged inputs.
- [ ] CI shows three `Go · Coverage · <os>` jobs and one `Go · Coverage` job, all green.
- [ ] The `Go · Coverage` log's `-report` output names at least one `_darwin.go` and one
      `_windows.go` file, or the second printed line's statement count is in the low thousands.
      Either proves the platform files are in the profile.
- [ ] Both threshold files hold the rounded-down values from the first run.
- [ ] A macOS-only file losing coverage fails `Go · Coverage`. Prove it once on a throwaway
      commit: skip every test in `sidecar/internal/run/sandbox/sandbox_darwin_test.go` by
      inserting `t.Skip("proof")` as the first line of each `func Test…(t *testing.T) {`, push,
      and watch the second printed line fall below its threshold. Skipping one test is not
      enough, since another test can reach the same statements. Skipping them all loses every
      statement only they reach, and each platform statement is about 0.05% of the slice, so
      a threshold rounded down to two decimals catches even one. Revert before review.
- [ ] `make lint-go` and `make vet-go` pass. `gofmt -l sidecar/scripts` prints nothing.

## Out of scope

- Coverage per architecture. No file splits on GOARCH.
- Running the macOS and Windows `Go · Build and Test` legs with `CGO_ENABLED=0`. The coverage
  legs now run those OSes without cgo, but without `-race`, and the Build and Test legs keep cgo.
  Closing that fully is a separate issue.
- Per-file thresholds. Two aggregates are enough while the platform slice is ~2,000 lines.
- Any change to which tests run, which tags build, or the `-coverpkg` setting.

## When it lands

The PR from `wip/go-coverage-per-os` into `main`:

- Keeps the `sidecar/CLAUDE.md` and `Makefile` wording from Task 4 as the current-state record.
- Writes no ADR: the decision is the one #415 records, and the workflow comments carry the why
  in two lines. If a reviewer asks for one, title it "Go coverage is a union over one runner per
  OS, gated twice".
- Deletes this file.
