package workflow_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theworker02/shiftlock/workflow"
)

func TestParallelGroupSuccess(t *testing.T) {
	var mu sync.Mutex
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var order []string

	def, err := workflow.Define("par").
		Step("prep", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			mu.Lock()
			order = append(order, "prep")
			mu.Unlock()
			return workflow.Result{}, nil
		}).
		Step("a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			started <- struct{}{}
			<-release
			mu.Lock()
			order = append(order, "a")
			mu.Unlock()
			return workflow.Result{}, nil
		}).
		Step("b", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			started <- struct{}{}
			<-release
			mu.Lock()
			order = append(order, "b")
			mu.Unlock()
			return workflow.Result{}, nil
		}).
		Step("done", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			mu.Lock()
			order = append(order, "done")
			mu.Unlock()
			return workflow.Result{}, nil
		}).
		Depend("a", "prep").
		Depend("b", "prep").
		Depend("done", "a", "b").
		ParallelGroup("fanout", "a", "b").
		Build()
	if err != nil {
		t.Fatal(err)
	}
	eng := workflow.NewEngine(workflow.EngineConfig{})
	_ = eng.Register(def)

	done := make(chan error, 1)
	go func() {
		_, err := eng.Run(context.Background(), "par", workflow.RunOptions{})
		done <- err
	}()
	// Both parallel steps should start before either finishes.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first parallel start")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for second parallel start")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 4 || order[0] != "prep" || order[3] != "done" {
		t.Fatalf("order=%v", order)
	}
}

func TestParallelGroupFailureCompensates(t *testing.T) {
	var aComp, prepComp atomic.Bool
	def, err := workflow.Define("par-fail").
		Step("prep", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			return workflow.Result{}, nil
		}).
		Compensate("prep", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			prepComp.Store(true)
			return workflow.Result{}, nil
		}).
		Step("a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			time.Sleep(20 * time.Millisecond)
			return workflow.Result{}, nil
		}).
		Compensate("a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			aComp.Store(true)
			return workflow.Result{}, nil
		}).
		Step("b", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			return workflow.Result{}, errors.New("b boom")
		}).
		Depend("a", "prep").
		Depend("b", "prep").
		ParallelGroup("g", "a", "b").
		Build()
	if err != nil {
		t.Fatal(err)
	}
	eng := workflow.NewEngine(workflow.EngineConfig{})
	_ = eng.Register(def)
	inst, err := eng.Run(context.Background(), "par-fail", workflow.RunOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if inst.State != workflow.StateFailed {
		t.Fatalf("state=%s", inst.State)
	}
	if !prepComp.Load() {
		t.Fatal("expected prep compensation")
	}
	// a may or may not have completed before b failed; if completed, must compensate.
	if inst.Checkpoint.Steps["a"].Status == workflow.StepCompleted {
		t.Fatal("a should be compensated or failed, not left completed")
	}
	if inst.Checkpoint.Steps["a"].Status == workflow.StepCompensated && !aComp.Load() {
		t.Fatal("a compensated flag missing")
	}
}

func TestJournalStoreSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoints.journal")
	store1, err := workflow.NewJournalStore(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	var aRuns atomic.Int32
	def, err := workflow.Define("resume-j").
		Step("a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			aRuns.Add(1)
			return workflow.Result{}, nil
		}).
		Step("b", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			return workflow.Result{}, errors.New("stop")
		}).
		Depend("b", "a").
		Build()
	if err != nil {
		t.Fatal(err)
	}
	eng1 := workflow.NewEngine(workflow.EngineConfig{Store: store1})
	_ = eng1.Register(def)
	_, _ = eng1.Run(context.Background(), "resume-j", workflow.RunOptions{InstanceID: "j1"})

	// Simulate crash: rewrite journal checkpoint mid-run after a completed.
	cp, err := store1.Load("j1")
	if err != nil {
		t.Fatal(err)
	}
	cp.State = workflow.StateRunning
	cp.Steps["a"] = workflow.StepState{Name: "a", Status: workflow.StepCompleted, Attempt: 1}
	cp.Steps["b"] = workflow.StepState{Name: "b", Status: workflow.StepPending}
	cp.CompletedSteps = []string{"a"}
	if err := store1.Save(cp); err != nil {
		t.Fatal(err)
	}

	store2, err := workflow.NewJournalStore(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	eng2 := workflow.NewEngine(workflow.EngineConfig{Store: store2})
	_ = eng2.Register(def)
	// b will fail again; a must not re-run.
	_, err = eng2.Run(context.Background(), "resume-j", workflow.RunOptions{InstanceID: "j1", Resume: true})
	if err == nil {
		t.Fatal("expected b failure")
	}
	if aRuns.Load() != 1 {
		t.Fatalf("a re-executed: %d", aRuns.Load())
	}
}

func TestFileStoreFsyncRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cp.json")
	store := workflow.NewFileStore(path, 0)
	cp := workflow.Checkpoint{
		InstanceID: "f1", Workflow: "w", State: workflow.StateRunning,
		Steps: map[string]workflow.StepState{
			"a": {Name: "a", Status: workflow.StepCompleted},
		},
		CompletedSteps: []string{"a"},
	}
	if err := store.Save(cp); err != nil {
		t.Fatal(err)
	}
	store2 := workflow.NewFileStore(path, 0)
	got, err := store2.Load("f1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps["a"].Status != workflow.StepCompleted {
		t.Fatalf("%+v", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestNotRetryableCompletedNotReexecuted(t *testing.T) {
	store := workflow.NewMemoryStore(0)
	var aRuns, bRuns atomic.Int32
	def, err := workflow.Define("nr").
		Step("a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			aRuns.Add(1)
			return workflow.Result{}, nil
		}).
		Idempotency("a", workflow.NotRetryable).
		Step("b", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			bRuns.Add(1)
			return workflow.Result{}, nil
		}).
		Depend("b", "a").
		Idempotency("b", workflow.RequiresOperationID).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	eng1 := workflow.NewEngine(workflow.EngineConfig{Store: store})
	_ = eng1.Register(def)
	inst, err := eng1.Run(context.Background(), "nr", workflow.RunOptions{InstanceID: "n1", OperationID: "op-1"})
	if err != nil {
		t.Fatal(err)
	}
	if inst.State != workflow.StateCompleted {
		t.Fatalf("%s", inst.State)
	}

	eng2 := workflow.NewEngine(workflow.EngineConfig{Store: store})
	_ = eng2.Register(def)
	inst2, err := eng2.Run(context.Background(), "nr", workflow.RunOptions{InstanceID: "n1", Resume: true, OperationID: "op-1"})
	if err != nil {
		t.Fatal(err)
	}
	if inst2.State != workflow.StateCompleted {
		t.Fatalf("%s", inst2.State)
	}
	if aRuns.Load() != 1 || bRuns.Load() != 1 {
		t.Fatalf("re-executed a=%d b=%d", aRuns.Load(), bRuns.Load())
	}
}

func TestNotRetryableFailedNotReexecuted(t *testing.T) {
	store := workflow.NewMemoryStore(0)
	var runs atomic.Int32
	def, err := workflow.Define("nr-fail").
		Step("x", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			runs.Add(1)
			return workflow.Result{}, errors.New("nope")
		}).
		Idempotency("x", workflow.NotRetryable).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	eng1 := workflow.NewEngine(workflow.EngineConfig{Store: store})
	_ = eng1.Register(def)
	_, err = eng1.Run(context.Background(), "nr-fail", workflow.RunOptions{InstanceID: "nf1"})
	if err == nil {
		t.Fatal("expected fail")
	}
	eng2 := workflow.NewEngine(workflow.EngineConfig{Store: store})
	_ = eng2.Register(def)
	_, err = eng2.Run(context.Background(), "nr-fail", workflow.RunOptions{InstanceID: "nf1", Resume: true})
	if !errors.Is(err, workflow.ErrNotRetryable) {
		t.Fatalf("got %v", err)
	}
	if runs.Load() != 1 {
		t.Fatalf("re-executed: %d", runs.Load())
	}
}
