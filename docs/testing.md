# Testing

```bash
go test ./...
go test -race ./...
go vet ./...
```

Optional:

```bash
go test -fuzz=FuzzStateTransition -fuzztime=10s
go test -bench=. ./...
staticcheck ./...
```

## Suites

| Suite | Location | Covers |
|-------|----------|--------|
| Core invariants | `*_test.go` | single owner, stale release, handoff, close, drain, readiness |
| Backend contract | `backend/backendtest` | all backends must pass |
| Memory contract | `backend/memory` | always on |
| Kubernetes contract | `backend/kubernetes` | MemoryLeaseClient |
| Postgres integration | `backend/postgres` | `integration,postgres` build tags |
| Fuzz | `fuzz_test.go` | transitions, tokens |
| Property | `TestPropertyExclusiveOwner` | repeated exclusive acquire |

## Deterministic time

Pass `internal/testclock.Clock` via `Config.Clock` and `memory.WithClock`.
Advance time explicitly instead of sleeping.

## Critical invariants (must hold)

1. Only one gen owns a claim at a committed fencing-token epoch
2. Tokens never decrease
3. Stale gen cannot release newer owner's claim
4. Failed transfer cannot remain pending forever
5. Closing coordinator stops internal goroutines
6. Successful handoff retires previous owner
7. Aborted handoff does not silently discard ownership
8. Backend retries do not duplicate committed transitions
