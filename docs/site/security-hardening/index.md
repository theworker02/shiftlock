# Security Hardening

Canonical model: [docs/security-model.md](../../security-model.md).

- Deny privileged by default
- Require signed config in production
- Bounded queues / anti-replay caches
- No shell exec by default
- Run `shiftlock security scan -production` in CI
