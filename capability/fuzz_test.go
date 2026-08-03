package capability

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/theworker02/shiftlock/security/signing"
)

func FuzzTokenJSONVerify(f *testing.F) {
	key, err := signing.GenerateKey()
	if err != nil {
		f.Fatal(err)
	}
	ring := signing.NewKeyRing()
	_ = ring.Add(key.PublicView())
	auth := New(WithSigner(key, ring))
	tok, err := auth.Issue(Request{Subject: "fuzz", Permission: "claim.inspect", TTL: time.Minute})
	if err != nil {
		f.Fatal(err)
	}
	seed, err := json.Marshal(tok)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"id":"x","permission":"*","epoch":0}`))
	f.Add([]byte(`{"permission":"claim.inspect","expires_at":"not-a-time"}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<16 {
			return
		}
		var tok Token
		if err := json.Unmarshal(raw, &tok); err != nil {
			return
		}
		// Must not panic; forged/malformed tokens must fail closed.
		_ = auth.Verify(tok)
	})
}

func FuzzDelegatePermissionReduce(f *testing.F) {
	f.Add("claim.release", "claim.release")
	f.Add("claim.*", "claim.release")
	f.Add("claim.release", "claim.revoke")
	f.Add("*", "claim.inspect")
	f.Add("a.b", "a.b.c")
	f.Fuzz(func(t *testing.T, parent, child string) {
		_ = permissionReduces(Permission(parent), Permission(child))
	})
}
