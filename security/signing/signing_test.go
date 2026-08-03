package signing

import (
	"crypto/ed25519"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ring := NewKeyRing()
	if err := ring.Add(key.PublicView()); err != nil {
		t.Fatal(err)
	}

	payload := map[string]any{"action": "checkpoint", "n": float64(1), "nested": map[string]any{"b": true, "a": "x"}}
	sig, raw, err := SignCanonical(key, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBytes(ring, raw, sig); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCanonical(ring, payload, sig); err != nil {
		t.Fatal(err)
	}

	// Key order independence of canonical form.
	reordered := map[string]any{"nested": map[string]any{"a": "x", "b": true}, "n": float64(1), "action": "checkpoint"}
	if err := VerifyCanonical(ring, reordered, sig); err != nil {
		t.Fatalf("canonical mismatch: %v", err)
	}
}

func TestRotationMultipleTrustedKeys(t *testing.T) {
	oldKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ring := NewKeyRing()
	_ = ring.Add(oldKey.PublicView())
	_ = ring.Add(newKey.PublicView())

	sigOld, _, err := SignCanonical(oldKey, map[string]string{"v": "1"})
	if err != nil {
		t.Fatal(err)
	}
	sigNew, _, err := SignCanonical(newKey, map[string]string{"v": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCanonical(ring, map[string]string{"v": "1"}, sigOld); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCanonical(ring, map[string]string{"v": "2"}, sigNew); err != nil {
		t.Fatal(err)
	}

	ring.Retire(oldKey.ID)
	if err := VerifyCanonical(ring, map[string]string{"v": "1"}, sigOld); err != ErrExpiredKey {
		t.Fatalf("want ErrExpiredKey, got %v", err)
	}
}

func TestUnknownAndTampered(t *testing.T) {
	key, _ := GenerateKey()
	ring := NewKeyRing()
	_ = ring.Add(key.PublicView())
	sig, raw, _ := SignCanonical(key, map[string]string{"ok": "yes"})
	raw[0] ^= 0xff
	if err := VerifyBytes(ring, raw, sig); err != ErrInvalidSig {
		t.Fatalf("want invalid sig, got %v", err)
	}
	sig.KeyID = "missing"
	if err := VerifyBytes(ring, []byte(`{"ok":"yes"}`), sig); err != ErrUnknownKey {
		t.Fatalf("want unknown key, got %v", err)
	}
}

func TestExpiredKey(t *testing.T) {
	key, _ := GenerateKey()
	past := time.Now().UTC().Add(-time.Hour)
	key.ExpiresAt = &past
	if _, err := SignBytes(key, []byte("x")); err != ErrExpiredKey {
		t.Fatalf("want expired, got %v", err)
	}
	_ = ed25519.PublicKeySize // keep crypto import used if trimmed
}
