package workflow

import (
	"context"
	"sort"

	"github.com/theworker02/shiftlock/resource"
)

// ActionFunc executes a step.
type ActionFunc func(ctx context.Context, exec *ExecContext) (Result, error)

// CompensateFunc undoes a completed step.
type CompensateFunc func(ctx context.Context, exec *ExecContext) (Result, error)

// Result is the outcome of an action or compensation.
type Result struct {
	Evidence Evidence
	// Ambiguous marks an unknown outcome — engine enters requires-reconciliation
	// instead of retrying when mode demands it.
	Ambiguous bool
}

// ExecContext is passed to actions.
type ExecContext struct {
	Workflow   string
	InstanceID string
	Step       string
	Attempt    int
	DryRun     bool
	OperationID string
	Attrs      map[string]string
	// ResourceEpoch is the epoch observed at step start (for compensation fencing).
	ResourceEpoch resource.ResourceEpoch
	ResourceID    resource.ResourceID
}

// StepDef describes one step in a definition.
type StepDef struct {
	Name              string
	Action            ActionFunc
	Compensate        CompensateFunc
	DependsOn         []string
	ParallelGroup     string
	Idempotency       IdempotencyMode
	Mutates           bool // protected mutation — blocked under lockdown
	RequiredCaps      resource.ResourceCapabilities
	ResourceID        resource.ResourceID // optional capability/epoch target
}

// Definition is an immutable workflow graph after Validate.
type Definition struct {
	Name  string
	steps map[string]*StepDef
	order []string // deterministic validation order
}

// Builder constructs a Definition.
type Builder struct {
	name  string
	steps map[string]*StepDef
	err   error
}

// Define starts a workflow definition.
func Define(name string) *Builder {
	b := &Builder{name: name, steps: make(map[string]*StepDef)}
	if name == "" {
		b.err = &Error{Op: "Define", Err: ErrInvalidDefinition, Message: "name required"}
	}
	return b
}

// Step adds or replaces a step.
func (b *Builder) Step(name string, action ActionFunc) *Builder {
	if b.err != nil {
		return b
	}
	if name == "" || action == nil {
		b.err = &Error{Op: "Step", Workflow: b.name, Err: ErrInvalidDefinition, Message: "name and action required"}
		return b
	}
	b.steps[name] = &StepDef{Name: name, Action: action, Idempotency: Idempotent}
	return b
}

// Compensate attaches a compensating action to a step.
func (b *Builder) Compensate(step string, fn CompensateFunc) *Builder {
	if b.err != nil {
		return b
	}
	s, ok := b.steps[step]
	if !ok {
		b.err = &Error{Op: "Compensate", Workflow: b.name, Step: step, Err: ErrUnknownStep, Message: "unknown step"}
		return b
	}
	s.Compensate = fn
	return b
}

// Depend declares that step depends on deps (deps run first).
func (b *Builder) Depend(step string, deps ...string) *Builder {
	if b.err != nil {
		return b
	}
	s, ok := b.steps[step]
	if !ok {
		b.err = &Error{Op: "Depend", Workflow: b.name, Step: step, Err: ErrUnknownStep, Message: "unknown step"}
		return b
	}
	s.DependsOn = append(append([]string(nil), s.DependsOn...), deps...)
	return b
}

// ParallelGroup assigns steps to a named parallel group (same group may run concurrently).
func (b *Builder) ParallelGroup(group string, steps ...string) *Builder {
	if b.err != nil {
		return b
	}
	for _, name := range steps {
		s, ok := b.steps[name]
		if !ok {
			b.err = &Error{Op: "ParallelGroup", Workflow: b.name, Step: name, Err: ErrUnknownStep, Message: "unknown step"}
			return b
		}
		s.ParallelGroup = group
	}
	return b
}

// Idempotency sets retry mode for a step.
func (b *Builder) Idempotency(step string, mode IdempotencyMode) *Builder {
	if b.err != nil {
		return b
	}
	s, ok := b.steps[step]
	if !ok {
		b.err = &Error{Op: "Idempotency", Workflow: b.name, Step: step, Err: ErrUnknownStep, Message: "unknown step"}
		return b
	}
	s.Idempotency = mode
	return b
}

// Mutating marks a step as a protected resource mutation.
func (b *Builder) Mutating(step string, mutates bool) *Builder {
	if b.err != nil {
		return b
	}
	s, ok := b.steps[step]
	if !ok {
		b.err = &Error{Op: "Mutating", Workflow: b.name, Step: step, Err: ErrUnknownStep, Message: "unknown step"}
		return b
	}
	s.Mutates = mutates
	return b
}

// RequireCaps requires resource capabilities when a ResourceID is set on the step.
func (b *Builder) RequireCaps(step string, id resource.ResourceID, caps resource.ResourceCapabilities) *Builder {
	if b.err != nil {
		return b
	}
	s, ok := b.steps[step]
	if !ok {
		b.err = &Error{Op: "RequireCaps", Workflow: b.name, Step: step, Err: ErrUnknownStep, Message: "unknown step"}
		return b
	}
	s.ResourceID = id
	s.RequiredCaps = caps
	return b
}

// Build validates and returns an immutable Definition.
func (b *Builder) Build() (*Definition, error) {
	if b.err != nil {
		return nil, b.err
	}
	if len(b.steps) == 0 {
		return nil, &Error{Op: "Build", Workflow: b.name, Err: ErrInvalidDefinition, Message: "no steps"}
	}
	for name, s := range b.steps {
		for _, d := range s.DependsOn {
			if _, ok := b.steps[d]; !ok {
				return nil, &Error{Op: "Build", Workflow: b.name, Step: name, Err: ErrUnknownStep, Message: "depends on unknown step " + d}
			}
		}
	}
	order, err := topoSteps(b.steps)
	if err != nil {
		return nil, err
	}
	cp := make(map[string]*StepDef, len(b.steps))
	for k, v := range b.steps {
		dup := *v
		dup.DependsOn = append([]string(nil), v.DependsOn...)
		cp[k] = &dup
	}
	return &Definition{Name: b.name, steps: cp, order: order}, nil
}

func topoSteps(steps map[string]*StepDef) ([]string, error) {
	indeg := make(map[string]int, len(steps))
	for name := range steps {
		indeg[name] = 0
	}
	for name, s := range steps {
		for range s.DependsOn {
			indeg[name]++
		}
	}
	ready := make([]string, 0)
	for n, d := range indeg {
		if d == 0 {
			ready = append(ready, n)
		}
	}
	sort.Strings(ready)
	order := make([]string, 0, len(steps))
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		var dependents []string
		for name, s := range steps {
			for _, d := range s.DependsOn {
				if d == n {
					dependents = append(dependents, name)
				}
			}
		}
		sort.Strings(dependents)
		for _, name := range dependents {
			indeg[name]--
			if indeg[name] == 0 {
				ready = append(ready, name)
			}
		}
		sort.Strings(ready)
	}
	if len(order) != len(steps) {
		return nil, &Error{Op: "Build", Err: ErrCycle, Message: "step dependency cycle"}
	}
	return order, nil
}

// Steps returns step names in deterministic dependency order.
func (d *Definition) Steps() []string {
	return append([]string(nil), d.order...)
}

// Step returns a step definition.
func (d *Definition) Step(name string) (*StepDef, bool) {
	s, ok := d.steps[name]
	return s, ok
}
