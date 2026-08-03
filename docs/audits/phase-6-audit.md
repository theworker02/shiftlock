# Phase 6 Repository Audit

**Date:** 2026-08-03 (updated Stages 4–7)  
**Module:** `github.com/theworker02/shiftlock`  
**Scope:** Security-first runtime control plane on top of Phase 5 ownership/fencing.

## What Phase 5 already delivered

| Area | Status | Notes |
|------|--------|-------|
| Ownership / fencing / handoff correctness | Done | Renewals during reserved; Abort restore; partial Transfer compensation |
| OperationID idempotency | Done | Memory (+ contract); commit replay |
| Token overflow | Done | `ErrTokenOverflow`; no silent wrap |
| Capabilities negotiation | Done | Fail-closed `ValidateCapabilities` |
| Formal `model/` + simulation | Done | Protocol states / transitions / invariants |
| Certification / journals / health | Done | See `phase-5-audit.md` |

## Stages 1–3 (delivered)

Runtime façade, security types, capability/guard/audit, anti-replay, supervise, election, barrier, syncprim, health graph, commands, execguard, maintenance, lockdown, quarantine profiles.

## Stages 4–5 (delivered)

| Item | Location |
|------|----------|
| Ed25519 signing / key rings | `security/signing` |
| Config lifecycle + signatures | `configlock` |
| Audit chain verify + NDJSON | `audit` |
| Anti-replay package | `security/antireplay` |
| Attestation helpers | `security/attestation` |
| Secrets refs + redaction | `secrets` |
| Snapshots | `control/snapshot` |
| Security scanner | `security/scanner` |
| Red-team harness | `security/redteam` |
| Unified CLI | `cmd/shiftlock` |
| Local agent skeleton | `cmd/shiftlock-agent` |
| Brand SVG + guidelines | `assets/logo`, `assets/brand` |
| Social preview assets | `assets/social` |

## Stages 6–7 (this pass)

| Item | Status |
|------|--------|
| Security fuzzers (capability, audit, configlock, snapshot) | Done |
| Resource-bound audit (barrier waiters, redis local watch) | Done |
| Expanded red-team catalog (12 runnable scenarios) | Done |
| CI minimal permissions + dependency-discipline check | Done |
| staticcheck / govulncheck workflows | Present (improved permissions) |
| Docs site hierarchy (`docs/site/`) | Done (stubs + mkdocs.yml) |
| `security-model.md` / `threat-model.md` / production checklist | Done |
| `examples/secure-control-plane` (+ thinner demos) | Done |
| Raster export docs/script | Done (README + script; PNGs optional) |
| CLI `redteam` / `tui` stub | Done |
| Full TUI | Deferred (documented) |
| Windows service example | Documented under `docs/site/windows` (not shipped) |
| Hardware keys / mTLS binding / Byzantine quorum | Deferred |

## Non-negotiable constraints (still apply)

- Do not redesign Phase 1–5 ownership/fencing/handoff/backends.
- Security features are opt-in; existing APIs keep working.
- Core package stays stdlib-heavy.
- Deny privileged ops by default; no arbitrary shell; no unbounded queues.
- Never weaken ownership correctness for availability.

## Disposition vs §70 completion criteria

Most Phase 6 functional criteria are met in-tree. Remaining polish items:

- MkDocs search/version selector not wired (content hierarchy ready)
- Raster PNG set may be generated locally via `assets/raster/README.md`
- Stable API review / v1.0 tagging is a release process step (not done here)
- Some red-team scenarios use library-level fencing checks rather than full multi-node lab

See `docs/roadmap-phase-6.md` and `docs/production-checklist.md`.
