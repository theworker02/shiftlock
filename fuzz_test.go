package shiftlock_test

import (
	"context"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/internal/stategraph"
)

func FuzzStateTransition(f *testing.F) {
	states := stategraph.AllStates()
	for _, a := range states {
		for _, b := range states {
			f.Add(string(a), string(b))
		}
	}
	f.Fuzz(func(t *testing.T, from, to string) {
		_ = stategraph.CanTransition(stategraph.State(from), stategraph.State(to))
		_ = stategraph.Transition(stategraph.State(from), stategraph.State(to))
	})
}

func FuzzTokenAccept(f *testing.F) {
	f.Add(uint64(0))
	f.Add(uint64(1))
	f.Add(uint64(^uint64(0)))
	f.Fuzz(func(t *testing.T, tok uint64) {
		v := shiftlock.NewTokenValidator()
		_ = v.Accept(shiftlock.FencingToken(tok / 2))
		_ = v.Accept(shiftlock.FencingToken(tok))
		cur := v.Current()
		if tok > 0 && shiftlock.FencingToken(tok) >= cur/2 {
			if v.Accept(shiftlock.FencingToken(0)) {
				t.Fatal("zero accepted")
			}
		}
	})
}

func TestPropertyExclusiveOwner(t *testing.T) {
	be := memory.New()
	defer be.Close()
	ctx := context.Background()
	for round := 0; round < 50; round++ {
		name := "p-" + itoa(round)
		rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
			ClaimName: name, GenerationID: "a", TTL: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = be.AcquireClaim(ctx, shiftlock.AcquireRequest{
			ClaimName: name, GenerationID: "b", TTL: time.Minute,
		})
		if err == nil {
			t.Fatal("dual owner")
		}
		got, _ := be.GetClaim(ctx, name)
		if got.OwnerGeneration != "a" || got.FencingToken != rec.FencingToken {
			t.Fatalf("%+v", got)
		}
	}
}

func BenchmarkAcquireRelease(b *testing.B) {
	be := memory.New()
	defer be.Close()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
			ClaimName: "bench", GenerationID: "g", TTL: time.Minute,
		})
		if err != nil {
			b.Fatal(err)
		}
		if err := be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{
			ClaimName: "bench", GenerationID: "g", Token: rec.FencingToken,
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTokenAccept(b *testing.B) {
	v := shiftlock.NewTokenValidator()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.Accept(shiftlock.FencingToken(i + 1))
	}
}
