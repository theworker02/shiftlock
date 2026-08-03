// Package scheduler integrates ShiftLock ownership with singleton schedulers.
package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/theworker02/shiftlock"
)

// Singleton runs tick only while this generation owns the claim.
type Singleton struct {
	Coordinator *shiftlock.Coordinator
	ClaimName   string
	Interval    time.Duration
}

// Run ticks fn under ownership until ctx is cancelled.
func (s Singleton) Run(ctx context.Context, fn func(ctx context.Context, lease *shiftlock.Lease) error) error {
	if s.Coordinator == nil || s.ClaimName == "" {
		return fmt.Errorf("scheduler: Coordinator and ClaimName required")
	}
	interval := s.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	return s.Coordinator.Run(ctx, shiftlock.Worker{
		Name: s.ClaimName,
		Run: func(wctx context.Context, lease *shiftlock.Lease) error {
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-wctx.Done():
					return wctx.Err()
				case <-t.C:
					if err := fn(wctx, lease); err != nil {
						return err
					}
				}
			}
		},
	})
}
