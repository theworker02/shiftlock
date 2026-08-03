package ratelimit

import (
	"context"
	"testing"

	"github.com/theworker02/shiftlock/resource"
)

func TestTokenBucket(t *testing.T) {
	id := resource.MustParseResourceID("rate-limit/test/demo/payment-provider")
	r, err := New(Config{ID: id, Capacity: 2, RefillPerSecond: 0.0001, MaxConcurrent: 2})
	if err != nil {
		t.Fatal(err)
	}
	if r.Capabilities().SupportsFencing {
		t.Fatal("must not claim fencing")
	}
	if err := r.Allow(); err != nil {
		t.Fatal(err)
	}
	if err := r.Allow(); err != nil {
		t.Fatal(err)
	}
	if err := r.Allow(); err != ErrLimited {
		t.Fatalf("got %v", err)
	}
	r.Release()
	r.Release()
	snap, _ := r.Snapshot(context.TODO())
	if snap["rejected"] == "0" {
		t.Fatal("expected rejection counted")
	}
}
