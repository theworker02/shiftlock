// Package stress holds longer-running stress helpers.
//
// PR CI must not run multi-hour stress. Use build tag `stress` or the nightly
// workflow stubs under .github/workflows/.
package stress

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/theworker02/shiftlock"
)

// Options configures a short stress burst suitable for local/nightly runs.
type Options struct {
	Workers  int
	Claims   int
	Duration time.Duration
}

// Burst runs concurrent acquire/release against be for Duration.
func Burst(ctx context.Context, be shiftlock.Backend, opt Options) (ops, fails int64, err error) {
	if opt.Workers <= 0 {
		opt.Workers = 4
	}
	if opt.Claims <= 0 {
		opt.Claims = 2
	}
	if opt.Duration <= 0 {
		opt.Duration = time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Duration)
	defer cancel()
	var o, f atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < opt.Workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("s-%d", w)
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				name := fmt.Sprintf("stress-%d", w%opt.Claims)
				rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
					ClaimName: name, GenerationID: id, TTL: time.Second,
				})
				if err != nil {
					f.Add(1)
					continue
				}
				o.Add(1)
				_ = be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{
					ClaimName: name, GenerationID: id, Token: rec.FencingToken,
				})
			}
		}(w)
	}
	wg.Wait()
	return o.Load(), f.Load(), nil
}
