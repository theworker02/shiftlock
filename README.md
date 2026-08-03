# ShiftLock

[![CI](https://github.com/theworker02/shiftlock/actions/workflows/ci.yml/badge.svg)](https://github.com/theworker02/shiftlock/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/theworker02/shiftlock.svg)](https://pkg.go.dev/github.com/theworker02/shiftlock)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

**ShiftLock** is a security-first Go **resource fabric**: coordinate, protect,
supervise, and recover application resources across processes, services, data
systems, and deployments — without a hosted control plane.

Graceful shutdown stops an old process. ShiftLock decides **who may perform
protected work**, how ownership moves, and (optionally) how databases, queues,
APIs, and workflows stay safe under fencing tokens, policy, and lockdown.

## Install

```bash
go get github.com/theworker02/shiftlock@latest
```

Supports the current and previous stable Go releases.

## Quick start

```go
be := memory.New()
coord, err := shiftlock.New(shiftlock.Config{
    Service: "billing", InstanceID: "pod-a", Backend: be, LeaseTTL: 15 * time.Second,
})
err = coord.Run(ctx, shiftlock.Worker{
    Name: "billing-reconciler",
    Run: func(ctx context.Context, ownership *shiftlock.Lease) error {
        // fence writes with ownership.FencingToken()
        <-ctx.Done()
        return nil
    },
})
```

```bash
go run ./examples/singleton-worker
go run ./examples/runtime-supervisor
go run ./examples/secure-control-plane
go run ./examples/infrastructure-orchestrator
go run ./examples/object-store-sync
go run ./cmd/shiftlock-inspect rehearse-handoff
```

Module: [`github.com/theworker02/shiftlock`](https://github.com/theworker02/shiftlock) — clone `https://github.com/theworker02/shiftlock.git`.

## Runtime (Phase 6, opt-in)

```go
rt, err := shiftlock.NewRuntime(shiftlock.RuntimeConfig{
    Config:          shiftlock.Config{Service: "billing", InstanceID: "pod-a", Backend: be},
    SecurityProfile: shiftlock.ProfileStandard,
    EnableSupervisor: true,
    EnableAudit:      true,
})
defer rt.Close()
_ = rt.Supervisor() // task modes, bounded restarts
_ = rt.Features()
```

Security features are opt-in; `shiftlock.New` / Coordinator APIs remain unchanged.
See [docs/migration/phase-5-to-phase-6.md](docs/migration/phase-5-to-phase-6.md) and
[docs/roadmap-phase-6.md](docs/roadmap-phase-6.md).

## Resource fabric & workflows (Phase 7, opt-in)

```go
rt, err := shiftlock.NewRuntime(shiftlock.RuntimeConfig{
    Config:          shiftlock.Config{Service: "billing", InstanceID: "pod-a", Backend: be},
    EnableResources: true,
    EnableWorkflows: true,
    EnableLockdown:  true,
})
defer rt.Close()

_, _ = rt.Resources().Register(resmemory.Worker("production", "billing", "reconciler"), resource.Metadata{})

def, _ := workflow.Define("drain-reconcile").
    Step("drain", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
        return workflow.Result{}, nil
    }).
    Build()
_ = rt.Workflows().Register(def)
_, _ = rt.Workflows().Run(ctx, "drain-reconcile", workflow.RunOptions{})
```

Local-first durable state:

```go
shiftlock.WithLocalStateDir("/var/lib/shiftlock")(&cfg) // workflows journal + registry path
```

See [docs/roadmap-phase-7.md](docs/roadmap-phase-7.md) and
[docs/audits/phase-7-audit.md](docs/audits/phase-7-audit.md). Ownership quick start above is unchanged.

## Core API

```go
claim, err := coordinator.Claim(ctx, "billing-reconciler")
lease, err := claim.WaitForOwnership(ctx)
handoff, err := coordinator.PrepareHandoff(ctx)
handoff.Drain(ctx)
handoff.Transfer(ctx, successorGenerationID)
handoff.Commit(ctx) // or Abort
```

Generation states: `joining → standby → preparing → active → draining → transferring → retired|failed`.

## Backends

| Backend | Package | Notes |
|---------|---------|-------|
| Memory | `backend/memory` | Tests, fault injection, certification |
| PostgreSQL | `backend/postgres` | Row locks + durable `OperationID` ops table (`Migrate`) |
| Redis | `backend/redis` | Lua CAS + Local in-process; AOF recommended |
| Kubernetes | `backend/kubernetes` | Lease objects; no k8s deps on core |

## Operator tooling

```bash
go run ./cmd/shiftlock version
go run ./cmd/shiftlock status
go run ./cmd/shiftlock security scan -production -format text
go run ./cmd/shiftlock redteam run
go run ./cmd/shiftlock audit verify -file audit.ndjson
go run ./cmd/shiftlock snapshot create -out snap.json

go run ./cmd/shiftlock-inspect timeline -journal events.ndjson -claim NAME
go run ./cmd/shiftlock-inspect explain -journal events.ndjson -claim NAME
go run ./cmd/shiftlock-inspect incident create -journal events.ndjson -out incident.tar.gz
go run ./cmd/shiftlock-inspect recovery abort-transfer --claim C --expected-owner G --expected-token N --reason "..." --dry-run
go run ./cmd/shiftlock-inspect readiness-report -format json
go run ./cmd/shiftlock-inspect rehearse-handoff
```

`shiftlock-inspect` remains the journal/recovery toolkit and thin-aliases unified
`shiftlock` subcommands when that binary is on `PATH`.

Recovery never blind force-unlocks: `--expected-owner`, `--expected-token`, `--reason`, and `--confirm` are required to mutate.

## Safety & certification

```bash
go test ./backend/memory -run TestCertification
go test ./backend/redis -run TestLocalCertification
go test ./backend/kubernetes -run TestCertification
go test ./lab
go test ./model -count=1
```

- Formal model: `model/`
- Simulation: `internal/simulation/`
- Fault injection: `backend/faultinject`
- Chaos lab: `lab/` (+ `lab/docker-compose.yml`)
- Audits: [phase-5](docs/audits/phase-5-audit.md) · [phase-6](docs/audits/phase-6-audit.md) · [phase-7](docs/audits/phase-7-audit.md)
- Red-team: `go test ./security/redteam`
- Scanner: `go run ./cmd/shiftlock security scan -production`

## Integrations

Optional ownership guards (no vendor SDKs on core): `integration/{kafka,nats,rabbitmq,sqs,scheduler,httpserver,grpcserver}` — see [integration/README.md](integration/README.md).

Identity providers: `identity/{hostname,environment,pod,aws}`.  
Fencing helpers: `fencing/{memory,postgres,redis}`.

## Documentation

- [Architecture](docs/architecture.md) · [Handoff](docs/handoff-protocol.md) · [Fencing](docs/fencing-tokens.md)
- [Failure model](docs/failure-model.md) · [Backends](docs/backends.md) · [Operations](docs/operations.md)
- [Clock safety](docs/clock-safety.md) · [Compatibility](docs/compatibility.md) · [Release policy](docs/release-policy.md)
- [Testing](docs/testing.md) · [Kubernetes](docs/kubernetes.md) · [Security](docs/security.md)
- [Phase 5 audit](docs/audits/phase-5-audit.md) · [Phase 6 audit](docs/audits/phase-6-audit.md) · [Phase 7 audit](docs/audits/phase-7-audit.md)
- [Phase 5→6 migration](docs/migration/phase-5-to-phase-6.md) · [Phase 6 roadmap](docs/roadmap-phase-6.md) · [Phase 7 roadmap](docs/roadmap-phase-7.md)
- Brand: [assets/brand/brand-guidelines.md](assets/brand/brand-guidelines.md) · [assets/logo/shiftlock-mark.svg](assets/logo/shiftlock-mark.svg)

## License

Apache License 2.0 — see [LICENSE](LICENSE).
