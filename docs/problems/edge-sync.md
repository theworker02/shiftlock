# Sync edge state after reconnect

**Problem:** An edge agent buffers writes offline and must merge with a remote store without silent data loss.

**Approach:** Use `sync.Engine` with an explicit conflict policy:

| Policy | Behavior |
|--------|----------|
| prefer-source | source overwrites target |
| prefer-target | keep target on conflict |
| prefer-latest | newer `UpdatedAt` / higher `Version` wins |
| manual | queue keys for operator resolution (bounded) |
| reject | stop on first conflict |

Memory stores implement both `Source` and `Target` for demos (`examples/edge-sync-agent`).

**See also:** `sync`, `docs/audits/phase-7-audit.md`.
