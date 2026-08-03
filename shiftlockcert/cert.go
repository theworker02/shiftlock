package shiftlockcert

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
)

// RunBackendSuite is the certification suite. Safety tests cannot be disabled
// while claiming certification — all subtests always run.
func RunBackendSuite(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	t.Helper()
	t.Run("SingleOwner", func(t *testing.T) { certSingleOwner(t, factory) })
	t.Run("TokenMonotonic", func(t *testing.T) { certTokens(t, factory) })
	t.Run("StaleRelease", func(t *testing.T) { certStale(t, factory) })
	t.Run("AbortNoAdvance", func(t *testing.T) { certAbort(t, factory) })
	t.Run("CommitIdempotent", func(t *testing.T) { certCommitIdempotent(t, factory) })
	t.Run("ConcurrentAcquire", func(t *testing.T) { certConcurrent(t, factory) })
	t.Run("Overflow", func(t *testing.T) { certOverflow(t, factory) })
}

func certSingleOwner(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	be := factory(t)
	defer be.Close()
	ctx := context.Background()
	_, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "a", TTL: time.Minute, OperationID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "b", TTL: time.Minute, OperationID: "2"})
	if !errors.Is(err, shiftlock.ErrClaimHeld) {
		t.Fatalf("got %v", err)
	}
}

func certTokens(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	be := factory(t)
	defer be.Close()
	ctx := context.Background()
	var last shiftlock.FencingToken
	for i := 0; i < 5; i++ {
		id := "g" + string(rune('a'+i))
		rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "j", GenerationID: id, TTL: time.Millisecond, OperationID: shiftlock.OperationID("a" + id)})
		if err != nil {
			t.Fatal(err)
		}
		if rec.FencingToken < last {
			t.Fatal("decreased")
		}
		last = rec.FencingToken
		_ = be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{ClaimName: "j", GenerationID: id, Token: rec.FencingToken, OperationID: shiftlock.OperationID("r" + id)})
	}
}

func certStale(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	be := factory(t)
	defer be.Close()
	ctx := context.Background()
	rec, _ := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "old", TTL: time.Minute, OperationID: "a"})
	old := rec.FencingToken
	_ = be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{ClaimName: "c", GenerationID: "old", Token: old, OperationID: "r"})
	_, _ = be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "new", TTL: time.Minute, OperationID: "b"})
	err := be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{ClaimName: "c", GenerationID: "old", Token: old, OperationID: "stale"})
	if !errors.Is(err, shiftlock.ErrStaleToken) {
		t.Fatalf("got %v", err)
	}
}

func certAbort(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	be := factory(t)
	defer be.Close()
	ctx := context.Background()
	rec, _ := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "a", TTL: time.Minute, OperationID: "a"})
	_, _ = be.PrepareTransfer(ctx, shiftlock.TransferRequest{ClaimName: "c", FromGeneration: "a", ToGeneration: "b", Token: rec.FencingToken, TTL: time.Minute, OperationID: "p"})
	out, err := be.AbortTransfer(ctx, shiftlock.AbortRequest{ClaimName: "c", FromGeneration: "a", ToGeneration: "b", ExpectedToken: rec.FencingToken, OperationID: "ab"})
	if err != nil {
		t.Fatal(err)
	}
	if out.FencingToken != rec.FencingToken || out.OwnerGeneration != "a" {
		t.Fatalf("%+v", out)
	}
}

func certCommitIdempotent(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	be := factory(t)
	defer be.Close()
	ctx := context.Background()
	rec, _ := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "c", GenerationID: "a", TTL: time.Minute, OperationID: "a"})
	_, _ = be.PrepareTransfer(ctx, shiftlock.TransferRequest{ClaimName: "c", FromGeneration: "a", ToGeneration: "b", Token: rec.FencingToken, TTL: time.Minute, OperationID: "p"})
	req := shiftlock.CommitRequest{ClaimName: "c", FromGeneration: "a", ToGeneration: "b", ExpectedToken: rec.FencingToken, TTL: time.Minute, OperationID: "same"}
	out1, err := be.CommitTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := be.CommitTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if out1.FencingToken != out2.FencingToken {
		t.Fatalf("idempotent commit advanced twice: %d vs %d", out1.FencingToken, out2.FencingToken)
	}
}

func certConcurrent(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	be := factory(t)
	defer be.Close()
	ctx := context.Background()
	var wins atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "x-" + itoaCert(i)
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

func itoaCert(i int) string {
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

func certOverflow(t *testing.T, factory func(t *testing.T) shiftlock.Backend) {
	// Only memory supports injecting near-max tokens via ForceSetToken if present.
	be := factory(t)
	defer be.Close()
	if m, ok := be.(interface{ ForceSetToken(string, shiftlock.FencingToken) }); ok {
		m.ForceSetToken("ov", shiftlock.MaxSafeFencingToken)
		_, err := be.AcquireClaim(context.Background(), shiftlock.AcquireRequest{ClaimName: "ov", GenerationID: "a", TTL: time.Minute})
		if !errors.Is(err, shiftlock.ErrTokenOverflow) && !errors.Is(err, shiftlock.ErrClaimUnavailable) {
			// Also accept wrapped ErrTokenOverflow
			var se *shiftlock.Error
			if !errors.As(err, &se) || !errors.Is(se, shiftlock.ErrTokenOverflow) {
				t.Fatalf("expected overflow, got %v", err)
			}
		}
	} else {
		t.Skip("backend does not support ForceSetToken")
	}
}
