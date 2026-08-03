# Supervisor

Package `supervise` runs ownership-aware tasks with bounded restarts.

Modes: singleton, per-instance, leader-only, claim-bound, scheduled, one-shot, maintenance-only, manual.

Default restart policy is **bounded** (not infinite). Panic recovery and cancel-on-ownership-loss are required.
