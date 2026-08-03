// Package syncprim provides Semaphore and Once built on fenced claim primitives.
package syncprim

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrUnavailable = errors.New("syncprim: unavailable")
	ErrNotHeld     = errors.New("syncprim: not held")
)

// FencedClaims acquires named claims with fencing tokens.
type FencedClaims interface {
	Acquire(ctx context.Context, claim string) (token uint64, err error)
	Release(ctx context.Context, claim string, token uint64) error
}

// Semaphore bounds concurrent holders using one claim name per permit slot.
type Semaphore struct {
	claims FencedClaims
	prefix string
	slots  int

	mu    sync.Mutex
	held  map[int]uint64 // slot -> token
}

// NewSemaphore creates a semaphore with n permits (n > 0, bounded).
func NewSemaphore(claims FencedClaims, namePrefix string, n int) (*Semaphore, error) {
	if claims == nil || namePrefix == "" || n <= 0 {
		return nil, errors.New("syncprim: claims, prefix, and n>0 required")
	}
	if n > 1024 {
		return nil, errors.New("syncprim: n exceeds bound 1024")
	}
	return &Semaphore{
		claims: claims,
		prefix: namePrefix,
		slots:  n,
		held:   make(map[int]uint64),
	}, nil
}

// Acquire obtains one permit (fencing per permit).
func (s *Semaphore) Acquire(ctx context.Context) (release func(context.Context) error, err error) {
	for i := 0; i < s.slots; i++ {
		name := fmt.Sprintf("%s#%d", s.prefix, i)
		tok, err := s.claims.Acquire(ctx, name)
		if err != nil {
			continue
		}
		s.mu.Lock()
		s.held[i] = tok
		s.mu.Unlock()
		slot := i
		return func(ctx context.Context) error {
			s.mu.Lock()
			tok, ok := s.held[slot]
			if !ok {
				s.mu.Unlock()
				return ErrNotHeld
			}
			delete(s.held, slot)
			s.mu.Unlock()
			return s.claims.Release(ctx, fmt.Sprintf("%s#%d", s.prefix, slot), tok)
		}, nil
	}
	return nil, ErrUnavailable
}

// Once ensures a single fenced execution of fn across participants.
type Once struct {
	claims FencedClaims
	name   string

	mu   sync.Mutex
	done bool
}

// NewOnce creates a claim-backed Once.
func NewOnce(claims FencedClaims, claimName string) (*Once, error) {
	if claims == nil || claimName == "" {
		return nil, errors.New("syncprim: claims and name required")
	}
	return &Once{claims: claims, name: claimName}, nil
}

// Do acquires the claim, runs fn once locally if acquired, then releases.
// If the claim is held elsewhere, returns ErrUnavailable without running fn.
func (o *Once) Do(ctx context.Context, fn func(ctx context.Context, token uint64) error) error {
	o.mu.Lock()
	if o.done {
		o.mu.Unlock()
		return nil
	}
	o.mu.Unlock()

	tok, err := o.claims.Acquire(ctx, o.name)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = o.claims.Release(context.Background(), o.name, tok) }()

	o.mu.Lock()
	if o.done {
		o.mu.Unlock()
		return nil
	}
	o.mu.Unlock()

	if err := fn(ctx, tok); err != nil {
		return err
	}
	o.mu.Lock()
	o.done = true
	o.mu.Unlock()
	return nil
}
