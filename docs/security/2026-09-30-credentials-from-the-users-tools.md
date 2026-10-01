# Security record — credentials from the user's tools, 30 September 2026

**Subject:** the sidecar can now borrow a credential from `aws`, `gh`, `gcloud` and `az` by running
each tool's own command on the host, and keeps what it answers in memory until it expires. This
is step 1D of [agent security](../specs/agent-security/1d-credentials-from-the-users-tools.md).
Nothing calls the store yet: `app` builds it, and the proxies of steps 5C, 6B and 6C will borrow
through it. The living model is [security-model.md](../security-model.md).

## What changed

- **The borrows are the tools' own commands** (`internal/credentials`). Each command is fixed by
  provider; its one variable is an argument the caller names, never a shell string. The tool does
  its own resolution, so the sidecar never parses `~/.aws`, `hosts.yml` or a token cache.
- **An AWS credential's account is its own.** The borrow reads it with `aws sts
  get-caller-identity`, run with the borrowed keys as the only identity in the child's environment
  and no profile, so the account a permission is scoped by is the one a request signed with those
  keys reaches (`TestTheAccountIsTheBorrowedCredentialsOwn`). It rides on the credential, read
  again with every new one (`TestANewBorrowRederivesTheAccount`).
- **A credential is read at use time** and cached to the expiry the tool reports, less a minute,
  or a default TTL when it reports none. A GitHub token is re-borrowed hourly.
- **Nothing is copied.** Every tool runs in the user's home, with stdin the null device, so a tool
  that caches beside where it runs writes there and never under Kstack's directories
  (`TestNoCredentialIsWrittenToDisk`, one case per provider). Every secret is registered with
  `safe` under a slot of its own (`safe.SetSecrets`) before anything can log it, and a refused one
  stays registered (`TestASecretIsRegisteredWithSafe`). An AWS credential's secrets are registered
  as soon as they are decoded, before `sts` runs with them, so an expiry line that echoes them is
  blanked in the status too (`TestAnAccountThatCannotBeReadFailsTheBorrow`).
- **A run takes what the tool started.** Each tool runs in a process group of its own, a
  kill-on-close job object on Windows, killed whole at the timeout or `Close` and again when the
  tool exits, before it is reaped, while its pid still names the group, so a helper such as AWS's
  `credential_process` cannot outlive the borrow
  (`TestCloseStopsWhatTheToolStarted`, `TestAHelperLeftBehindIsKilled`). On Windows the tool
  runs only once it is in the job. `Close` returns once every run it stopped has been reaped, so
  no tool outlives the sidecar (`TestCloseWaitsForTheRunsItStops`).
- **A batch wrapper gets a command line quoted for `cmd.exe`.** On Windows `gcloud` and `az` are
  `.cmd` files, run through the system's `cmd.exe` with each argument in double quotes, where its
  metacharacters are text; an argument holding `"`, `%` or a line break is refused, since nothing
  escapes those inside quotes (`TestBatchLineRefusesWhatCmdCannotQuote`).
- **A failure names the tool and its exit code, never its output**
  (`TestAFailedBorrowNamesTheProviderAndNeverTheOutput`). An expiry line from stderr is kept as
  the status's detail, rendered through `safe.String`.
- **A refused credential stays refused.** A proxy that meets a refusal reports what it sent; the
  store keeps a SHA-256 of it, in memory, and refuses the same answer from the tool until it hands
  back another (`TestARefusedCredentialStaysRefused`). A refusal of a secret the cache has since
  replaced refuses that secret alone and leaves the identity as it is
  (`TestARefusalOfAReplacedSecretLeavesTheIdentity`). One that returns after another worked is
  refused and `Expired` again (`TestARefusedTokenThatReturnsIsExpiredAgain`). It keeps the last 16
  distinct values, so a refusal reported many times cannot push another out
  (`TestARefusalRepeatedTakesOnePlace`). A discovery that proved `gh`'s token works clears no
  refusal written after it began (`TestDiscoverKeepsARefusalNewerThanItsCheck`).
- **`GH_CONFIG_DIR` joins the macOS login-shell allowlist**, beside the AWS, `gcloud` and Azure
  config variables, so `gh` finds the config the user's terminal finds
  (`TestImportedIsExactlyTheAllowlist`).

## Not widened

No sandboxed command can reach the store: nothing in this step serves it, and the proxies that
will hand a command a placeholder, never the credential.

## Residuals

- A borrowed credential is whatever the user's tool holds, usually an admin one.
- The store runs the first tool on the sidecar's `PATH`, which on macOS is the login shell's, so a
  startup file decides what runs, as it already does for the kubeconfig's `exec` plugins. On Linux
  the `PATH` is the desktop session's, which can lack a tool the terminal finds.
- `GH_CONFIG_DIR` lets a startup file point `gh` at another config, as it can already point `aws`,
  `gcloud` and `az` at theirs.
- The tools see the sidecar's environment, not the terminal's. `GH_TOKEN`, `GH_HOST`,
  `CLOUDSDK_ACTIVE_CONFIG_NAME` and static `AWS_ACCESS_KEY_ID` are not imported, so a user who
  sets one in their shell gets another identity here.
- `gh auth logout` does not revoke the token, and `gh auth switch` changes the account, so Kstack
  can use the old token or account for up to an hour after either.
- The account read hands the borrowed keys to one `aws sts` child in its environment, on the
  host, as the user, who can already read the tool's own store.
- The tools inherit the sidecar's owner-only umask, so a file one writes into its own store is
  created `0600` or `0700`.
