# ShiftLock

<p align="center">
  <img src="assets/logo/shiftlock-horizontal.svg" alt="ShiftLock" width="440"/>
</p>

<p align="center">
  <strong>A security-first Go resource fabric</strong><br/>
  Coordinate ownership, supervise workloads, enforce runtime policy,<br/>
  and lock down sensitive operations — without a hosted control plane.
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/theworker02/shiftlock"><img src="https://pkg.go.dev/badge/github.com/theworker02/shiftlock.svg" alt="Go Reference"/></a>
  <a href="https://github.com/theworker02/shiftlock/actions/workflows/ci.yml"><img src="https://github.com/theworker02/shiftlock/actions/workflows/ci.yml/badge.svg" alt="CI"/></a>
  <a href="https://github.com/theworker02/shiftlock/releases/tag/v0.8.0"><img src="https://img.shields.io/github/v/release/theworker02/shiftlock?include_prereleases&sort=semver" alt="Release"/></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="License"/></a>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/theworker02/shiftlock"><strong>Go module docs</strong></a>
  ·
  <a href="https://github.com/theworker02/shiftlock/releases/tag/v0.8.0">v0.8.0</a>
  ·
  <a href="docs/architecture.md">Architecture</a>
  ·
  <a href="docs/problems/README.md">Problem guides</a>
</p>

---

Graceful shutdown stops an old process. **ShiftLock** decides who may perform
protected work next — with fencing tokens so a stale process cannot keep acting
after losing ownership — and optionally extends that same model to supervisors,
workflows, databases, queues, and APIs.

## Go module

| | |
|---|---|
| **Module path** | [`github.com/theworker02/shiftlock`](https://pkg.go.dev/github.com/theworker02/shiftlock) |
| **Latest release** | [`v0.8.0`](https://pkg.go.dev/github.com/theworker02/shiftlock@v0.8.0) |
| **Repository** | [github.com/theworker02/shiftlock](https://github.com/theworker02/shiftlock) |

```bash
go get github.com/theworker02/shiftlock@v0.8.0
```

Supports the current and previous stable Go releases. Core stays stdlib-first;
backends and integrations are isolated optional packages.

## Quick start

```go
package main

import (
	"context"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
)

func main() {
	be := memory.New()
	defer be.Close()

	coord, err := shiftlock.New(shiftlock.Config{
		Service:    "billing",
		InstanceID: "pod-a",
		Backend:    be,
		LeaseTTL:   15 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	defer coord.Close()

	_ = coord.Run(context.Background(), shiftlock.Worker{
		Name: "billing-reconciler",
		Run: func(ctx context.Context, ownership *shiftlock.Lease) error {
			// Persist ownership.FencingToken() with every protected write.
			<-ctx.Done()
			return nil
		},
	})
}
```

Try the examples:

```bash
go run ./examples/singleton-worker
go run ./examples/runtime-supervisor
go run ./examples/secure-control-plane
go run ./examples/infrastructure-orchestrator
go run ./examples/object-store-sync
```

## What ShiftLock is (and is not)

| It is | It is not |
|-------|-----------|
| Ownership handoff + fencing for Go processes | A hosted control plane |
| An opt-in runtime supervisor & security layer | A Kubernetes-only framework |
| A shared fabric around DBs, queues, APIs, files | A replacement for those systems |
| Importable as a normal Go module | A SaaS product |

> Protect who may perform sensitive work, when it may run, and how responsibility moves safely between instances.

## Core coordination

```go
claim, err := coordinator.Claim(ctx, "billing-reconciler")
lease, err := claim.WaitForOwnership(ctx)
// lease.Context(), lease.FencingToken()

handoff, err := coordinator.PrepareHandoff(ctx)
_ = handoff.Drain(ctx)
_ = handoff.Transfer(ctx, successorGenerationID)
_ = handoff.Commit(ctx) // or Abort — rolls back reservation safely
```

Generation flow: `joining → standby → preparing → active → draining → transferring → retired | failed`.

## Runtime & security (opt-in)

```go
rt, err := shiftlock.NewRuntime(shiftlock.RuntimeConfig{
	Config:           shiftlock.Config{Service: "billing", InstanceID: "pod-a", Backend: be},
	SecurityProfile:  shiftlock.ProfileStandard,
	EnableSupervisor: true,
	EnableAudit:      true,
})
defer rt.Close()

_ = rt.Supervisor()  // ownership-aware tasks, bounded restarts
_ = rt.Lockdown()    // emergency stop without erasing evidence
_ = rt.Capabilities()
```

Existing `shiftlock.New` / Coordinator APIs stay unchanged. See
[Phase 5→6 migration](docs/migration/phase-5-to-phase-6.md).

## Resource fabric (opt-in)

```go
rt, err := shiftlock.NewRuntime(shiftlock.RuntimeConfig{
	Config:          shiftlock.Config{Service: "billing", InstanceID: "pod-a", Backend: be},
	EnableResources: true,
	EnableWorkflows: true,
})
defer rt.Close()

_, _ = rt.Resources().Register(/* adapters */)
_, _ = rt.Workflows().Run(ctx, "drain-reconcile", workflow.RunOptions{})
```

Local-first durable state:

```go
shiftlock.WithLocalStateDir("/var/lib/shiftlock")(&cfg)
```

Problem-oriented guides: [docs/problems](docs/problems/README.md).

## Backends

| Backend | Package | Notes |
|---------|---------|-------|
| Memory | [`backend/memory`](backend/memory) | Tests, fault injection, certification |
| PostgreSQL | [`backend/postgres`](backend/postgres) | Transactions, row locks, durable `OperationID` |
| Redis | [`backend/redis`](backend/redis) | Lua CAS; AOF recommended for durability |
| Kubernetes | [`backend/kubernetes`](backend/kubernetes) | Lease objects; no k8s deps on core |

## Operator tooling

```bash
go run ./cmd/shiftlock version
go run ./cmd/shiftlock security scan -production -format text
go run ./cmd/shiftlock-inspect timeline -journal events.ndjson -claim NAME
go run ./cmd/shiftlock-inspect readiness-report -format json
```

Destructive recovery requires `--expected-owner`, `--expected-token`, `--reason`, and `--confirm` — never a blind force-unlock.

## Documentation

| Topic | Link |
|-------|------|
| Architecture | [docs/architecture.md](docs/architecture.md) |
| Handoff protocol | [docs/handoff-protocol.md](docs/handoff-protocol.md) |
| Fencing tokens | [docs/fencing-tokens.md](docs/fencing-tokens.md) |
| Failure model | [docs/failure-model.md](docs/failure-model.md) |
| Security model | [docs/security-model.md](docs/security-model.md) |
| Production checklist | [docs/production-checklist.md](docs/production-checklist.md) |
| Brand assets | [assets/brand/brand-guidelines.md](assets/brand/brand-guidelines.md) |
| **Go package reference** | **[pkg.go.dev/github.com/theworker02/shiftlock](https://pkg.go.dev/github.com/theworker02/shiftlock)** |

## License

Apache License 2.0 — see [LICENSE](LICENSE).
