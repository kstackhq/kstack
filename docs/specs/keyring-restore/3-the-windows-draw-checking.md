---
title: The windows draw checking
scope: webview
status: Planned
---

# The windows draw checking

**Answers:** [#1094](https://github.com/kstackhq/kstack/issues/1094), step 3 of
[the sequence](README.md). **Needs:** step 2, which puts `Restoring` on GraphQL and gRPC.
**Unblocks:** nothing.

## In short

After step 2 the sidecar reports `Restoring` until the keyring answers, and the windows draw it as
signed out. After this step:

- Every connector row and every provider row the credential gauge sends draws *Checking your
  keychain…* while `Restoring`, with no action: never signed out, never *No API key found*, never
  *Not connected*.
- `useAuthState` stays `loading` while the account is restoring.

## Not in this step

- Any change to the sidecar or the wire. Step 2 put `restoring` on both; this step reads it.
- A way to retry a keyring that could not be read
  ([the sequence](README.md#not-in-this-sequence)).
- The tray. Its account section is hidden (`SHOW_ACCOUNT_SECTION` is `false`), so it draws
  nothing for a restoring account. Showing it draws `Restoring` as signed out, recorded in
  [`docs/TODO.md`](../../TODO.md#host-taurirust).

## Design

### 1. The account: `src/lib/auth.tsx`

The `AuthStateWatch` subscription selects `restoring`, which it leaves out today, and `pnpm
codegen` regenerates `src/gql/`. `loading` is also true while `restoring`. `AccountAvatar`
disables *Sign in* while `loading`, and nothing else changes. The app bar leaves `AccountAvatar`
out today, so this changes nothing a user sees. It keeps the avatar right for when it is shown.

### 2. The connector rows: `settings-dialog/connectors.tsx`

Each real row (ChatGPT, OpenRouter, AWS, GitHub, Google) while `phase === 'Restoring'`:

- Detail: *Checking your keychain…*
- No *Connect*, no *Cancel*, no menu.
- `connected` is false (`src/lib/connectors.tsx`).

Only the detail changes. Each row already draws *Connect* for `SignedOut` and `Failed` alone and a
menu for `Connected` alone, and step 2 hid ChatGPT's menu while `Restoring`. The OpenRouter, AWS,
GitHub and Google detail functions each `switch` over the phase; their `Restoring` case, which
step 2 added to answer what `SignedOut` does, now answers the line above. `ChatGPTRow` sets its
detail with an `if` per phase and gains one for `Restoring`. ChatGPT's plan confirmation resets on
`SignedOut` and `Failed` alone, so `Restoring` leaves it.

OpenRouter's signed-out line reads `providerCredentialsWatch` to name the key that signs. While
that provider's `source` is `Restoring`, which outlasts the connector's own restore, it draws
*Checking your keychain…* in place of the offer. Its *Connect* stays: the connector has restored.

### 3. The provider rows: `settings-dialog/providers.tsx`

`Provider`'s `configured: boolean` becomes `status: 'set-up' | 'not-set-up' | 'checking'`. Every
row the gauge sends, with a key or without one (ChatGPT and Vertex AI take none and follow their
connector), is `checking` for `source === 'Restoring'`, `not-set-up` for `None`, else `set-up`.
The `PROVIDERS` and catalog-only rows keep their two states. Every reader of `configured` reads
`status`: the row, the dialog's *Configure* / *Set up* title, and `Detail`.

| `status` | Dot | Detail | Action |
| --- | --- | --- | --- |
| `set-up` | filled | as today | menu |
| `not-set-up` | ring | as today | *Set up* |
| `checking` | dashed ring (`border border-dashed`), `data-set-up` absent | *Checking your keychain…* | none |

The dot keeps the row's rule of shape as well as color: filled is set up, a solid ring is not, a
dashed ring is not known yet.

`Detail` answers the line above for `Restoring` first, ahead of its keyless branch (*Not
connected*) and `keyFailureText`. A `checking` row draws no action whatever `removable` says, so
neither the setup form nor the remove dialog can open on a restoring provider.

### 4. What stays

- Nothing draws before each watch's first frame.
- The model picker lists what `modelsWatch` serves. A provider with an environment key is there
  while its row says *checking*.
- Every string from a gauge goes through `VisibleText`.
- `design/components.json` is updated if `make lint-design` asks.

## Docs

- `src/components/settings/CLAUDE.md`: *Connectors* and *Providers* each say a `Restoring` row is
  *checking*, with no action.

## Tasks

| # | Task | Status |
| --- | --- | --- |
| 1 | `useAuthState`'s `loading` and its test | Planned |
| 2 | The connector rows, the provider rows, and their tests | Planned |

Verification is `docs/specs/README.md`'s *Verification commands*: `make test-changed` while
working, `make check-changed` before the pull request, and `make lint-tailwind` for the new class.
The subscription's new field adds its *Wire checks*: `pnpm codegen`, then `pnpm build`.

## Acceptance

- Each real connector row draws *Checking your keychain…* with no button and no menu while
  `Restoring`, and its usual row on the next frame (`connectors.test.tsx`).
- A keyed provider row draws the same line, the `checking` dot with no `data-set-up`, and no
  action; it never draws *No API key found* for `Restoring` (`providers.test.tsx`, whose
  `data-set-up="false"` assertion for `Restoring` changes).
- A keyless provider row (ChatGPT, Vertex AI) draws the same while `Restoring`, never *Not
  connected* or *Connect* (`providers.test.tsx`).
- Each provider row draws its usual row on the frame that leaves `Restoring`
  (`providers.test.tsx`). Keyed, to `SavedKey`: *Using a saved key*, the filled dot with
  `data-set-up="true"`, and its menu; to `None`: *No API key found*, the ring with
  `data-set-up="false"`, and *Set up*. Keyless, to `Connector`: *Connected on the Connectors
  tab*, the filled dot, and its menu; to `None`: *Not connected*, the ring, and *Connect*.
- OpenRouter's signed-out row draws *Checking your keychain…* while its provider is `Restoring`,
  with its *Connect*.
- `useAuthState` is `loading` while `restoring`, and not after (`auth.test.tsx`).

By hand, once, on Linux with the login keyring locked, for a keyed provider no connector signs
(Anthropic, say) that has a saved key and its environment key both, so its models are listed
before the keyring answers: launch, see the window open with the unlock prompt up, every real
connector row and every provider row the gauge sends *checking* (the placeholder and
catalog-only rows draw as before); send a turn to that provider and see it wait; unlock, and see
the rows settle, that provider's on *Using a saved key*, and the turn run. Then again, dismissing
the prompt: one prompt is shown, every connector row settles signed out, that provider's row
settles on its environment key, and the turn runs under it.

`coverage / Gate` is green.

## Things to watch

- **A row can stay *checking* for as long as the prompt is up.** That is the design: the line says
  where to look. Nothing here times it out.
- **Exhaustive switches.** Step 2 added each `Restoring` case, so the compiler no longer points at
  them. Search both files for `'Restoring'`, and read `ChatGPTRow`'s `if`s by hand.

## When it lands

The guides already say what the pull request built. This spec is deleted, and its row leaves
[the sequence's README](README.md), which the `wip/keyring-restore` pull request then deletes.
