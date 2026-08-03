package memory

import (
	"context"
	"testing"

	"github.com/theworker02/shiftlock/resource"
)

func TestMemoryCacheGeneration(t *testing.T) {
	id := resource.MustParseResourceID("cache/test/demo/customers")
	r, err := New(Config{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if r.Capabilities().SupportsFencing {
		t.Fatal("must not claim fencing")
	}
	r.Set("a", "1")
	gen, err := r.BuildGeneration(context.Background())
	if err != nil || gen != 1 {
		t.Fatalf("gen=%d err=%v", gen, err)
	}
	if _, err := r.BuildGeneration(context.Background()); err == nil {
		t.Fatal("expected concurrent build reject")
	}
	if err := r.ActivateGeneration(gen, map[string]string{"b": "2"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("a"); ok {
		t.Fatal("old keys should be cleared")
	}
	if v, ok := r.Get("b"); !ok || v != "2" {
		t.Fatalf("got %q ok=%v", v, ok)
	}
	snap, _ := r.Snapshot(context.Background())
	if snap["generation"] != "gen=1" {
		t.Fatalf("%v", snap)
	}
	h := r.Health(context.Background())
	if h.Overall != resource.HealthHealthy {
		t.Fatal(h.Overall)
	}
}
