# Threat Model

Canonical document:
[docs/threat-model.md](https://github.com/theworker02/shiftlock/blob/main/docs/threat-model.md).

Trust the backend store and the deploying operator. Library callers are not
cryptographically authenticated unless you bind principals via capabilities or
mTLS at the application edge.

ShiftLock focuses on:

- Preventing stale owners from acting after losing a claim (fencing)
- Fail-closed privileged operations when security features are enabled
- Tamper-evident audit when the audit subsystem is opted in

Continue with [Security Hardening](../security-hardening/index.md).
