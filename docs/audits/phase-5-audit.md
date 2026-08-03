# Phase 5 Repository Audit

**Date:** 2026-08-03 (updated same day — continuation pass)  
**Module:** `github.com/theworker02/shiftlock`  
**Scope:** Full repository inspection prior to / during Phase 5 production-proof work.

## Current architecture

ShiftLock is a reusable Go library that coordinates ownership of named **claims**
across process **generations**, using monotonically increasing **fencing tokens**.

```
Coordinator → Claim / Lease / Handoff / DrainGroup / Readiness / Events
                    ↓
                 Backend (memory | postgres | redis | kubernetes)
```

Generation states: `joining → standby → preparing → active → draining → transferring → retired|failed`.

## Implemented features (Phases 1–4 + Phase 5 progress)

| Area | Status |
|------|--------|
| Public API (`New`, `Claim`, `WaitForOwnership`, `Run`, `PrepareHandoff`) | Done |
| Fencing tokens + `TokenValidator` | Done |
| Memory / Postgres / Redis / Kubernetes backends | Done |
| Shared backend contract suite | Done |
| DrainGroup, readiness gates, event bus | Done |
| HTTP diagnostics, observe adapters, inspect CLI | Done |
| Docs, examples, CI, Apache-2.0 | Done |
| Handoff lease lifecycle / Abort re-arm / partial compensation | Done (incl. parallel bugfix) |
| Acquire serialization / Close compensation | Done |
| PG/Redis expire-before-mutate (prepare + mutate paths) | Done |
| K8s conflict → `ErrClaimHeld` | Done |
| `OperationID` idempotency (memory, redis Local, redis Lua wrap, postgres, k8s) | Done |
| `shiftlockcert` on memory + redis Local + k8s | Done |
| Operator CLI (timeline/explain/incident/recovery/readiness/rehearse) | Done |
| Integrations + chaos lab + loadtest stub | Done (compile-time / in-process) |
| Identity + fencing helpers (memory/postgres/redis + providers) | Done |

## Critical correctness risks (pre-Phase-5) — disposition

| # | Risk | Status |
|---|------|--------|
| 1 | Abort does not restore local lease/renewals | **Fixed** (`ensureLocalLease` / `Controls`) |
| 2 | Commit vs transfer-timeout Abort race | **Fixed** (serialized / compensated) |
| 3 | Partial Transfer/Commit | **Fixed** |
| 4 | TransferTimeout > LeaseTTL without renewals | **Fixed** (renew during reserved) |
| 5 | Acquire then Close leak | **Fixed** |
| 6 | Postgres/Redis prepare without expiry | **Fixed** |
| 7 | Memory WatchClaim double-close | **Fixed** |
| 8 | No OperationID / idempotency | **Fixed** (all primary backends; redis Lua via Go-side op keys) |
| 9 | No token overflow policy | **Fixed** |
| 10 | No capabilities negotiation | **Fixed** |

## Still open before v1.0.0-rc

- Live Redis Lua scripts do not embed OperationID atomically inside Lua (Go-side op key + state-based commit idempotency); prefer AOF + careful client usage; expand integration tests against real Redis.
- Postgres OperationID requires Migrate (`shiftlock_ops`); needs live `SHIFTLOCK_POSTGRES_URL` certification in CI.
- Redis generation map / K8s generations durability still process-local.
- P3 coordination: ownership groups, claim deps, ClaimScope, adaptive heartbeats, warm-up/activation checks, checkpoints, protocol version negotiation, schema migration CLI.
- Optional K8s controller/CRDs + admission validator.
- Multi-hour stress remains nightly/manual only (`-tags=stress`, `nightly.yml`).
- Queue integrations are ownership guards without vendor SDKs (by design); expand runnable examples as needed.
- Public API nits still open: unused `AllowEmptyOnly` / `ErrAlreadyOwner`, policy flags not consulted by backends, `Claim(ctx)` ignoring context.
- Opt-in `Runtime` security composition (`runtime.go`) is wired to current `capability.Authority` / `audit.Store` APIs; deeper Runtime integration tests and epoch seeding still thin.

## Public API inconsistencies (unchanged / low priority)

- `AcquireRequest.AllowEmptyOnly` unused
- `ErrAlreadyOwner` unused
- `Policy.AllowForceRelease` / `RejectStaleRelease` not consulted by backends
- `Claim(ctx)` / `PrepareHandoff(ctx)` ignore context
- `OwnedBy` false during reserved (intentional for work; use `Controls` for renewals)

## Test / ops coverage now

- Contract + certification: memory, redis Local, kubernetes (in-memory lease client)
- Lost-success-response commit idempotency tests (memory/redis/k8s/lab)
- Chaos lab `lab/` scenarios + docker-compose deps
- `shiftlock-inspect` operator commands
- `shiftlock-loadtest` (explicit destructive ack; memory only in-binary)
- CI certification workflow expanded; nightly stub for short stress

## Migration notes

- Empty `OperationID` = legacy non-idempotent path (backward compatible).
- Capability negotiation may reject unsafe configs (fail-closed) — intentional.
- Postgres: run `Migrate` to create `ops` table before relying on durable idempotency.
