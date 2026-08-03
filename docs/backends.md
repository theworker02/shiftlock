# Backends

## Memory (`backend/memory`)

- Process-local, mutex-serialized.
- Test helpers: `SetPartition`, `FailNext`, `SetDelay`, `CrashOnCommit`, `ForceExpire`.
- Use with `internal/testclock` for deterministic expiry.

## PostgreSQL (`backend/postgres`)

- Uses `database/sql` only (no ORM).
- `Migrate` is **opt-in**.
- Ownership mutations run in transactions with `SELECT … FOR UPDATE`.
- `WatchClaim` polls; optionally uses `pg_notify` on mutate.
- Integration tests: build tag `integration,postgres` + `SHIFTLOCK_POSTGRES_URL`.

## Redis (`backend/redis`)

- Atomic claim updates via Lua scripts (CAS on fencing token).
- Implement `redis.Client` for your driver (go-redis adapter is straightforward).
- **Durability**: enable AOF; prefer Sentinel/Cluster. See package comment.
- Lease TTL recovers crashed owners; fencing tokens reject partitioned owners.

## Kubernetes (`backend/kubernetes`)

- Separate module path; core does **not** import `k8s.io/*`.
- Stores fencing metadata in Lease annotations.
- Provide a `LeaseClient` wrapping `coordination/v1` leases.
- `MemoryLeaseClient` for unit/contract tests without a cluster.
