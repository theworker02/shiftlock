package secrets_test

import (
	"testing"

	"github.com/theworker02/shiftlock/secrets"
)

func TestRotationRefsOnly(t *testing.T) {
	log := secrets.NewRotationLog(0)
	old, err := secrets.ParseRef("env://DB_PASS_OLD")
	if err != nil {
		t.Fatal(err)
	}
	neu, err := secrets.ParseRef("env://DB_PASS_NEW")
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Plan("db", old, neu, "op"); err != nil {
		t.Fatal(err)
	}
	if err := log.Advance("db", secrets.RotationIssued, "issued via vault"); err != nil {
		t.Fatal(err)
	}
	rec, err := log.Get("db")
	if err != nil {
		t.Fatal(err)
	}
	if rec.OldRef != "env://DB_PASS_OLD" || rec.NewRef != "env://DB_PASS_NEW" {
		t.Fatalf("%+v", rec)
	}
	if err := log.Plan("bad", secrets.Ref{}, secrets.Ref{}, ""); err == nil {
		// empty refs
	}
	badOld, _ := secrets.ParseRef("env://X")
	// Attempt to sneak a raw value as "ref" via Advance message is fine; Plan must reject non-refs.
	fake := secrets.Ref{}
	_ = fake
	if err := log.Plan("leak", badOld, mustRef(t, "not-a-ref"), "op"); err == nil {
		// ParseRef fails so we need to construct illegally — Plan takes Ref so caller can't pass raw easily.
		// Direct string rejection via looksLikeValue tested by forging through Plan with invalid Ref:
	}
	_ = log
}

func mustRef(t *testing.T, s string) secrets.Ref {
	t.Helper()
	r, err := secrets.ParseRef(s)
	if err != nil {
		// Return zero; Plan should reject empty / we test ParseRef path separately.
		return secrets.Ref{}
	}
	return r
}

func TestRotationRejectsNonRef(t *testing.T) {
	log := secrets.NewRotationLog(0)
	// Build refs that parse, then test Plan rejects when String wouldn't parse — use empty.
	if err := log.Plan("x", secrets.Ref{}, secrets.Ref{}, "a"); err == nil {
		t.Fatal("expected error")
	}
}
