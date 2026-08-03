package resourcetest

import (
	"context"
	"testing"

	"github.com/theworker02/shiftlock/resource"
)

// Factory creates a Resource under test.
type Factory func(t *testing.T) resource.Resource

// RunAdapterSuite certifies adapter honesty: ID, kind, capabilities, health,
// and optional snapshot sanitization (no secret-like attr keys).
func RunAdapterSuite(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("IDValid", func(t *testing.T) { checkID(t, factory) })
	t.Run("KindMatchesID", func(t *testing.T) { checkKind(t, factory) })
	t.Run("CapabilitiesHonest", func(t *testing.T) { checkCaps(t, factory) })
	t.Run("HealthBounded", func(t *testing.T) { checkHealth(t, factory) })
	t.Run("SnapshotSanitized", func(t *testing.T) { checkSnapshot(t, factory) })
}

func checkID(t *testing.T, factory Factory) {
	r := factory(t)
	if err := r.ID().Validate(); err != nil {
		t.Fatal(err)
	}
}

func checkKind(t *testing.T, factory Factory) {
	r := factory(t)
	if r.Kind() != r.ID().Kind {
		t.Fatalf("kind mismatch: Kind()=%s ID.Kind=%s", r.Kind(), r.ID().Kind)
	}
	if !resource.ValidKind(r.Kind()) {
		t.Fatalf("unknown kind %q", r.Kind())
	}
}

func checkCaps(t *testing.T, factory Factory) {
	r := factory(t)
	caps := r.Capabilities()
	// Honesty probe: requiring an unsupported capability must fail.
	need := resource.ResourceCapabilities{SupportsTransactions: true}
	if caps.SupportsTransactions {
		need = resource.ResourceCapabilities{SupportsFailover: true}
		if caps.SupportsFailover {
			return // adapter supports both; skip negative probe
		}
	}
	if err := caps.Require(need); err == nil {
		t.Fatal("expected capability require to fail for unsupported flag")
	}
}

func checkHealth(t *testing.T, factory Factory) {
	r := factory(t)
	h := r.Health(context.Background())
	if h.Overall == "" {
		t.Fatal("empty overall health")
	}
}

func checkSnapshot(t *testing.T, factory Factory) {
	r := factory(t)
	if !r.Capabilities().SupportsSnapshots {
		return
	}
	type snapper interface {
		Snapshot(ctx context.Context) (map[string]string, error)
	}
	s, ok := r.(snapper)
	if !ok {
		return
	}
	m, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range m {
		lk := toLower(k)
		lv := toLower(v)
		for _, bad := range []string{"password", "secret", "api_key", "private_key", "access_token", "auth_token"} {
			if contains(lk, bad) || contains(lv, bad) {
				t.Fatalf("snapshot appears to include secret-like field %q=%q", k, v)
			}
		}
	}
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
