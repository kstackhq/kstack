# Specs

A spec describes **what we are about to build**, in enough detail to implement it and no more.
It is a plan with a shelf life.

The other two doc kinds keep their jobs: `CLAUDE.md` says what is true now, `docs/adr/` says why a
design was chosen. A spec says what comes next.

## Where a spec lives

**A spec never reaches `main`.** It lives on a pull request, then on a `wip/<topic>` branch of
this repository, and is deleted in the PR that lands its code:

1. **Propose.** Add the spec on a branch of your fork, open a draft PR into `main` titled
   `📜 Spec: <what it builds>`, labelled `spec`, and link the issue it answers. The spec is
   reviewed there, line by line. Use the spec PR template:
   `gh pr create --draft --template spec.md`, or add `?template=spec.md` to the compare URL.
2. **Accept.** A maintainer creates `wip/<topic>` from `main`, retargets the spec PR to it and
   merges it. They then open a draft PR from `wip/<topic>` into `main`, which shows the whole
   change as it grows.
3. **Build.** Each implementation PR targets `wip/<topic>`, and updates the spec as the work
   moves. Keep `wip/<topic>` current with `main` as the work goes.
4. **Land.** When the work is finished, the `wip/<topic>` PR folds what is now true into the
   relevant `CLAUDE.md`, writes an ADR if a decision needs its reasons recorded, deletes the spec,
   and merges into `main`.

How `wip/<topic>` keeps up with `main`, and whether it lands as a squash, a rebase or a merge
commit, is the maintainers' call for each piece of work. A rebase rewrites the branch under any
open PR, so warn their authors first.

The open `spec` PRs are the proposals; the open PRs from `wip/*` are the work in progress.

Whether something is worth building is settled before its spec, in an issue, or in a Discussion
for an open-ended proposal. A security spec's gap keeps its line in [`TODO.md`](../TODO.md#security),
linking the PR, and its row in [`security-model.md`](../security-model.md); the spec's own
*When it lands* section says where the row moves.

## Naming

A spec is named for its shape. No dates: a spec is edited as the work moves, not appended to.

| Shape | File | Prose |
| --- | --- | --- |
| One spec | `docs/specs/<topic>.md` | — |
| Steps built one after another | `docs/specs/<topic>/<n>-<slug>.md` | step 3 |
| Waves of steps built in parallel | `docs/specs/<topic>/<n><a>-<slug>.md` | step 3B |

**A sequence** — the second and third shapes — has a `docs/specs/<topic>/README.md` naming its
steps, and shares one `wip/<topic>`: the README and each step's spec merge into it as they are
accepted, and it lands once the last step is built. In the third shape, `<n>` is the wave: every
step in one wave can be built at the same time, and a step needs only steps of earlier waves. The
letter orders a wave's steps for reading, not for building. Use it only when a sequence has a wave
of more than one step.

Branches on a fork are the contributor's to name. We suggest the spec's own name: `<topic>`,
`<topic>-<n>` or `<topic>-<n><a>`.

## Working a numbered spec

The numbered specs share these rules, so each states only what is its own.

Implement a spec after its stated prerequisites have landed. The numbered spec is the unit of
integration and retirement; its tasks are smaller assignments that run in the listed order.
Assign one task at a time with the spec, the applicable `CLAUDE.md`, and the current code.
All tasks start Planned; record their status and implementation commit/PR in the spec as the work
moves.
Each handoff reports changed files, acceptance cases, verification results and remaining failures.

Keep dependent cutover work on `wip/<topic>` until its integration gate passes. Supporting
work should compile and pass focused checks; do not merge an incomplete schema/service cutover.
Do not introduce a generic repository framework, a second DB owner, or dual writes.
Paths in task lists are relative to `sidecar/internal/` unless explicitly rooted elsewhere.

When complete, update current-state documentation and ADRs as the spec's *When it lands* lists,
then delete **that spec only** and remove its row from the sequence's README in the same change. Keep later spec numbers
stable: their prerequisites are delivered code and current-state docs, never a retired spec file.

### What every spec inherits

Delivered by the retired specs 1–4 and documented in `sidecar/CLAUDE.md`.

The app owns and injects one `*appdb.DB`; a service's statements are a `sqlstmt.Set` over a table
of `sqlstmt.Statement`s declared `OnWriter`/`OnReader`/`OnBoth`, prepared on `db.Write, db.Read`. Writer transactions use BEGIN IMMEDIATE. Subscribe
before the first read, notify after commit, and use the DB's `Notify`/`Subscribe`. Timestamps are Unix
milliseconds; NULL differs from zero. `appdb.NewID()` generates canonical UUIDv7 IDs, monotonic within one process only, so ids are identity and never a contractual order;
`appdb.ValidateUUID` accepts canonical lowercase RFC-variant UUIDv4/v7 request keys, rejecting nil IDs.
Clients never supply row IDs. An id is identity and never authorizes: knowing one grants nothing,
and a share link is its own row with a random token. A synced row keeps the id it was minted with,
so the cloud stores client-minted ids unchanged and scopes their uniqueness per account. Shared watch folds use `internal/deltafold`.

Clusters are rows in `app.db` (`clusters`, owned by `clustersvc`), addressed by the `ClusterID`
scalar; beehive holds one runtime object per row, named by its id, that the mirror keeps. A
cluster's deletion is a mark: the mirror tears the runtime down, the chat sweeper deletes the
chats, and the row goes last. Every send checks its cluster's mark inside its transaction
(`ErrClusterGone` → `KSTACK_RECORD_NOT_FOUND`). `chats.cluster_id` references `clusters(id)`.

Conversations, messages, agent runs, model calls, tool calls and approvals are the seven
application tables of `appdb/migrations/0001_init.sql`, the only schema authority; `chatsvc`
owns all but `clusters`. A send files the user message (carrying the client's UUID request key),
a queued chat run and the empty assistant message in one transaction; the turn claims its run
and settles it with the answer; a message's public status is its run's. A model call's row is
written before the provider is contacted and a tool call's running row before the tool executes,
each a full-row upsert by id; settlement writes every call row of the turn whole, and the next
start closes what a crash left open.

Edit `appdb/migrations/0001_init.sql` under the existing
[pre-release schema policy](../adr/2026-08-29-schema-edit-not-migration.md). The native-tools
cutover (*A native tool is a contract* ADR) changed the stored content
format and the `llm_calls` columns in place: a dev `app.db` from before it is reset.
Tests use temporary databases. Document fresh developer data directories at schema cutovers;
do not delete the user's live database as an implementation step.

### Verification commands

Before any build/test/install in the Linux sandbox run `bash scripts/sandbox-dev-setup.sh`, per
root `CLAUDE.md`. All commands below run from the repository root unless explicitly prefixed.

- **Go checks:** `make test-go`, `make lint-go`, `make vet-go`. During development use focused
  `cd sidecar && go test ./internal/<affected-package>`; broaden before handing off the task.
- **Wire checks:** `cd sidecar && go run github.com/99designs/gqlgen generate`, then from root
  `pnpm codegen`, `pnpm build`, `make test-js`, `make lint-js`. Never hand-edit generated files.
- **Full checks at a spec's integration gate:** `make test`, `make lint`, `make vet`, `make cover-go`,
  `make cover-js`. Do not lower coverage gates. Include codegen/build checks when wire changed.

## Index

Proposed: the open spec PRs, [`is:pr is:open label:spec`](https://github.com/kstackhq/kstack/pulls?q=is%3Apr+is%3Aopen+label%3Aspec).
In progress: the open PRs from a `wip/<topic>` branch into `main`.
