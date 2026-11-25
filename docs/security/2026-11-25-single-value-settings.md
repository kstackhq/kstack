# Security record — the single-value settings move into one table, 25 November 2026

**Subject:** the cloud platform switches, the approval modes, the tool settings and the `PATH`
setting, moved from `sandbox_settings`, `approval_modes` and `tool_settings` into `settings`, a
row per key and scope (migration `0010`). The decision is
[ADR: keep each single-value setting as a row of one settings table](../adr/2026-11-25-a-single-value-setting-is-a-row-of-settings.md).
The living model is [security-model.md](../security-model.md).

## What it changes

- **The tables' `CHECK`s on a mode and a switch are gone.** A stored value Kstack cannot use is
  now refused at the read: a switch or a tool reads off, a mode read-only. Before, such a value
  could not be stored.
- **A missing row is the default, not a failed read.** Deleting `sandbox_settings`' row turned
  every platform off and started no run; with no `settings` row every switch is on and the
  `PATH` is `$PATH`. Whoever can delete a row in `app.db` can also write one.
- **A value Kstack cannot use is logged by key and reason, never by value**, and is not listed in
  Settings. Choosing there writes over it.

## What holds

- **No value Kstack cannot use grants anything.** A switch reads off
  (`TestASwitchValueKstackCannotUseIsOff`), a mode read-only, in a cluster's row or the default's
  (`TestAModeKstackCannotUseIsReadOnly`), a tool off in Settings and in a turn's offer
  (`TestARowKstackCannotUseTurnsItsToolOff`, `TestATurnIsNotOfferedAToolWhoseSettingCannotBeUsed`),
  and the log names no value (`TestASettingKstackCannotUseReadsItsStrictestOrDefault`).
- **A `PATH` Kstack cannot use starts no run.** A refused value and one that is not text are read
  with their refusal, never as `$PATH` (`TestARefusedPathSettingIsReadWithItsRefusal`,
  `TestAPathSettingThatIsNotTextIsReadWithItsRefusal`, `TestARefusedPathStartsNoRun`).
- **A failed read never permits.** The switches read off at a run's start and at a grant, a mode
  read-only, and a failed read of the tools refuses the send, as before
  (`TestACloudPlatformSwitchReadThatFailsIsAnError`, `TestAPolicyThatCannotBeReadRefuses`,
  `TestASendThatCannotReadTheToolSettingsIsRefused`).
- **Every user choice survives the move**: a switch off, a changed `PATH`, each mode and each tool
  setting for every cluster (`TestMigration0010MovesTheSingleValueSettings`).

## Residual risk

- **A value Kstack cannot use is not shown.** A cluster whose stored mode Kstack cannot read shows
  read-only in Settings as if the user chose it. It fails safe, and choosing a mode repairs it.
