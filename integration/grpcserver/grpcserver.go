// Package grpcserver integrates ShiftLock ownership with gRPC unary interceptors.
//
// Compiles without google.golang.org/grpc. Adapt Guard.Allow to your interceptor.
package grpcserver

import (
	"context"
	"fmt"

	"github.com/theworker02/shiftlock"
)

// Guard checks claim ownership for RPC handlers.
type Guard struct {
	Coordinator *shiftlock.Coordinator
	ClaimName   string
}

// Allow returns nil when this generation owns the claim.
func (g Guard) Allow(ctx context.Context) error {
	if g.Coordinator == nil || g.ClaimName == "" {
		return fmt.Errorf("grpcserver: Coordinator and ClaimName required")
	}
	cl, err := g.Coordinator.Claim(ctx, g.ClaimName)
	if err != nil {
		return err
	}
	if !cl.Ownership().OwnedBy(g.Coordinator.Generation().ID) {
		return shiftlock.ErrNotOwner
	}
	return nil
}
