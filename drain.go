package shiftlock

import (
	"context"
	"sync"
	"sync/atomic"
)

// DrainGroup tracks in-flight work that must complete before ownership
// transfer or shutdown. Begin/BeginNamed return a release function.
type DrainGroup struct {
	mu       sync.Mutex
	active   int
	named    map[string]int
	maxNamed int
	draining atomic.Bool
	done     chan struct{}
	deadline <-chan struct{}
	closed   atomic.Bool
}

// NewDrainGroup creates an empty drain group.
// maxNamed bounds concurrent named operations (0 = unlimited).
func NewDrainGroup(maxNamed int) *DrainGroup {
	return &DrainGroup{
		named:    make(map[string]int),
		maxNamed: maxNamed,
		done:     make(chan struct{}),
	}
}

// Begin starts an anonymous in-flight operation.
// Returns ErrDraining if drain has started, ErrClosed if closed.
func (g *DrainGroup) Begin() (func(), error) {
	return g.BeginNamed("")
}

// BeginNamed starts a named in-flight operation.
func (g *DrainGroup) BeginNamed(name string) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed.Load() {
		return nil, ErrClosed
	}
	if g.draining.Load() {
		return nil, ErrDraining
	}
	if name != "" && g.maxNamed > 0 {
		if g.named[name] >= g.maxNamed {
			return nil, &Error{Op: "drain.BeginNamed", Err: ErrPolicy, Message: "named op limit reached", Claim: name}
		}
		g.named[name]++
	}
	g.active++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.active--
			if name != "" {
				g.named[name]--
				if g.named[name] <= 0 {
					delete(g.named, name)
				}
			}
			if g.draining.Load() && g.active == 0 {
				select {
				case <-g.done:
				default:
					close(g.done)
				}
			}
		})
	}, nil
}

// Active returns the number of in-flight operations.
func (g *DrainGroup) Active() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active
}

// StartDrain marks the group as draining. New Begin calls fail with ErrDraining.
func (g *DrainGroup) StartDrain() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.draining.Swap(true) {
		return
	}
	if g.active == 0 {
		select {
		case <-g.done:
		default:
			close(g.done)
		}
	}
}

// Wait blocks until all in-flight ops complete or ctx/deadline expires.
func (g *DrainGroup) Wait(ctx context.Context) error {
	g.StartDrain()
	select {
	case <-g.done:
		return nil
	case <-ctx.Done():
		return &Error{Op: "drain.Wait", Err: ErrTimeout, Message: ctx.Err().Error()}
	case <-g.deadline:
		return &Error{Op: "drain.Wait", Err: ErrTimeout, Message: "drain deadline exceeded"}
	}
}

// SetDeadline cancels Wait when the channel closes.
func (g *DrainGroup) SetDeadline(ch <-chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.deadline = ch
}

// Close permanently rejects new work.
func (g *DrainGroup) Close() {
	g.closed.Store(true)
	g.StartDrain()
}

// Draining reports whether drain has started.
func (g *DrainGroup) Draining() bool { return g.draining.Load() }
