# Security Model

ShiftLock’s security model is **opt-in and fail-closed** for privileged operations.

## Layers

1. **Ownership & fencing** — claims, generations, monotonic fencing tokens (always on for coordination).
2. **Authorization** — capabilities (`capability/`) and policy (`guard/`) when enabled.
3. **Integrity** — Ed25519 signing (`security/signing`), config bundles (`configlock/`), audit hash chains (`audit/`).
4. **Containment** — maintenance, lockdown, quarantine, exec allowlists (`control/*`).
5. **Detection** — scanner (`security/scanner`), red-team harness (`security/redteam`), snapshots with redaction.

## Defaults

| Control | Default |
|---------|---------|
| Privileged ops | Denied when guard/capabilities enabled |
| Shell execution | Denied (empty allowlist / dry-run) |
| Unsigned production config | Rejected when signatures required |
| Anti-replay cache | Bounded max entries |
| Event / election buffers | Bounded; drop on overflow |
| Barrier waiters / participants | Hard-capped by `MaxParticipants` |
| Task restarts | Bounded (not infinite) |
| Lockdown unlock | Requires expected ID + confirm + strong auth |

## Guarantees

- Stale fencing tokens cannot overwrite newer ownership (CAS / token checks).
- Capability delegation cannot widen permissions.
- Security epochs do not decrease; advance invalidates prior capabilities.
- Audit verification detects mutation, removal, and sequence gaps (tamper-evident, not tamper-proof).
- Snapshots and incident bundles redact secret-looking fields.
- Diagnostics omit connection strings / credentials.

## Non-goals

- Hosted multi-tenant SaaS control plane
- Arbitrary remote shell
- Perfect mutual exclusion from leases alone under a malicious backend
- Hardware-backed keys / mTLS principal binding (application responsibility; hooks only)

## Profiles

`SecurityProfile` expands to inspectable `SecuritySettings` (`ProfileTesting`, `ProfileStandard`, hardened/maximum variants). Overrides are explicit — no silent production weakenings.
