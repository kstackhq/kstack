# Restore keyring credentials in the background

The build order for [#1094](https://github.com/kstackhq/kstack/issues/1094). Each spec here is one
pull request into `wip/keyring-restore`, built in order. The rules in
[Working a numbered spec](../README.md#working-a-numbered-spec) apply.

## Why

Today every service that keeps a credential reads the OS keyring in its `New`, inside `build`
(`sidecar/internal/app/runtime.go`), before the sidecar serves. A keyring read has no bound: on
Linux `go-keyring` unlocks the collection first and waits for the user's prompt
(`secret_service.handlePrompt` receives with no timeout), and on macOS `/usr/bin/security` can
raise a dialog. Until the read answers, the launch stalls.

## Where the sequence ends

- **No `New` calls the keyring.** Each service publishes `Restoring`, reads on a goroutine, and
  publishes what it read. `app.New` returns whatever the keyring does.
- **A read has no deadline.** It logs once after 30 s and keeps waiting.
- **The keyring is opened once, by a probe, for every reader**: a locked keyring raises one
  prompt, and a dismissed one fails every read without asking again, the old entry's cleanup
  included. One service's bad entry fails that service alone. On macOS the one prompt waits on
  step 1's manual check.
- A Secret Service that is not on the bus yet is retried with a backoff, once for every reader.
- A turn sent while the keyring is locked waits for it, and its idle clock stops meanwhile.
- **While `Restoring`, a request that needs the credential waits; a request that would change it is
  refused.**
- Every window draws `Restoring` as *checking*, never as signed out and never as an empty state.

## The steps

| Step | Spec | Builds | After it |
| --- | --- | --- | --- |
| 1 | [The read that waits](1-the-read-that-waits.md) | `oskeyring.Read`, the gate, `signer.Pending`, one keyring for every service | `New` still reads, so the launch still waits. A dismissed unlock prompt is shown once, where today it returns for each service. |
| 2 | [The services restore in the background](2-the-services-restore-in-the-background.md) | `Restoring` in every service, on GraphQL and on gRPC; the idle clock stopped while signing | The launch no longer waits. The windows draw `Restoring` rows with their default detail. |
| 3 | [The windows draw checking](3-the-windows-draw-checking.md) | The *checking* rows in the webview | The work is whole. |

Every step can ship. Step 1 stays, marked built, until step 2 is complete, since step 2 cites its
API.

## Not in this sequence

- A deadline on `Set` and `Delete`. Both follow a user's action, and `NotSaved` and `NotDeleted`
  already report a refusal.
- Reading the keyring again after a restore reported it unreadable. A follow-up issue, opened when
  the sequence lands. It gives the gate a new opening: a failed one is kept for the process.
- Sharing the versioned entry's rules between connectors (`docs/TODO.md`). Each `keyring.go` keeps
  its own.
- A new persisted format. No entry, account or row changes.

## When it lands

The `wip/keyring-restore` pull request checks that nothing in the three specs is left only here,
opens the follow-up issue for reading again after an unreadable keyring, and deletes this
directory.
