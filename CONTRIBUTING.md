# Contributing to ShiftLock

Thanks for contributing. Correctness and explicit state transitions matter more
than cleverness.

## Repository

```bash
git clone https://github.com/theworker02/shiftlock.git
cd shiftlock
```

Module path: `github.com/theworker02/shiftlock`

## Development

1. Use the current or previous stable Go toolchain.
2. Run `go test ./...` and `go test -race ./...` before opening a PR.
3. Run `go vet ./...` (and `staticcheck ./...` if installed).
4. Prefer deterministic tests with `internal/testclock` over sleeps.
5. New backends must pass `backend/backendtest.RunContract`.

## Pull requests

- Keep the public API small and idiomatic.
- Document any safety-relevant invariant and add a test that proves it.
- Do not hide races behind retries or eventual-consistency wording.
- Core package should stay primarily stdlib; isolate heavy deps in backends.

## Code of conduct

See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
