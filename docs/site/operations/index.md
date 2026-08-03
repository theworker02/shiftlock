# Operations

Operator runbooks and production guidance:

- CLI: `go run ./cmd/shiftlock` and `go run ./cmd/shiftlock-inspect`
- Destructive recovery requires `--expected-owner`, `--expected-token`, `--reason`, and `--confirm`
- Prefer readiness reports and audit timelines over blind force-unlock

Repository runbook:
[docs/operations.md](https://github.com/theworker02/shiftlock/blob/main/docs/operations.md).

See also the [Production Checklist](../production-checklist/index.md).
