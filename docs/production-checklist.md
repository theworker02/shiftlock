# Production Checklist — Security Control Plane

Use before enabling Phase 6 security features in production.

## Ownership & backends

- [ ] Backend chosen and certified (`shiftlockcert` / contract tests)
- [ ] TLS to Postgres/Redis; least-privilege credentials
- [ ] `LeaseTTL` sized for GC pauses without long unowned windows
- [ ] Journals durable and rotated

## Authorization

- [ ] Security profile explicit (`ProfileStandard` or stricter — not testing)
- [ ] Privileged ops require capabilities; deny-by-default verified
- [ ] Signing keys provisioned; key ring rotation documented
- [ ] Capability TTLs short; single-use for unlock / destructive commands
- [ ] No wildcard permissions issued

## Integrity

- [ ] `configlock` RequireSignatures + Production for prod activation
- [ ] Audit enabled; verify on start if fail-closed
- [ ] Snapshot / incident pipelines redaction-tested

## Containment

- [ ] Lockdown auto-triggers reviewed (audit tamper, split-brain, flood)
- [ ] Unlock runbook: expected ID + confirm + strong auth
- [ ] Exec allowlist empty unless explicitly required (never shell-by-default)
- [ ] Agent listener local-only (no unauthenticated TCP)

## Queues & resources

- [ ] EventBuffer / election EventBuffer bounded
- [ ] Anti-replay max entries set
- [ ] Supervisor restart bounds set
- [ ] No unbounded application channels feeding ShiftLock hooks

## Verification

```bash
go test ./...
go test -race ./...
go vet ./...
go run ./cmd/shiftlock security scan -production -format text
go run ./cmd/shiftlock redteam run
go run ./examples/secure-control-plane
```

- [ ] `security scan -production` reports no critical/high findings
- [ ] Red-team scenarios pass
- [ ] CI: contents:read, staticcheck, govulncheck green

## Operations

- [ ] On-call knows lockdown unlock procedure
- [ ] Audit export retention meets compliance needs
- [ ] Migration notes reviewed (`docs/migration/phase-5-to-phase-6.md`)
