package redis

import (
	"context"
	"testing"

	"github.com/theworker02/shiftlock/resource"
)

func TestRedisLocalHealthAndGeneration(t *testing.T) {
	id := resource.MustParseResourceID("cache/test/demo/session")
	local := NewLocal()
	r, err := New(Config{ID: id, Client: local})
	if err != nil {
		t.Fatal(err)
	}
	if r.Capabilities().SupportsFencing {
		t.Fatal("must not claim fencing")
	}
	h := r.Health(context.Background())
	if h.Overall != resource.HealthHealthy {
		t.Fatal(h)
	}
	gen, err := r.BuildGeneration(context.Background())
	if err != nil || gen != 1 {
		t.Fatalf("%d %v", gen, err)
	}
	if err := r.ActivateGeneration(context.Background(), gen); err != nil {
		t.Fatal(err)
	}
	local.SetDown(true)
	h = r.Health(context.Background())
	if h.Overall != resource.HealthUnhealthy {
		t.Fatal(h.Overall)
	}
}
