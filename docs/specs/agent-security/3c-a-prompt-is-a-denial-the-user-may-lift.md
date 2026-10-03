---
title: A prompt is a denial the user may lift
scope: sidecar
status: Planned
---

# A prompt is a denial the user may lift

**Needs:** step 3B, whose `permissions.Policy.Decide` this step splits in two. It has landed, so
this is the last step of wave 3 and nothing waits on it to build in parallel. **Unblocks:** steps
4B, 4C, 5A and 6B, each of which today reasons about `Decide`'s branch order in prose and after
this step names one verdict instead.

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

`Decide` answers `Allowed`, `Prompted` or `Denied` by walking eight branches in a fixed order. The
order is right, and every answer a session can get today stays the same. What changes is how it
is stated, so that the later steps can build on a model instead of a branch list.

- **Authorization is binary and fails closed.** The policy either permits an action or it does
  not. A forbid wins over a permit, and nothing matching is a denial. That is the only question a
  rule or a mode answers, and it is what every authorization engine in the field answers.
- **A prompt is a denial the user may lift.** The policy did not permit the action, and the
  denial is one a human may override, once or with a grant. A refusal is a denial no answer
  lifts. Which of the two a denial is comes from what denied it: a `Deny` rule and the mode's
  refusal are final; an `AskFor` rule, class 5 and the default denial are not.
- **An approval is a grant.** The answer to a prompt that lasts past the request is a permit the
  policy reads next time, which is what step 4B's chat and always rules are.

In code, `Decide` becomes two calls: `Authorize` answers a `Verdict`, one of four, and the
verdict's `Outcome` is the `Decision` the proxies act on. `Decide` stays, as the two composed, so
no caller changes. The mode table pins every answer, and §2 names the one answer the engine
changes, which no rule Kstack reads can reach.

## What is not in this step

- **No new prompt, rule or duration.** Step 4B adds the five answers and the rules they write.
  Its `Grantable` is restated here in the verdict's terms, and the step builds it.
- **No `NoPrompts` and no `NoSecretData`.** Step 5A adds both; this step says where each slots in
  (§3).
- **No policy library.** Cedar's model is what this step adopts, in that authorization is binary
  and forbid wins. Its engine is not: the rules stay the flat struct Settings draws and the file
  holds, and the glob stays `Match`. Decision 4 says why.
- **No change to the classifier, the modes' storage, or the file.**

Nothing changes on Windows: no command there reaches a proxy.

## Design

### 1. The verdict

```go
// Verdict is what the policy says of an action before anyone is asked:
// permitted, or denied in one of three ways. The strongest of what matched
// wins, so a forbid beats a permit, and the verdicts are numbered in that
// order: the greatest that applies is the answer, and the zero value is the
// default denial.
type Verdict int

const (
	// Unmatched: nothing matched. The default denial, which an answer lifts
	// once and a grant lifts for good.
	Unmatched Verdict = iota
	// Permit: a permit matched and no forbid did. The action runs.
	Permit
	// Forbid: a forbid an answer lifts once and no grant lifts, since a
	// forbid wins over any permit: an AskFor rule, or class 5.
	Forbid
	// Refuse: a forbid no answer lifts: a Deny rule, or the mode.
	Refuse
)
```

Four states, not two flags: a denial is one of three kinds, and a reader of a `switch` sees
all of them. `Unmatched` is the one a grant changes, which is what step 4B's `Grantable` reads
(§4). The numbering is the strength order of §2, so `max` over what matched is the verdict, and
a `Verdict` nothing set is a denial, never a permit.

### 2. Layer 1: `Authorize`

```go
// Authorize is the policy's verdict on act, and the reason in the user's
// words: the strongest of what matched, a rule's line when a rule decided it.
func (p Policy) Authorize(act Action) (Verdict, string)
```

What permits and what forbids, each read against the action:

| Source | Verdict | Reason |
| --- | --- | --- |
| a `Deny` rule matches | `Refuse` | *a rule denies it: <line>* |
| the mode is `ReadOnly` and the class is 3, 4 or 5 | `Refuse` | *this context is read-only* |
| an `AskFor` rule matches | `Forbid` | *a rule asks for it: <line>* |
| the class is 5 | `Forbid` | *it always asks* |
| the class is 1 or 2 | `Permit` | *it changes nothing in the cluster* |
| an `Allow` rule matches | `Permit` | *a rule allows it: <line>* |
| the mode is `Auto` | `Permit` | *auto mode* |
| none of the above | `Unmatched` | *<mode> mode* |

The verdict is the strongest row that applies, `Refuse` over `Forbid` over `Permit` over
`Unmatched`, and the reason is the first row of that verdict in the table's order, the first
matching rule for a row that names one. The policy's rules are read in order only to pick the
reason: **the verdict is the same under any order of the rules**, and a test pins that.

The table is step 3B's eight branches, regrouped. Step 3B read class 1 and 2 before the mode, so
the mode's refusal names classes 3 to 5 here to keep them permitted; class 6 under `ReadOnly`
was never refused by the mode and stays `Unmatched`, as the mode table has it.

**The one answer that changes.** Step 3B allowed a class 1 or 2 action before reading any rule;
here a matching `Deny` or `AskFor` wins over that permit, as it does over every other. No rule
Kstack reads can carry one today: `securityconfig`'s `ruleClasses` refuses a rule of any class
but 4 and 5, and nothing writes a `chat_grants` row until wave 4. The classes are not idle,
though: the classifier tags a cluster read class 1, and step 4D adds class 1 and 2 `Allow` rules
to the same list with every cluster field unset, which `Rule.Matches` matches against that read.
So the premise moves from the order to the shape checks: **a rule of class 1 or 2 is an `Allow`
or it is refused**, in `ruleRefusal` and in 4D's read-back of a chat's grants, and
`TestAForbidOnAFolderClassRefuses` pins what the engine does should one reach it anyway. The
shape checks should not carry it alone: `Rule.Matches` reads the cluster fields and nothing
else, so a rule with a field it does not know matches a cluster action whenever the cluster
fields are unset. Step 4D, which adds `Folder`, has `Matches` refuse a rule naming one against
any action, so a folder rule never meets the cluster's verdict whatever its effect.

### 3. Layer 2: `Outcome`

```go
// Outcome is what the proxies do with a verdict: a permit runs, a refusal is
// refused, and either other denial is put to the user.
func (v Verdict) Outcome() Decision
```

| Verdict | Decision |
| --- | --- |
| `Permit` | `Allowed` |
| `Unmatched` | `Prompted` |
| `Forbid` | `Prompted` |
| `Refuse` | `Denied` |

`Decide` is the two composed and keeps its signature:

```go
func (p Policy) Decide(act Action) (Decision, string) {
	v, why := p.Authorize(act)
	return v.Outcome(), why
}
```

**Where step 5A's two fields go.** Layer 1 is what the policy permits; layer 2 is what is done
with a denial. `NoSecretData` is a forbid: the session never reads Secret data, whatever matched,
so it is a `Refuse` row in §2's table for class 6, above the `Deny` rule's, with the reason *this
session never reads Secret data*. `NoPrompts` permits and forbids nothing: it says the session
cannot be asked, so it is `Outcome`'s, turning `Unmatched` and `Forbid` into `Denied` with a
reason naming the session. Step 5A adds `Outcome`'s parameter when it adds the field; this step
leaves `Outcome` with none.

### 4. What the later steps read

Each of these is a wording change in its spec, made when this step is accepted, and a line of
code when that step is built.

- **Step 4B, `Grantable`.** Today: class 5 is not grantable, an action an `AskFor` rule matches is
  not, and a cluster action with no context is not. After this step: an action is grantable when
  its verdict is `Unmatched` and, for the cluster, it has a context. The first two cases are
  what `Forbid` means, a forbid no permit beats; the third is about the rule's scope, not the
  verdict, and stays a check of its own. `TestGrantableRefusesWhatNoRuleAllows` keeps its cases.
- **Step 4C, the host.** *`Auto` asks for a new host* is a change in layer 1, the one condition
  its decision 8 counts on, and **4C makes it, not this step**: this step's `Auto` row permits
  every class, class 3 included, as the mode table has it. 4C narrows that row to every class
  but 3, so an unlisted host is `Unmatched` under every mode, put to the user and grantable. A
  host an `AskFor` rule names is `Forbid`, so not grantable, which is why its request offers
  *Approve once* alone. A handler with no asker refuses before `Authorize`, as the cluster proxy
  does, and that is about the asker, not the verdict.
- **Step 5A, `NoPrompts` and `NoSecretData`.** *A `Prompted` under `NoPrompts` is a denial*
  becomes §3's rule on `Outcome`, and *`Decide` reads `NoSecretData` first, ahead of every rule*
  becomes a `Refuse` row of §2's table, which no order makes stronger or weaker.
- **Step 6B, the monitor.** Its policy `{Mode: ReadOnly, NoPrompts: true}` answers `Refuse` for
  classes 3 to 5 and `Unmatched` for class 6, which `NoPrompts` denies.

## Decisions this step asks for

1. **The split is made now, before wave 4.** Steps 4B, 4C, 5A and 6B each explain a behavior by
   where it falls in `Decide`'s order (*`AskFor` comes before `Allow`, so a rule written from the
   answer would never be read*). With the split each names a verdict. Made after 4B, `Grantable`
   is written twice. Recommended: the change is one file, no answer a session can get today, and
   wave 4 has not started.
2. **Four verdicts, not a decision plus a flag.** `Prompted` with a *grantable* bit would carry
   the same information, but the bit's meaning is the verdict's: `Unmatched` is the default
   denial and `Forbid` is a matched one. Naming them keeps the proxies, the request and the
   Settings words agreeing on what a prompt is. Recommended.
3. **`Decide` stays as the one call.** The proxies and the later specs call it; the split is
   below it. `Authorize` is exported for the proxies' tests, which are written in the verdict's
   vocabulary; step 4B's `Grantable` is in the package and would read it either way.
   Recommended.
4. **No policy library.** Cedar-go would give a vetted evaluator for layer 1. Against it: the
   rules are a flat struct the file holds and Settings draws, so a policy language on disk would
   accept rules Settings cannot show, and the security model needs every rule on screen in the
   user's words; compiled in memory it would replace about sixty lines, with the classifier,
   the reasons, the mode resolution and the held-field flow untouched; and the match semantics
   (`*` crossing `/`, a resource covering its `scale`, a set namespace skipping a cluster-scoped
   write) would become generated conditions. Revisit if a rule ever needs set membership or
   hierarchy. The ADR records it.
5. **The verdict is order-independent, and a test says so.** It is already true of step 3B's
   engine, since `Deny` beats `AskFor` beats `Allow` whatever the file order. Stating it as an
   invariant is what makes a rule safe to add: adding one never changes what another means.
   Recommended.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | `Verdict`, `Authorize`, `Outcome`; `Decide` composed from them; the comments on the package and on `Decide` say the model | `permissions/permissions.go`, `permissions/permissions_test.go` | — | Planned |
| 2 | Docs, per *When it lands* | see there | 1 | Planned |

## Tests

**`permissions`**

- `TestDecideFollowsTheModeTable` stands unchanged: every answer in the four-mode table is the
  same before and after.
- `TestTheVerdictMatchesTheOrderedTable`: the eight-branch priority list, written into the test
  as a function of its own, answers the same `Decision` and reason as `Authorize` then `Outcome`
  over every mode, every class and every subset of a matching `Deny`, `AskFor` and `Allow` of
  class 4 or 5 — the classes a rule can have. This is the refactor's proof, exhaustive over the
  inputs that exist, and it stays: the list is a second statement of the same policy, so a
  change to either that the other does not make fails here first. So a step that changes the
  table — 4C's `Auto` row, 5A's two fields — changes the list in the same commit, and the
  test's comment says so, not that the list was once the engine.
- `TestAVerdictIsOrderIndependent`: a policy holding a matching `Deny`, `AskFor` and `Allow`
  answers `Refuse` under every permutation of the three, and the same reason.
- `TestAForbidWinsOverAPermit`: `AskFor` with `Allow` is `Forbid`; `Deny` with `Allow` is
  `Refuse`; class 5 under `Auto` with an `Allow` is `Forbid`.
- `TestAForbidOnAFolderClassRefuses`: a class 1 action with a matching `Deny` of class 1 is
  `Refuse`, with a matching `AskFor` `Forbid`, and with a matching `Allow` `Permit` with the
  reason *it changes nothing in the cluster*. The first two are the answer §2 changes, pinned so a
  rule that slips the shape checks refuses rather than runs.
- `TestTheZeroVerdictIsADenial`: `Verdict(0)` is `Unmatched`, and the four are numbered in the
  strength order of §2.
- `TestAnUnmatchedActionIsTheDefaultDenial`: a class 4 write under `Ask` with no rule is
  `Unmatched` and `Prompted`, with the reason *ask mode*; under `Auto` it is `Permit`.
- `TestReadOnlyRefusesClassesThreeToFive`: under `ReadOnly`, classes 3, 4 and 5 are `Refuse`;
  classes 1 and 2 are `Permit` and class 6 is `Unmatched`.
- `TestOutcomeOfEveryVerdict`: the four rows of §3.
- `TestClassFiveAsksInEveryMode`, `TestDenyWinsOverAskWinsOverAllow`,
  `TestAClassFourRuleCoversClassFive` and `TestAReasonSaysWhatDecided` stand unchanged.

## Security

No boundary moves, and no answer a session can get today changes. What this step records is the
model the engine already implements, so that a later step cannot widen it by misreading the
order: a prompt is a denial, so a session that cannot ask refuses (step 5A), and a grant lifts
only the default denial, so no answer writes a rule that reaches past a forbid (step 4B). The one
answer it changes narrows: a forbid on class 1 or 2 now refuses where it was never read (§2).
What holds it: `TestTheVerdictMatchesTheOrderedTable` pins every answer to step 3B's over every
rule that can exist, `TestAVerdictIsOrderIndependent` pins that a rule's position never decides,
`TestAForbidOnAFolderClassRefuses` pins the narrowing, and `TestTheZeroVerdictIsADenial` that a
verdict nothing set is a denial.

Residuals: step 3B's stand.

## When it lands

- `sidecar/CLAUDE.md`, the `internal/permissions` paragraph: `Decide` is `Authorize` then
  `Outcome`; the four verdicts and the table of §2 in place of the branch list; the invariant that
  the verdict is order-independent.
- An ADR, *authorization is binary and a prompt is a denial the user may lift*: the model, the
  two layers, why a prompt is not a third effect, and the alternatives of decision 4. It names
  the step 3B ADR's *Decide's order is fixed* bullet as what it replaces, and that ADR stays
  accepted, its body untouched, and gains an `amended_by` frontmatter pointer to the new one, as
  `docs/adr/README.md` has it.
- `docs/security-model.md`: the cluster-write rows cite `TestTheVerdictMatchesTheOrderedTable`
  and `TestAVerdictIsOrderIndependent`.
- The agent-security README's `permissions.Policy` vocabulary entry says the two calls.
- No security record: no boundary moves.

## Verification

- `bash scripts/sandbox-dev-setup.sh`, then `cd sidecar && go test ./internal/permissions
  ./internal/kubeproxy`, then `make test-go`, `make lint-go`, `make vet-go`.
- By hand: nothing to see. A sandboxed `kubectl` write asks, runs or is refused exactly as
  before under each of the three modes.
