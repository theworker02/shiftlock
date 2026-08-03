// Package dualwrite provides an app-supplied dual-write migration helper.
// Callers supply write funcs for primary and secondary; this package never
// executes SQL or opaque scripts.
package dualwrite

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidArg = errors.New("dualwrite: invalid argument")
	ErrPrimary    = errors.New("dualwrite: primary write failed")
	ErrSecondary  = errors.New("dualwrite: secondary write failed")
	ErrClosed     = errors.New("dualwrite: closed")
)

// WriteFunc applies a mutation for one store. Implementations must be
// idempotent when OperationID is non-empty.
type WriteFunc func(ctx context.Context, key, value, operationID string) error

// Mode selects write ordering.
type Mode string

const (
	// ModePrimaryFirst writes primary then secondary (default cutover shadow).
	ModePrimaryFirst Mode = "primary-first"
	// ModeSecondaryFirst writes secondary then primary (prepare flip).
	ModeSecondaryFirst Mode = "secondary-first"
	// ModePrimaryOnly disables secondary (pre-dual-write or post-cutover).
	ModePrimaryOnly Mode = "primary-only"
)

// Helper dual-writes with optional secondary failure policy.
type Helper struct {
	mu        sync.Mutex
	primary   WriteFunc
	secondary WriteFunc
	mode      Mode
	// FailOpenSecondary records secondary errors without failing the call.
	FailOpenSecondary bool
	closed            bool
	secondaryErrors   int
}

// Config configures a Helper.
type Config struct {
	Primary           WriteFunc
	Secondary         WriteFunc
	Mode              Mode
	FailOpenSecondary bool
}

// New creates a dual-write helper.
func New(cfg Config) (*Helper, error) {
	if cfg.Primary == nil {
		return nil, ErrInvalidArg
	}
	if cfg.Mode == "" {
		cfg.Mode = ModePrimaryFirst
	}
	switch cfg.Mode {
	case ModePrimaryFirst, ModeSecondaryFirst, ModePrimaryOnly:
	default:
		return nil, ErrInvalidArg
	}
	if cfg.Mode != ModePrimaryOnly && cfg.Secondary == nil {
		return nil, ErrInvalidArg
	}
	return &Helper{
		primary: cfg.Primary, secondary: cfg.Secondary, mode: cfg.Mode,
		FailOpenSecondary: cfg.FailOpenSecondary,
	}, nil
}

// Result summarizes one Write.
type Result struct {
	PrimaryOK   bool
	SecondaryOK bool
	Mode        Mode
	At          time.Time
}

// Write applies the configured dual-write policy.
func (h *Helper) Write(ctx context.Context, key, value, operationID string) (Result, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return Result{}, ErrClosed
	}
	mode := h.mode
	failOpen := h.FailOpenSecondary
	primary, secondary := h.primary, h.secondary
	h.mu.Unlock()

	res := Result{Mode: mode, At: time.Now().UTC()}
	writePri := func() error {
		if err := primary(ctx, key, value, operationID); err != nil {
			return errors.Join(ErrPrimary, err)
		}
		res.PrimaryOK = true
		return nil
	}
	writeSec := func() error {
		if secondary == nil {
			res.SecondaryOK = true
			return nil
		}
		if err := secondary(ctx, key, value, operationID); err != nil {
			h.mu.Lock()
			h.secondaryErrors++
			h.mu.Unlock()
			if failOpen {
				return nil
			}
			return errors.Join(ErrSecondary, err)
		}
		res.SecondaryOK = true
		return nil
	}

	switch mode {
	case ModePrimaryOnly:
		return res, writePri()
	case ModeSecondaryFirst:
		if err := writeSec(); err != nil {
			return res, err
		}
		return res, writePri()
	default: // ModePrimaryFirst
		if err := writePri(); err != nil {
			return res, err
		}
		return res, writeSec()
	}
}

// SetMode updates the write mode (e.g. during cutover).
func (h *Helper) SetMode(mode Mode) error {
	switch mode {
	case ModePrimaryFirst, ModeSecondaryFirst, ModePrimaryOnly:
	default:
		return ErrInvalidArg
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mode = mode
	return nil
}

// SecondaryErrors returns counted secondary failures (including fail-open).
func (h *Helper) SecondaryErrors() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.secondaryErrors
}

// Close prevents further writes.
func (h *Helper) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
}
