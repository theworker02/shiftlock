# Failure model

ShiftLock is designed so competing generations **cannot both hold a valid
committed fencing-token epoch**, and a stale generation **cannot overwrite or
release newer ownership**.

## Crash of active owner

- Heartbeat/renew stops; lease `ExpiresAt` elapses.
- Backend marks claim `unowned` but **keeps** the fencing token.
- Successor `AcquireClaim` advances token and becomes owner.
- If the crashed owner restarts with the old token, renew/release fail with
  `ErrStaleToken`.

## Crash of successor before commit

- Claim remains `reserved` with previous owner still recorded.
- `TransferTimeout` triggers abort (or operator calls `Abort`).
- Token unchanged; previous owner continues.

## Failed commit (backend error)

- Reservation remains; commit is not partially visible.
- `Abort` restores `owned` by the previous generation.

## Partition of previous owner after handoff

- Renewals fail.
- `ReleaseClaim` with stale token is rejected — cannot clear the new owner's claim.

## Concurrent acquire storms

- Backend serializes with locks (memory mutex, `SELECT FOR UPDATE`, Redis Lua,
  K8s resourceVersion).
- Exactly one winner per epoch; proven by contract tests with 100+ goroutines.

## Interrupted deploy

- New generation fails readiness → `failed`; does not acquire.
- Old generation keeps ownership (no silent gap) unless it already drained and
  committed.

## Retry amplification

- All mutating backend calls are CAS-guarded on fencing token / phase.
- Retries do not duplicate state transitions once committed.
