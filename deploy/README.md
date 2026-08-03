# ShiftLock Deploy Templates

Minimal templates for running services that use ShiftLock. These do **not**
start ShiftLock as a standalone server — they show how to wire identity,
leases, and metrics into your process.

See subdirectories:

- `kubernetes/` — Deployment + Lease RBAC
- `docker-compose/` — local postgres/redis for integration tests
- `systemd/` — unit file with instance identity
- `nomad/` — job stanza sketch
- `bare-metal/` — env + run script
- `prometheus/` — alert examples
