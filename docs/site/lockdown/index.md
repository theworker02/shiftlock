# Lockdown

Package `control/lockdown`: fail-closed emergency stop.

Unlock requires **expected ID + confirm + strong auth**. Evidence is retained (never deleted on unlock).

Auto-triggers (audit tamper, split-brain, command flood) are rate-limited and deterministic when configured.
