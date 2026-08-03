package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

// DefaultMaxParallel bounds concurrent step execution within a parallel group.
const DefaultMaxParallel = 8

// EngineConfig configures the workflow engine.
type EngineConfig struct {
	Store       Store
	Hooks       Hooks
	Clock       func() time.Time
	MaxRuns     int // bound concurrent tracked instances; 0 → DefaultMaxInstances
	MaxParallel int // bound parallel group concurrency; 0 → DefaultMaxParallel
}

// Engine executes workflow definitions with checkpoints and soft Runtime hooks.
type Engine struct {
	mu          sync.Mutex
	defs        map[string]*Definition
	store       Store
	hooks       Hooks
	clock       func() time.Time
	max         int
	maxParallel int
	closed      bool
	instances   map[string]*Instance
}

// Instance is a running or paused workflow.
type Instance struct {
	ID         string
	Workflow   string
	State      State
	DryRun     bool
	Checkpoint Checkpoint
}

// RunOptions configures a single run.
type RunOptions struct {
	InstanceID  string
	DryRun      bool
	OperationID string
	Attrs       map[string]string
	// Resume loads an existing checkpoint by InstanceID instead of starting fresh.
	Resume bool
}

// NewEngine constructs an engine. Store defaults to MemoryStore.
func NewEngine(cfg EngineConfig) *Engine {
	if cfg.Store == nil {
		cfg.Store = NewMemoryStore(cfg.MaxRuns)
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	max := cfg.MaxRuns
	if max <= 0 {
		max = DefaultMaxInstances
	}
	mp := cfg.MaxParallel
	if mp <= 0 {
		mp = DefaultMaxParallel
	}
	return &Engine{
		defs:        make(map[string]*Definition),
		store:       cfg.Store,
		hooks:       cfg.Hooks,
		clock:       cfg.Clock,
		max:         max,
		maxParallel: mp,
		instances:   make(map[string]*Instance),
	}
}

// SetHooks updates soft Runtime integrations.
func (e *Engine) SetHooks(h Hooks) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hooks = h
}

// Register adds a validated definition.
func (e *Engine) Register(def *Definition) error {
	if def == nil || def.Name == "" {
		return &Error{Op: "Register", Err: ErrInvalidDefinition, Message: "nil definition"}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return &Error{Op: "Register", Err: ErrClosed, Message: "engine closed"}
	}
	e.defs[def.Name] = def
	return nil
}

// Close prevents new runs.
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
}

// Run executes (or resumes) a workflow.
func (e *Engine) Run(ctx context.Context, workflowName string, opts RunOptions) (*Instance, error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, &Error{Op: "Run", Workflow: workflowName, Err: ErrClosed, Message: "engine closed"}
	}
	def, ok := e.defs[workflowName]
	if !ok {
		e.mu.Unlock()
		return nil, &Error{Op: "Run", Workflow: workflowName, Err: ErrNotFound, Message: "definition not registered"}
	}
	if !opts.Resume && len(e.instances) >= e.max {
		e.mu.Unlock()
		return nil, &Error{Op: "Run", Workflow: workflowName, Err: ErrBoundExceeded, Message: "max instances"}
	}
	id := opts.InstanceID
	if id == "" {
		id = newID()
	}
	var inst *Instance
	if opts.Resume {
		cp, err := e.store.Load(id)
		if err != nil {
			e.mu.Unlock()
			return nil, err
		}
		inst = &Instance{ID: id, Workflow: cp.Workflow, State: cp.State, DryRun: cp.DryRun, Checkpoint: cp}
		e.instances[id] = inst
	} else {
		inst = &Instance{
			ID: id, Workflow: workflowName, State: StateCreated, DryRun: opts.DryRun,
			Checkpoint: Checkpoint{
				InstanceID: id, Workflow: workflowName, State: StateCreated,
				Steps: make(map[string]StepState), DryRun: opts.DryRun, Attrs: cloneAttrs(opts.Attrs),
				EpochAtStep: map[string]uint64{},
			},
		}
		e.instances[id] = inst
	}
	hooks := e.hooks
	e.mu.Unlock()

	e.audit(hooks, "workflow", "workflow.run", workflowName, "start", opts.OperationID)
	err := e.execute(ctx, def, inst, opts)
	if err != nil {
		e.audit(hooks, "workflow", "workflow.run", workflowName, "error", opts.OperationID)
		return inst, err
	}
	e.audit(hooks, "workflow", "workflow.run", workflowName, string(inst.State), opts.OperationID)
	return inst, nil
}

// Get returns a live instance snapshot.
func (e *Engine) Get(instanceID string) (*Instance, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	inst, ok := e.instances[instanceID]
	if !ok {
		cp, err := e.store.Load(instanceID)
		if err != nil {
			return nil, err
		}
		return &Instance{ID: cp.InstanceID, Workflow: cp.Workflow, State: cp.State, DryRun: cp.DryRun, Checkpoint: cp}, nil
	}
	cp := cloneCheckpoint(inst.Checkpoint)
	return &Instance{ID: inst.ID, Workflow: inst.Workflow, State: inst.State, DryRun: inst.DryRun, Checkpoint: cp}, nil
}

// ListDefinitions returns registered workflow names.
func (e *Engine) ListDefinitions() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.defs))
	for name := range e.defs {
		out = append(out, name)
	}
	return out
}

// ListInstances returns snapshots of tracked instances.
func (e *Engine) ListInstances() []Instance {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Instance, 0, len(e.instances))
	for _, inst := range e.instances {
		cp := cloneCheckpoint(inst.Checkpoint)
		out = append(out, Instance{ID: inst.ID, Workflow: inst.Workflow, State: inst.State, DryRun: inst.DryRun, Checkpoint: cp})
	}
	return out
}

func (e *Engine) execute(ctx context.Context, def *Definition, inst *Instance, opts RunOptions) error {
	inst.State = StateValidating
	inst.Checkpoint.State = StateValidating
	if err := e.persist(inst); err != nil {
		return err
	}

	for _, name := range def.order {
		step, _ := def.Step(name)
		if step.ResourceID.IsZero() {
			continue
		}
		if err := e.validateCaps(step); err != nil {
			inst.State = StateFailed
			inst.Checkpoint.State = StateFailed
			_ = e.persist(inst)
			return err
		}
	}

	inst.State = StateRunning
	inst.Checkpoint.State = StateRunning
	_ = e.persist(inst)

	hasParallel := false
	for _, name := range def.order {
		step, _ := def.Step(name)
		if step.ParallelGroup != "" {
			hasParallel = true
			break
		}
	}
	if hasParallel {
		return e.executeParallel(ctx, def, inst, opts)
	}
	return e.executeSequential(ctx, def, inst, opts)
}

func (e *Engine) executeSequential(ctx context.Context, def *Definition, inst *Instance, opts RunOptions) error {
	for _, name := range def.order {
		if err := ctx.Err(); err != nil {
			inst.State = StateCancelled
			inst.Checkpoint.State = StateCancelled
			_ = e.persist(inst)
			return &Error{Op: "Run", Workflow: def.Name, Err: ErrCancelled, Message: err.Error()}
		}
		if skip, err := e.prepareStepResume(def, inst, name, opts); err != nil {
			return err
		} else if skip {
			continue
		}
		step, _ := def.Step(name)
		if step.Mutates {
			if e.hooks.Lockdown != nil && e.hooks.Lockdown.BlocksMutations() {
				inst.State = StateLockedDown
				inst.Checkpoint.State = StateLockedDown
				_ = e.persist(inst)
				return &Error{Op: "Run", Workflow: def.Name, Step: name, Err: ErrLockdown, Message: "lockdown blocks mutation step"}
			}
		}
		if err := e.runStep(ctx, def, inst, step, opts); err != nil {
			return err
		}
	}
	inst.State = StateCompleted
	inst.Checkpoint.State = StateCompleted
	return e.persist(inst)
}

func (e *Engine) executeParallel(ctx context.Context, def *Definition, inst *Instance, opts RunOptions) error {
	done := make(map[string]bool, len(def.order))
	for _, name := range def.order {
		st := inst.Checkpoint.Steps[name]
		if st.Status == StepCompleted || st.Status == StepCompensated || st.Status == StepSkipped {
			done[name] = true
		}
	}

	for len(done) < len(def.order) {
		if err := ctx.Err(); err != nil {
			inst.State = StateCancelled
			inst.Checkpoint.State = StateCancelled
			_ = e.persist(inst)
			return &Error{Op: "Run", Workflow: def.Name, Err: ErrCancelled, Message: err.Error()}
		}

		var ready []string
		for _, name := range def.order {
			if done[name] {
				continue
			}
			st := inst.Checkpoint.Steps[name]
			if st.Status == StepFailed || st.Status == StepReconcile {
				continue
			}
			step, _ := def.Step(name)
			ok := true
			for _, d := range step.DependsOn {
				if !done[d] {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			skip, err := e.prepareStepResume(def, inst, name, opts)
			if err != nil {
				return err
			}
			if skip {
				done[name] = true
				continue
			}
			ready = append(ready, name)
		}
		if len(ready) == 0 {
			// Check if remaining are failed/reconcile — already handled; otherwise stuck.
			for _, name := range def.order {
				if !done[name] {
					st := inst.Checkpoint.Steps[name]
					if st.Status == StepFailed {
						inst.State = StateFailed
						inst.Checkpoint.State = StateFailed
						_ = e.persist(inst)
						return &Error{Op: "Run", Workflow: def.Name, Step: name, Err: ErrNotRetryable, Message: st.Error}
					}
					if st.Status == StepReconcile {
						inst.State = StateRequiresReconciliation
						inst.Checkpoint.State = StateRequiresReconciliation
						_ = e.persist(inst)
						return &Error{Op: "Run", Workflow: def.Name, Step: name, Err: ErrRequiresReconciliation, Message: st.Error}
					}
				}
			}
			return &Error{Op: "Run", Workflow: def.Name, Err: ErrInvalidState, Message: "no ready steps"}
		}

		// Partition: named parallel groups vs sequential (empty group).
		groups := map[string][]string{}
		var sequential []string
		for _, name := range ready {
			step, _ := def.Step(name)
			if step.ParallelGroup == "" {
				sequential = append(sequential, name)
			} else {
				groups[step.ParallelGroup] = append(groups[step.ParallelGroup], name)
			}
		}

		// Prefer running a parallel group wave when present.
		if len(groups) > 0 {
			// Deterministic: pick smallest group name.
			gname := ""
			for g := range groups {
				if gname == "" || g < gname {
					gname = g
				}
			}
			batch := groups[gname]
			if err := e.runParallelBatch(ctx, def, inst, batch, opts); err != nil {
				return err
			}
			for _, name := range batch {
				st := inst.Checkpoint.Steps[name]
				if st.Status == StepCompleted || st.Status == StepSkipped || st.Status == StepCompensated {
					done[name] = true
				}
			}
			continue
		}

		// Sequential ready steps: one at a time in topo order.
		name := sequential[0]
		step, _ := def.Step(name)
		if step.Mutates {
			if e.hooks.Lockdown != nil && e.hooks.Lockdown.BlocksMutations() {
				inst.State = StateLockedDown
				inst.Checkpoint.State = StateLockedDown
				_ = e.persist(inst)
				return &Error{Op: "Run", Workflow: def.Name, Step: name, Err: ErrLockdown, Message: "lockdown blocks mutation step"}
			}
		}
		if err := e.runStep(ctx, def, inst, step, opts); err != nil {
			return err
		}
		done[name] = true
	}

	inst.State = StateCompleted
	inst.Checkpoint.State = StateCompleted
	return e.persist(inst)
}

func (e *Engine) runParallelBatch(ctx context.Context, def *Definition, inst *Instance, names []string, opts RunOptions) error {
	for _, name := range names {
		step, _ := def.Step(name)
		if step.Mutates && e.hooks.Lockdown != nil && e.hooks.Lockdown.BlocksMutations() {
			inst.State = StateLockedDown
			inst.Checkpoint.State = StateLockedDown
			_ = e.persist(inst)
			return &Error{Op: "Run", Workflow: def.Name, Step: name, Err: ErrLockdown, Message: "lockdown blocks mutation step"}
		}
	}

	type prepared struct {
		name  string
		step  *StepDef
		exec  *ExecContext
		st    StepState
		epoch resource.ResourceEpoch
	}
	preps := make([]prepared, 0, len(names))
	now := e.clock().UTC()
	for _, name := range names {
		step, _ := def.Step(name)
		st := inst.Checkpoint.Steps[name]
		st.Name = name
		st.Status = StepRunning
		st.Attempt++
		st.StartedAt = now
		inst.Checkpoint.Steps[name] = st
		var epoch resource.ResourceEpoch
		if !step.ResourceID.IsZero() && e.hooks.Resources != nil {
			if ent, err := e.hooks.Resources.Get(step.ResourceID); err == nil {
				epoch = ent.Epoch
			}
		}
		preps = append(preps, prepared{
			name: name, step: step, st: st, epoch: epoch,
			exec: &ExecContext{
				Workflow: def.Name, InstanceID: inst.ID, Step: name,
				Attempt: st.Attempt, DryRun: inst.DryRun, OperationID: opts.OperationID,
				Attrs: cloneAttrs(opts.Attrs), ResourceEpoch: epoch, ResourceID: step.ResourceID,
			},
		})
	}
	if err := e.persist(inst); err != nil {
		return err
	}

	type outcome struct {
		p    prepared
		res  Result
		err  error
	}
	sem := make(chan struct{}, e.maxParallel)
	outCh := make(chan outcome, len(preps))
	var wg sync.WaitGroup
	for _, p := range preps {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				outCh <- outcome{p: p, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			if inst.DryRun {
				outCh <- outcome{p: p, res: Result{Evidence: Evidence{Time: now, Event: "dry-run", Summary: "skipped side effects"}}}
				return
			}
			res, err := p.step.Action(ctx, p.exec)
			outCh <- outcome{p: p, res: res, err: err}
		}()
	}
	wg.Wait()
	close(outCh)

	var outcomes []outcome
	for o := range outCh {
		outcomes = append(outcomes, o)
	}
	// Apply successes first (deterministic by name), then first failure.
	sort.Slice(outcomes, func(i, j int) bool { return outcomes[i].p.name < outcomes[j].p.name })
	var fail *outcome
	for i := range outcomes {
		o := &outcomes[i]
		if o.err != nil || o.res.Ambiguous {
			if fail == nil {
				fail = o
			}
			continue
		}
		st := o.p.st
		st.Status = StepCompleted
		st.FinishedAt = e.clock().UTC()
		st.Evidence = append(st.Evidence, o.res.Evidence)
		inst.Checkpoint.Steps[o.p.name] = st
		inst.Checkpoint.CompletedSteps = append(inst.Checkpoint.CompletedSteps, o.p.name)
		if inst.Checkpoint.EpochAtStep == nil {
			inst.Checkpoint.EpochAtStep = map[string]uint64{}
		}
		inst.Checkpoint.EpochAtStep[o.p.name] = uint64(o.p.epoch)
		e.audit(e.hooks, "workflow", "workflow.step.ok", o.p.name, "completed", opts.OperationID)
	}
	_ = e.persist(inst)
	if fail != nil {
		// Mark other non-completed batch members so they are not re-run as "running".
		for _, o := range outcomes {
			if o.p.name == fail.p.name {
				continue
			}
			st := inst.Checkpoint.Steps[o.p.name]
			if st.Status == StepRunning {
				st.Status = StepFailed
				st.Error = "sibling step failed in parallel group"
				st.FinishedAt = e.clock().UTC()
				inst.Checkpoint.Steps[o.p.name] = st
			}
		}
		_ = e.persist(inst)
		if fail.err != nil && (errors.Is(fail.err, context.Canceled) || errors.Is(fail.err, context.DeadlineExceeded)) {
			inst.State = StateCancelled
			inst.Checkpoint.State = StateCancelled
			_ = e.persist(inst)
			return &Error{Op: "Run", Workflow: def.Name, Err: ErrCancelled, Message: fail.err.Error()}
		}
		return e.handleStepFailure(ctx, def, inst, fail.p.step, fail.p.st, fail.res, fail.err, opts)
	}
	return nil
}

// prepareStepResume returns skip=true when the step must not re-run.
func (e *Engine) prepareStepResume(def *Definition, inst *Instance, name string, opts RunOptions) (skip bool, err error) {
	st := inst.Checkpoint.Steps[name]
	step, _ := def.Step(name)
	switch st.Status {
	case StepCompleted, StepCompensated, StepSkipped:
		return true, nil
	case StepFailed:
		// Completed-as-failed NotRetryable / missing OperationID: do not re-execute.
		if step.Idempotency == NotRetryable {
			return false, e.failResumeNotRetryable(def, inst, step, st, opts)
		}
		if step.Idempotency == RequiresOperationID && opts.OperationID == "" {
			return false, e.failResumeNotRetryable(def, inst, step, st, opts)
		}
		// With OperationID (or idempotent), clear failed to allow retry.
		st.Status = StepPending
		st.Error = ""
		inst.Checkpoint.Steps[name] = st
		return false, nil
	case StepRunning:
		// Crash mid-step: never blind-retry NotRetryable / RequiresReconciliation.
		if step.Idempotency == NotRetryable || step.Idempotency == RequiresReconciliation {
			st.Status = StepReconcile
			st.Error = "interrupted while running; requires reconciliation"
			inst.Checkpoint.Steps[name] = st
			inst.State = StateRequiresReconciliation
			inst.Checkpoint.State = StateRequiresReconciliation
			_ = e.persist(inst)
			return false, &Error{Op: "Run", Workflow: def.Name, Step: name, Err: ErrRequiresReconciliation, Message: st.Error}
		}
		if step.Idempotency == RequiresOperationID && opts.OperationID == "" {
			st.Status = StepFailed
			st.Error = "operation id required to retry interrupted step"
			inst.Checkpoint.Steps[name] = st
			return false, e.failResumeNotRetryable(def, inst, step, st, opts)
		}
		st.Status = StepPending
		inst.Checkpoint.Steps[name] = st
		return false, nil
	case StepReconcile:
		inst.State = StateRequiresReconciliation
		inst.Checkpoint.State = StateRequiresReconciliation
		_ = e.persist(inst)
		return false, &Error{Op: "Run", Workflow: def.Name, Step: name, Err: ErrRequiresReconciliation, Message: st.Error}
	default:
		return false, nil
	}
}

func (e *Engine) failResumeNotRetryable(def *Definition, inst *Instance, step *StepDef, st StepState, opts RunOptions) error {
	if cerr := e.compensate(context.Background(), def, inst, opts); cerr != nil {
		inst.State = StateRequiresIntervention
		inst.Checkpoint.State = StateRequiresIntervention
		_ = e.persist(inst)
		return cerr
	}
	inst.State = StateFailed
	inst.Checkpoint.State = StateFailed
	_ = e.persist(inst)
	return &Error{Op: "Run", Workflow: def.Name, Step: step.Name, Err: ErrNotRetryable, Message: st.Error}
}

func (e *Engine) validateCaps(step *StepDef) error {
	if e.hooks.Resources == nil {
		if step.RequiredCaps != (resource.ResourceCapabilities{}) {
			return &Error{Op: "validateCaps", Step: step.Name, Err: ErrCapability, Message: "resource lookup not configured"}
		}
		return nil
	}
	ent, err := e.hooks.Resources.Get(step.ResourceID)
	if err != nil {
		return &Error{Op: "validateCaps", Step: step.Name, Err: ErrCapability, Message: err.Error()}
	}
	if err := ent.Resource.Capabilities().Require(step.RequiredCaps); err != nil {
		return &Error{Op: "validateCaps", Step: step.Name, Err: ErrCapability, Message: err.Error()}
	}
	return nil
}

func (e *Engine) runStep(ctx context.Context, def *Definition, inst *Instance, step *StepDef, opts RunOptions) error {
	now := e.clock().UTC()
	st := inst.Checkpoint.Steps[step.Name]
	st.Name = step.Name
	st.Status = StepRunning
	st.Attempt++
	st.StartedAt = now
	inst.Checkpoint.Steps[step.Name] = st
	_ = e.persist(inst)

	var epoch resource.ResourceEpoch
	if !step.ResourceID.IsZero() && e.hooks.Resources != nil {
		if ent, err := e.hooks.Resources.Get(step.ResourceID); err == nil {
			epoch = ent.Epoch
		}
	}

	exec := &ExecContext{
		Workflow: def.Name, InstanceID: inst.ID, Step: step.Name,
		Attempt: st.Attempt, DryRun: inst.DryRun, OperationID: opts.OperationID,
		Attrs: cloneAttrs(opts.Attrs), ResourceEpoch: epoch, ResourceID: step.ResourceID,
	}

	var res Result
	var err error
	if inst.DryRun {
		res = Result{Evidence: Evidence{Time: now, Event: "dry-run", Summary: "skipped side effects"}}
	} else {
		res, err = step.Action(ctx, exec)
	}

	if err != nil || res.Ambiguous {
		return e.handleStepFailure(ctx, def, inst, step, st, res, err, opts)
	}

	st.Status = StepCompleted
	st.FinishedAt = e.clock().UTC()
	st.Evidence = append(st.Evidence, res.Evidence)
	inst.Checkpoint.Steps[step.Name] = st
	inst.Checkpoint.CompletedSteps = append(inst.Checkpoint.CompletedSteps, step.Name)
	if inst.Checkpoint.EpochAtStep == nil {
		inst.Checkpoint.EpochAtStep = map[string]uint64{}
	}
	inst.Checkpoint.EpochAtStep[step.Name] = uint64(epoch)
	e.audit(e.hooks, "workflow", "workflow.step.ok", step.Name, "completed", opts.OperationID)
	return e.persist(inst)
}

func (e *Engine) handleStepFailure(ctx context.Context, def *Definition, inst *Instance, step *StepDef, st StepState, res Result, err error, opts RunOptions) error {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if res.Ambiguous || step.Idempotency == RequiresReconciliation || errors.Is(err, ErrAmbiguous) {
		st.Status = StepReconcile
		st.Error = msg
		if res.Ambiguous {
			st.Error = "ambiguous outcome"
		}
		st.FinishedAt = e.clock().UTC()
		st.Evidence = append(st.Evidence, res.Evidence)
		inst.Checkpoint.Steps[step.Name] = st
		inst.State = StateRequiresReconciliation
		inst.Checkpoint.State = StateRequiresReconciliation
		_ = e.persist(inst)
		e.audit(e.hooks, "workflow", "workflow.step.reconcile", step.Name, "requires-reconciliation", opts.OperationID)
		return &Error{Op: "Run", Workflow: def.Name, Step: step.Name, Err: ErrRequiresReconciliation, Message: st.Error}
	}

	if step.Idempotency == NotRetryable || (step.Idempotency == RequiresOperationID && opts.OperationID == "") {
		st.Status = StepFailed
		st.Error = msg
		if step.Idempotency == RequiresOperationID && opts.OperationID == "" {
			st.Error = "operation id required for retry"
		}
		st.FinishedAt = e.clock().UTC()
		inst.Checkpoint.Steps[step.Name] = st
		if cerr := e.compensate(ctx, def, inst, opts); cerr != nil {
			inst.State = StateRequiresIntervention
			inst.Checkpoint.State = StateRequiresIntervention
			_ = e.persist(inst)
			return cerr
		}
		inst.State = StateFailed
		inst.Checkpoint.State = StateFailed
		_ = e.persist(inst)
		return &Error{Op: "Run", Workflow: def.Name, Step: step.Name, Err: ErrNotRetryable, Message: st.Error}
	}

	st.Status = StepFailed
	st.Error = msg
	st.FinishedAt = e.clock().UTC()
	st.Evidence = append(st.Evidence, res.Evidence)
	inst.Checkpoint.Steps[step.Name] = st
	if cerr := e.compensate(ctx, def, inst, opts); cerr != nil {
		inst.State = StateRequiresIntervention
		inst.Checkpoint.State = StateRequiresIntervention
		_ = e.persist(inst)
		return cerr
	}
	inst.State = StateFailed
	inst.Checkpoint.State = StateFailed
	_ = e.persist(inst)
	e.audit(e.hooks, "workflow", "workflow.step.fail", step.Name, "failed", opts.OperationID)
	return &Error{Op: "Run", Workflow: def.Name, Step: step.Name, Err: err, Message: msg}
}

func (e *Engine) compensate(ctx context.Context, def *Definition, inst *Instance, opts RunOptions) error {
	inst.State = StateCompensating
	inst.Checkpoint.State = StateCompensating
	_ = e.persist(inst)

	completed := append([]string(nil), inst.Checkpoint.CompletedSteps...)
	for i := len(completed) - 1; i >= 0; i-- {
		name := completed[i]
		step, ok := def.Step(name)
		if !ok || step.Compensate == nil {
			continue
		}
		st := inst.Checkpoint.Steps[name]
		if st.Status == StepCompensated {
			continue
		}
		if step.Mutates {
			if e.hooks.Lockdown != nil && e.hooks.Lockdown.BlocksMutations() {
				return &Error{Op: "Compensate", Workflow: def.Name, Step: name, Err: ErrLockdown, Message: "lockdown during compensate"}
			}
		}
		if !step.ResourceID.IsZero() && e.hooks.Resources != nil {
			ent, err := e.hooks.Resources.Get(step.ResourceID)
			if err == nil {
				recorded := resource.ResourceEpoch(inst.Checkpoint.EpochAtStep[name])
				if ent.Epoch > recorded {
					return &Error{Op: "Compensate", Workflow: def.Name, Step: name, Err: ErrStaleEpoch, Message: fmt.Sprintf("current epoch %d > recorded %d", ent.Epoch, recorded)}
				}
			}
		}
		st.Status = StepCompensating
		inst.Checkpoint.Steps[name] = st
		_ = e.persist(inst)

		exec := &ExecContext{
			Workflow: def.Name, InstanceID: inst.ID, Step: name,
			Attempt: st.Attempt, DryRun: inst.DryRun, OperationID: opts.OperationID,
			Attrs: cloneAttrs(opts.Attrs), ResourceID: step.ResourceID,
			ResourceEpoch: resource.ResourceEpoch(inst.Checkpoint.EpochAtStep[name]),
		}
		var res Result
		var err error
		if inst.DryRun {
			res = Result{Evidence: Evidence{Time: e.clock().UTC(), Event: "dry-run-compensate"}}
		} else {
			res, err = step.Compensate(ctx, exec)
		}
		if err != nil || res.Ambiguous {
			st.Status = StepFailed
			st.Error = "compensation failed"
			if err != nil {
				st.Error = err.Error()
			}
			inst.Checkpoint.Steps[name] = st
			e.audit(e.hooks, "workflow", "workflow.compensate", name, "failed", opts.OperationID)
			return &Error{Op: "Compensate", Workflow: def.Name, Step: name, Err: ErrCompensationFailed, Message: st.Error}
		}
		st.Status = StepCompensated
		st.FinishedAt = e.clock().UTC()
		st.Evidence = append(st.Evidence, res.Evidence)
		inst.Checkpoint.Steps[name] = st
		e.audit(e.hooks, "workflow", "workflow.compensate", name, "ok", opts.OperationID)
		_ = e.persist(inst)
	}
	return nil
}

func (e *Engine) persist(inst *Instance) error {
	inst.Checkpoint.State = inst.State
	inst.Checkpoint.UpdatedAt = e.clock().UTC()
	return e.store.Save(inst.Checkpoint)
}

func (e *Engine) audit(h Hooks, actor, action, res, result, op string) {
	if h.Audit != nil {
		h.Audit.Audit(actor, action, res, result, op)
	}
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func cloneAttrs(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
