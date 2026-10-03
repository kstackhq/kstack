---
title: A sandboxed cluster write runs, asks or is refused by its class, the context's mode and the user's rules
date: 2026-10-02
scope: cross-cutting
status: Accepted
amended_by:
  - [Authorization is binary, and a prompt is a denial the user may lift](2026-10-03-authorization-is-binary-and-a-prompt-is-a-denial-the-user-may-lift.md)
---

# A sandboxed cluster write runs, asks or is refused by its class, the context's mode and the user's rules

## Context

The cluster proxy put every write a sandboxed command sent to the user
([a sandboxed command asks for each cluster write](2026-09-29-a-sandboxed-command-asks-for-each-cluster-write.md)),
or refused it when nobody could be asked. A user who trusts one namespace answered the same
request over and over, a production context was one approval from any change, and a `kubectl
diff` asked like a delete. The agent-security note wants every action in one of six classes,
and the user's approval mode and rules to decide, per class and scope, whether it runs, asks
or is refused. Step 3B of the agent-security sequence builds that engine and wires Kubernetes.

## Decision

**`permissions.Decide` is the one decision, and the proxy is where it is enforced.** The
`permissions` package is a leaf: the classes, the modes (`ReadOnly`, `Ask`, `Auto`), the rules,
`Match`, `Literal` and `Policy.Decide`. `kubeproxy/classify.go` turns a request into
a `permissions.Action`; the write path asks `Decide` under the write lock, and forwards an
`Allowed` write unasked once it is recorded, refuses a `Denied` one with a `Status` naming the
mode or the rule, and puts a `Prompted` one to the user as before. The model is never asked what
a write is.

- **Decide's order is fixed.** Classes 1 and 2 allow; a `Deny` rule refuses; a read-only mode
  refuses every write; an `AskFor` rule asks; class 5 asks; an `Allow` rule allows; `Auto`
  allows; anything else asks.
- **Three modes.** The note's *Trusted scopes* is `Ask` with `Allow` rules scoped to the trusted
  contexts and namespaces.
- **Modes are per context**, in `securityconfig`: the first `Modes` entry whose pattern matches,
  else `DefaultMode` (`Ask` when unset). A mode set in Settings is an entry naming that context
  alone, first in the list. No context is read-only by its name: the user marks a production
  context by setting its mode.
- **Rules last for a chat or always.** A chat's are `chat_grants` rows in `app.db`, gone with the
  chat; the always rules are `securityconfig`'s `Rules`.
- **A pattern's `*` crosses `/` and `:`**, since a context is often an ARN or a GKE name. A rule
  Kstack writes from a value escapes it with `Literal`. A pattern keeps its case.
- **A kind with no `/` covers the resource and its `scale` alone.** A scale writes the object's
  own replicas; any other subresource does something else under the same verb (a `create` of
  `pods/eviction` removes a pod), so a rule names it to cover it.
- **Class 4 covers class 5.** A `Deny` or `AskFor` of cluster writes reaches a destructive one; no
  `Allow` does, since class 5 asks before any `Allow` is read.
- **A dry run is a read on a stable group version the API server serves itself**, and runs
  unasked in every mode. On any other it is classed as the write it names: the aggregator routes
  by group and version, so an aggregated API's server — even one serving a new version of a
  built-in group — may ignore `dryRun` and apply the change, and the proxy reads no discovery to
  tell one from a custom resource.
- **A replica write is class 4 only in a plain form.** A write of a `scale` subresource or of a
  deployment, stateful set, replica set or replication controller is class 5 unless the proxy reads it whole and it leaves
  at least one replica: a literal positive `spec.replicas` with no `$` directive beside it, or on a
  workload a body that leaves replicas alone. Anything else asks.
- **A value the file holds that Kstack cannot read refuses.** A held `modes` makes every context
  read-only, a held `rules` refuses every cluster write, and a chat grant that does not decode
  refuses the chat's. A held list's edits wait for the fix or an explicit discard.
- **A background command's writes stay refused**: its grant has no asker, and so no call to
  record a write against.
- **Every write that ran or was refused unasked is recorded first**, as an `approvals` row of
  status `allowed` or `refused` with its reason, against the call that sent it. A write whose
  record cannot be written does not run.

This replaces the earlier ADR's rule that every write asks. The rest of it — one write at a time,
what cannot be shown refused, the wait belonging to the call — stands.

## Alternatives considered

**A fourth mode for trusted scopes.** It would be a second spelling of `Ask` with `Allow` rules,
and the prompt's "this chat" and "always" answers have to work in `Ask` for the prompt to be
honest.

**An exact field beside each pattern on a rule.** Two shapes of rule, and a file a user cannot
read at a glance. Escaping a value keeps one shape.

**A read-only default for production contexts, with shipped rules that refuse deleting a
production namespace.** The note had `prod*`. A prefix misses `arn:aws:eks:…:cluster/prod-eu` and
`gke_project_zone_prod`; `*prod*` reaches them but also catches `nonprod` and `dev-products`, and
misses a production cluster named otherwise. A guess from a name brings its own machinery: a mode
source, a confirm before lifting it, and rules nobody can remove. The user can already mark a
context with its mode or a `Deny` rule, and without the guess every context starts in `Ask`, where
every write asks. A way to flag a context as production waits until users ask for one.

**A dry run that asks.** `kubectl diff` and `--dry-run=server` are how a careful model previews a
change; asking for each preview teaches the user to approve without reading.

**Listing the forms that scale to zero.** Each review found another way a patch drops replicas — a
removed or moved field, a `null` parent, a strategic directive, a key spelled in another case — and
every one missed ran unasked under `Auto`. Listing the forms that keep replicas fails toward asking.

## Consequences

- A write in a trusted scope runs without a request, and is still on screen as a tagged line of
  its call.
- A production context asks for every sandboxed change, as any context does, until the user sets
  its mode; a chat switched outside the sandbox still asks for each command.
- A dry run's body reaches the cluster's admission webhooks unasked.
- A replica write in any but the plain forms asks in every mode, a strategic directive on a
  workload's spec or a JSON Patch of `/spec` included.
- A bulk delete is decided object by object. kubectl deletes one object per request, so
  `kubectl delete pods --all` asks once per pod in `Ask`, and under `Auto` or an `Allow` rule
  every pod goes with nobody asked; only a raw `deletecollection` is class 5.
- The class 5 list is a list: a destructive write it does not name, a privileged Pod included, is
  class 4, which `Auto` or an `Allow` rule runs unasked.
- Each later provider (hosts in step 4C, folders in step 4D) adds its classifier, its scope field
  and its line to the held-field refusals.

## Revisit when

A background command needs to change the cluster, or a mode needs to bind a command run outside
the sandbox.
