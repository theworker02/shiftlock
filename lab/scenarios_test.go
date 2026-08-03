package lab_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/backend/redis"
)

func TestLostCommitResponse(t *testing.T) {
	backends := []struct {
		name string
		be   shiftlock.Backend
	}{
		{"memory", memory.New()},
		{"redis-local", redis.NewLocal()},
	}
	for _, tc := range backends {
		t.Run(tc.name, func(t *testing.T) {
			be := tc.be
			defer be.Close()
			ctx := context.Background()
			rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
				ClaimName: "lab", GenerationID: "a", TTL: time.Minute, OperationID: "a",
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = be.PrepareTransfer(ctx, shiftlock.TransferRequest{
				ClaimName: "lab", FromGeneration: "a", ToGeneration: "b",
				Token: rec.FencingToken, TTL: time.Minute, OperationID: "p",
			})
			if err != nil {
				t.Fatal(err)
			}
			req := shiftlock.CommitRequest{
				ClaimName: "lab", FromGeneration: "a", ToGeneration: "b",
				ExpectedToken: rec.FencingToken, TTL: time.Minute, OperationID: "c",
			}
			out1, err := be.CommitTransfer(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			out2, err := be.CommitTransfer(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if out1.FencingToken != out2.FencingToken {
				t.Fatalf("token advanced twice: %d vs %d", out1.FencingToken, out2.FencingToken)
			}
		})
	}
}

func TestOwnerCrashMidReserved(t *testing.T) {
	be := memory.New()
	defer be.Close()
	ctx := context.Background()
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
	if out.OwnerGeneration != "a" || out.FencingToken != rec.FencingToken {
		t.Fatalf("%+v", out)
	}
}

func TestSplitAcquire(t *testing.T) {
	be := redis.NewLocal()
	defer be.Close()
	ctx := context.Background()
	var wins atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "g-" + itoa(i)
			_, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{ClaimName: "hot", GenerationID: id, TTL: time.Minute})
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, shiftlock.ErrClaimHeld) {
				t.Errorf("unexpected %v", err)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("wins=%d", wins.Load())
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
