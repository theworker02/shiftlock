// Package redis is a thin cache resource adapter.
//
// Production Redis clients may be injected via Pinger/Commander interfaces.
// A local in-memory redis-like backend is provided for tests (no network).
package redis

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

// Pinger checks connectivity (real redis clients can adapt to this).
type Pinger interface {
	Ping(ctx context.Context) error
}

// GenerationController coordinates cache rebuild generations.
type GenerationController interface {
	Current(ctx context.Context) (uint64, error)
	Reserve(ctx context.Context) (uint64, error)
	Activate(ctx context.Context, gen uint64) error
	Abort(ctx context.Context, gen uint64) error
}

// Config configures the redis cache resource.
type Config struct {
	ID          resource.ResourceID
	DisplayName string
	Client      Pinger
	Generations GenerationController
	PingTimeout time.Duration
}

// Resource implements resource.Resource for Redis-style caches.
type Resource struct {
	cfg Config
}

// New validates and constructs a Resource.
func New(cfg Config) (*Resource, error) {
	if cfg.Client == nil {
		return nil, errors.New("redis: Client required")
	}
	if cfg.ID.Kind == "" {
		cfg.ID.Kind = resource.KindCache
	}
	if cfg.ID.Kind != resource.KindCache {
		return nil, errors.New("redis: id kind must be cache")
	}
	if err := cfg.ID.Validate(); err != nil {
		return nil, err
	}
	if cfg.PingTimeout <= 0 {
		cfg.PingTimeout = 2 * time.Second
	}
	if cfg.Generations == nil {
		cfg.Generations = NewLocalGenerations()
	}
	return &Resource{cfg: cfg}, nil
}

func (r *Resource) ID() resource.ResourceID { return r.cfg.ID }
func (r *Resource) Kind() resource.Kind     { return resource.KindCache }

func (r *Resource) Describe() resource.Description {
	name := r.cfg.DisplayName
	if name == "" {
		name = r.cfg.ID.Name
	}
	return resource.Description{
		DisplayName: name,
		Summary:     "Redis cache resource (injected client; local OK for tests)",
		Labels:      map[string]string{"adapter": "redis"},
	}
}

func (r *Resource) Capabilities() resource.ResourceCapabilities {
	return resource.ResourceCapabilities{
		SupportsHealth:    true,
		SupportsSnapshots: true,
		SupportsRecovery:  true,
		// Distributed fencing is not enforced by this thin adapter.
		SupportsFencing: false,
	}
}

func (r *Resource) Health(ctx context.Context) resource.ResourceHealth {
	h := resource.ResourceHealth{
		CheckedAt:  time.Now().UTC(),
		Dimensions: map[resource.HealthDimension]resource.DimensionHealth{},
	}
	pingCtx, cancel := context.WithTimeout(ctx, r.cfg.PingTimeout)
	defer cancel()
	if err := r.cfg.Client.Ping(pingCtx); err != nil {
		h.Dimensions[resource.DimConnectivity] = resource.DimensionHealth{
			Status: resource.HealthUnhealthy, Message: err.Error(),
		}
		h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{Status: resource.HealthUnhealthy}
		h.ComputeOverall()
		h.Message = "ping failed"
		return h
	}
	h.Dimensions[resource.DimConnectivity] = resource.DimensionHealth{Status: resource.HealthHealthy}
	h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{Status: resource.HealthHealthy}
	if gen, err := r.cfg.Generations.Current(ctx); err == nil {
		h.Dimensions[resource.DimConsistency] = resource.DimensionHealth{
			Status: resource.HealthHealthy, Message: formatUint(gen),
		}
	}
	h.ComputeOverall()
	h.Message = "ok"
	return h
}

// BuildGeneration reserves a new cache generation.
func (r *Resource) BuildGeneration(ctx context.Context) (uint64, error) {
	return r.cfg.Generations.Reserve(ctx)
}

// ActivateGeneration activates a reserved generation.
func (r *Resource) ActivateGeneration(ctx context.Context, gen uint64) error {
	return r.cfg.Generations.Activate(ctx, gen)
}

// AbortGeneration aborts a reserved generation.
func (r *Resource) AbortGeneration(ctx context.Context, gen uint64) error {
	return r.cfg.Generations.Abort(ctx, gen)
}

// Snapshot is sanitized (no keys/values/passwords).
func (r *Resource) Snapshot(ctx context.Context) (map[string]string, error) {
	out := map[string]string{"adapter": "redis"}
	if gen, err := r.cfg.Generations.Current(ctx); err == nil {
		out["generation"] = formatUint(gen)
	}
	h := r.Health(ctx)
	out["health"] = string(h.Overall)
	return out, nil
}

// Local is an in-memory redis-like pinger + store for tests.
type Local struct {
	mu   sync.Mutex
	down bool
	kv   map[string]string
}

// NewLocal creates a healthy local fake.
func NewLocal() *Local {
	return &Local{kv: make(map[string]string)}
}

// Ping implements Pinger.
func (l *Local) Ping(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return errors.New("redis local: down")
	}
	return nil
}

// SetDown injects connectivity failure.
func (l *Local) SetDown(v bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.down = v
}

// Set stores a value (test helper).
func (l *Local) Set(k, v string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kv[k] = v
}

// Get reads a value (test helper).
func (l *Local) Get(k string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.kv[k]
	return v, ok
}

// LocalGenerations is an in-process generation controller.
type LocalGenerations struct {
	mu       sync.Mutex
	current  uint64
	reserved uint64
}

// NewLocalGenerations creates a generation controller starting at 0.
func NewLocalGenerations() *LocalGenerations { return &LocalGenerations{} }

func (g *LocalGenerations) Current(context.Context) (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.current, nil
}

func (g *LocalGenerations) Reserve(context.Context) (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reserved != 0 {
		return 0, errors.New("redis: generation already reserved")
	}
	g.reserved = g.current + 1
	return g.reserved, nil
}

func (g *LocalGenerations) Activate(_ context.Context, gen uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reserved == 0 || gen != g.reserved {
		return errors.New("redis: invalid generation activate")
	}
	g.current = gen
	g.reserved = 0
	return nil
}

func (g *LocalGenerations) Abort(_ context.Context, gen uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reserved == 0 || gen != g.reserved {
		return errors.New("redis: invalid generation abort")
	}
	g.reserved = 0
	return nil
}

func formatUint(n uint64) string {
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
