//go:build stress

package stress_test

import (
	"context"
	"testing"
	"time"

	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/stress"
)

func TestBurstMemory(t *testing.T) {
	be := memory.New()
	defer be.Close()
	ops, _, err := stress.Burst(context.Background(), be, stress.Options{
		Workers: 8, Claims: 4, Duration: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ops == 0 {
		t.Fatal("expected some ops")
	}
}
