package workflow_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/theworker02/shiftlock/resource"
	"github.com/theworker02/shiftlock/resource/memory"
	"github.com/theworker02/shiftlock/workflow"
)

func TestSequentialSuccess(t *testing.T) {
	var order []string
	def, err := workflow.Define("ok").
		Step("a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			order = append(order, "a")
			return workflow.Result{}, nil
		}).
		Step("b", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			order = append(order, "b")
			return workflow.Result{}, nil
		}).
		Depend("b", "a").
		Build()
	if err != nil {
		t.Fatal(err)
	}
	eng := workflow.NewEngine(workflow.EngineConfig{})
	if err := eng.Register(def); err != nil {
		t.Fatal(err)
	}
	inst, err := eng.Run(context.Background(), "ok", workflow.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if inst.State != workflow.StateCompleted {
		t.Fatalf("state=%s", inst.State)
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("%v", order)
	}
}

func TestStepFailureCompensates(t *testing.T) {
	var compensated atomic.Bool
	def, err := workflow.Define("fail").
		Step("a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			return workflow.Result{}, nil
		}).
		Compensate("a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			compensated.Store(true)
			return workflow.Result{}, nil
		}).
		Step("b", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			return workflow.Result{}, errors.New("boom")
		}).
		Depend("b", "a").
		Build()
	if err != nil {
		t.Fatal(err)
	}
	eng := workflow.NewEngine(workflow.EngineConfig{})
	_ = eng.Register(def)
	inst, err := eng.Run(context.Background(), "fail", workflow.RunOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if inst.State != workflow.StateFailed {
		t.Fatalf("state=%s", inst.State)
	}
	if !compensated.Load() {
		t.Fatal("expected compensation")
	}
	st := inst.Checkpoint.Steps["a"]
	if st.Status != workflow.StepCompensated {
		t.Fatalf("a status=%s", st.Status)
	}
}

func TestAmbiguousRequiresReconciliation(t *testing.T) {
	def, err := workflow.Define("amb").
		Step("x", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			return workflow.Result{Ambiguous: true}, nil
		}).
		Idempotency("x", workflow.RequiresReconciliation).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	eng := workflow.NewEngine(workflow.EngineConfig{})
	_ = eng.Register(def)
	inst, err := eng.Run(context.Background(), "amb", workflow.RunOptions{})
	if !errors.Is(err, workflow.ErrRequiresReconciliation) {
		t.Fatalf("got %v", err)
	}
	if inst.State != workflow.StateRequiresReconciliation {
		t.Fatalf("state=%s", inst.State)
	}
}

func TestRestartResumeFromCheckpoint(t *testing.T) {
	store := workflow.NewMemoryStore(0)
	var aRuns, bRuns atomic.Int32
	def, err := workflow.Define("resume").
		Step("a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			aRuns.Add(1)
			return workflow.Result{}, nil
		}).
		Step("b", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			if bRuns.Add(1) == 1 {
				return workflow.Result{}, errors.New("interrupt")
			}
			return workflow.Result{}, nil
		}).
		Depend("b", "a").
		Build()
	if err != nil {
		t.Fatal(err)
	}

	eng1 := workflow.NewEngine(workflow.EngineConfig{Store: store})
	_ = eng1.Register(def)
	_, err = eng1.Run(context.Background(), "resume", workflow.RunOptions{InstanceID: "i1"})
	if err == nil {
		t.Fatal("expected fail")
	}

	// Simulate crash after a completed, before b finished — rewrite checkpoint.
	cp, _ := store.Load("i1")
	cp.State = workflow.StateRunning
	cp.Steps["a"] = workflow.StepState{Name: "a", Status: workflow.StepCompleted, Attempt: 1}
	cp.Steps["b"] = workflow.StepState{Name: "b", Status: workflow.StepPending}
	cp.CompletedSteps = []string{"a"}
	_ = store.Save(cp)

	eng2 := workflow.NewEngine(workflow.EngineConfig{Store: store})
	_ = eng2.Register(def)
	inst2, err := eng2.Run(context.Background(), "resume", workflow.RunOptions{InstanceID: "i1", Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if inst2.State != workflow.StateCompleted {
		t.Fatalf("state=%s", inst2.State)
	}
	if aRuns.Load() != 1 {
		t.Fatalf("a re-ran: aRuns=%d", aRuns.Load())
	}
	if bRuns.Load() != 2 {
		t.Fatalf("bRuns=%d", bRuns.Load())
	}
}

func TestCapabilityValidationFailure(t *testing.T) {
	reg := resource.NewRegistry(resource.RegistryConfig{})
	feat := memory.Feature("dev", "svc", "f")
	_, _ = reg.Register(feat, resource.Metadata{})

	def, err := workflow.Define("caps").
		Step("m", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			return workflow.Result{}, nil
		}).
		RequireCaps("m", feat.ID(), resource.ResourceCapabilities{SupportsFencing: true}).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	eng := workflow.NewEngine(workflow.EngineConfig{
		Hooks: workflow.Hooks{Resources: lookup{reg}},
	})
	_ = eng.Register(def)
	_, err = eng.Run(context.Background(), "caps", workflow.RunOptions{})
	if !errors.Is(err, workflow.ErrCapability) {
		t.Fatalf("got %v", err)
	}
}

type lockdownOn struct{}

func (lockdownOn) BlocksMutations() bool { return true }

func TestLockdownBlocksMutationSteps(t *testing.T) {
	def, err := workflow.Define("ld").
		Step("m", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			return workflow.Result{}, nil
		}).
		Mutating("m", true).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	eng := workflow.NewEngine(workflow.EngineConfig{
		Hooks: workflow.Hooks{Lockdown: lockdownOn{}},
	})
	_ = eng.Register(def)
	inst, err := eng.Run(context.Background(), "ld", workflow.RunOptions{})
	if !errors.Is(err, workflow.ErrLockdown) {
		t.Fatalf("got %v", err)
	}
	if inst.State != workflow.StateLockedDown {
		t.Fatalf("state=%s", inst.State)
	}
}

func TestDryRun(t *testing.T) {
	var ran atomic.Bool
	def, err := workflow.Define("dry").
		Step("a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			ran.Store(true)
			return workflow.Result{}, nil
		}).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	eng := workflow.NewEngine(workflow.EngineConfig{})
	_ = eng.Register(def)
	inst, err := eng.Run(context.Background(), "dry", workflow.RunOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if ran.Load() {
		t.Fatal("action should not run in dry-run")
	}
	if inst.State != workflow.StateCompleted {
		t.Fatalf("%s", inst.State)
	}
}

type lookup struct{ reg *resource.Registry }

func (l lookup) Get(id resource.ResourceID) (*resource.Entry, error) {
	return l.reg.Get(id)
}
