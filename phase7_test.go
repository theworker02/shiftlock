package shiftlock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/control/lockdown"
	"github.com/theworker02/shiftlock/resource"
	resmem "github.com/theworker02/shiftlock/resource/memory"
	"github.com/theworker02/shiftlock/workflow"
)

func TestPhase7ResourcesAndWorkflows(t *testing.T) {
	be := memory.New()
	rt, err := shiftlock.NewRuntime(shiftlock.RuntimeConfig{
		Config: shiftlock.Config{
			Service: "billing", InstanceID: "pod-a", Backend: be, LeaseTTL: time.Minute,
		},
		EnableResources: true,
		EnableWorkflows: true,
		EnableLockdown:  true,
		EnableAudit:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	if !rt.Features().Resources || !rt.Features().Workflows {
		t.Fatalf("features=%+v", rt.Features())
	}
	reg := rt.Resources()
	w := resmem.Worker("dev", "billing", "reconciler")
	if _, err := reg.Register(w, resource.Metadata{Source: "test"}); err != nil {
		t.Fatal(err)
	}

	def, err := workflow.Define("tiny").
		Step("ping", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			return workflow.Result{Evidence: workflow.Evidence{Event: "ping"}}, nil
		}).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Workflows().Register(def); err != nil {
		t.Fatal(err)
	}
	inst, err := rt.Workflows().Run(context.Background(), "tiny", workflow.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if inst.State != workflow.StateCompleted {
		t.Fatalf("%s", inst.State)
	}

	_, err = rt.Lockdown().Enter(lockdown.EnterRequest{
		Mode: lockdown.ModeFailClosed, Reason: "test", ActorID: "op",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Register(resmem.Feature("dev", "billing", "x"), resource.Metadata{})
	if !errors.Is(err, resource.ErrLockdown) {
		t.Fatalf("expected lockdown, got %v", err)
	}
}

func TestPhase7Facades(t *testing.T) {
	be := memory.New()
	rt, err := shiftlock.NewRuntime(shiftlock.RuntimeConfig{
		Config: shiftlock.Config{
			Service: "billing", InstanceID: "pod-a", Backend: be, LeaseTTL: time.Minute,
		},
		EnableResources: true,
		EnableWorkflows: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if rt.Migrations() == nil || rt.Failover() == nil || rt.Sync() == nil {
		t.Fatal("expected migrations/failover/sync façades")
	}
	if err := rt.Migrations().DefineSimple("m1", "a", "b"); err != nil {
		t.Fatal(err)
	}
}

func TestWithLocalStateStub(t *testing.T) {
	be := memory.New()
	cfg := shiftlock.RuntimeConfig{
		Config: shiftlock.Config{
			Service: "billing", InstanceID: "pod-a", Backend: be, LeaseTTL: time.Minute,
		},
	}
	shiftlock.WithLocalState(shiftlock.LocalStateConfig{Mode: shiftlock.LocalStateMemory})(&cfg)
	rt, err := shiftlock.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if rt.Resources() == nil || rt.Workflows() == nil {
		t.Fatal("expected fabric enabled")
	}
}

func TestWithLocalStateDirRestart(t *testing.T) {
	dir := t.TempDir()
	be := memory.New()
	cfg := shiftlock.RuntimeConfig{
		Config: shiftlock.Config{
			Service: "billing", InstanceID: "pod-a", Backend: be, LeaseTTL: time.Minute,
		},
	}
	shiftlock.WithLocalStateDir(dir)(&cfg)
	rt1, err := shiftlock.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	def, err := workflow.Define("local").
		Step("a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			return workflow.Result{}, nil
		}).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	_ = rt1.Workflows().Register(def)
	inst, err := rt1.Workflows().Run(context.Background(), "local", workflow.RunOptions{InstanceID: "local-1"})
	if err != nil {
		t.Fatal(err)
	}
	if inst.State != workflow.StateCompleted {
		t.Fatalf("%s", inst.State)
	}
	_ = rt1.Close()

	be2 := memory.New()
	cfg2 := shiftlock.RuntimeConfig{
		Config: shiftlock.Config{
			Service: "billing", InstanceID: "pod-b", Backend: be2, LeaseTTL: time.Minute,
		},
	}
	shiftlock.WithLocalStateDir(dir)(&cfg2)
	rt2, err := shiftlock.NewRuntime(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.Close()
	_ = rt2.Workflows().Register(def)
	got, err := rt2.Workflows().Get("local-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != workflow.StateCompleted {
		t.Fatalf("after restart state=%s", got.State)
	}
	if rt2.LocalStateDir() != dir {
		t.Fatalf("dir=%s", rt2.LocalStateDir())
	}
}
