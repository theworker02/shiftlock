package attestation

import "testing"

func TestSelfReportIsUnverified(t *testing.T) {
	r := SelfReport("svc", "i1", "g1", "v0")
	if r.OverallTrust != TrustSelfReported {
		t.Fatalf("trust=%s", r.OverallTrust)
	}
	if r.RequireMinTrust(TrustPlatformVerified) {
		t.Fatal("self-reported must not satisfy platform-verified")
	}
	for _, e := range r.Evidence {
		if e.Verified() {
			t.Fatalf("evidence %s unexpectedly verified", e.Kind)
		}
	}
}

func TestOverallTrustPicksStrongest(t *testing.T) {
	ev := []Evidence{
		{Kind: "a", Trust: TrustSelfReported},
		{Kind: "b", Trust: TrustCryptoVerified},
		{Kind: "c", Trust: TrustPlatformVerified},
	}
	if got := OverallTrustFromEvidence(ev); got != TrustCryptoVerified {
		t.Fatalf("got %s", got)
	}
}
