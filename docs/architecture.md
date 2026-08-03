# Architecture

ShiftLock separates **generation lifecycle** from **claim ownership**.

```
┌─────────────┐     Claim / Renew / Transfer      ┌──────────────┐
│ Coordinator │ ─────────────────────────────────►│   Backend    │
│ (process)   │ ◄─────────────────────────────────│ memory/pg/…  │
└─────────────┘     fencing token + phase           └──────────────┘
       │
       ├── Claim handles (per named ownership unit)
       ├── DrainGroup (in-flight work)
       ├── Readiness gates
       ├── Handoff (drain → reserve → commit)
       └── Event bus (hooks + observers)
```

## Invariants

1. At most one generation owns a claim at a committed fencing-token epoch.
2. Fencing tokens never decrease for a claim.
3. A stale generation cannot release or mutate a newer owner's claim.
4. A failed/expired transfer cannot remain pending forever (timeout → abort).
5. Closing a coordinator cancels and awaits all supervised goroutines.
6. Successful handoff retires the previous owner generation.
7. Aborted handoff restores prior ownership without advancing the token.
8. Backend retries must be idempotent with respect to committed state
   (CAS on expected token / version).

## Packages

| Path | Role |
|------|------|
| `shiftlock` | Public API |
| `internal/stategraph` | Allowed generation transitions |
| `internal/supervisor` | Structured concurrency |
| `internal/testclock` | Deterministic time |
| `backend/*` | Pluggable stores |
| `observe/*` | Optional telemetry adapters |
