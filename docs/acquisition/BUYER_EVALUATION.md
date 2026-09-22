# Buyer evaluation â€” ShiftLock

## Goal

In 15â€“45 minutes, verify the Product builds or runs as documented and that proprietary notices are present.

## Steps

1. Confirm root `LICENSE` is proprietary and `ACQUISITION.md` exists.
2. Skim `README.md` install/run claims.
3. Execute:

```
```bash
go get github.com/theworker02/shiftlock@v0.11.0
```
```go
package main

import (
	"context"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
)

func main() {
	be := memory.New()
	defer be.Close()

	coord, err := shiftlock.New(shiftlock.Config{
		Service:    "billing",
		InstanceID: "pod-a",
		Backend:    be,
		LeaseTTL:   15 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	defer coord.Close()

	_ = coord.Run(context.Background(), shiftlock.Worker{
		Name: "billing-reconciler",
		Run: func(ctx context.Context, ownership *shiftlock.Lease) error {
			// Persist ownership.FencingToken() with every protected write.
			<-ctx.Done()
			return nil
		},
	})
}
```
```bash
```

4. Run tests if present (`npm test`, `pytest`, `cargo test`, `go test ./...`, etc.).
5. Record README vs observed behavior gaps in workpapers.

## Pass criteria

- [ ] Clone succeeds
- [ ] Documented happy path works **or** failure is explained
- [ ] Minimal path needs no surprise secrets
- [ ] License notices intact

*Updated: 2026-09-22*
