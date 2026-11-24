# Security record — the sandbox's PATH is one setting, 24 November 2026

**Subject:** a sandboxed command's `PATH` is `sandbox_settings.path`, one string the user writes
where `$PATH` stands for the login shell's, in place of the frozen list in `settings.json`
(migration `0009`). The decision is
[ADR: a sandboxed command's PATH is one setting the user writes](../adr/2026-11-24-the-sandbox-path-is-one-setting-the-user-writes.md).
The living model is [security-model.md](../security-model.md).

## What it changes

- **`sandboxPathSet` is a grant path.** A folder the user types into the setting is readable to
  every sandboxed run: a read grant made by typing, beside Settings' folder grants. Include, a
  click on a drawn folder, is gone.
- Nothing is stored per folder. A shell folder is judged afresh at every run, so the sync, its
  tombstones and its `strict` flag are gone, and so is the gap they left: a removal no longer
  needs to outlive a store that cannot read it.
- Remove of one shell folder is gone. Writing a value without `$PATH` drops every shell folder
  from the search.

## What holds

- **A startup file opens nothing.** The value is split before it is expanded, and the shell's
  answer is spliced in already split, so no folder it lists is ever a typed one
  (`TestTheShellsPathIsNeverTyped`). A shell folder is searched only when every run reads it
  already and no group but an administrators' one can write it
  (`TestAShellFolderIsSearchedOnlyWhenOpen`). A startup file still chooses among open folders, and
  `logToolMoves` still logs when it moves `kubectl` or `helm` (`TestAMovedToolIsLogged`).
- **A typed folder is held to a folder grant's checks.** A link or another spelling is left out,
  naming the folder to type instead (`TestATypedLinkIsLeftOut`,
  `TestATypedFolderInAnotherCaseIsLeftOut`); the denied-always list, Kstack's directories, a closed
  folder itself and any broad, world-writable or project folder are left out
  (`TestAFilteredFolderIsLeftOut`, `TestTheRunLeavesOutAFolderNoRuleOpens`,
  `TestTheRunLeavesOutABroadFolder`). A folder inside a closed one opens that folder alone
  (`TestATypedFolderInsideAClosedOneIsOpened`).
- **The grammar expands only `$PATH`, `~` and `$HOME`**, and refuses `..`, a relative path, the
  bare home and an empty element, which a shell would search as the working directory
  (`TestCheckPathRefusesEachBadElement`). `~` is the zones' home, never a value from the shell.
- **No read-write grant reaches a `PATH` folder**: every folder of the shell's, whatever the
  value, and every typed one, in Settings and at the run
  (`TestAWriteGrantOnAShellFolderIsRefusedWithoutPath`,
  `TestAWriteGrantOnATypedPathFolderIsRefused`). A run reads its search and its grant check from
  one read (`TestARunKeepsItsNoWriteAfterTheSettingChanges`).
- **A read that fails never permits.** A run whose setting or shell `PATH` cannot be read, or whose
  setting Kstack cannot use, does not start (`TestARunWhosePathCannotBeReadDoesNotStart`,
  `TestARefusedPathStartsNoRun`, `TestARunPathReadThatFailsIsAnError`); a folder check whose read
  fails is an error, never a refusal on the grant (`TestAFolderCheckReadThatFailsIsAnError`,
  `TestAFailedFolderCheckReadEndsTheWatch`).
- **`shell_path` is a copy.** It holds what the shell answered, and every folder of it is checked
  at each run, so a stale one grants nothing. A failed or empty read keeps it
  (`TestAFailedShellReadKeepsTheLastPath`, `TestAnEmptyShellPathKeepsTheLastRow`).
- **Settings shows what the user saves.** Add copies a folder from the login shell's `PATH`, never
  from command output, into the field, and saves nothing; a draft holding a hidden character is
  drawn through `VisibleText` before Save.

## Residual risk

- **A typed folder is readable to every run**, as a read grant is. Its checks stop the folders
  Kstack knows to hold secrets, not a folder the user names that holds one.
- **A typed folder is checked again at each run, by its name.** A link put in its place is left
  out; a folder renamed and another made under the old name is read.
- **An install of `desktop/v0.0.1` loses its `PATH` decisions**, by decision: an included folder
  the sandbox does not already read must be typed again, and a removed one that is open is searched
  again. The release notes must say so.
