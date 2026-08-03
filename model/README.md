# Formal Protocol Model

Package `model` is a deterministic reference implementation of the ShiftLock
ownership protocol. It performs **no backend I/O**. Use it to:

- Continuously verify ownership invariants under randomized action sequences
- Produce minimal reproducible failure sequences (JSON)
- Drive `internal/simulation` scenarios

## Invariants (15)

1. At most one committed owner per claim fencing-token epoch
2. Fencing tokens never decrease
3. Stale generation cannot release newer ownership
4. Failed/expired transfer cannot remain pending forever (bounded by timeout)
5. Aborted transfer restores prior owner without token advance
6. Committed transfer advances token exactly once
7. Unowned claims retain last fencing token
8. Reserved claims still have a controlling owner generation
9. Crash of candidate before commit does not discard ownership
10. Concurrent acquire yields exactly one winner per epoch
11. Expire clears owner but not token
12. Overflow refuses further acquires (claim unavailable)
13. Idempotent ops with same OperationID do not double-advance tokens
14. Disconnect does not invent local lease extensions
15. Only current fencing-token epoch may modify protected resources (model tracks Accept)

## Reproduction

On failure, tests print:

```
go test ./model -run TestRandom -shiftlock.seed=12345
```

And write `testdata/failures/<seed>.json`.
