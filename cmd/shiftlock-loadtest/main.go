// Command shiftlock-loadtest is a DESTRUCTIVE load generator for ShiftLock backends.
//
// It will acquire, transfer, and release claims. Never point at production without
// an explicit dedicated backend configuration.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
)

func main() {
	fs := flag.NewFlagSet("shiftlock-loadtest", flag.ExitOnError)
	backend := fs.String("backend", "", "REQUIRED: memory (only built-in; others via library)")
	confirm := fs.Bool("i-understand-destructive", false, "REQUIRED acknowledgement")
	workers := fs.Int("workers", 8, "concurrent workers")
	claims := fs.Int("claims", 4, "claim count")
	duration := fs.Duration("duration", 3*time.Second, "run duration")
	_ = fs.Parse(os.Args[1:])

	fmt.Fprintln(os.Stderr, "WARNING: shiftlock-loadtest is DESTRUCTIVE against the configured backend.")
	if *backend == "" || !*confirm {
		fmt.Fprintln(os.Stderr, "refusing: set -backend=memory and -i-understand-destructive")
		os.Exit(2)
	}

	var be shiftlock.Backend
	switch *backend {
	case "memory":
		be = memory.New()
	default:
		fmt.Fprintf(os.Stderr, "unsupported -backend %q in this binary (memory only)\n", *backend)
		os.Exit(2)
	}
	defer be.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	var ops, fails atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("w-%d", w)
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				name := fmt.Sprintf("lt-%d", w%*claims)
				rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
					ClaimName: name, GenerationID: id, TTL: time.Second,
					OperationID: shiftlock.OperationID(fmt.Sprintf("%s-%d", id, ops.Load())),
				})
				if err != nil {
					fails.Add(1)
					continue
				}
				ops.Add(1)
				_ = be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{
					ClaimName: name, GenerationID: id, Token: rec.FencingToken,
				})
			}
		}(w)
	}
	wg.Wait()
	fmt.Printf("loadtest done ops=%d fails=%d backend=%s\n", ops.Load(), fails.Load(), *backend)
}
