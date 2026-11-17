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
| 2D | [`2d-azure.md`](2d-azure.md) | The token-injecting proxy for Azure, and the door's TLS route | [#934](https://github.com/kstackhq/kstack/issues/934) | 1 |

The sequence lands once, when its last step is built. Step 2D opens with a spike; if it fails,
that step is retired unbuilt and the sequence lands without it.

## The sequence's gate

Each step has its own integration gate. The sequence has one more, run once the last step is
built, since two cloud platforms can collide where neither does alone:

- One sandboxed run with every cloud platform built turned on and a cluster, network off, through
  the real sandbox with a fake upstream per cloud platform: each cloud platform's read answers, and
  `kubectl` still reads the cluster.
- The same run's environment holds no variable two cloud platforms set to different values.
- The full checks in [`../README.md`](../README.md#verification-commands), with the wire checks.
