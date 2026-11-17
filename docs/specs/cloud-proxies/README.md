# Cloud proxies

A sandboxed command reaches AWS, GitHub, Google Cloud and Azure as the user, through a proxy in
the sidecar that adds the credential, so the sandbox holds none. One sequence, on
`wip/cloud-proxies`.

Step 1 builds what the four cloud platforms share, and is built. Wave 2 builds one cloud platform
per step; its steps need step 1 alone and can be built at the same time.

| Step | Spec | Builds | Answers | Needs |
| --- | --- | --- | --- | --- |
| 1 | [`1-shared-pieces.md`](1-shared-pieces.md) | Built: the run's door, the per-grant token, the gate, the request's shape, the consent switch | [#935](https://github.com/kstackhq/kstack/issues/935) | — |
| 2A | [`2a-aws.md`](2a-aws.md) | The re-signing proxy for AWS | [#931](https://github.com/kstackhq/kstack/issues/931) | 1 |
| 2B | [`2b-github.md`](2b-github.md) | The token-injecting proxy for GitHub | [#932](https://github.com/kstackhq/kstack/issues/932) | 1 |
| 2C | [`2c-gcp.md`](2c-gcp.md) | The token-injecting proxy for Google Cloud | [#933](https://github.com/kstackhq/kstack/issues/933) | 1 |

The sequence lands once, when its last step is built.

## What wave 2's steps share

Each step uses these as they are and adds none of its own:

- **The list.** A step adds its cloud platform to `builtCloudPlatforms`
  (`app/cloudplatforms_unix.go`), in `PermissionTarget`'s order, and nothing to `runtime.go`.
- **The grant's limits.** `cloudLimits` (`tools/bash/cloud.go`); a step that needs `Challenges`
  sets it on a copy.
- **A run's file.** `writeNew` (`tools/bash/rundir.go`).
- **Refusals.** `runproxy.BodyRefusal` and `runproxy.SlotRefusal` word the statuses `Body` and
  `Slot` answer. The door refuses a cloud platform's upgrade, so no proxy checks for one.
- **The prompt.** `prompts/sandbox.md` states once what holds for every cloud platform: through a
  proxy as the user, a read runs, a change asks each time, a background command changes nothing,
  a credential is refused. A step adds one paragraph after it, opening *Where `cloud` names
  `<word>`,*, that says only what is its own: its tools, what it reaches, and what does not work.
- **Names.** One shape for the four, `<name>` being `aws`, `github`, `gcp` or `azure`:

  | Piece | Name |
  | --- | --- |
  | The proxy | `run/<name>proxy` |
  | The credential's service | `services/<name>auth` |
  | The mount | `tools/bash/<name>.go`, with `_unix` only where it cannot build on Windows |
  | Its type and constructor | `awsPlatform` and `NewAWS`, `gitHubPlatform` and `NewGitHub`, `gcpPlatform` and `NewGCP`, `azurePlatform` and `NewAzure` |
  | What the mount needs of the service | an interface the mount file declares, `<Name>Login` |
- **Words.** AWS, GitHub, Google Cloud and Azure are *cloud platforms*: `CloudPlatform` in Go,
  "cloud platform" in prose. A bare "platform" is an OS, and a "provider" is a model's.

A step that is built deletes its spec and its row above, and edits no other step's spec
([`../README.md`](../README.md)).

## The sequence's gate

Each step has its own integration gate. The sequence has one more, run once the last step is
built, since two cloud platforms can collide where neither does alone:

- One sandboxed run with every cloud platform built turned on and a cluster, network off, through
  the real sandbox with a fake upstream per cloud platform: each cloud platform's read answers, and
  `kubectl` still reads the cluster.
- The same run's environment holds no variable two cloud platforms set to different values.
- The full checks in [`../README.md`](../README.md#verification-commands), with the wire checks.
