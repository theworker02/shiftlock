package configlock

import (
	"encoding/json"
	"testing"

	"github.com/theworker02/shiftlock/security/signing"
)

func TestLifecycleAndSignatures(t *testing.T) {
	key, err := signing.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ring := signing.NewKeyRing()
	_ = ring.Add(key.PublicView())

	m := NewManager(WithKeyRing(ring), Production(true), RequireSignatures(true))
	b, err := m.Draft("svc", "prod", json.RawMessage(`{"max":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Stage(b.Revision); err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(b.Revision); err != nil {
		t.Fatal(err)
	}
	if err := m.Approve(b.Revision); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate(b.Revision); err != ErrUnsignedRequired {
		t.Fatalf("want unsigned required, got %v", err)
	}
	if err := m.SignRevision(b.Revision, key); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate(b.Revision); err != nil {
		t.Fatal(err)
	}
	active, ok := m.Active()
	if !ok || active.State != StateActive {
		t.Fatalf("active=%v ok=%v", active, ok)
	}
}

func TestHashMismatch(t *testing.T) {
	m := NewManager()
	b, _ := m.Draft("s", "dev", json.RawMessage(`{"a":1}`))
	_ = m.Stage(b.Revision)
	got, _ := m.Get(b.Revision)
	// mutate stored content via Activate path — Validate checks hash after we corrupt
	m.mu.Lock()
	m.bundles[b.Revision].Content = json.RawMessage(`{"a":2}`)
	m.mu.Unlock()
	if err := m.Validate(got.Revision); err != ErrHashMismatch {
		t.Fatalf("want hash mismatch, got %v", err)
	}
}

func TestInvalidTransition(t *testing.T) {
	m := NewManager()
	b, _ := m.Draft("s", "dev", json.RawMessage(`{}`))
	if err := m.Approve(b.Revision); err == nil {
		t.Fatal("draft → approved should fail")
	}
}
