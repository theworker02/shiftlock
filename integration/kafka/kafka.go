// Package kafka integrates ShiftLock ownership with Kafka consumer groups.
//
// Optional dependency: this package compiles without kafka client libraries.
// Wire your consumer pause/resume to Lease.Context() and reject produce/consume
// with a stale fencing token.
package kafka

import (
	"context"
	"fmt"

	"github.com/theworker02/shiftlock"
)

// Guard binds Kafka partition ownership to a ShiftLock claim.
type Guard struct {
	Coordinator *shiftlock.Coordinator
	ClaimName   string
}

// RunOwned pauses until ownership, then invokes fn until lease context ends.
func (g Guard) RunOwned(ctx context.Context, fn func(ctx context.Context, lease *shiftlock.Lease) error) error {
	if g.Coordinator == nil || g.ClaimName == "" {
		return fmt.Errorf("kafka: Coordinator and ClaimName required")
	}
	cl, err := g.Coordinator.Claim(ctx, g.ClaimName)
	if err != nil {
		return err
	}
	lease, err := cl.WaitForOwnership(ctx)
	if err != nil {
		return err
	}
	return fn(lease.Context(), lease)
}
