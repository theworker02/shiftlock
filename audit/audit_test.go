package audit

import (
	"bytes"
	"testing"

	"github.com/theworker02/shiftlock/security/signing"
)

func TestHashChainVerifyOK(t *testing.T) {
	s := New()
	a := Actor{ID: "op", Type: "user"}
	for i := 0; i < 5; i++ {
		if _, err := s.Append(a, "test.action", "res", "ok", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	rep := s.Verify()
	if !rep.OK {
		t.Fatalf("findings: %+v", rep.Findings)
	}
}

func TestDetectMutation(t *testing.T) {
	s := New()
	a := Actor{ID: "op"}
	_, _ = s.Append(a, "a", "", "ok", "", nil)
	_, _ = s.Append(a, "b", "", "ok", "", nil)
	recs := s.Records()
	recs[0].Action = "tampered"
	rep := VerifyRecords(recs, nil)
	if rep.OK {
		t.Fatal("expected mutation detection")
	}
	found := false
	for _, f := range rep.Findings {
		if f.Code == "mutation" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want mutation finding, got %+v", rep.Findings)
	}
}

func TestDetectRemovalGap(t *testing.T) {
	s := New()
	a := Actor{ID: "op"}
	_, _ = s.Append(a, "a", "", "ok", "", nil)
	_, _ = s.Append(a, "b", "", "ok", "", nil)
	_, _ = s.Append(a, "c", "", "ok", "", nil)
	recs := s.Records()
	// remove middle
	trimmed := []Record{recs[0], recs[2]}
	rep := VerifyRecords(trimmed, nil)
	if rep.OK {
		t.Fatal("expected gap/removal detection")
	}
}

func TestDuplicateOpID(t *testing.T) {
	s := New()
	a := Actor{ID: "op"}
	if _, err := s.Append(a, "a", "", "ok", "op-1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(a, "b", "", "ok", "op-1", nil); err == nil {
		t.Fatal("expected duplicate op error on append")
	}
	recs := s.Records()
	recs = append(recs, recs[0])
	recs[1].Sequence = 2
	rep := VerifyRecords(recs, nil)
	found := false
	for _, f := range rep.Findings {
		if f.Code == "duplicate_op_id" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want duplicate_op_id, got %+v", rep.Findings)
	}
}

func TestInvalidSignature(t *testing.T) {
	key, err := signing.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ring := signing.NewKeyRing()
	_ = ring.Add(key.PublicView())
	s := New(WithSigner(key, ring))
	a := Actor{ID: "op"}
	_, _ = s.Append(a, "signed", "", "ok", "", nil)
	recs := s.Records()
	recs[0].Signature[0] ^= 0xff
	rep := VerifyRecords(recs, ring)
	if rep.OK {
		t.Fatal("expected invalid signature")
	}
	found := false
	for _, f := range rep.Findings {
		if f.Code == "invalid_signature" {
			found = true
		}
	}
	if !found {
		t.Fatalf("findings=%+v", rep.Findings)
	}
}

func TestExportRoundTrip(t *testing.T) {
	s := New()
	_, _ = s.Append(Actor{ID: "a"}, "x", "", "ok", "", nil)
	b, err := ExportJSON(s.Records())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"action": "x"`)) {
		t.Fatalf("export=%s", b)
	}
}
