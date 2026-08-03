# Audit

Package `audit`: hash-chained, optionally signed records.

`Verify` / `VerifyRecords` detect mutation, removal, and sequence gaps. Fail closed when `AuditFailClosed` is set.

```bash
go run ./cmd/shiftlock audit verify -file audit.ndjson
```
