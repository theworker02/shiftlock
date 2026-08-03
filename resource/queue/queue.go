// Package queue provides a generic queue resource plus an in-memory adapter
// for demos and tests. Optional NATS/Kafka wrappers stay thin and injection-based
// so core ShiftLock does not require broker SDKs.
package queue

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

// Backend is the minimal queue control surface.
type Backend interface {
	Ping(ctx context.Context) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Depth(ctx context.Context) (int, error)
}

// Config configures a queue resource.
type Config struct {
	ID          resource.ResourceID
	DisplayName string
	Backend     Backend
	// MaxDepth soft capacity signal for health (0 = 10000).
	MaxDepth int
}

// Resource implements resource.Resource for queues.
type Resource struct {
	cfg Config
}

// New constructs a queue resource.
func New(cfg Config) (*Resource, error) {
	if cfg.Backend == nil {
		return nil, errors.New("queue: Backend required")
	}
	if cfg.ID.Kind == "" {
		cfg.ID.Kind = resource.KindQueue
	}
	if cfg.ID.Kind != resource.KindQueue {
		return nil, errors.New("queue: id kind must be queue")
	}
	if err := cfg.ID.Validate(); err != nil {
		return nil, err
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 10000
	}
	return &Resource{cfg: cfg}, nil
}

func (r *Resource) ID() resource.ResourceID { return r.cfg.ID }
func (r *Resource) Kind() resource.Kind     { return resource.KindQueue }

func (r *Resource) Describe() resource.Description {
	name := r.cfg.DisplayName
	if name == "" {
		name = r.cfg.ID.Name
	}
	return resource.Description{
		DisplayName: name,
		Summary:     "Queue resource (application-level pause/drain coordination)",
		Labels:      map[string]string{"adapter": "queue"},
	}
}

func (r *Resource) Capabilities() resource.ResourceCapabilities {
	return resource.ResourceCapabilities{
		SupportsHealth:    true,
		SupportsDrain:     true,
		SupportsSnapshots: true,
		SupportsRecovery:  true,
		SupportsFencing:   false,
	}
}

func (r *Resource) Health(ctx context.Context) resource.ResourceHealth {
	h := resource.ResourceHealth{
		CheckedAt:  time.Now().UTC(),
		Dimensions: map[resource.HealthDimension]resource.DimensionHealth{},
	}
	if err := r.cfg.Backend.Ping(ctx); err != nil {
		h.Dimensions[resource.DimConnectivity] = resource.DimensionHealth{
			Status: resource.HealthUnhealthy, Message: err.Error(),
		}
		h.ComputeOverall()
		return h
	}
	h.Dimensions[resource.DimConnectivity] = resource.DimensionHealth{Status: resource.HealthHealthy}
	depth, err := r.cfg.Backend.Depth(ctx)
	if err != nil {
		h.Dimensions[resource.DimCapacity] = resource.DimensionHealth{
			Status: resource.HealthUnknown, Message: err.Error(),
		}
	} else if depth > r.cfg.MaxDepth {
		h.Dimensions[resource.DimCapacity] = resource.DimensionHealth{
			Status: resource.HealthDegraded, Message: "backlog above threshold",
		}
	} else {
		h.Dimensions[resource.DimCapacity] = resource.DimensionHealth{Status: resource.HealthHealthy}
	}
	if m, ok := r.cfg.Backend.(*Memory); ok && m.Paused() {
		h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{
			Status: resource.HealthBlocked, Message: "paused",
		}
	} else {
		h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{Status: resource.HealthHealthy}
	}
	h.ComputeOverall()
	h.Message = "ok"
	return h
}

// Pause pauses consumers (application-level).
func (r *Resource) Pause(ctx context.Context) error { return r.cfg.Backend.Pause(ctx) }

// Resume resumes consumers.
func (r *Resource) Resume(ctx context.Context) error { return r.cfg.Backend.Resume(ctx) }

// Depth returns approximate backlog.
func (r *Resource) Depth(ctx context.Context) (int, error) { return r.cfg.Backend.Depth(ctx) }

// Snapshot is sanitized (no payloads).
func (r *Resource) Snapshot(ctx context.Context) (map[string]string, error) {
	out := map[string]string{"adapter": "queue"}
	if d, err := r.cfg.Backend.Depth(ctx); err == nil {
		out["depth"] = itoa(d)
	}
	h := r.Health(ctx)
	out["health"] = string(h.Overall)
	return out, nil
}

// Memory is an in-process queue backend for demos/tests.
type Memory struct {
	mu      sync.Mutex
	msgs    []string
	paused  bool
	maxMsgs int
}

// NewMemory creates a bounded memory queue (default max 1024).
func NewMemory(maxMsgs int) *Memory {
	if maxMsgs <= 0 {
		maxMsgs = 1024
	}
	return &Memory{maxMsgs: maxMsgs}
}

func (m *Memory) Ping(context.Context) error { return nil }

func (m *Memory) Pause(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paused = true
	return nil
}

func (m *Memory) Resume(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paused = false
	return nil
}

func (m *Memory) Depth(context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.msgs), nil
}

// Paused reports pause state.
func (m *Memory) Paused() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.paused
}

// Publish enqueues a message (rejects when paused or full).
func (m *Memory) Publish(msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paused {
		return errors.New("queue: paused")
	}
	if len(m.msgs) >= m.maxMsgs {
		return errors.New("queue: bound exceeded")
	}
	m.msgs = append(m.msgs, msg)
	return nil
}

// Consume removes and returns the next message.
func (m *Memory) Consume() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paused || len(m.msgs) == 0 {
		return "", false
	}
	msg := m.msgs[0]
	m.msgs = m.msgs[1:]
	return msg, true
}

// NATS is a thin optional wrapper documenting injection of a pause/ping backend.
// No NATS SDK dependency is pulled into the module.
type NATS struct {
	Backend Backend
}

// Kafka is a thin optional wrapper documenting injection of a pause/ping backend.
type Kafka struct {
	Backend Backend
}

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
