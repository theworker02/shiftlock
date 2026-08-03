# ShiftLock chaos lab

Docker Compose brings up Postgres + Redis. Scenarios are Go tests/binaries that
exercise ownership under failure.

## Start dependencies

```bash
docker compose -f lab/docker-compose.yml up -d
```

## Scenarios

| Scenario | Description |
|----------|-------------|
| `lost_commit_response` | Commit succeeds; retry same OperationID must not advance token twice |
| `owner_crash_mid_reserved` | Owner dies during reserved; Abort / expire recovers |
| `split_acquire` | Concurrent acquires → single winner |

Run in-process (no Docker required) with memory/redis Local:

```bash
go test ./lab -count=1
```

Destructive load against a real backend belongs in `cmd/shiftlock-loadtest`
(requires explicit backend config; labeled destructive).
