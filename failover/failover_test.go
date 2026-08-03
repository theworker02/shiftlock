package failover

import (
	"context"
	"testing"

	"github.com/theworker02/shiftlock/resource"
	"github.com/theworker02/shiftlock/resource/memory"
)

func TestManualFailoverAdvancesEpoch(t *testing.T) {
	reg := resource.NewRegistry(resource.RegistryConfig{})
	primary := memory.New(
		resource.MustParseResourceID("http-service/prod/pay/provider-a"),
		resource.Description{DisplayName: "A"},
		resource.ResourceCapabilities{SupportsHealth: true, SupportsFailover: true},
	)
	standby := memory.New(
		resource.MustParseResourceID("http-service/prod/pay/provider-b"),
		resource.Description{DisplayName: "B"},
		resource.ResourceCapabilities{SupportsHealth: true, SupportsFailover: true},
	)
	_, _ = reg.Register(primary, resource.Metadata{})
	_, _ = reg.Register(standby, resource.Metadata{})

	m := NewManager(reg)
	if err := m.Register(GroupConfig{
		Name: "payment-providers", Primary: primary.ID(),
		Standbys: []resource.ResourceID{standby.ID()}, Policy: PolicyManual,
	}); err != nil {
		t.Fatal(err)
	}
	target := standby.ID()
	d, err := m.ExecuteFailover(context.Background(), "payment-providers", "provider-a down", &target)
	if err != nil {
		t.Fatal(err)
	}
	if !d.To.Equal(standby.ID()) || d.EpochAdv.To != 1 {
		t.Fatalf("%+v", d)
	}
	ent, _ := reg.Get(standby.ID())
	if ent.Epoch != 1 {
		t.Fatalf("epoch=%d", ent.Epoch)
	}
	// Failback is separate.
	fb, err := m.ExecuteFailback(context.Background(), "payment-providers", "provider-a recovered")
	if err != nil || !fb.Failback {
		t.Fatalf("%+v %v", fb, err)
	}
}

func TestHealthBasedEvaluate(t *testing.T) {
	reg := resource.NewRegistry(resource.RegistryConfig{})
	primary := memory.New(
		resource.MustParseResourceID("http-service/prod/pay/a"),
		resource.Description{},
		resource.ResourceCapabilities{SupportsHealth: true, SupportsFailover: true},
	)
	standby := memory.New(
		resource.MustParseResourceID("http-service/prod/pay/b"),
		resource.Description{},
		resource.ResourceCapabilities{SupportsHealth: true, SupportsFailover: true},
	)
	_, _ = reg.Register(primary, resource.Metadata{})
	_, _ = reg.Register(standby, resource.Metadata{})
	primary.SetHealth(resource.ResourceHealth{Overall: resource.HealthUnhealthy})

	m := NewManager(reg)
	_ = m.Register(GroupConfig{
		Name: "g", Primary: primary.ID(), Standbys: []resource.ResourceID{standby.ID()},
		Policy: PolicyHealthBased,
	})
	rec, ok, err := m.EvaluateHealth(context.Background(), "g")
	if err != nil || !ok || !rec.Equal(standby.ID()) {
		t.Fatalf("rec=%v ok=%v err=%v", rec, ok, err)
	}
}
