package shiftlock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/faultinject"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/internal/testclock"
)

func TestAbortRestoresRenewals(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()
	a, err := shiftlock.New(shiftlock.Config{
		Service: "t", InstanceID: "a", GenerationID: "gen-a", Backend: be, Clock: clock,
		LeaseTTL: time.Second, RenewInterval: 100 * time.Millisecond,
		TransferTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	cl, _ := a.Claim(ctx, "c")
	lease, err := cl.WaitForOwnership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tok := lease.FencingToken()
	h, _ := a.PrepareHandoff(ctx)
	_ = h.Drain(ctx)
	_ = h.Transfer(ctx, "gen-b")
	if err := h.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	rec, _ := be.GetClaim(ctx, "c")
	if rec.OwnerGeneration != "gen-a" || rec.FencingToken != tok {
		t.Fatalf("%+v", rec)
	}
	// Advance past original lease in steps so renewals can extend.
	for i := 0; i < 15; i++ {
		clock.Advance(100 * time.Millisecond)
		time.Sleep(5 * time.Millisecond)
	}
	rec2, _ := be.GetClaim(ctx, "c")
	if rec2.Phase != shiftlock.ClaimOwned || rec2.OwnerGeneration != "gen-a" {
		t.Fatalf("lost ownership after abort: %+v", rec2)
	}
}

func TestIdempotentCommit(t *testing.T) {
	be := memory.New()
	defer be.Close()
	ctx := context.Background()
	rec, _ := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "a", TTL: time.Minute, OperationID: "acq"})
	_, _ = be.PrepareTransfer(ctx, shiftlock.TransferRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b", Token: rec.FencingToken, TTL: time.Minute, OperationID: "p",
	})
	req := shiftlock.CommitRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b", ExpectedToken: rec.FencingToken, TTL: time.Minute, OperationID: "c1",
	}
	o1, err := be.CommitTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	o2, err := be.CommitTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if o1.FencingToken != o2.FencingToken {
		t.Fatal("double advance")
	}
}

func TestTokenOverflow(t *testing.T) {
	be := memory.New()
	defer be.Close()
	be.ForceSetToken("c", shiftlock.MaxSafeFencingToken)
	_, err := be.AcquireClaim(context.Background(), shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "a", TTL: time.Minute})
	if !errors.Is(err, shiftlock.ErrTokenOverflow) {
		t.Fatalf("got %v", err)
	}
}

func TestAmbiguousCommitThenRead(t *testing.T) {
	inner := memory.New()
	defer inner.Close()
	be := faultinject.Wrap(inner)
	ctx := context.Background()
	rec, _ := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "a", TTL: time.Minute, OperationID: "a"})
	_, _ = be.PrepareTransfer(ctx, shiftlock.TransferRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b", Token: rec.FencingToken, TTL: time.Minute, OperationID: "p",
	})
	be.SetFault(faultinject.Fault{CommitThenFail: true})
	_, err := be.CommitTransfer(ctx, shiftlock.CommitRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b", ExpectedToken: rec.FencingToken, TTL: time.Minute, OperationID: "c",
	})
	if !errors.Is(err, shiftlock.ErrAmbiguous) {
		t.Fatalf("got %v", err)
	}
	// Must not assume failure — re-read shows commit applied.
	got, err := inner.GetClaim(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerGeneration != "b" {
		t.Fatalf("expected commit applied despite client error: %+v", got)
	}
	// Retry with same OperationID must be safe
	be.SetFault(faultinject.Fault{})
	out, err := be.CommitTransfer(ctx, shiftlock.CommitRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b", ExpectedToken: rec.FencingToken, TTL: time.Minute, OperationID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.FencingToken != got.FencingToken {
		t.Fatal("retry advanced token")
	}
}

func TestCapabilitiesExposed(t *testing.T) {
	be := memory.New()
	defer be.Close()
	c, err := shiftlock.New(shiftlock.Config{Service: "s", InstanceID: "i", Backend: be, LeaseTTL: time.Second, RenewInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	caps := c.Capabilities()
	if !caps.AtomicCAS || !caps.IdempotentMutations {
		t.Fatalf("%+v", caps)
	}
	d := c.Diagnostics()
	if !d.Capabilities.AtomicCAS {
		t.Fatal("diagnostics missing caps")
	}
}
