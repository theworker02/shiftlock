# Ownership

Ownership is acquired through claims backed by a Backend (memory, Postgres, Redis, Kubernetes).

- At most one owner per claim under correct backend semantics
- Renewals keep the lease alive; expiry releases ownership
- Never weaken exclusivity for availability

See also root docs: `docs/architecture.md`, `docs/fencing-tokens.md`.
