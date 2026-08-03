package syncprim_test

import (
	"context"
	"sync"
	"testing"

	"github.com/theworker02/shiftlock/syncprim"
)

type fakeClaims struct {
	mu    sync.Mutex
	held  map[string]uint64
	next  uint64
}

func (f *fakeClaims) Acquire(ctx context.Context, claim string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held == nil {
		f.held = map[string]uint64{}
	}
	if _, ok := f.held[claim]; ok {
		return 0, syncprim.ErrUnavailable
	}
	f.next++
	f.held[claim] = f.next
	return f.next, nil
}

func (f *fakeClaims) Release(ctx context.Context, claim string, token uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held[claim] == token {
		delete(f.held, claim)
	}
	return nil
}

func TestSemaphoreBound(t *testing.T) {
	fc := &fakeClaims{}
	sem, err := syncprim.NewSemaphore(fc, "s", 1)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := sem.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sem.Acquire(context.Background()); err != syncprim.ErrUnavailable {
		t.Fatalf("got %v", err)
	}
	_ = rel(context.Background())
}
