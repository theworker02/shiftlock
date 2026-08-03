package queue

import (
	"context"
	"testing"

	"github.com/theworker02/shiftlock/resource"
)

func TestMemoryQueuePauseResume(t *testing.T) {
	id := resource.MustParseResourceID("queue/test/demo/billing-events")
	mem := NewMemory(8)
	r, err := New(Config{ID: id, Backend: mem, MaxDepth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if r.Capabilities().SupportsFencing {
		t.Fatal("must not claim fencing")
	}
	if err := mem.Publish("a"); err != nil {
		t.Fatal(err)
	}
	if err := r.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := r.Health(context.Background())
	if h.Overall != resource.HealthBlocked {
		t.Fatalf("got %s", h.Overall)
	}
	if err := mem.Publish("b"); err == nil {
		t.Fatal("expected pause reject")
	}
	if err := r.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	msg, ok := mem.Consume()
	if !ok || msg != "a" {
		t.Fatalf("%q %v", msg, ok)
	}
}
