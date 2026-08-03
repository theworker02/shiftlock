# Phase 7 Roadmap — Multi-Resource Runtime Fabric

ShiftLock expands from process handoff and runtime security into a **security-first Go resource fabric** for coordinating work across processes, services, data systems, infrastructure, and deployments.

Phase 5 ownership/fencing and Phase 6 control-plane guarantees are **not** weakened.

## Positioning

> Coordinate, protect, supervise, and recover application resources across processes, services, deployments, and infrastructure boundaries.

It remains an importable Go module — not a hosted control plane.

## Design principles

1. **Opt-in** — `shiftlock.New` / Coordinator APIs unchanged; fabric via `NewRuntime` flags.
2. **No global registries** — `resource.Registry` and `workflow.Engine` are Runtime-owned.
3. **Capability honesty** — never silently claim `Supports*` a resource cannot honor.
4. **Bounded** — max resources/instances with metrics and explicit errors (no unbounded queues).
5. **Lockdown** — when Runtime lockdown is active, protected resource mutations and mutating workflow steps are blocked.
6. **No blind retries** — ambiguous step outcomes enter `requires-reconciliation`.
7. **Stdlib core** — heavy adapters stay in isolated packages; root `go.mod` stays dependency-light.

## Version sequence

| Version | Focus |
|---------|--------|
| v0.19.0 | Resource registry and bundles |
| v0.20.0 | Multi-resource workflows |
| v0.21.0 | Database, queue, HTTP, and filesystem adapters |
| v0.22.0 | Migration and synchronization |
| v0.23.0 | Failover groups and resource epochs |
| v0.24.0 | Drift detection and reconciliation |
| v0.25.0 | Deployment and promotion workflows |
| v0.26.0 | Resource CLI and TUI |
| v0.27.0 | Adapter certification and simulation |
| v0.28.0 | API stabilization |

Official adapters may later split into independently versioned modules if maintenance quality requires it.

## Stage status

| Stage | Focus | Status |
|-------|--------|--------|
| 1 | Resource foundation (ID, kinds, registry, bundles, deps, health, epoch, memory adapters) | **Done** |
| 2 | Workflow foundation (definition, states, idempotency, compensate, checkpoints, dry-run) | **Done** |
| 3 | Adapters: postgres, redis, memory cache, filesystem, HTTP, queue | **Done** (stdlib / injection; no live cloud) |
| 4 | Leases + AcquireAll, failover, rate limits, budgets, drift, reconcile | **Done** |
| 5 | Migration cutover, sync, cache generation, secret rotation | **Done** |
| 6 | Resource CLI, recovery playbooks, examples | **Done** (full TUI deferred) |
| 7 | Certification expansion, simulation, fuzz, bounds | **Done** |

## Packages

| Package | Role |
|---------|------|
| `resource/` | Fabric types, registry, leases, drift, snapshots |
| `resource/database/postgres` | SQL pinger adapter (optional fencing injection) |
| `resource/cache/{memory,redis}` | Cache + generation control |
| `resource/storage/filesystem` | Hardened directory ownership / atomic replace |
| `resource/storage/object` | S3-shaped object store abstraction (+ memory) |
| `resource/service/http` | HTTP health, circuit, idempotent Execute |
| `resource/queue` | Queue pause/resume + memory backend |
| `resource/ratelimit` | Token-bucket rate-limit resource |
| `failover/` | Primary/standby groups; epoch on failover |
| `budget/` | MaxBytes/Duration/Retries with stop/pause/degrade |
| `reconcile/` | Bounded retry controllers; pause on lockdown |
| `migration/` | Phase-tracking with ownership/fencing, pause/resume, dry-run, app cutover hooks |
| `migration/dualwrite` | App-supplied dual-write helper |
| `promotion/` | Environment promotion workflow skeleton |
| `sync/` | Source/target sync with conflict policies + memory demo |
| `recovery/playbook` | Versioned recovery playbooks (validate/dry-run/confirm) |
| `workflow/` | Definitions, engine, parallel groups, journal/file checkpoints |

## Problem-oriented docs

See [`docs/problems/`](problems/index.md).

## Non-goals

- Hosted control plane / SaaS resource catalog
- Claiming distributed transactions from fabric metadata alone
- Pulling cloud SDKs into core

See `docs/audits/phase-7-audit.md`.
