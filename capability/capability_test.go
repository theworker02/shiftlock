package capability

import (
	"testing"
	"time"

	"github.com/theworker02/shiftlock/security/signing"
)

func TestIssueVerifyRevoke(t *testing.T) {
	key, err := signing.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ring := signing.NewKeyRing()
	_ = ring.Add(key.PublicView())
	auth := New(WithSigner(key, ring))

	tok, err := auth.Issue(Request{
		Subject:    "gen-1",
		Permission: "claim.revoke",
		Resource:   "billing",
		TTL:        time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Verify(tok); err != nil {
		t.Fatal(err)
	}
	_ = auth.Revoke(tok.ID)
	if err := auth.Verify(tok); err != ErrRevoked {
		t.Fatalf("want revoked, got %v", err)
	}
}

func TestEpochInvalidates(t *testing.T) {
	auth := New()
	tok, err := auth.Issue(Request{Subject: "g", Permission: "claim.inspect", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = auth.AdvanceEpoch()
	if err := auth.Verify(tok); err != ErrEpochMismatch {
		t.Fatalf("want epoch mismatch, got %v", err)
	}
}

func TestDelegateCannotWiden(t *testing.T) {
	auth := New()
	parent, err := auth.Issue(Request{Subject: "g", Permission: "claim.release", Resource: "a", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Delegate(parent, Request{Permission: "claim.revoke", Resource: "a"}); err != ErrWidenDelegate {
		t.Fatalf("want widen deny, got %v", err)
	}
	child, err := auth.Delegate(parent, Request{Permission: "claim.release", Resource: "a", TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID != parent.ID {
		t.Fatal("missing parent")
	}
}

func TestSingleUse(t *testing.T) {
	auth := New()
	tok, err := auth.Issue(Request{
		Subject: "g", Permission: "command.execute", TTL: time.Minute,
		Constraints: Constraints{SingleUse: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Verify(tok); err != nil {
		t.Fatal(err)
	}
	if err := auth.Verify(tok); err != ErrSingleUseSpent {
		t.Fatalf("want spent, got %v", err)
	}
}

func TestDenyEmptyAndStar(t *testing.T) {
	auth := New()
	if _, err := auth.Issue(Request{Permission: ""}); err != ErrDenied {
		t.Fatal(err)
	}
	if _, err := auth.Issue(Request{Permission: "*"}); err != ErrDenied {
		t.Fatal(err)
	}
}
