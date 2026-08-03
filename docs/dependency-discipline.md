# Dependency Discipline

ShiftLock’s **core module** (`github.com/theworker02/shiftlock`) must stay lightweight.

## Rules

1. Prefer the Go standard library.
2. Do not add direct third-party `require` directives to the root `go.mod` without an explicit design review.
3. Isolate Kubernetes, cloud SDKs, metrics, and TUI dependencies in optional packages/modules or build-tagged code so a normal import does not pull them.
4. Avoid reflection-heavy frameworks, global registries, and runtime code generation.
5. Crypto uses stdlib only (`crypto/ed25519`, `crypto/sha256`) via `security/signing` — no custom cryptography.

## CI check

The CI workflow fails if root `go.mod` gains a `require` block. When a dependency is truly needed, document rationale here and prefer an optional sub-module.

## Current state

Root `go.mod` is stdlib-only (`go 1.25`). Backend drivers that need external clients should remain carefully scoped; prefer interfaces in core and implementations that keep heavy SDKs optional.
