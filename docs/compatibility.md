# Compatibility Matrix

Only claim what is exercised by CI or documented integration tags.

| Component | Go 1.25 | Go 1.26 | Notes |
|-----------|---------|---------|-------|
| Core + memory backend | exercised | exercised | `go test ./...` |
| Kubernetes MemoryLeaseClient | exercised | exercised | contract suite |
| Redis Local CAS | exercised | exercised | contract suite |
| Postgres | integration tag | integration tag | requires `SHIFTLOCK_POSTGRES_URL` |
| Redis Lua + live server | not in CI | not in CI | needs Client adapter |
| K8s client-go | not in CI | not in CI | user-provided LeaseClient |

OS: Windows + Linux exercised locally for unit tests; CI targets `ubuntu-latest`.
