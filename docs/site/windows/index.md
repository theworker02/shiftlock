# Windows

Windows Service wrappers are application-owned. ShiftLock remains a library; use SCM or NSSM to host your process.

Named-pipe agent transport exists under `cmd/shiftlock-agent` (Windows build tags). Do not expose unauthenticated TCP control in production.
