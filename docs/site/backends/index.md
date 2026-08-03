# Backends

| Backend | Package | Notes |
|---------|---------|-------|
| Memory | `backend/memory` | Tests / certification |
| PostgreSQL | `backend/postgres` | Durable ops table |
| Redis | `backend/redis` | Lua CAS; AOF recommended |
| Kubernetes | `backend/kubernetes` | Lease objects |

Core module does not import cloud SDKs.
