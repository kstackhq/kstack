---
title: A cloud platform is how a vendor's wire is authenticated, never a dialect
date: 2026-10-17
scope: sidecar
status: Accepted
---

# A cloud platform is how a vendor's wire is authenticated, never a dialect

## Context

This decision predates the file; it was recorded in `sidecar/CLAUDE.md` (*Models*) as the design
the `llm` rebuild has to reproduce. The security side is
[the cloud platforms record](../security/2026-09-13-cloud-platforms.md).

Bedrock, Vertex AI and Foundry serve the same three wires the vendors do (Messages, Responses,
Chat Completions). What each changes is how a request is signed and where it goes. Each has its
own conventions for region, project and credentials, which its SDKs and CLIs already read.

## Decision

**A platform is entries, not a dialect.** `Provider.Auth` (`AuthKey`, `AuthAWS`, `AuthGoogle`,
`AuthAzure`) picks how a request is signed, and each wire's client builder switches on it, calling
into the platform's file, which holds its entries beside what fills and signs them. One entry per
dialect a platform serves: Bedrock is Mantle (`bedrock-mantle.<region>.api.aws`), Vertex has no
Responses API, Foundry serves all three.

**The platform's own conventions supply every value**, read on the entry's turn, never at startup.
No `KSTACK_` variable exists for any of it. A convention that names nothing is a refused turn.

**The Claude and GPT entries reuse the vendor catalogs under the platform's spelling** (`renamed`,
one explicit map per entry), so efforts and caps stay in one place.

**A credential chain is walked once per entry**, on the first turn, off the turn's cancel, and a
turn waits for it at most `chainTimeout`.

## Alternatives considered

- **A dialect per platform.** The platforms speak the vendors' wires; a dialect per platform would
  copy a wire to change only how its client is built and signed.
- **Bedrock's legacy `bedrock-runtime` path.** It is not the vendor's own wire; Mantle is.
- **Kstack variables for region, project and keys.** A platform then behaves differently in Kstack
  than in every other tool on the machine.
- **Discovering a platform's models.** The lists sit behind each platform's own APIs and grants.

## Consequences

Adding a platform is data plus a signer, never a change to a wire. The conformance suite runs every
case under every auth, and a test pins that no vendor variable reaches a request under any auth.
