// Package ratelimit provides rate-limit resources (token bucket and concurrency).
package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

// Config configures a rate-limit resource.
type Config struct {
	ID          resource.ResourceID
	DisplayName string
	// Capacity is the token bucket size (0 = 1).
	Capacity float64
	// RefillPerSecond adds tokens each second (0 = Capacity per second).
	RefillPerSecond float64
	// MaxConcurrent bounds in-flight Allow (0 = unlimited concurrency gate).
	MaxConcurrent int
}

// Resource is a first-class rate-limit resource.
type Resource struct {
	cfg Config

	mu         sync.Mutex
	tokens     float64
	last       time.Time
	inFlight   int
	rejected   uint64
	allowed    uint64
}

// New constructs a rate-limit resource.
func New(cfg Config) (*Resource, error) {
	if cfg.ID.Kind == "" {
		cfg.ID.Kind = resource.KindRateLimit
	}
	if cfg.ID.Kind != resource.KindRateLimit {
		return nil, errors.New("ratelimit: id kind must be rate-limit")
	}
	if err := cfg.ID.Validate(); err != nil {
		return nil, err
	}
	if cfg.Capacity <= 0 {
		cfg.Capacity = 1
	}
	if cfg.RefillPerSecond <= 0 {
		cfg.RefillPerSecond = cfg.Capacity
	}
	return &Resource{
		cfg:    cfg,
		tokens: cfg.Capacity,
		last:   time.Now(),
	}, nil
}

func (r *Resource) ID() resource.ResourceID { return r.cfg.ID }
func (r *Resource) Kind() resource.Kind     { return resource.KindRateLimit }

func (r *Resource) Describe() resource.Description {
	name := r.cfg.DisplayName
	if name == "" {
		name = r.cfg.ID.Name
	}
	return resource.Description{
		DisplayName: name,
		Summary:     "Token-bucket / concurrency rate-limit resource",
		Labels:      map[string]string{"adapter": "ratelimit"},
	}
}

func (r *Resource) Capabilities() resource.ResourceCapabilities {
	return resource.ResourceCapabilities{
		SupportsHealth:    true,
		SupportsRateLimit: true,
		SupportsSnapshots: true,
		SupportsFencing:   false,
	}
}

func (r *Resource) Health(ctx context.Context) resource.ResourceHealth {
	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refillLocked(time.Now())
	status := resource.HealthHealthy
	msg := "ok"
	if r.tokens < 1 {
		status = resource.HealthDegraded
		msg = "tokens exhausted"
	}
	return resource.ResourceHealth{
		Overall:   status,
		CheckedAt: time.Now().UTC(),
		Message:   msg,
		Dimensions: map[resource.HealthDimension]resource.DimensionHealth{
			resource.DimCapacity: {Status: status},
		},
	}
}

// Allow consumes one token. Returns ErrLimited when empty.
func (r *Resource) Allow() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refillLocked(time.Now())
	if r.cfg.MaxConcurrent > 0 && r.inFlight >= r.cfg.MaxConcurrent {
		r.rejected++
		return ErrLimited
	}
	if r.tokens < 1 {
		r.rejected++
		return ErrLimited
	}
	r.tokens--
	r.inFlight++
	r.allowed++
	return nil
}

// Release ends an in-flight slot from Allow (concurrency accounting).
func (r *Resource) Release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inFlight > 0 {
		r.inFlight--
	}
}

// Wait blocks until a token is available or ctx is done.
func (r *Resource) Wait(ctx context.Context) error {
	for {
		if err := r.Allow(); err == nil {
			return nil
		} else if !errors.Is(err, ErrLimited) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// ErrLimited is returned when the rate limit rejects a request.
var ErrLimited = errors.New("ratelimit: limited")

// Snapshot is sanitized.
func (r *Resource) Snapshot(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refillLocked(time.Now())
	return map[string]string{
		"adapter":   "ratelimit",
		"tokens":    ftoa(r.tokens),
		"capacity":  ftoa(r.cfg.Capacity),
		"in_flight": itoa(r.inFlight),
		"allowed":   uitoa(r.allowed),
		"rejected":  uitoa(r.rejected),
	}, nil
}

func (r *Resource) refillLocked(now time.Time) {
	elapsed := now.Sub(r.last).Seconds()
	if elapsed <= 0 {
		return
	}
	r.tokens += elapsed * r.cfg.RefillPerSecond
	if r.tokens > r.cfg.Capacity {
		r.tokens = r.cfg.Capacity
	}
	r.last = now
}

func ftoa(f float64) string {
	// simple integer-ish formatting
	n := int64(f)
	return itoa64(n)
}

func itoa(n int) string { return itoa64(int64(n)) }

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func uitoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
