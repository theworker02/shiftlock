# Handoffs

Controlled ownership transfer: prepare → drain → transfer → commit (or abort with restore).

Operation IDs provide idempotency for backend commits. Partial transfer compensation preserves correctness under faults.

See `docs/handoff-protocol.md`.
