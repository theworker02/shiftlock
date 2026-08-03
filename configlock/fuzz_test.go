package configlock

import (
	"encoding/json"
	"testing"

	"github.com/theworker02/shiftlock/security/signing"
)

func FuzzBundleVerify(f *testing.F) {
	key, err := signing.GenerateKey()
	if err != nil {
		f.Fatal(err)
	}
	ring := signing.NewKeyRing()
	_ = ring.Add(key.PublicView())
	m := NewManager(WithKeyRing(ring))
	b, err := m.Draft("svc", "dev", json.RawMessage(`{"k":1}`))
	if err != nil {
		f.Fatal(err)
	}
	if err := m.SignRevision(b.Revision, key); err != nil {
		f.Fatal(err)
	}
	active, _ := m.Get(b.Revision)
	seed, _ := json.Marshal(active)
	f.Add(seed)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"content":"not-json","content_hash":[]}`))
	f.Add([]byte(`{"version":1,"content":{},"signatures":[{"key_id":"x","sig":"YQ=="}]}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<16 {
			return
		}
		var b Bundle
		if err := json.Unmarshal(raw, &b); err != nil {
			return
		}
		_ = b.VerifyContentHash()
		_ = b.VerifySignatures(ring)
	})
}
