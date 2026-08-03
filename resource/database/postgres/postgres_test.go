package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

type fakeDB struct {
	err error
}

func (f *fakeDB) PingContext(context.Context) error { return f.err }

type fixedSchema string

func (s fixedSchema) SchemaVersion(context.Context) (string, error) { return string(s), nil }

type fakeFencer struct {
	min uint64
}

func (f fakeFencer) Check(_ context.Context, token uint64) error {
	if token < f.min {
		return errors.New("stale fencing token")
	}
	return nil
}

func TestHealthAndCapabilities(t *testing.T) {
	id := resource.MustParseResourceID("database/test/demo/orders")
	r, err := New(Config{
		ID:     id,
		DB:     &fakeDB{},
		Schema: fixedSchema("8"),
	})
	if err != nil {
		t.Fatal(err)
	}
	caps := r.Capabilities()
	if caps.SupportsFencing {
		t.Fatal("must not advertise fencing without Fencer")
	}
	if !caps.SupportsHealth || !caps.SupportsDrain {
		t.Fatal("expected health+drain")
	}
	h := r.Health(context.Background())
	if h.Overall != resource.HealthHealthy {
		t.Fatalf("health=%s msg=%s", h.Overall, h.Message)
	}
	ver, err := r.SchemaVersion(context.Background())
	if err != nil || ver != "8" {
		t.Fatalf("schema=%q err=%v", ver, err)
	}
}

func TestUnhealthyPing(t *testing.T) {
	id := resource.MustParseResourceID("database/test/demo/orders")
	r, err := New(Config{ID: id, DB: &fakeDB{err: errors.New("down")}})
	if err != nil {
		t.Fatal(err)
	}
	h := r.Health(context.Background())
	if h.Overall != resource.HealthUnhealthy {
		t.Fatalf("got %s", h.Overall)
	}
}

func TestFencingAdvertisedOnlyWhenConfigured(t *testing.T) {
	id := resource.MustParseResourceID("database/test/demo/orders")
	r, err := New(Config{ID: id, DB: &fakeDB{}, Fencer: fakeFencer{min: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Capabilities().SupportsFencing {
		t.Fatal("expected fencing")
	}
	if err := r.CheckFence(context.Background(), 5); err == nil {
		t.Fatal("expected stale")
	}
	if err := r.CheckFence(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
}

func TestDrainReady(t *testing.T) {
	id := resource.MustParseResourceID("database/test/demo/orders")
	r, err := New(Config{ID: id, DB: &fakeDB{}, PingTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Ready(context.Background()); err == nil {
		t.Fatal("expected not ready while draining")
	}
	snap, err := r.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap["drained"] != "true" {
		t.Fatalf("%v", snap)
	}
	for _, k := range []string{"password", "dsn", "secret"} {
		if _, ok := snap[k]; ok {
			t.Fatalf("secret key %q in snapshot", k)
		}
	}
}
