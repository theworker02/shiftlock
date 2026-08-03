package supervisor

import (
	"context"
	"sync"
)

// Supervisor owns background goroutines and awaits them on Shutdown.
type Supervisor struct {
	mu     sync.Mutex
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
}

// New creates a supervisor derived from parent.
func New(parent context.Context) *Supervisor {
	ctx, cancel := context.WithCancel(parent)
	return &Supervisor{ctx: ctx, cancel: cancel}
}

// Context returns the supervisor context (canceled on Shutdown).
func (s *Supervisor) Context() context.Context {
	return s.ctx
}

// Go starts a goroutine tracked by the supervisor.
// fn should exit promptly when ctx is canceled.
func (s *Supervisor) Go(fn func(ctx context.Context)) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		fn(s.ctx)
	}()
}

// GoNamed is like Go but panics are recovered (name for future diagnostics).
func (s *Supervisor) GoNamed(_ string, fn func(ctx context.Context)) {
	s.Go(func(ctx context.Context) {
		defer func() { _ = recover() }()
		fn(ctx)
	})
}

// Shutdown cancels the context and waits for all goroutines.
func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}

// Closed reports whether Shutdown has been called.
func (s *Supervisor) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
