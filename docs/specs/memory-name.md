# The memory editor names a memory itself

**Package:** `src/components/widgets/memory/` (webview only).
**Prerequisites:** none.
**Status:** Planned.

## Goal

A user adding a memory types its text and nothing else. The editor derives the name from the
body, and a name the user does type is turned into one as they type. The name's shape does not
change: lower-case letters, digits and single hyphens, at most 48 characters. It is the key the
model saves and forgets by, and the model reproduces a slug reliably, so the sidecar, the
`Memory` tool and the prompt stay as they are.

Today the editor requires the name, drawn as a monospace `name` field, and refuses anything
outside the slug rule with a sentence the user has to read and apply. People do not think of a
memory as something with an identifier: a memory is its text. The friction is the asking, not
the rule.

## Behaviour

**Folding to ASCII.** Both rules below start the same way: `normalize('NFD')`, strip combining
marks (`\p{M}`), lower-case. So `Café prod` becomes `cafe-prod` and `naïve` becomes `naive`.
Only ASCII letters and digits survive what follows: a body in another script folds to nothing.

**Slugifying.** `slugify(text)` in `writes.ts` is the rule for a name derived from the body:
fold; each run of anything outside `[a-z0-9]` becomes one hyphen; leading and trailing hyphens
are dropped; then the cut. The cut takes the first 48 characters; if the character after them is
a hyphen, or there is none, that is the name; otherwise it backs off to the last hyphen within
them and drops it. A first word longer than 48 characters is cut hard.

**The derived name.** With the name field empty, a save sends `slugify(body)`, or `memory` when
that is empty. If the scope already holds the name, the editor tries `<name>-2`, `<name>-3`, and
so on. Each candidate is built the same way: cut `<name>` so that it and the suffix fit in 48
characters, strip the hyphens the cut leaves at its end, append the suffix, then check that
candidate against the scope. Stripping after the cut is what keeps `…-bc` from becoming `…--2`.
The scope's memories are the list the editor sits in, so the check is local; the sidecar's
`KSTACK_MEMORY_NAME_TAKEN` remains the backstop for a race, and is shown as it is today.

A derived name can be 48 characters of words the body already holds, and the sidecar's
`ScopeBudget` counts the name. That is a cost of a few percent of a scope, accepted.

**A typed name.** The field stays, optional, placed under the body with the placeholder
`name (optional)`. Focus opens on the body, not the name. The field normalizes on every change
with `typedName`, a rule that never deletes what the user is in the middle of typing: fold; each
run of anything outside `[a-z0-9]` becomes one hyphen; a leading hyphen is dropped; a trailing
hyphen is kept, since a space has to land before the next word; and the value is cut hard at 48
characters, never at a word boundary. Typing `Prod Deploy` shows `prod-deploy`.

Normalizing a controlled input moves the caret to the end whenever the value shrinks, which it
does on a space typed beside a hyphen or on an edit in the middle. So the editor puts the caret
back: at `typedName(value.slice(0, caret)).length`, the length of the normalized text before
it, set through `setSelectionRange` in a layout effect after the change. Keeping the trailing
hyphen is what makes this prefix rule exact. IME composition text is folded as it is typed; it
would not survive the rule anyway.

On save the trailing hyphen is dropped and nothing else changes: a typed `prod-` sends `prod`,
and `slugify` never runs on a typed name. `NAME_RULE` and `nameBroken` go away: the field cannot
hold a name outside the rule. A typed name that collides is not suffixed; the sidecar refuses it
and the form says *Another memory here has this name*, as today.

**Save's gate.** `ready` is `body !== '' && !bodyBroken`. The name no longer gates it.

**An existing memory.** Its editor shows no name field, as today. A save sends `memory.name`
and never calls `uniqueName`: an edit's name is never empty, so the derived path is never
entered. (Were it entered, the memory's own name is in the scope's taken list, and the edit
would rename itself to `-2`.)

**What stays.** The body is the row's primary text and the name its metadata in `Provenance`,
as today. `KSTACK_VALIDATION_ERROR`'s message loses its name clause, since the editor can no
longer send a bad name.

## Tasks

1. `writes.ts`: add `slugify`, `typedName` and `uniqueName(base, taken)`; drop `NAME_RE`,
   `nameFits`, `NAME_RULE`; shorten the validation message; reword the header comment, which
   says the shape rules are checked as the user types. `writes.test.ts` replaces the `nameFits`
   cases and covers, for `slugify`: case, accented Latin (`Café prod` → `cafe-prod`),
   punctuation and whitespace runs, leading and trailing hyphens, a non-Latin body, and the
   cut: a hyphen right after character 48 keeps the word, a letter there backs off to the last
   hyphen, one long word is cut hard. For `typedName`: a trailing hyphen kept, a leading one
   dropped, the hard cut at 48 keeping the word being typed. For `uniqueName`: the suffix
   sequence, `-9` to `-10`, and a base whose cut ends in a hyphen (45 letters plus `-bc`)
   yielding no double hyphen.
2. `list.tsx`: `MemoryEditor` takes the scope's names, moves the name field under the body,
   makes it optional, normalizes its value on change with `typedName` and restores the caret,
   autofocuses the body for a new memory, drops the name from `ready`, and sends the derived
   name when the field is empty. `list.test.tsx` covers: a new editor focuses the body; Save is
   enabled with a body and no name; a save with no name sends the body's slug; a second memory
   with the same opening words gets `-2`; typed `Prod Deploy` is sent as `prod-deploy`; typed
   `prod-` is sent as `prod`; keystrokes that carry a typed name past 48 characters keep its
   first 48; a space typed in the middle of a name leaves the caret after the hyphen it became;
   a non-Latin body saves as `memory`; editing an existing memory's body sends its own name
   unchanged.
3. `src/CLAUDE.md`, the *Memory* paragraph: replace "Its editor checks the shape itself
   (`writes.ts`; `KSTACK_VALIDATION_ERROR` names no field)" with: the editor derives the name
   from the body, folds a typed one to the slug rule as it is typed, and checks the body's size
   itself (`writes.ts`).

## Verification

`make test-changed`, `make lint-js`. No wire or sidecar change.

## When it lands

Task 3 already holds what `src/CLAUDE.md` needs; check it reads true, then delete this spec.
No ADR: the one decision, that the name stays a slug because it is the model's key, is carried
by the `CLAUDE.md` sentence.
