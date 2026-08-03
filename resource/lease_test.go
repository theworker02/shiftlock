package resource_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theworker02/shiftlock/resource"
	"github.com/theworker02/shiftlock/resource/memory"
)

func TestLeaseModesAndAcquireAll(t *testing.T) {
	reg := resource.NewRegistry(resource.RegistryConfig{})
	a := memory.Worker("dev", "svc", "a")
	b := memory.Worker("dev", "svc", "b")
	if _, err := reg.Register(a, resource.Metadata{}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Register(b, resource.Metadata{}); err != nil {
		t.Fatal(err)
	}
	lm := resource.NewLeaseManager(reg)

	_, err := lm.Lease(context.Background(), a.ID(), resource.LeaseRequest{
		Owner: "o1", Mode: resource.LeaseExclusive, Purpose: "migrate",
	})
	if !errors.Is(err, resource.ErrFenceRequired) {
		t.Fatalf("expected fence required, got %v", err)
	}
	l1, err := lm.Lease(context.Background(), a.ID(), resource.LeaseRequest{
		Owner: "o1", Mode: resource.LeaseExclusive, Purpose: "migrate", Fence: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lm.Lease(context.Background(), a.ID(), resource.LeaseRequest{
		Owner: "o2", Mode: resource.LeaseExclusive, Fence: 2,
	}); !errors.Is(err, resource.ErrLeaseHeld) {
		t.Fatalf("expected held, got %v", err)
	}
	if err := lm.Release(a.ID(), l1.Owner, l1.Mode); err != nil {
		t.Fatal(err)
	}

	feat := memory.Feature("dev", "svc", "flag")
	if _, err := reg.Register(feat, resource.Metadata{}); err != nil {
		t.Fatal(err)
	}
	if _, err := lm.Lease(context.Background(), feat.ID(), resource.LeaseRequest{
		Owner: "r1", Mode: resource.LeaseShared,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := lm.Lease(context.Background(), feat.ID(), resource.LeaseRequest{
		Owner: "r2", Mode: resource.LeaseReadOnly,
	}); err != nil {
		t.Fatal(err)
	}

	handle, err := lm.AcquireAll(context.Background(), map[string]resource.LeaseRequest{
		b.ID().String(): {Owner: "batch", Mode: resource.LeaseExclusive, Fence: 5},
		a.ID().String(): {Owner: "batch", Mode: resource.LeaseExclusive, Fence: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(handle.IDs) != 2 || handle.IDs[0].Name != "a" || handle.IDs[1].Name != "b" {
		t.Fatalf("order=%v", handle.IDs)
	}
	lm.ReleaseHandle(handle)
}

func TestAcquireAllPartialOnConflict(t *testing.T) {
	reg := resource.NewRegistry(resource.RegistryConfig{})
	a := memory.Worker("dev", "svc", "x")
	b := memory.Worker("dev", "svc", "y")
	_, _ = reg.Register(a, resource.Metadata{})
	_, _ = reg.Register(b, resource.Metadata{})
	lm := resource.NewLeaseManager(reg)
	_, err := lm.Lease(context.Background(), b.ID(), resource.LeaseRequest{
		Owner: "other", Mode: resource.LeaseExclusive, Fence: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = lm.AcquireAll(context.Background(), map[string]resource.LeaseRequest{
		a.ID().String(): {Owner: "me", Mode: resource.LeaseExclusive, Fence: 2},
		b.ID().String(): {Owner: "me", Mode: resource.LeaseExclusive, Fence: 2},
	})
	if !errors.Is(err, resource.ErrPartialAcquire) {
		t.Fatalf("%v", err)
	}
	if held := lm.Held(a.ID()); len(held) != 0 {
		t.Fatalf("partial not released: %v", held)
	}
	if held := lm.Held(b.ID()); len(held) != 1 {
		t.Fatalf("b should remain held by other: %v", held)
	}
}

func TestLeaseTTLExpiry(t *testing.T) {
	reg := resource.NewRegistry(resource.RegistryConfig{})
	w := memory.Feature("dev", "svc", "tmp")
	_, _ = reg.Register(w, resource.Metadata{})
	lm := resource.NewLeaseManager(reg)
	now := time.Unix(1000, 0).UTC()
	lm.SetClock(func() time.Time { return now })
	_, err := lm.Lease(context.Background(), w.ID(), resource.LeaseRequest{
		Owner: "o", Mode: resource.LeaseShared, TTL: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if held := lm.Held(w.ID()); len(held) != 0 {
		t.Fatalf("expected expiry, got %v", held)
	}
}
