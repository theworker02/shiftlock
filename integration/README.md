# ShiftLock integrations

These packages are **optional adapters**. They compile without queue/cloud SDKs
so the core module stays dependency-light.

| Package | Role |
|---------|------|
| `integration/kafka` | Bind consumer work to claim ownership |
| `integration/nats` | Queue-group singleton guard |
| `integration/rabbitmq` | Consumer ownership guard |
| `integration/sqs` | SQS poll loop ownership guard |
| `integration/scheduler` | Singleton ticker under ownership |
| `integration/httpserver` | HTTP middleware requiring ownership |
| `integration/grpcserver` | RPC ownership gate (no grpc dep) |

Pattern: wait for `Lease`, run work on `lease.Context()`, fence mutations with
`lease.FencingToken()`.
