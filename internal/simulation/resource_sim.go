package simulation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theworker02/shiftlock/resource"
	"github.com/theworker02/shiftlock/resource/memory"
	"github.com/theworker02/shiftlock/resource/queue"
	"github.com/theworker02/shiftlock/workflow"
)

// ResourceEnv is a multi-resource simulation harness.
type ResourceEnv struct {
	Registry *resource.Registry
	Queue    *queue.Memory
	QueueRes *queue.Resource
	Primary  *memory.Base
	Standby  *memory.Base
}

// NewResourceEnv seeds a registry with fail-able resources and a delayable queue.
func NewResourceEnv(t *testing.T) *ResourceEnv {
	t.Helper()
	reg := resource.NewRegistry(resource.RegistryConfig{MaxResources: 64})
	t.Cleanup(func() { reg.Close() })

	primary := memory.New(
		resource.MustParseResourceID("database/sim/app/primary"),
		resource.Description{DisplayName: "primary"},
		resource.ResourceCapabilities{SupportsHealth: true, SupportsFailover: true},
	)
	standby := memory.New(
		resource.MustParseResourceID("database/sim/app/standby"),
		resource.Description{DisplayName: "standby"},
		resource.ResourceCapabilities{SupportsHealth: true, SupportsFailover: true},
	)
	qMem := queue.NewMemory(32)
	qRes, err := queue.New(queue.Config{
		ID: resource.MustParseResourceID("queue/sim/app/events"), Backend: qMem,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []resource.Resource{primary, standby, qRes} {
		if _, err := reg.Register(r, resource.Metadata{Source: "sim"}); err != nil {
			t.Fatal(err)
		}
	}
	return &ResourceEnv{Registry: reg, Queue: qMem, QueueRes: qRes, Primary: primary, Standby: standby}
}

// FailResource marks a memory resource unhealthy.
func (e *ResourceEnv) FailResource(res *memory.Base) {
	res.SetHealth(resource.ResourceHealth{Overall: resource.HealthUnhealthy, Message: "simulated failure", CheckedAt: time.Now().UTC()})
}

// DelayQueue pauses the queue to simulate broker delay/backpressure.
func (e *ResourceEnv) DelayQueue(ctx context.Context) error {
	return e.Queue.Pause(ctx)
}

func TestResourceFailInvariantNoDualActiveWrite(t *testing.T) {
	env := NewResourceEnv(t)
	ctx := context.Background()
	env.FailResource(env.Primary)

	// Invariant: unhealthy primary must not be treated as writable alongside standby
	// without an explicit epoch advance / failover decision.
	h := env.Primary.Health(ctx)
	if h.Overall != resource.HealthUnhealthy {
		t.Fatalf("expected unhealthy, got %s", h.Overall)
	}
	ent, err := env.Registry.Get(env.Primary.ID())
	if err != nil {
		t.Fatal(err)
	}
	if ent.Epoch != 0 {
		t.Fatalf("epoch must not auto-advance on health flip: %v", ent.Epoch)
	}
	// Standby remains healthy; still epoch 0 — failover is operator/manager driven.
	if env.Standby.Health(ctx).Overall != resource.HealthHealthy {
		t.Fatal("standby should stay healthy")
	}
}

func TestDelayedQueueWorkflowInvariant(t *testing.T) {
	env := NewResourceEnv(t)
	ctx := context.Background()
	if err := env.DelayQueue(ctx); err != nil {
		t.Fatal(err)
	}
	if err := env.Queue.Publish("x"); err == nil {
		t.Fatal("expected publish reject while delayed/paused")
	}

	def, err := workflow.Define("wait-queue").
		Step("check", func(ctx context.Context, _ *workflow.ExecContext) (workflow.Result, error) {
			if env.Queue.Paused() {
				return workflow.Result{Evidence: workflow.Evidence{Event: "paused"}}, nil
			}
			return workflow.Result{}, errors.New("expected paused")
		}).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	eng := workflow.NewEngine(workflow.EngineConfig{MaxRuns: 4})
	_ = eng.Register(def)
	inst, err := eng.Run(ctx, "wait-queue", workflow.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if inst.State != workflow.StateCompleted {
		t.Fatalf("%s", inst.State)
	}
	_ = env.Queue.Resume(ctx)
	if err := env.Queue.Publish("x"); err != nil {
		t.Fatal(err)
	}
}
