# Clock Safety

ShiftLock never invents lease extensions locally. Lease expiry is determined by
backend timestamps (`ExpiresAt`) advanced only via successful `RenewClaim` /
`AcquireClaim` / transfer mutations.

## Rules

1. Wall-clock jumps forward may expire leases; that is correct fail-closed behavior.
2. Wall-clock jumps backward do not extend leases; `ExpiresAt` is absolute.
3. Test clocks (`internal/testclock`) must be advanced explicitly; do not mix
   `SetDelay` with a fake clock unless the test advances time.
4. Adaptive heartbeats (when enabled) only change renew *attempt* interval —
   never the backend TTL without a successful renew RPC.

## Ambiguous outcomes

If a renew/commit returns an error that may mean “succeeded but response lost”
(`ErrAmbiguous`), ShiftLock must re-read claim state (and rely on `OperationID`
idempotency) rather than assuming failure. Default degradation is fail-closed.
