# Quick Start

Install the module and try the examples:

```bash
go get github.com/theworker02/shiftlock@latest

go run ./examples/singleton-worker
go run ./examples/runtime-supervisor
go run ./examples/secure-control-plane
```

Full package reference: [pkg.go.dev/github.com/theworker02/shiftlock](https://pkg.go.dev/github.com/theworker02/shiftlock).
Source and releases: [github.com/theworker02/shiftlock](https://github.com/theworker02/shiftlock).

## Coordinator (core)

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

## Runtime (opt-in)

```go
rt, err := shiftlock.NewRuntime(shiftlock.RuntimeConfig{
	Config: shiftlock.Config{
		Service:    "billing",
		InstanceID: "pod-a",
		Backend:    be,
		LeaseTTL:   15 * time.Second,
	},
	SecurityProfile:  shiftlock.ProfileStandard,
	EnableSupervisor: true,
	EnableAudit:      true,
	EnableCapabilities: true,
	EnableLockdown:   true,
})
if err != nil {
	panic(err)
}
defer rt.Close()
```

Existing `shiftlock.New` / Coordinator APIs stay unchanged when you enable the
runtime. See [Concepts](../concepts/index.md) next, or jump to
[API Reference](../api-reference/index.md).
