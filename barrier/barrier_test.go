package barrier_test

import (
	"context"
	"testing"
	"time"

	"github.com/theworker02/shiftlock/barrier"
)

func TestBoundedParticipants(t *testing.T) {
	b, err := barrier.New(barrier.Config{MaxParticipants: 2, Policy: barrier.PolicyAll, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Arrive("a", 1)
	_ = b.Arrive("b", 1)
	if err := b.Arrive("c", 1); err == nil {
		t.Fatal("expected full")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := b.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}
