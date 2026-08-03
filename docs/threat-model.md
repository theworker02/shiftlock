# Threat Model

## Assets

- Claim ownership and fencing-token high-water marks
- Capability tokens and signing keys
- Signed configuration bundles
- Audit chain integrity
- Lockdown / maintenance durable state
- Incident bundles and runtime snapshots (must stay secret-free)

## Actors

| Actor | Trust |
|-------|-------|
| Deploying operator | Trusted to configure backends, keys, profiles |
| Backend store (DB/Redis/K8s API) | Trusted for durability/CAS; compromise is catastrophic |
| Application process | Partially trusted; quarantine / lockdown contain damage |
| Network adversary | Can replay messages; anti-replay + signatures mitigate |
| Malicious generation / coworker pod | Assumed; fencing + capabilities + lockdown required |

## Trust boundaries

```text
[Operator / CI] --keys/config--> [Process: ShiftLock Runtime]
                                      |
                                      v
                              [Backend store]
```

Callers of the Go API are **not** authenticated by ShiftLock itself unless the
application binds verified identity into capabilities / command authorizers
(e.g. mTLS at an agent edge). Self-reported `ActorID` strings are **unverified**
labels for audit unless marked otherwise by the integrator.

## Threats and mitigations

| Threat | Mitigation |
|--------|------------|
| Dual ownership / stale writer | Fencing tokens + backend CAS |
| Forged / stolen capability | Signatures, TTL, single-use, revoke, epoch |
| Replay of commands | Bounded anti-replay nonce cache |
| Audit history rewrite | Hash chain verify; optional signatures |
| Config substitution | Content hash + required signatures in production |
| Lockdown bypass | Expected ID + confirm + strong auth; evidence retained |
| Resource exhaustion | Bounded buffers, restart limits, barrier caps |
| Secret leakage in dumps | `secrets` redaction; snapshot sanitization |
| Shell / RCE via control plane | Exec allowlist deny-by-default; no shell default |
| Supply-chain CI abuse | Minimal workflow permissions; govulncheck/staticcheck |

## Out of scope

- Compromised backend that ignores CAS
- Compromised host with signing private keys
- Social engineering of operators who unlock lockdown
- Formal verification of capability algebra (deferred)

## Related

- [Security model](security-model.md)
- [Security review notes](security.md)
- Red-team catalog: `security/redteam`
