package testutil

import (
	"runtime"
	"testing"
	"time"
)

// AssertNoLeaks snapshots goroutine count before/after fn and fails if growth exceeds delta.
// This is a best-effort certification helper, not a precise leak detector.
func AssertNoLeaks(t *testing.T, fn func(), maxDelta int) {
	t.Helper()
	if maxDelta <= 0 {
		maxDelta = 2
	}
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	before := runtime.NumGoroutine()
	fn()
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after-before > maxDelta {
		t.Fatalf("goroutine leak: before=%d after=%d delta=%d max=%d", before, after, after-before, maxDelta)
	}
}
