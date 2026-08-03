package resource_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/theworker02/shiftlock/resource"
	"github.com/theworker02/shiftlock/resource/memory"
	"github.com/theworker02/shiftlock/resource/resourcetest"
)

func TestParseResourceID(t *testing.T) {
	id, err := resource.ParseResourceID("database/production/payments-api/orders")
	if err != nil {
		t.Fatal(err)
	}
	if id.Kind != resource.KindDatabase || id.Environment != "production" ||
		id.Service != "payments-api" || id.Name != "orders" {
		t.Fatalf("%+v", id)
	}
	if id.String() != "database/production/payments-api/orders" {
		t.Fatal(id.String())
	}
	_, err = resource.ParseResourceID("bad")
	if !errors.Is(err, resource.ErrInvalidID) {
		t.Fatalf("got %v", err)
	}
}

func TestRegisterCustomKind(t *testing.T) {
	k := resource.Kind("test-kind-phase7")
	if err := resource.RegisterCustomKind(k); err != nil {
		t.Fatal(err)
	}
	if !resource.ValidKind(k) {
		t.Fatal("not valid")
	}
	if err := resource.RegisterCustomKind(k); !errors.Is(err, resource.ErrDuplicate) {
		t.Fatalf("got %v", err)
	}
}

func TestRegistryDuplicateAndBounds(t *testing.T) {
	reg := resource.NewRegistry(resource.RegistryConfig{MaxResources: 2})
	w1 := memory.Worker("dev", "svc", "a")
	w2 := memory.Worker("dev", "svc", "b")
	w3 := memory.Worker("dev", "svc", "c")
	if _, err := reg.Register(w1, resource.Metadata{}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Register(w1, resource.Metadata{}); !errors.Is(err, resource.ErrDuplicate) {
		t.Fatalf("got %v", err)
	}
	if _, err := reg.Register(w2, resource.Metadata{}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Register(w3, resource.Metadata{}); !errors.Is(err, resource.ErrBoundExceeded) {
		t.Fatalf("got %v", err)
	}
	m := reg.Metrics()
	if m.BoundRejects != 1 || m.Duplicates != 1 {
		t.Fatalf("%+v", m)
	}
}

type lockdownOn struct{}

func (lockdownOn) BlocksMutations() bool { return true }

func TestRegistryLockdownBlocksMutation(t *testing.T) {
	reg := resource.NewRegistry(resource.RegistryConfig{Lockdown: lockdownOn{}})
	_, err := reg.Register(memory.Feature("dev", "svc", "f"), resource.Metadata{})
	if !errors.Is(err, resource.ErrLockdown) {
		t.Fatalf("got %v", err)
	}
}

func TestDependenciesCycleAndOrder(t *testing.T) {
	reg := resource.NewRegistry(resource.RegistryConfig{})
	a := memory.Worker("dev", "svc", "a")
	b := memory.Worker("dev", "svc", "b")
	c := memory.Worker("dev", "svc", "c")
	for _, r := range []resource.Resource{a, b, c} {
		if _, err := reg.Register(r, resource.Metadata{}); err != nil {
			t.Fatal(err)
		}
	}
	// b depends on a; c depends on b → startup a,b,c
	if err := reg.DefineDependency(b.ID(), a.ID()); err != nil {
		t.Fatal(err)
	}
	if err := reg.DefineDependency(c.ID(), b.ID()); err != nil {
		t.Fatal(err)
	}
	if err := reg.DefineDependency(a.ID(), c.ID()); !errors.Is(err, resource.ErrCycle) {
		t.Fatalf("expected cycle, got %v", err)
	}
	order, err := reg.StartupOrder()
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0].Name != "a" || order[1].Name != "b" || order[2].Name != "c" {
		t.Fatalf("%v", order)
	}
	shut, err := reg.ShutdownOrder()
	if err != nil {
		t.Fatal(err)
	}
	if shut[0].Name != "c" || shut[2].Name != "a" {
		t.Fatalf("%v", shut)
	}
}

func TestBundleReadiness(t *testing.T) {
	reg := resource.NewRegistry(resource.RegistryConfig{})
	f := memory.Feature("dev", "svc", "flag")
	_, _ = reg.Register(f, resource.Metadata{})
	b := resource.NewBundle("core", f.ID()).WithMode(resource.BundleAllRequired)
	rep := b.EvaluateReadiness(context.Background(), reg)
	if !rep.Ready {
		t.Fatalf("%+v", rep)
	}
}

func TestEpochNeverDecreases(t *testing.T) {
	_, _, err := resource.AdvanceEpoch(0, "")
	if !errors.Is(err, resource.ErrInvalidArgument) {
		t.Fatalf("%v", err)
	}
	e, adv, err := resource.AdvanceEpoch(1, "rotate")
	if err != nil || e != 2 || adv.From != 1 {
		t.Fatalf("%v %v %v", e, adv, err)
	}
	if err := resource.EnsureNotDecreased(5, 4); !errors.Is(err, resource.ErrEpochDecreased) {
		t.Fatalf("%v", err)
	}
	_, _, err = resource.AdvanceEpoch(resource.MaxResourceEpoch, "x")
	if !errors.Is(err, resource.ErrEpochOverflow) {
		t.Fatalf("%v", err)
	}
}

func TestCapabilitiesRequire(t *testing.T) {
	c := resource.ResourceCapabilities{SupportsHealth: true}
	err := c.Require(resource.ResourceCapabilities{SupportsFencing: true})
	if !errors.Is(err, resource.ErrCapabilityClaimed) {
		t.Fatalf("%v", err)
	}
}

func TestAcquireAllPartialRelease(t *testing.T) {
	var acquired, released []string
	ids := []resource.ResourceID{
		resource.MustParseResourceID("worker/dev/svc/b"),
		resource.MustParseResourceID("worker/dev/svc/a"),
	}
	_, err := resource.AcquireAll(context.Background(), ids,
		func(_ context.Context, id resource.ResourceID) error {
			acquired = append(acquired, id.Name)
			if id.Name == "b" {
				return errors.New("boom")
			}
			return nil
		},
		func(_ context.Context, id resource.ResourceID) error {
			released = append(released, id.Name)
			return nil
		},
	)
	if !errors.Is(err, resource.ErrPartialAcquire) {
		t.Fatalf("%v", err)
	}
	// canonical order a then b; a acquired then released on b failure
	if len(acquired) != 2 || acquired[0] != "a" || acquired[1] != "b" {
		t.Fatalf("acquired=%v", acquired)
	}
	if len(released) != 1 || released[0] != "a" {
		t.Fatalf("released=%v", released)
	}
}

func TestMemoryAdapterSuite(t *testing.T) {
	resourcetest.RunAdapterSuite(t, func(t *testing.T) resource.Resource {
		return memory.RateLimit("dev", "svc", "rl")
	})
}

func TestSanitizeEvidence(t *testing.T) {
	ev, err := resource.SanitizeEvidence(resource.Evidence{
		Event: "ok", Summary: "s",
		Attrs: map[string]string{"a": "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Attrs["a"] != "b" {
		t.Fatal(ev)
	}
	big := map[string]string{}
	for i := 0; i < resource.MaxEvidenceAttrs+1; i++ {
		big[fmt.Sprintf("k%02d", i)] = "x"
	}
	_, err = resource.SanitizeEvidence(resource.Evidence{Attrs: big})
	if !errors.Is(err, resource.ErrEvidenceTooLarge) {
		t.Fatalf("%v", err)
	}
}
