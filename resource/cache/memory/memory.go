// Package memory provides an in-process cache resource for tests and demos.
package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

// Config configures the memory cache resource.
type Config struct {
	ID          resource.ResourceID
	DisplayName string
}

// Resource is a full in-process key/value cache with generation control.
type Resource struct {
	cfg        Config
	mu         sync.RWMutex
	data       map[string]string
	generation uint64
	building   bool
}

// New creates an empty memory cache resource.
func New(cfg Config) (*Resource, error) {
	if cfg.ID.Kind == "" {
		cfg.ID.Kind = resource.KindCache
	}
	if cfg.ID.Kind != resource.KindCache {
		return nil, errors.New("memory: id kind must be cache")
	}
	if err := cfg.ID.Validate(); err != nil {
		return nil, err
	}
	return &Resource{cfg: cfg, data: make(map[string]string)}, nil
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
		Summary:     "In-process cache resource",
		Labels:      map[string]string{"adapter": "memory"},
	}
}

func (r *Resource) Capabilities() resource.ResourceCapabilities {
	return resource.ResourceCapabilities{
		SupportsHealth:    true,
		SupportsSnapshots: true,
		SupportsRecovery:  true,
		// No distributed fencing in-process.
		SupportsFencing: false,
	}
}

func (r *Resource) Health(ctx context.Context) resource.ResourceHealth {
	_ = ctx
	r.mu.RLock()
	defer r.mu.RUnlock()
	msg := "ok"
	status := resource.HealthHealthy
	if r.building {
		status = resource.HealthDegraded
		msg = "building generation"
	}
	h := resource.ResourceHealth{
		Overall:   status,
		CheckedAt: time.Now().UTC(),
		Message:   msg,
		Dimensions: map[resource.HealthDimension]resource.DimensionHealth{
			resource.DimAvailability: {Status: status},
			resource.DimConsistency:  {Status: status, Message: formatGen(r.generation)},
		},
	}
	return h
}

// Get returns a value.
func (r *Resource) Get(key string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.data[key]
	return v, ok
}

// Set stores a value in the active generation.
func (r *Resource) Set(key, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[key] = value
}

// Delete removes a key.
func (r *Resource) Delete(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, key)
}

// Len returns entry count.
func (r *Resource) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.data)
}

// Generation returns the active cache generation.
func (r *Resource) Generation() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.generation
}

// BuildGeneration reserves a new generation for rebuild. Call ActivateGeneration
// after verification. Partial builds never become active until ActivateGeneration.
func (r *Resource) BuildGeneration(_ context.Context) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.building {
		return 0, errors.New("memory: generation build already in progress")
	}
	r.building = true
	return r.generation + 1, nil
}

// ActivateGeneration switches to the reserved generation and clears old data.
func (r *Resource) ActivateGeneration(gen uint64, seed map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.building || gen != r.generation+1 {
		return errors.New("memory: invalid generation activation")
	}
	next := make(map[string]string, len(seed))
	for k, v := range seed {
		next[k] = v
	}
	r.data = next
	r.generation = gen
	r.building = false
	return nil
}

// AbortGeneration cancels an in-progress build.
func (r *Resource) AbortGeneration() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.building = false
}

// Snapshot returns sanitized cache metadata (no values).
func (r *Resource) Snapshot(_ context.Context) (map[string]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return map[string]string{
		"adapter":    "memory",
		"generation": formatGen(r.generation),
		"entries":    itoa(len(r.data)),
		"building":   boolStr(r.building),
	}, nil
}

func formatGen(g uint64) string { return "gen=" + itoa(int(g)) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
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

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
