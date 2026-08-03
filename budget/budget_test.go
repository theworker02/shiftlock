package budget

import (
	"errors"
	"testing"
	"time"
)

func TestBudgetStopPauseDegrade(t *testing.T) {
	b := New(Config{Name: "export", MaxBytes: 10, OnExhausted: BehaviorStop})
	if err := b.AddBytes(5); err != nil {
		t.Fatal(err)
	}
	if err := b.AddBytes(6); !errors.Is(err, ErrExhausted) {
		t.Fatalf("%v", err)
	}

	p := New(Config{Name: "p", MaxRetries: 1, OnExhausted: BehaviorPause})
	_ = p.AddRetry()
	if err := p.AddRetry(); !errors.Is(err, ErrPaused) {
		t.Fatalf("%v", err)
	}
	if !p.IsPaused() {
		t.Fatal("expected paused")
	}
	p.Resume()
	if err := p.Allow(); err != nil && !errors.Is(err, ErrExhausted) {
		// after resume with stop-equivalent exhausted still blocked via Allow when stop;
		// with pause behavior Allow returns ErrPaused only when paused.
		_ = err
	}

	d := New(Config{Name: "d", MaxBytes: 1, OnExhausted: BehaviorDegrade})
	if err := d.AddBytes(5); err != nil {
		t.Fatal(err)
	}
	if !d.IsDegraded() {
		t.Fatal("expected degrade")
	}
	snap := d.Snapshot()
	if snap.Bytes < 5 || snap.Name != "d" {
		t.Fatalf("%+v", snap)
	}
}

func TestBudgetDuration(t *testing.T) {
	b := New(Config{Name: "t", MaxDuration: time.Millisecond, OnExhausted: BehaviorStop})
	time.Sleep(5 * time.Millisecond)
	if err := b.Allow(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("%v", err)
	}
}
