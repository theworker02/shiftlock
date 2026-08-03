# CLI

```bash
go run ./cmd/shiftlock help
go run ./cmd/shiftlock security scan -production
go run ./cmd/shiftlock audit verify -file PATH
go run ./cmd/shiftlock snapshot create|diff
go run ./cmd/shiftlock redteam run
go run ./cmd/shiftlock resources list
go run ./cmd/shiftlock workflows list
go run ./cmd/shiftlock migrations list
go run ./cmd/shiftlock failover status
go run ./cmd/shiftlock-inspect timeline -journal events.ndjson
```

Fabric commands (`resources`, `workflows`, `migrations`, `failover`) seed an in-process demo Runtime.
Mutating paths require `-dry-run` or `-confirm`.

`shiftlock inspect` is a thin alias of `status`; prefer `shiftlock-inspect` for journals.
