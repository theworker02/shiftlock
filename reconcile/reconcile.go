// Package reconcile provides bounded reconciliation controllers.
//
// Controllers pause during lockdown and never run unbounded hot loops.
package reconcile

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

var (
	ErrDuplicate = errors.New("reconcile: duplicate name")
	ErrNotFound  = errors.New("reconcile: not found")
	ErrPaused    = errors.New("reconcile: paused")
	ErrLockdown  = errors.New("reconcile: lockdown")
)

// DesiredState is an opaque desired configuration version.
type DesiredState struct {
	Version uint64
	Fields  map[string]string
}

// ActualState is an opaque observed configuration.
type ActualState struct {
	Fields map[string]string
}

// Func reconciles desired toward actual.
type Func func(ctx context.Context, desired DesiredState, actual ActualState) error

// Reconciler is a named reconciliation controller.
type Reconciler struct {
	Name      string
	Resource  resource.ResourceID
	Reconcile Func
	// MaxAttempts bounds retries per Run (0 = 3).
	MaxAttempts int
	// InitialBackoff (0 = 10ms), doubles each retry up to MaxBackoff.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// LockdownChecker mirrors resource.LockdownChecker.
type LockdownChecker interface {
	BlocksMutations() bool
}

// Registry holds reconcilers.
type Registry struct {
	mu       sync.Mutex
	items    map[string]Reconciler
	paused   bool
	lockdown LockdownChecker
	runs     uint64
}

// NewRegistry creates an empty reconciler registry.
func NewRegistry() *Registry {
	return &Registry{items: make(map[string]Reconciler)}
}

// SetLockdown wires lockdown pausing.
func (r *Registry) SetLockdown(c LockdownChecker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lockdown = c
}

// Pause stops reconciliation (e.g. operator hold).
func (r *Registry) Pause() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused = true
}

// Resume clears operator pause.
func (r *Registry) Resume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused = false
}

// Register adds a reconciler.
func (r *Registry) Register(rec Reconciler) error {
	if rec.Name == "" || rec.Reconcile == nil {
		return errors.New("reconcile: name and Reconcile required")
	}
	if rec.MaxAttempts <= 0 {
		rec.MaxAttempts = 3
	}
	if rec.InitialBackoff <= 0 {
		rec.InitialBackoff = 10 * time.Millisecond
	}
	if rec.MaxBackoff <= 0 {
		rec.MaxBackoff = 250 * time.Millisecond
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[rec.Name]; ok {
		return ErrDuplicate
	}
	r.items[rec.Name] = rec
	return nil
}

// Run executes one reconciler with bounded retry/backoff.
func (r *Registry) Run(ctx context.Context, name string, desired DesiredState, actual ActualState) error {
	r.mu.Lock()
	if r.paused {
		r.mu.Unlock()
		return ErrPaused
	}
	if r.lockdown != nil && r.lockdown.BlocksMutations() {
		r.mu.Unlock()
		return ErrLockdown
	}
	rec, ok := r.items[name]
	r.mu.Unlock()
	if !ok {
		return ErrNotFound
	}

	var last error
	backoff := rec.InitialBackoff
	for attempt := 1; attempt <= rec.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.mu.Lock()
		if r.paused {
			r.mu.Unlock()
			return ErrPaused
		}
		if r.lockdown != nil && r.lockdown.BlocksMutations() {
			r.mu.Unlock()
			return ErrLockdown
		}
		r.runs++
		r.mu.Unlock()

		last = rec.Reconcile(ctx, desired, actual)
		if last == nil {
			return nil
		}
		if attempt == rec.MaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > rec.MaxBackoff {
			backoff = rec.MaxBackoff
		}
	}
	return last
}

// Runs returns total reconcile invocations.
func (r *Registry) Runs() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs
}
