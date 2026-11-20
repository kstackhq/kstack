---
title: The windows draw checking
scope: webview, host
status: Planned
---

# The windows draw checking

**Answers:** [#1094](https://github.com/kstackhq/kstack/issues/1094), step 3 of
[the sequence](README.md). **Needs:** step 2, which puts `Restoring` on GraphQL and gRPC.
**Unblocks:** nothing.

## In short

After step 2 the sidecar reports `Restoring` until the keyring answers, and the windows draw it as
signed out. After this step:

- Every connector row and every keyed provider row draws *Checking your keychain…* while
  `Restoring`, with no action: never signed out, never *No API key found*.
- The account avatar stays in its loading state while the account is restoring.
- The tray's account menu has a `Restoring` descriptor.

## Not in this step

- Any change to the sidecar or the wire.
- A way to retry a keyring that could not be read
  ([the sequence](README.md#not-in-this-sequence)).

## Design

### 1. The account: `src/lib/auth.tsx`

`loading` is also true while `authStateWatch.restoring`. The account avatar already draws
`loading`, and nothing else changes.

### 2. The connector rows: `settings-dialog/connectors.tsx`

Each real row (ChatGPT, OpenRouter, AWS, GitHub, Google) while `phase === 'Restoring'`:

- Detail: *Checking your keychain…*
- No *Connect*, no *Cancel*, no menu. ChatGPT's menu is also hidden when `registered`, as it is
  while `SigningIn`.
- `connected` is false (`src/lib/connectors.tsx`).

Each connector's detail function is a `switch` over the phase; its `Restoring` case, which step 2
added to answer what `SignedOut` does, now answers the line above. ChatGPT's plan confirmation
resets on `SignedOut` and `Failed` alone, so `Restoring` leaves it.

OpenRouter's signed-out line reads `providerCredentialsWatch` to name the key that signs. While
that provider's `source` is `Restoring` it draws the offer with no key named.

### 3. The provider rows: `settings-dialog/providers.tsx`

A keyed row's `configured: boolean` becomes `status: 'set-up' | 'not-set-up' | 'checking'`:
`checking` for `source === 'Restoring'`, `not-set-up` for `None`, else `set-up`. The `PROVIDERS`
rows keep their two states.

| `status` | Dot | Detail | Action |
| --- | --- | --- | --- |
| `set-up` | filled | as today | menu |
| `not-set-up` | ring | as today | *Set up* |
| `checking` | filled, muted (`bg-muted-foreground/50`), `data-set-up` absent | *Checking your keychain…* | none |

`Detail`'s `Restoring` case answers the line above, ahead of `keyFailureText`. *Remove key* is not
offered while `checking`, so the setup form cannot open on a restoring provider.

### 4. What stays

- Nothing draws before each watch's first frame.
- The model picker lists what `modelsWatch` serves. A provider with an environment key is there
  while its row says *checking*.
- Every string from a gauge goes through `VisibleText`.
- `design/components.json` is updated if `make lint-design` asks.

### 5. The tray: `src-tauri/src/tray/account_menu.rs`

- `AccountSnapshot` gains `restoring`, set by `account_snapshot_from`.
- `AccountMenuDescriptor` gains `Restoring`, which `build_account_descriptor` answers before it
  reads `authenticated`. It is drawn as one disabled item, *Checking account…*.
- Before the watch's first frame the menu stays signed out.

The account section is hidden today (`SHOW_ACCOUNT_SECTION` is `false`), so this changes nothing a
user sees. It keeps the menu right for when the section is shown.

## Docs

- `src/components/settings/CLAUDE.md`: *Connectors* and *Providers* each say a `Restoring` row is
  *checking*, with no action.
- `src-tauri/`'s tray guide: the `Restoring` descriptor.

## Tasks

| # | Task | Status |
| --- | --- | --- |
| 1 | `useAuth`'s `loading`; the connector rows; the provider rows; their tests | Planned |
| 2 | The host's snapshot and descriptor, and their tests | Planned |

Verification is `docs/specs/README.md`'s *Verification commands*: `make test-changed` while
working, `make check-changed` before the pull request, and `make lint-tailwind` for the new class.

## Acceptance

Webview:

- Each real connector row draws *Checking your keychain…* with no button and no menu while
  `Restoring`, and its usual row on the next frame (`connectors.test.tsx`).
- A keyed provider row draws the same line, the `checking` dot and no action; it never draws *No
  API key found* for `Restoring` (`providers.test.tsx`).
- OpenRouter's signed-out line names no key while its provider is `Restoring`.
- `useAuth` is `loading` while `restoring`, and not after (`auth.test.tsx`).

Host:

- `account_snapshot_from` carries `restoring`, and `build_account_descriptor` answers `Restoring`
  for it, whatever `authenticated` says.

By hand, once, on Linux with the login keyring locked, for a provider that has a saved key and
its environment key both, so its models are listed before the keyring answers: launch, see the
window open with the unlock prompt up and every row *checking*; send a turn to that provider and
see it wait; unlock, and see the rows settle, that provider's on *Using a saved key*, and the turn
run. Then again, dismissing the prompt: one prompt is shown, every connector row settles signed
out, that provider's row settles on its environment key, and the turn runs under it.

`coverage / Gate` is green.

## Things to watch

- **A row can stay *checking* for as long as the prompt is up.** That is the design: the line says
  where to look. Nothing here times it out.
- **Exhaustive switches.** Step 2 added each `Restoring` case, so the compiler no longer points at
  them. Search both files for `'Restoring'`.

## When it lands

The guides already say what the pull request built. This spec is deleted, and its row leaves
[the sequence's README](README.md), which the `wip/keyring-restore` pull request then deletes.
