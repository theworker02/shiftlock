// Package budget provides operation budgets with stop/pause/degrade behaviors.
package budget

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrExhausted = errors.New("budget: exhausted")
	ErrPaused    = errors.New("budget: paused")
	ErrClosed    = errors.New("budget: closed")
)

// Behavior selects what happens when a budget limit is hit.
type Behavior string

const (
	BehaviorStop    Behavior = "stop"
	BehaviorPause   Behavior = "pause"
	BehaviorDegrade Behavior = "degrade"
)

// Config defines budget limits.
type Config struct {
	Name        string
	MaxBytes    int64
	MaxDuration time.Duration
	MaxRetries  int
	OnExhausted Behavior
}

// State is a sanitized snapshot of budget usage.
type State struct {
	Name      string   `json:"name"`
	Bytes     int64    `json:"bytes"`
	MaxBytes  int64    `json:"max_bytes,omitempty"`
	Retries   int      `json:"retries"`
	MaxRetries int     `json:"max_retries,omitempty"`
	Elapsed   string   `json:"elapsed,omitempty"`
	MaxDuration string `json:"max_duration,omitempty"`
	Paused    bool     `json:"paused"`
	Degraded  bool     `json:"degraded"`
	Exhausted bool     `json:"exhausted"`
	Behavior  Behavior `json:"behavior"`
}

// Budget tracks usage against configured limits.
type Budget struct {
	cfg Config

	mu        sync.Mutex
	bytes     int64
	retries   int
	started   time.Time
	paused    bool
	degraded  bool
	exhausted bool
	closed    bool
}

// New creates a budget. Zero limits mean unlimited for that dimension.
func New(cfg Config) *Budget {
	if cfg.OnExhausted == "" {
		cfg.OnExhausted = BehaviorStop
	}
	return &Budget{cfg: cfg, started: time.Now().UTC()}
}

// Allow checks whether work may proceed. Returns ErrPaused/ErrExhausted when blocked.
func (b *Budget) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	if b.paused {
		return ErrPaused
	}
	if b.exhausted && b.cfg.OnExhausted == BehaviorStop {
		return ErrExhausted
	}
	if err := b.checkLocked(); err != nil {
		return b.applyExhaustedLocked(err)
	}
	return nil
}

// AddBytes records transferred/processed bytes.
func (b *Budget) AddBytes(n int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	if n < 0 {
		n = 0
	}
	b.bytes += n
	if err := b.checkLocked(); err != nil {
		return b.applyExhaustedLocked(err)
	}
	return nil
}

// AddRetry records a retry attempt.
func (b *Budget) AddRetry() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	b.retries++
	if err := b.checkLocked(); err != nil {
		return b.applyExhaustedLocked(err)
	}
	return nil
}

// Resume clears pause state (does not reset counters).
func (b *Budget) Resume() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.paused = false
}

// IsDegraded reports degrade behavior is active.
func (b *Budget) IsDegraded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.degraded
}

// IsPaused reports pause state.
func (b *Budget) IsPaused() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.paused
}

// Snapshot returns sanitized usage (no secrets).
func (b *Budget) Snapshot() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	elapsed := time.Since(b.started)
	st := State{
		Name: b.cfg.Name, Bytes: b.bytes, MaxBytes: b.cfg.MaxBytes,
		Retries: b.retries, MaxRetries: b.cfg.MaxRetries,
		Elapsed: elapsed.String(), Paused: b.paused, Degraded: b.degraded,
		Exhausted: b.exhausted, Behavior: b.cfg.OnExhausted,
	}
	if b.cfg.MaxDuration > 0 {
		st.MaxDuration = b.cfg.MaxDuration.String()
	}
	return st
}

// Close prevents further use.
func (b *Budget) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
}

func (b *Budget) checkLocked() error {
	if b.cfg.MaxBytes > 0 && b.bytes > b.cfg.MaxBytes {
		return ErrExhausted
	}
	if b.cfg.MaxRetries > 0 && b.retries > b.cfg.MaxRetries {
		return ErrExhausted
	}
	if b.cfg.MaxDuration > 0 && time.Since(b.started) > b.cfg.MaxDuration {
		return ErrExhausted
	}
	return nil
}

func (b *Budget) applyExhaustedLocked(err error) error {
	b.exhausted = true
	switch b.cfg.OnExhausted {
	case BehaviorPause:
		b.paused = true
		return ErrPaused
	case BehaviorDegrade:
		b.degraded = true
		return nil // allow degraded continuation
	default:
		return err
	}
}
