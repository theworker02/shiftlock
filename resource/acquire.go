package resource

import (
	"context"
	"sort"
)

// LeaseHandle holds the result of a multi-resource acquire.
// IDs are in canonical acquisition order. Leases is populated by LeaseManager.AcquireAll.
type LeaseHandle struct {
	IDs    []ResourceID
	Leases []Lease
}

// AcquireFunc attempts to acquire a single resource lease/ownership.
// Adapters that do not support ownership should return ErrCapabilityClaimed.
type AcquireFunc func(ctx context.Context, id ResourceID) error

// ReleaseFunc releases a previously acquired resource.
type ReleaseFunc func(ctx context.Context, id ResourceID) error

// AcquireAll acquires resources in canonical lexicographic ID order.
// On any failure it releases the partial set in reverse order and returns
// ErrPartialAcquire (wrapping the underlying error when present).
//
// Prefer LeaseManager.AcquireAll when using lease modes and fencing tokens.
func AcquireAll(ctx context.Context, ids []ResourceID, acquire AcquireFunc, release ReleaseFunc) (LeaseHandle, error) {
	if acquire == nil {
		return LeaseHandle{}, &Error{Op: "AcquireAll", Err: ErrInvalidArgument, Message: "nil acquire"}
	}
	ordered := append([]ResourceID(nil), ids...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].String() < ordered[j].String()
	})
	held := make([]ResourceID, 0, len(ordered))
	for _, id := range ordered {
		if err := ctx.Err(); err != nil {
			releasePartial(ctx, held, release)
			return LeaseHandle{}, &Error{Op: "AcquireAll", Err: err, Message: "context done"}
		}
		if err := acquire(ctx, id); err != nil {
			releasePartial(ctx, held, release)
			return LeaseHandle{}, &Error{Op: "AcquireAll", ID: id, Err: ErrPartialAcquire, Message: err.Error()}
		}
		held = append(held, id)
	}
	return LeaseHandle{IDs: held}, nil
}

// ReleaseAll releases held resources in reverse acquisition order.
func ReleaseAll(ctx context.Context, h LeaseHandle, release ReleaseFunc) error {
	releasePartial(ctx, h.IDs, release)
	return nil
}

func releasePartial(ctx context.Context, held []ResourceID, release ReleaseFunc) {
	if release == nil {
		return
	}
	for i := len(held) - 1; i >= 0; i-- {
		_ = release(ctx, held[i])
	}
}
