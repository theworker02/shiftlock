package shiftlock

import (
	"fmt"
	"time"
)

// Config configures a Coordinator.
type Config struct {
	// Service is the logical service name (required).
	Service string

	// InstanceID uniquely identifies this process instance (required).
	InstanceID string

	// Backend stores ownership state (required).
	Backend Backend

	// GenerationID overrides the auto-generated generation id.
	// If empty, Service/InstanceID/timestamp is used.
	GenerationID string

	// LeaseTTL is how long a claim lease remains valid without renewal.
	// Default: 15s.
	LeaseTTL time.Duration

	// RenewInterval is how often the owner renews claims.
	// Default: LeaseTTL / 3.
	RenewInterval time.Duration

	// AcquireInterval is the retry interval while waiting for ownership.
	// Default: 500ms.
	AcquireInterval time.Duration

	// TransferTimeout bounds how long a reserved transfer may stay pending.
	// Default: 30s. Expired transfers are aborted.
	TransferTimeout time.Duration

	// DrainTimeout bounds graceful drain before forced transfer.
	// Default: 30s.
	DrainTimeout time.Duration

	// ReadinessTimeout bounds readiness gate evaluation.
	// Default: 30s.
	ReadinessTimeout time.Duration

	// WatchBuffer is the per-claim event channel buffer size.
	// Default: 16.
	WatchBuffer int

	// EventBuffer is the async observer buffer size.
	// Default: 64.
	EventBuffer int

	// Policy validates configuration and runtime constraints.
	Policy Policy

	// Clock overrides the wall clock (tests).
	Clock Clock

	// Hooks are synchronous callbacks invoked inline on events.
	Hooks []Hook

	// Observers receive events asynchronously.
	Observers []Observer
}

// defaults applies default values and returns a validated copy.
func (c Config) defaults() (Config, error) {
	if c.Service == "" {
		return c, &Error{Op: "config", Err: ErrPolicy, Message: "Service is required"}
	}
	if c.InstanceID == "" {
		return c, &Error{Op: "config", Err: ErrPolicy, Message: "InstanceID is required"}
	}
	if c.Backend == nil {
		return c, &Error{Op: "config", Err: ErrPolicy, Message: "Backend is required"}
	}
	if c.LeaseTTL <= 0 {
		c.LeaseTTL = 15 * time.Second
	}
	if c.RenewInterval <= 0 {
		c.RenewInterval = c.LeaseTTL / 3
	}
	if c.AcquireInterval <= 0 {
		c.AcquireInterval = 500 * time.Millisecond
	}
	if c.TransferTimeout <= 0 {
		c.TransferTimeout = 30 * time.Second
	}
	if c.DrainTimeout <= 0 {
		c.DrainTimeout = 30 * time.Second
	}
	if c.ReadinessTimeout <= 0 {
		c.ReadinessTimeout = 30 * time.Second
	}
	if c.WatchBuffer <= 0 {
		c.WatchBuffer = 16
	}
	if c.EventBuffer <= 0 {
		c.EventBuffer = 64
	}
	if c.Clock == nil {
		c.Clock = realClock{}
	}
	if c.GenerationID == "" {
		c.GenerationID = fmt.Sprintf("%s-%s-%d", c.Service, c.InstanceID, c.Clock.Now().UnixNano())
	}
	c.Policy = c.Policy.withDefaults()
	if err := c.Policy.Validate(c); err != nil {
		return c, err
	}
	return c, nil
}
