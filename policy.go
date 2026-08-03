package shiftlock

import (
	"fmt"
	"time"
)

// Policy constrains coordinator behavior for safety.
type Policy struct {
	// MinLeaseTTL rejects configs with shorter leases.
	MinLeaseTTL time.Duration

	// MaxLeaseTTL rejects configs with longer leases.
	MaxLeaseTTL time.Duration

	// RequireRenewBelowTTL requires RenewInterval < LeaseTTL.
	RequireRenewBelowTTL bool

	// MaxConcurrentClaims limits claims per coordinator (0 = unlimited).
	MaxConcurrentClaims int

	// AllowForceRelease permits ReleaseClaim without being active owner.
	// Default false — required for fencing safety.
	AllowForceRelease bool

	// RejectStaleRelease ensures backends refuse stale-token releases.
	// Always enforced by memory/postgres/redis backends.
	RejectStaleRelease bool

	// RequireDurable refuses backends without durable storage.
	RequireDurable bool

	// RequireIdempotent requires OperationID-capable backends.
	RequireIdempotent bool

	// RequireGlobalExclusive requires the backend to guarantee cross-client exclusivity.
	RequireGlobalExclusive bool

	// FailClosedOnAmbiguous stops workers when backend outcomes are ambiguous (default true).
	FailClosedOnAmbiguous bool

	// AllowLocalDegradation permits non-durable backends (default true for tests/dev).
	AllowLocalDegradation bool
}

func (p Policy) withDefaults() Policy {
	if p.MinLeaseTTL <= 0 {
		p.MinLeaseTTL = 100 * time.Millisecond
	}
	if p.MaxLeaseTTL <= 0 {
		p.MaxLeaseTTL = 24 * time.Hour
	}
	if !p.RequireRenewBelowTTL {
		p.RequireRenewBelowTTL = true
	}
	p.RejectStaleRelease = true
	p.FailClosedOnAmbiguous = true
	if !p.AllowLocalDegradation && !p.RequireDurable {
		// zero-value AllowLocalDegradation is false; treat as true for backward compat
		p.AllowLocalDegradation = true
	}
	return p
}

// Validate checks config against policy.
func (p Policy) Validate(cfg Config) error {
	if cfg.LeaseTTL < p.MinLeaseTTL {
		return &Error{
			Op:      "policy",
			Err:     ErrPolicy,
			Message: fmt.Sprintf("LeaseTTL %s below minimum %s", cfg.LeaseTTL, p.MinLeaseTTL),
		}
	}
	if cfg.LeaseTTL > p.MaxLeaseTTL {
		return &Error{
			Op:      "policy",
			Err:     ErrPolicy,
			Message: fmt.Sprintf("LeaseTTL %s above maximum %s", cfg.LeaseTTL, p.MaxLeaseTTL),
		}
	}
	if p.RequireRenewBelowTTL && cfg.RenewInterval >= cfg.LeaseTTL {
		return &Error{
			Op:      "policy",
			Err:     ErrPolicy,
			Message: fmt.Sprintf("RenewInterval %s must be < LeaseTTL %s", cfg.RenewInterval, cfg.LeaseTTL),
		}
	}
	return nil
}
