# Production Checklist

Before running ShiftLock in production:

- Pick a durable backend (PostgreSQL, Redis with AOF, or Kubernetes leases) — not memory
- Persist fencing tokens with every protected write
- Enable audit when using the opt-in runtime control plane
- Run `shiftlock security scan -production` in CI
- Prefer signed/config-locked configuration in production profiles
- Treat lockdown and maintenance as distinct: lockdown is fail-closed and stronger

Canonical checklist in the repository:
[docs/production-checklist.md](https://github.com/theworker02/shiftlock/blob/main/docs/production-checklist.md).
