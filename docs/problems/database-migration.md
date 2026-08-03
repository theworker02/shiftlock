# Coordinate a database migration

**Problem:** Schema or data cutover must not race with writers, and verification must stay under application control.

**Approach:**

1. Register a migration with `migration.Coordinator` (source/target, optional resource IDs, owner, fence).
2. Advance phases: planned → validated → preparing → copying → verifying → cutover → completed (or paused / failed / compensating).
3. Supply `VerifySteps` and optional `Cutover` hooks — ShiftLock never executes arbitrary SQL.
4. Use dry-run to walk validation/copy/verify while skipping cutover mutation.
5. Pause/resume via checkpoints; lockdown and ownership/fencing hooks block unsafe advances.

**CLI:** `shiftlock migrations list|start|pause` (`-dry-run` or `-confirm` required for start).

**See also:** `migration`, `resource/database/postgres`, workflow compensation, `docs/audits/phase-7-audit.md`.
