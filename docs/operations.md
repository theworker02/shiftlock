# Operations

## Heartbeats

Configure `LeaseTTL` and `RenewInterval` (must be `< LeaseTTL`). The coordinator
renews owned claims on a supervised goroutine. Lost renewals revoke the local
lease context so workers stop.

## Diagnostics

Mount the HTTP handler on your existing server (ShiftLock does not listen):

```go
http.Handle("/debug/shiftlock", shiftlock.DiagnosticsHandler(coord))
```

JSON includes generation state, claims, fencing tokens, drain/transfer status,
and last heartbeat. No secrets are included.

## Inspector CLI

```bash
go run ./cmd/shiftlock-inspect -service demo -claim billing-reconciler
go run ./cmd/shiftlock-inspect timeline -journal events.ndjson -claim NAME
go run ./cmd/shiftlock-inspect explain -journal events.ndjson -claim NAME
go run ./cmd/shiftlock-inspect incident create -journal events.ndjson -out incident.tar.gz
go run ./cmd/shiftlock-inspect recovery abort-transfer \
  --claim NAME --expected-owner GEN --expected-token N --reason "ops" --dry-run
go run ./cmd/shiftlock-inspect readiness-report -format text
go run ./cmd/shiftlock-inspect rehearse-handoff
```

Recovery mutations require `--expected-owner`, `--expected-token`, `--reason`, and
`--confirm`. There is no blind force-unlock path.

## Production readiness report

`readiness-report` emits text, JSON, or SARIF summarizing capability gates that
matter for an RC cut (CAS, OperationID idempotency, expire-before-mutate, renew
during reserved).

## Events

Register sync `Hooks` and async `Observers` on `Config`. Async delivery uses a
bounded buffer; overflow increments a drop counter (`Coordinator.EventDropped`).
Hook/observer panics are recovered.

## Telemetry

- `observe/slog` — structured logs
- `observe/prometheus` — atomic counters (wire to client_golang yourself)
- `observe/otel` — optional tracer interface without hard OTel dependency

## Graceful deploy

1. Start new generation; wait for readiness.
2. Old: `PrepareHandoff` → `Drain` → `Transfer(newID)` → `Commit`.
3. Confirm new generation is active; terminate old.
