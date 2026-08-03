// Package sqs integrates ShiftLock ownership with AWS SQS consumers.
package sqs

import (
	"context"
	"fmt"

	"github.com/theworker02/shiftlock"
)

// Guard binds an SQS consumer loop to a ShiftLock claim.
type Guard struct {
	Coordinator *shiftlock.Coordinator
	ClaimName   string
}

// RunOwned waits for ownership then runs fn until the lease ends.
func (g Guard) RunOwned(ctx context.Context, fn func(ctx context.Context, lease *shiftlock.Lease) error) error {
	if g.Coordinator == nil || g.ClaimName == "" {
		return fmt.Errorf("sqs: Coordinator and ClaimName required")
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
