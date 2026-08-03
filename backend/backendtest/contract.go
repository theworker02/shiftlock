package backendtest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
)

// RunContract exercises critical ownership invariants against any Backend.
func RunContract(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	t.Helper()
	t.Run("SingleOwner", func(t *testing.T) { testSingleOwner(t, factory) })
	t.Run("TokensMonotonic", func(t *testing.T) { testTokensMonotonic(t, factory) })
	t.Run("StaleReleaseRejected", func(t *testing.T) { testStaleRelease(t, factory) })
	t.Run("TransferCommitAbort", func(t *testing.T) { testTransfer(t, factory) })
	t.Run("ConcurrentAcquire", func(t *testing.T) { testConcurrent(t, factory) })
	t.Run("AbortKeepsOwnership", func(t *testing.T) { testAbortKeeps(t, factory) })
}

func testSingleOwner(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	be := factory(t)
	defer be.Close()
	ctx := context.Background()
	_ = be.RegisterGeneration(ctx, shiftlock.Generation{ID: "a", Service: "s", InstanceID: "i", State: shiftlock.StateStandby, StartedAt: time.Now(), UpdatedAt: time.Now()})
	_ = be.RegisterGeneration(ctx, shiftlock.Generation{ID: "b", Service: "s", InstanceID: "j", State: shiftlock.StateStandby, StartedAt: time.Now(), UpdatedAt: time.Now()})

	rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "a", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	_, err = be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "b", TTL: time.Minute})
	if !errors.Is(err, shiftlock.ErrClaimHeld) {
		t.Fatalf("got %v", err)
	}
	if rec.FencingToken == 0 {
		t.Fatal("zero token")
	}
}

func testTokensMonotonic(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	be := factory(t)
	defer be.Close()
	ctx := context.Background()
	var last shiftlock.FencingToken
	for i := 0; i < 10; i++ {
		id := "g" + string(rune('a'+i))
		_ = be.RegisterGeneration(ctx, shiftlock.Generation{ID: id, Service: "s", InstanceID: id, State: shiftlock.StateStandby, StartedAt: time.Now(), UpdatedAt: time.Now()})
		rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "job", GenerationID: id, TTL: time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		if rec.FencingToken < last {
			t.Fatalf("decreased %d -> %d", last, rec.FencingToken)
		}
		last = rec.FencingToken
		if err := be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{ClaimName: "job", GenerationID: id, Token: rec.FencingToken}); err != nil {
			t.Fatal(err)
		}
	}
}

func testStaleRelease(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	be := factory(t)
	defer be.Close()
	ctx := context.Background()
	_ = be.RegisterGeneration(ctx, shiftlock.Generation{ID: "old", Service: "s", InstanceID: "1", State: shiftlock.StateStandby, StartedAt: time.Now(), UpdatedAt: time.Now()})
	_ = be.RegisterGeneration(ctx, shiftlock.Generation{ID: "new", Service: "s", InstanceID: "2", State: shiftlock.StateStandby, StartedAt: time.Now(), UpdatedAt: time.Now()})
	rec, _ := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "old", TTL: time.Minute})
	oldTok := rec.FencingToken
	_ = be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{ClaimName: "c", GenerationID: "old", Token: oldTok})
	rec2, _ := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "new", TTL: time.Minute})
	err := be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{ClaimName: "c", GenerationID: "old", Token: oldTok})
	if !errors.Is(err, shiftlock.ErrStaleToken) {
		t.Fatalf("got %v", err)
	}
	got, _ := be.GetClaim(ctx, "c")
	if got.OwnerGeneration != "new" || got.FencingToken != rec2.FencingToken {
		t.Fatalf("corrupted: %+v", got)
	}
}

func testTransfer(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	be := factory(t)
	defer be.Close()
	ctx := context.Background()
	_ = be.RegisterGeneration(ctx, shiftlock.Generation{ID: "a", Service: "s", InstanceID: "1", State: shiftlock.StateActive, StartedAt: time.Now(), UpdatedAt: time.Now()})
	_ = be.RegisterGeneration(ctx, shiftlock.Generation{ID: "b", Service: "s", InstanceID: "2", State: shiftlock.StateStandby, StartedAt: time.Now(), UpdatedAt: time.Now()})
	rec, _ := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "a", TTL: time.Minute})
	_, err := be.PrepareTransfer(ctx, shiftlock.TransferRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b", Token: rec.FencingToken, TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := be.CommitTransfer(ctx, shiftlock.CommitRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b", ExpectedToken: rec.FencingToken, TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.OwnerGeneration != "b" || out.FencingToken <= rec.FencingToken {
		t.Fatalf("%+v", out)
	}
}

func testConcurrent(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	be := factory(t)
	defer be.Close()
	ctx := context.Background()
	const n = 100
	var wg sync.WaitGroup
	var wins atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "g-" + itoa(i)
			_ = be.RegisterGeneration(ctx, shiftlock.Generation{ID: id, Service: "s", InstanceID: id, State: shiftlock.StateStandby, StartedAt: time.Now(), UpdatedAt: time.Now()})
			_, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "hot", GenerationID: id, TTL: time.Minute})
			if err == nil {
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("wins=%d", wins.Load())
	}
}

func testAbortKeeps(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	be := factory(t)
	defer be.Close()
	ctx := context.Background()
	_ = be.RegisterGeneration(ctx, shiftlock.Generation{ID: "a", Service: "s", InstanceID: "1", State: shiftlock.StateActive, StartedAt: time.Now(), UpdatedAt: time.Now()})
	rec, _ := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "a", TTL: time.Minute})
	_, _ = be.PrepareTransfer(ctx, shiftlock.TransferRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b", Token: rec.FencingToken, TTL: time.Minute,
	})
	out, err := be.AbortTransfer(ctx, shiftlock.AbortRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b", ExpectedToken: rec.FencingToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.OwnerGeneration != "a" || out.FencingToken != rec.FencingToken || out.Phase != shiftlock.ClaimOwned {
		t.Fatalf("%+v", out)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [16]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
