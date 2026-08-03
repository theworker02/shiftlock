# Phase 6 Roadmap

ShiftLock evolves into a **security-first runtime coordination and control module** without delaying a stable core indefinitely.

## Version sequence

| Version | Focus |
|---------|--------|
| v0.10.0 | Runtime supervisor and leader election |
| v0.11.0 | Capabilities and policy engine |
| v0.12.0 | Maintenance and lockdown |
| v0.13.0 | Signed configuration and audit chain |
| v0.14.0 | Barriers, quorum, and distributed semaphore |
| v0.15.0 | Secure control API and local agent |
| v0.16.0 | Security scanner and red-team harness |
| v0.17.0 | Unified CLI and TUI |
| v0.18.0 | API stabilization |
| v1.0.0 | Stable coordination release |
| v1.1.0 | Security control-plane features stabilized |

## Stage status (repo)

| Stage | Focus | Status |
|-------|-------|--------|
| 1–3 | Runtime, capability, guard, audit, supervise, election, barrier, control | Done |
| 4–5 | Signing, configlock, scanner, redteam, CLI, agent, brand SVGs | Done |
| 6 | Hardening, fuzzers, resource bounds, supply-chain CI, docs | Done (this pass) |
| 7 | Brand polish, examples, docs site stubs, CLI polish | Done (TUI deferred) |

## Product standard

Every feature must support:

> ShiftLock protects and controls who may perform sensitive work, when that work may run, and how responsibility moves safely between application instances.

## Non-goals

- Hosted SaaS control plane
- Arbitrary remote shell execution
- Replacing full feature-flag or SIEM platforms
- Claiming perfect mutual exclusion from leases alone

## Remaining before calling Phase 6 “release complete”

- Wire MkDocs (or similar) for search/version selector on `docs/site/`
- Optional raster PNG generation (`assets/raster/export-rasters.ps1`)
- Stable API review + version tagging (v0.18 → v1.0 process)
- Full interactive TUI (intentionally deferred; CLI stub present)

See `docs/audits/phase-6-audit.md`, `docs/production-checklist.md`, and completion criteria in the Phase 6 plan (§70).
