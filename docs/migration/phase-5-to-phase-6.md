# Migrating from Phase 5 to Phase 6

Phase 6 adds an optional **runtime control plane** on top of ShiftLock’s ownership and fencing model. Existing claim, handoff, and backend APIs continue to work.

## What stays the same

- `shiftlock.New` / `Coordinator` / `Claim` / `Lease` / fencing tokens
- Memory, PostgreSQL, Redis, and Kubernetes backends
- Drain groups, readiness gates, journals, certification suite
- Lease and fencing semantics are **not** silently changed

## What is new (opt-in)

| Capability | Package / API | Default |
|------------|---------------|---------|
| Unified runtime | `shiftlock.NewRuntime` | Opt-in |
| Supervisor | `runtime.Supervisor()` | Opt-in |
| Leader election | `election` (claims underneath) | Opt-in |
| Capabilities & policy | `capability`, `guard` | Deny-by-default when enabled |
| Maintenance / lockdown | `control/maintenance`, `control/lockdown` | Opt-in |
| Tamper-evident audit | `audit` | Opt-in signing |
| Config locking | `configlock` | Unsigned production activation rejected when required |
| Secrets refs | `secrets` (`env://`, `file://`) | Opaque refs; redaction helpers |
| Signing / anti-replay / attestation | `security/{signing,antireplay,attestation}` | Stdlib Ed25519 |
| Security scanner / red-team | `security/{scanner,redteam}` | CLI + harness |
| Snapshots | `control/snapshot` | Sanitized, hashed |
| Unified CLI / local agent | `cmd/shiftlock`, `cmd/shiftlock-agent` | No default TCP agent |
| Secure profiles | `SecurityProfile(...)` | Explicit configuration |

Security features are **opt-in before v2**. Hardened and maximum-security profiles require explicit configuration and never activate unsigned production policy by default.

## Incremental adoption

1. Keep using `Coordinator` for ownership handoffs.
2. Wrap with `NewRuntime` when you want supervisor + health composition.
3. Enable a security profile only after reviewing expanded settings.
4. Add capabilities/policy around operator and command surfaces.
5. Enable audit signing, configlock signatures, and lockdown triggers last.
6. Run `shiftlock security scan -production` and `shiftlock audit verify` in CI.

## Compatibility guarantees

- No silent change to lease duration semantics
- No silent change to fencing-token monotonicity
- Deprecated APIs receive migration notes in release changelogs
- Importing `github.com/theworker02/shiftlock` does not pull Kubernetes, TUI, or cloud SDKs

## Breaking changes

None expected for Phase 5 consumers who continue to use the coordination API only.
