# Handoff protocol

## Normal sequence

1. **Register** — successor generation enters `joining` → `standby`.
2. **Readiness** — successor runs gates (`preparing`); on success returns to `standby`.
3. **Drain** — current owner `PrepareHandoff` → `Drain`: reject new work, wait for `DrainGroup`.
4. **Reserve** — `Transfer(successorID)` sets claim phase `reserved` with `pending_successor` (token unchanged).
5. **Token advance** — `Commit` atomically increments fencing token and assigns owner to successor.
6. **Active** — successor observes ownership / acquires lease and becomes `active`.
7. **Stale observed** — previous owner's renewals fail with `ErrStaleToken` / lease context canceled.
8. **Confirm / retire** — previous generation transitions to `retired`.

## Rollback

If the successor fails before `Commit`, call `Abort`:

- claim returns to `owned` by the previous generation
- fencing token is **not** advanced
- ownership is not silently discarded

## Failure modes

| Failure | Behavior |
|---------|----------|
| Owner crashes | Lease expires; another generation may acquire (new token) |
| Successor crashes after prepare | Transfer timeout aborts; owner restored |
| Commit fails mid-flight | Reservation remains abortable; no dual owners |
| Network partition of old owner | Renewals fail; successor may acquire after expiry; old release with stale token rejected |
| Multiple candidates | Only one `AcquireClaim` wins; others get `ErrClaimHeld` |
| Drain timeout | `Drain` returns `ErrTimeout`; handoff can Abort |
| Abort after commit | Rejected (`ErrInvalidState`) |
| Double prepare different successors | `ErrConcurrentTransfer` |

See [failure-model.md](failure-model.md) for expanded scenarios.
