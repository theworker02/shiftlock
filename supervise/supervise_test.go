package supervise_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theworker02/shiftlock/supervise"
)

func TestPanicContained(t *testing.T) {
	s := supervise.New(context.Background(), nil)
	defer s.Close()
	var n atomic.Int32
	if err := s.StartSpec(supervise.Spec{
		Name: "panic", Mode: supervise.ModeOneShot,
		Run: func(ctx context.Context) error {
			n.Add(1)
			panic("boom")
		},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if n.Load() != 1 {
		t.Fatalf("n=%d", n.Load())
	}
}
