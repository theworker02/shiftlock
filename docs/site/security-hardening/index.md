# Security Hardening

Canonical model:
[docs/security-model.md](https://github.com/theworker02/shiftlock/blob/main/docs/security-model.md).

- Deny privileged operations by default when security subsystems are enabled
- Require signed config in production
- Bounded queues / anti-replay caches
- No shell exec by default
- Run `shiftlock security scan -production` in CI

See also [Threat Model](../threat-model/index.md) and the
[Production Checklist](../production-checklist/index.md).
