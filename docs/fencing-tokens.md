# Fencing tokens

```go
type FencingToken uint64
```

Every successful acquire or transfer commit advances the claim's fencing token
by one. Tokens are totally ordered per claim and never decrease — including
across release and expiry (the token value is retained when the claim becomes
unowned).

## Why lease timeout alone is insufficient

A partitioned owner may still believe its lease is valid while a successor has
already taken over after expiry. If the old owner then writes to a shared
resource (database row, queue, file), you get split-brain corruption.

Fencing tokens let the **resource** reject stale writers:

```go
v := shiftlock.NewTokenValidator()
if !v.Accept(tokenFromRequest) {
    return ErrStaleToken
}
// perform mutation
```

## CAS SQL pattern (PostgreSQL)

```sql
UPDATE claims
SET fencing_token = fencing_token + 1,
    owner_generation = $1,
    updated_at = now()
WHERE name = $2
  AND fencing_token = $3  -- expected
RETURNING fencing_token;
```

Zero rows updated means another writer won; abort and re-read.

ShiftLock backends embed this CAS into `CommitTransfer` / `AcquireClaim` so
ownership changes are atomic under concurrency.
