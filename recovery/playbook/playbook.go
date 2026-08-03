// Package playbook provides versioned recovery playbooks with validate and dry-run.
package playbook

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrNotFound     = errors.New("playbook: not found")
	ErrDuplicate    = errors.New("playbook: duplicate")
	ErrInvalid      = errors.New("playbook: invalid")
	ErrBoundExceeded = errors.New("playbook: bound exceeded")
	ErrValidation   = errors.New("playbook: validation failed")
)

// DefaultMaxPlaybooks bounds registered playbooks.
const DefaultMaxPlaybooks = 128

// Step is one versioned recovery action (description only — no auto-destructive exec).
type Step struct {
	ID          string
	Title       string
	Description string
	Destructive bool
	Validate    func(ctx context.Context) error
	DryRun      func(ctx context.Context) error
	Execute     func(ctx context.Context) error
}

// Playbook is a named, versioned recovery procedure.
type Playbook struct {
	Name    string
	Version string
	Summary string
	Steps   []Step
}

// Result is a sanitized run report.
type Result struct {
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	DryRun    bool      `json:"dry_run"`
	Validated bool      `json:"validated"`
	StepsOK   int       `json:"steps_ok"`
	Failed    string    `json:"failed_step,omitempty"`
	Message   string    `json:"message,omitempty"`
	At        time.Time `json:"at"`
}

// Registry holds playbooks.
type Registry struct {
	mu   sync.Mutex
	book map[string]Playbook
	max  int
}

// NewRegistry creates an empty playbook registry.
func NewRegistry(max int) *Registry {
	if max <= 0 {
		max = DefaultMaxPlaybooks
	}
	return &Registry{book: make(map[string]Playbook), max: max}
}

// Register adds a playbook.
func (r *Registry) Register(pb Playbook) error {
	if pb.Name == "" || pb.Version == "" || len(pb.Steps) == 0 {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.book) >= r.max {
		return ErrBoundExceeded
	}
	key := pb.Name + "@" + pb.Version
	if _, ok := r.book[key]; ok {
		return ErrDuplicate
	}
	r.book[key] = clone(pb)
	return nil
}

// Get returns a playbook by name and version.
func (r *Registry) Get(name, version string) (Playbook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pb, ok := r.book[name+"@"+version]
	if !ok {
		return Playbook{}, ErrNotFound
	}
	return clone(pb), nil
}

// List returns name@version keys.
func (r *Registry) List() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.book))
	for k := range r.book {
		out = append(out, k)
	}
	return out
}

// RunOptions configures Validate/DryRun/Execute.
type RunOptions struct {
	DryRun  bool
	Confirm bool // required when any step is Destructive and not DryRun
}

// Run validates then dry-runs or executes steps in order.
func (r *Registry) Run(ctx context.Context, name, version string, opts RunOptions) (Result, error) {
	pb, err := r.Get(name, version)
	if err != nil {
		return Result{}, err
	}
	res := Result{Name: pb.Name, Version: pb.Version, DryRun: opts.DryRun, At: time.Now().UTC()}

	for _, st := range pb.Steps {
		if st.Validate != nil {
			if err := st.Validate(ctx); err != nil {
				res.Failed = st.ID
				res.Message = err.Error()
				return res, fmt.Errorf("%w: %s: %v", ErrValidation, st.ID, err)
			}
		}
	}
	res.Validated = true

	destructive := false
	for _, st := range pb.Steps {
		if st.Destructive {
			destructive = true
			break
		}
	}
	if destructive && !opts.DryRun && !opts.Confirm {
		res.Message = "destructive steps require confirm"
		return res, ErrInvalid
	}

	for _, st := range pb.Steps {
		if err := ctx.Err(); err != nil {
			res.Failed = st.ID
			return res, err
		}
		if opts.DryRun {
			if st.DryRun != nil {
				if err := st.DryRun(ctx); err != nil {
					res.Failed = st.ID
					res.Message = err.Error()
					return res, err
				}
			}
		} else if st.Execute != nil {
			if err := st.Execute(ctx); err != nil {
				res.Failed = st.ID
				res.Message = err.Error()
				return res, err
			}
		}
		res.StepsOK++
	}
	return res, nil
}

func clone(pb Playbook) Playbook {
	out := pb
	out.Steps = append([]Step(nil), pb.Steps...)
	return out
}
