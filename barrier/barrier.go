// Package barrier coordinates participants with epoch isolation and bounds.
package barrier

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrClosed      = errors.New("barrier: closed")
	ErrFull        = errors.New("barrier: participant bound exceeded")
	ErrEpoch       = errors.New("barrier: epoch mismatch")
	ErrDuplicate   = errors.New("barrier: duplicate participant")
	ErrPolicy      = errors.New("barrier: policy not satisfied")
)

// Policy selects release conditions.
type Policy string

const (
	PolicyMinimumCount Policy = "minimum-count"
	PolicyQuorum       Policy = "quorum" // majority of MaxParticipants
	PolicyExact        Policy = "exact"
	PolicyAll          Policy = "all" // == MaxParticipants
)

// Config configures a Barrier.
type Config struct {
	Name            string
	Epoch           uint64
	MaxParticipants int // hard bound; required > 0
	Policy          Policy
	MinimumCount    int // for minimum-count
	Clock           func() time.Time
}

// Barrier is an epoch-scoped rendezvous.
type Barrier struct {
	cfg Config

	mu           sync.Mutex
	participants map[string]struct{}
	released     bool
	closed       bool
	waiters      []chan struct{}
}

// New creates a Barrier.
func New(cfg Config) (*Barrier, error) {
	if cfg.MaxParticipants <= 0 {
		return nil, errors.New("barrier: MaxParticipants required")
	}
	if cfg.Policy == "" {
		cfg.Policy = PolicyMinimumCount
	}
	if cfg.MinimumCount <= 0 {
		cfg.MinimumCount = cfg.MaxParticipants
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Barrier{
		cfg:          cfg,
		participants: make(map[string]struct{}),
	}, nil
}

// Arrive registers participant identity for this epoch and may release waiters.
func (b *Barrier) Arrive(participantID string, epoch uint64) error {
	if participantID == "" {
		return ErrPolicy
	}
	if epoch != b.cfg.Epoch {
		return ErrEpoch
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	if _, ok := b.participants[participantID]; ok {
		return ErrDuplicate
	}
	if len(b.participants) >= b.cfg.MaxParticipants {
		return ErrFull
	}
	b.participants[participantID] = struct{}{}
	if b.satisfiedLocked() {
		b.releaseLocked()
	}
	return nil
}

// Wait blocks until the policy is satisfied or ctx ends.
func (b *Barrier) Wait(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	if b.released {
		b.mu.Unlock()
		return nil
	}
	// Bound waiters to MaxParticipants so Wait cannot grow without limit.
	if len(b.waiters) >= b.cfg.MaxParticipants {
		b.mu.Unlock()
		return ErrFull
	}
	ch := make(chan struct{})
	b.waiters = append(b.waiters, ch)
	b.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		return nil
	}
}

// Count returns current participants.
func (b *Barrier) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.participants)
}

// Released reports whether the barrier has released.
func (b *Barrier) Released() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.released
}

// Close fails the barrier and wakes waiters.
func (b *Barrier) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	b.releaseLocked()
	return nil
}

func (b *Barrier) satisfiedLocked() bool {
	n := len(b.participants)
	switch b.cfg.Policy {
	case PolicyQuorum:
		need := b.cfg.MaxParticipants/2 + 1
		return n >= need
	case PolicyExact:
		return n == b.cfg.MinimumCount
	case PolicyAll:
		return n >= b.cfg.MaxParticipants
	default: // minimum-count
		return n >= b.cfg.MinimumCount
	}
}

func (b *Barrier) releaseLocked() {
	b.released = true
	for _, ch := range b.waiters {
		close(ch)
	}
	b.waiters = nil
}
