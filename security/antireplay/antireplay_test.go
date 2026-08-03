package antireplay

import (
	"testing"
	"time"
)

func TestReplayDetection(t *testing.T) {
	c := New(16)
	if err := c.CheckAndStore("req-1", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckAndStore("req-1", time.Minute); err != ErrReplay {
		t.Fatalf("want replay, got %v", err)
	}
}

func TestEpochAdvanceInvalidates(t *testing.T) {
	c := New(16)
	if err := c.CheckAndStore("nonce-a", time.Hour); err != nil {
		t.Fatal(err)
	}
	ep, err := c.AdvanceEpoch()
	if err != nil || ep != 1 {
		t.Fatalf("epoch=%d err=%v", ep, err)
	}
	if c.Seen("nonce-a") {
		t.Fatal("nonce should be cleared after epoch advance")
	}
	if err := c.CheckAndStore("nonce-a", time.Hour); err != nil {
		t.Fatal(err)
	}
}

func TestEpochRollbackRejected(t *testing.T) {
	c := New(8)
	_, _ = c.AdvanceEpoch()
	if err := c.SetEpoch(0); err != ErrEpochRollback {
		t.Fatalf("want rollback err, got %v", err)
	}
}

func TestExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	c := New(8, WithClock(func() time.Time { return now }))
	if err := c.CheckAndStore("x", time.Second); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if c.Seen("x") {
		t.Fatal("should expire")
	}
	if err := c.CheckAndStore("x", time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestCapacity(t *testing.T) {
	c := New(2)
	_ = c.CheckAndStore("a", time.Minute)
	_ = c.CheckAndStore("b", time.Minute)
	if err := c.CheckAndStore("c", time.Minute); err != ErrCapacity {
		t.Fatalf("want capacity, got %v", err)
	}
}
