// Package migration coordinates multi-resource data migration lifecycles.
//
// Cutover verification steps are application-supplied hooks — this package
// never executes arbitrary SQL or opaque scripts. Ownership and fencing are
// soft hooks so adapters remain honest about what they can enforce.
package migration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

var (
	ErrNotFound      = errors.New("migration: not found")
	ErrDuplicate     = errors.New("migration: duplicate")
	ErrInvalidPhase  = errors.New("migration: invalid phase transition")
	ErrInvalidArg    = errors.New("migration: invalid argument")
	ErrPaused        = errors.New("migration: paused")
	ErrLockdown      = errors.New("migration: lockdown blocks mutation")
	ErrFence         = errors.New("migration: fencing check failed")
	ErrOwnership     = errors.New("migration: ownership required")
	ErrDryRun        = errors.New("migration: dry-run cannot mutate")
	ErrVerifyFailed  = errors.New("migration: cutover verification failed")
	ErrBoundExceeded = errors.New("migration: bound exceeded")
)

// DefaultMaxMigrations bounds tracked definitions.
const DefaultMaxMigrations = 256

// Phase is a migration lifecycle stage.
type Phase string

const (
	PhasePlanned      Phase = "planned"
	PhaseValidated    Phase = "validated"
	PhasePreparing    Phase = "preparing"
	PhaseCopying      Phase = "copying"
	PhaseVerifying    Phase = "verifying"
	PhaseCutover      Phase = "cutover"
	PhasePaused       Phase = "paused"
	PhaseCompensating Phase = "compensating"
	PhaseCompleted    Phase = "completed"
	PhaseFailed       Phase = "failed"
)

// Terminal reports whether p is terminal.
func (p Phase) Terminal() bool {
	return p == PhaseCompleted || p == PhaseFailed
}

// VerifyStep is an app-supplied cutover check (no SQL execution here).
type VerifyStep func(ctx context.Context, run *Run) error

// CutoverHook runs during cutover (app-owned switch / dual-write flip).
type CutoverHook func(ctx context.Context, run *Run) error

// OwnershipGate is a soft ownership check (e.g. lease held).
type OwnershipGate interface {
	Owns(resourceID resource.ResourceID, owner string) bool
}

// FenceGate is a soft fencing check.
type FenceGate interface {
	CheckFence(resourceID resource.ResourceID, fence uint64) error
}

// LockdownGate blocks mutating phase advances when active.
type LockdownGate interface {
	BlocksMutations() bool
}

// Hooks wires soft Runtime/adapter integrations.
type Hooks struct {
	Ownership OwnershipGate
	Fence     FenceGate
	Lockdown  LockdownGate
}

// Checkpoint is a pause/resume snapshot (no secrets).
type Checkpoint struct {
	Name       string            `json:"name"`
	Phase      Phase             `json:"phase"`
	ResumeFrom Phase             `json:"resume_from,omitempty"`
	Copied     int64             `json:"copied,omitempty"`
	Total      int64             `json:"total,omitempty"`
	Percent    float64           `json:"percent,omitempty"`
	Attrs      map[string]string `json:"attrs,omitempty"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// Progress is a sanitized progress view.
type Progress struct {
	Name      string    `json:"name"`
	Phase     Phase     `json:"phase"`
	Copied    int64     `json:"copied,omitempty"`
	Total     int64     `json:"total,omitempty"`
	Percent   float64   `json:"percent,omitempty"`
	DryRun    bool      `json:"dry_run,omitempty"`
	Message   string    `json:"message,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Definition is a registered migration plan.
type Definition struct {
	Name        string
	Source      string
	Target      string
	Owner       string
	Fence       uint64
	SourceID    resource.ResourceID
	TargetID    resource.ResourceID
	VerifySteps []VerifyStep
	Cutover     CutoverHook
	MaxBytes    int64 // optional budget hint recorded in progress attrs only
}

// Run is a live or paused migration instance.
type Run struct {
	Name       string
	Source     string
	Target     string
	Phase      Phase
	DryRun     bool
	Owner      string
	Fence      uint64
	SourceID   resource.ResourceID
	TargetID   resource.ResourceID
	Copied     int64
	Total      int64
	Message    string
	Checkpoint Checkpoint
	UpdatedAt  time.Time
}

// Coordinator tracks and advances migrations with ownership/fencing hooks.
type Coordinator struct {
	mu    sync.Mutex
	defs  map[string]*Definition
	runs  map[string]*Run
	hooks Hooks
	max   int
	clock func() time.Time
}

// Config configures a Coordinator.
type Config struct {
	Hooks        Hooks
	MaxMigrations int
	Clock        func() time.Time
}

// NewCoordinator creates an empty coordinator.
func NewCoordinator() *Coordinator {
	return New(Config{})
}

// New creates a coordinator with config.
func New(cfg Config) *Coordinator {
	if cfg.MaxMigrations <= 0 {
		cfg.MaxMigrations = DefaultMaxMigrations
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Coordinator{
		defs:  make(map[string]*Definition),
		runs:  make(map[string]*Run),
		hooks: cfg.Hooks,
		max:   cfg.MaxMigrations,
		clock: cfg.Clock,
	}
}

// SetHooks updates soft integrations.
func (c *Coordinator) SetHooks(h Hooks) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hooks = h
}

// DefineSimple registers a migration by name/source/target (planned).
func (c *Coordinator) DefineSimple(name, source, target string) error {
	return c.Define(Definition{Name: name, Source: source, Target: target})
}

// Define registers a migration in planned phase.
func (c *Coordinator) Define(def Definition) error {
	if def.Name == "" || def.Source == "" || def.Target == "" {
		return ErrInvalidArg
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.defs) >= c.max {
		return ErrBoundExceeded
	}
	if _, ok := c.defs[def.Name]; ok {
		return ErrDuplicate
	}
	cp := def
	c.defs[def.Name] = &cp
	c.runs[def.Name] = &Run{
		Name: def.Name, Source: def.Source, Target: def.Target,
		Phase: PhasePlanned, Owner: def.Owner, Fence: def.Fence,
		SourceID: def.SourceID, TargetID: def.TargetID,
		UpdatedAt: c.clock().UTC(),
		Checkpoint: Checkpoint{Name: def.Name, Phase: PhasePlanned, UpdatedAt: c.clock().UTC()},
	}
	return nil
}

// List returns sanitized progress for all runs.
func (c *Coordinator) List() []Progress {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Progress, 0, len(c.runs))
	for _, r := range c.runs {
		out = append(out, progressOf(r))
	}
	return out
}

// Get returns a copy of the run.
func (c *Coordinator) Get(name string) (Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.runs[name]
	if !ok {
		return Run{}, ErrNotFound
	}
	return cloneRun(r), nil
}

// ProgressOf returns sanitized progress.
func (c *Coordinator) ProgressOf(name string) (Progress, error) {
	r, err := c.Get(name)
	if err != nil {
		return Progress{}, err
	}
	return progressOf(&r), nil
}

// Start begins advancing from planned (or resumes from paused checkpoint).
func (c *Coordinator) Start(ctx context.Context, name string, opts StartOptions) (Progress, error) {
	c.mu.Lock()
	def, ok := c.defs[name]
	if !ok {
		c.mu.Unlock()
		return Progress{}, ErrNotFound
	}
	run := c.runs[name]
	if run.Phase.Terminal() {
		c.mu.Unlock()
		return Progress{}, ErrInvalidPhase
	}
	if opts.DryRun {
		run.DryRun = true
	}
	if run.Phase == PhasePaused {
		c.mu.Unlock()
		return c.Resume(ctx, name)
	}
	if run.Phase == PhaseFailed || run.Phase == PhaseCompensating {
		c.mu.Unlock()
		return Progress{}, ErrInvalidPhase
	}
	hooks := c.hooks
	inclusive := opts.resume
	c.mu.Unlock()

	if err := c.checkGates(hooks, def, true); err != nil {
		return Progress{}, err
	}

	phases := []Phase{PhaseValidated, PhasePreparing, PhaseCopying, PhaseVerifying, PhaseCutover, PhaseCompleted}
	startIdx := 0
	c.mu.Lock()
	cur := c.runs[name].Phase
	c.mu.Unlock()
	if cur == PhasePlanned {
		startIdx = 0
	} else {
		for i, p := range phases {
			if cur == p {
				if inclusive {
					startIdx = i
				} else {
					startIdx = i + 1
				}
				break
			}
		}
	}

	for _, p := range phases[startIdx:] {
		if err := ctx.Err(); err != nil {
			_ = c.fail(name, err.Error())
			return Progress{}, err
		}
		c.mu.Lock()
		if c.runs[name].Phase == PhasePaused {
			prog := progressOf(c.runs[name])
			c.mu.Unlock()
			return prog, ErrPaused
		}
		c.mu.Unlock()

		if err := c.advanceTo(ctx, name, p, opts); err != nil {
			if errors.Is(err, ErrPaused) {
				prog, _ := c.ProgressOf(name)
				return prog, err
			}
			_ = c.fail(name, err.Error())
			return Progress{}, err
		}
	}
	return c.ProgressOf(name)
}

// StartOptions configures Start.
type StartOptions struct {
	DryRun bool
	// Total optional expected units for progress percent.
	Total int64
	// CopiedSeed seeds copied counter (resume).
	CopiedSeed int64
	// PauseAfter advances until this phase then pauses (empty = run through).
	PauseAfter Phase
	// resume re-enters the current phase (set by Resume).
	resume bool
}

func (c *Coordinator) advanceTo(ctx context.Context, name string, target Phase, opts StartOptions) error {
	c.mu.Lock()
	def := c.defs[name]
	run := c.runs[name]
	hooks := c.hooks
	dry := run.DryRun || opts.DryRun
	c.mu.Unlock()

	mutating := target == PhasePreparing || target == PhaseCopying || target == PhaseCutover
	if err := c.checkGates(hooks, def, mutating); err != nil {
		return err
	}
	if dry && (target == PhaseCutover) {
		// Dry-run validates up through verifying; cutover is recorded as skipped.
		c.mu.Lock()
		run = c.runs[name]
		run.Phase = PhaseCompleted
		run.Message = "dry-run: cutover skipped"
		run.UpdatedAt = c.clock().UTC()
		run.Checkpoint = Checkpoint{
			Name: name, Phase: PhaseCompleted, UpdatedAt: run.UpdatedAt,
			Copied: run.Copied, Total: run.Total, Percent: percent(run.Copied, run.Total),
			Attrs: map[string]string{"dry_run": "true", "cutover": "skipped"},
		}
		c.mu.Unlock()
		return nil
	}

	switch target {
	case PhaseValidated:
		// no-op validate gate already passed
	case PhasePreparing:
		// ownership already checked
	case PhaseCopying:
		c.mu.Lock()
		if opts.Total > 0 {
			c.runs[name].Total = opts.Total
		}
		if opts.CopiedSeed > 0 && c.runs[name].Copied == 0 {
			c.runs[name].Copied = opts.CopiedSeed
		}
		c.mu.Unlock()
	case PhaseVerifying:
		for i, step := range def.VerifySteps {
			if step == nil {
				continue
			}
			c.mu.Lock()
			runCopy := cloneRun(c.runs[name])
			c.mu.Unlock()
			if err := step(ctx, &runCopy); err != nil {
				return fmt.Errorf("%w: step %d: %v", ErrVerifyFailed, i, err)
			}
		}
	case PhaseCutover:
		if def.Cutover != nil {
			c.mu.Lock()
			runCopy := cloneRun(c.runs[name])
			c.mu.Unlock()
			if err := def.Cutover(ctx, &runCopy); err != nil {
				return err
			}
		}
	case PhaseCompleted:
		// fallthrough set below
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	run = c.runs[name]
	run.Phase = target
	run.UpdatedAt = c.clock().UTC()
	run.Checkpoint = Checkpoint{
		Name: name, Phase: target, UpdatedAt: run.UpdatedAt,
		Copied: run.Copied, Total: run.Total, Percent: percent(run.Copied, run.Total),
	}
	if opts.PauseAfter != "" && target == opts.PauseAfter && !target.Terminal() {
		run.Checkpoint.ResumeFrom = target
		run.Phase = PhasePaused
		run.Checkpoint.Phase = PhasePaused
		run.Message = "paused after " + string(target)
		return ErrPaused
	}
	return nil
}

// ReportCopy updates copy progress (sanitized counters only).
func (c *Coordinator) ReportCopy(name string, copied, total int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	run, ok := c.runs[name]
	if !ok {
		return ErrNotFound
	}
	if run.Phase != PhaseCopying && run.Phase != PhasePaused {
		return ErrInvalidPhase
	}
	run.Copied = copied
	if total > 0 {
		run.Total = total
	}
	run.UpdatedAt = c.clock().UTC()
	run.Checkpoint.Copied = run.Copied
	run.Checkpoint.Total = run.Total
	run.Checkpoint.Percent = percent(run.Copied, run.Total)
	run.Checkpoint.UpdatedAt = run.UpdatedAt
	return nil
}

// Pause checkpoints and enters paused phase.
func (c *Coordinator) Pause(name, reason string) (Progress, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	run, ok := c.runs[name]
	if !ok {
		return Progress{}, ErrNotFound
	}
	if run.Phase.Terminal() || run.Phase == PhasePaused {
		return Progress{}, ErrInvalidPhase
	}
	run.Checkpoint.ResumeFrom = run.Phase
	run.Checkpoint.Phase = PhasePaused
	run.Checkpoint.UpdatedAt = c.clock().UTC()
	run.Phase = PhasePaused
	run.Message = reason
	run.UpdatedAt = run.Checkpoint.UpdatedAt
	return progressOf(run), nil
}

// Resume continues from a paused checkpoint.
func (c *Coordinator) Resume(ctx context.Context, name string) (Progress, error) {
	c.mu.Lock()
	run, ok := c.runs[name]
	if !ok {
		c.mu.Unlock()
		return Progress{}, ErrNotFound
	}
	if run.Phase != PhasePaused {
		c.mu.Unlock()
		return Progress{}, ErrInvalidPhase
	}
	from := run.Checkpoint.ResumeFrom
	if from == "" {
		from = PhaseCopying
	}
	run.Phase = from
	run.Message = "resumed"
	run.UpdatedAt = c.clock().UTC()
	dry := run.DryRun
	total := run.Total
	copied := run.Copied
	c.mu.Unlock()
	return c.Start(ctx, name, StartOptions{DryRun: dry, Total: total, CopiedSeed: copied, resume: true})
}

// Compensate enters compensating then failed (app hooks may extend later).
func (c *Coordinator) Compensate(name, reason string) (Progress, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	run, ok := c.runs[name]
	if !ok {
		return Progress{}, ErrNotFound
	}
	if run.Phase == PhaseCompleted {
		return Progress{}, ErrInvalidPhase
	}
	run.Phase = PhaseCompensating
	run.Message = reason
	run.UpdatedAt = c.clock().UTC()
	run.Phase = PhaseFailed
	run.Checkpoint.Phase = PhaseFailed
	run.Checkpoint.UpdatedAt = run.UpdatedAt
	return progressOf(run), nil
}

// SetPhase is a low-level phase update for tests/operators (validates transition).
func (c *Coordinator) SetPhase(name string, phase Phase) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	run, ok := c.runs[name]
	if !ok {
		return ErrNotFound
	}
	if !allowedTransition(run.Phase, phase) {
		return ErrInvalidPhase
	}
	run.Phase = phase
	run.UpdatedAt = c.clock().UTC()
	run.Checkpoint.Phase = phase
	run.Checkpoint.UpdatedAt = run.UpdatedAt
	return nil
}

func (c *Coordinator) fail(name, msg string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	run, ok := c.runs[name]
	if !ok {
		return ErrNotFound
	}
	run.Phase = PhaseFailed
	run.Message = msg
	run.UpdatedAt = c.clock().UTC()
	run.Checkpoint.Phase = PhaseFailed
	run.Checkpoint.UpdatedAt = run.UpdatedAt
	return nil
}

func (c *Coordinator) checkGates(hooks Hooks, def *Definition, mutating bool) error {
	if mutating && hooks.Lockdown != nil && hooks.Lockdown.BlocksMutations() {
		return ErrLockdown
	}
	if def.Owner != "" && !def.SourceID.IsZero() && hooks.Ownership != nil {
		if !hooks.Ownership.Owns(def.SourceID, def.Owner) {
			return ErrOwnership
		}
	}
	if def.Fence != 0 && !def.SourceID.IsZero() && hooks.Fence != nil {
		if err := hooks.Fence.CheckFence(def.SourceID, def.Fence); err != nil {
			return fmt.Errorf("%w: %v", ErrFence, err)
		}
	}
	return nil
}

func allowedTransition(from, to Phase) bool {
	if from == to {
		return true
	}
	if to == PhaseFailed || to == PhasePaused || to == PhaseCompensating {
		return !from.Terminal()
	}
	order := []Phase{PhasePlanned, PhaseValidated, PhasePreparing, PhaseCopying, PhaseVerifying, PhaseCutover, PhaseCompleted}
	fi, ti := -1, -1
	for i, p := range order {
		if p == from {
			fi = i
		}
		if p == to {
			ti = i
		}
	}
	if fi < 0 || ti < 0 {
		// resume from paused into prior phase
		if from == PhasePaused {
			return true
		}
		return false
	}
	return ti == fi+1 || (from == PhasePaused)
}

func percent(copied, total int64) float64 {
	if total <= 0 {
		return 0
	}
	p := float64(copied) / float64(total) * 100
	if p > 100 {
		return 100
	}
	if p < 0 {
		return 0
	}
	return p
}

func progressOf(r *Run) Progress {
	return Progress{
		Name: r.Name, Phase: r.Phase, Copied: r.Copied, Total: r.Total,
		Percent: percent(r.Copied, r.Total), DryRun: r.DryRun,
		Message: r.Message, UpdatedAt: r.UpdatedAt,
	}
}

func cloneRun(r *Run) Run {
	out := *r
	out.Checkpoint.Attrs = copyAttrs(r.Checkpoint.Attrs)
	return out
}

func copyAttrs(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
