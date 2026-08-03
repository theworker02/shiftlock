# Security review notes

## Threat model (library)

ShiftLock trusts the backend store and the deploying operator. It does **not**
authenticate callers beyond whatever the backend provides (DB creds, Redis ACL,
Kubernetes RBAC) unless the application binds verified identity into
capabilities / command authorizers.

Canonical docs:

- [Security model](security-model.md)
- [Threat model](threat-model.md)
- [Production checklist](production-checklist.md)
- [Dependency discipline](dependency-discipline.md)

## Guarantees

- Stale fencing tokens cannot release or overwrite newer ownership (CAS).
- Diagnostics and events omit credentials and connection strings.
- Hooks/observers recover from panics to avoid process-wide crashes from
  telemetry bugs.

## Non-goals

- Cryptographic attestation of generation identity
- Encrypting claim payloads at rest (delegate to the backend)
- Multi-tenant isolation beyond backend-level ACLs

## Recommendations

- Use least-privilege DB/Redis/K8s credentials per service.
- Prefer TLS to PostgreSQL/Redis.
- Treat claim names as non-sensitive identifiers; do not put secrets in claim
  metadata or event attributes.
- Keep `LeaseTTL` short enough to bound unowned windows after crashes, but long
  enough to survive GC pauses / brief partitions.
