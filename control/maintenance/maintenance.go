// Package maintenance manages scoped, durable, auto-expiring maintenance windows.
package maintenance

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

var (
	ErrActive   = errors.New("maintenance: already active")
	ErrInactive = errors.New("maintenance: not active")
	ErrDenied   = errors.New("maintenance: denied")
	ErrExpired  = errors.New("maintenance: expired")
)

// Scope limits what maintenance affects.
type Scope struct {
	Claims []string `json:"claims,omitempty"`
	Tasks  []string `json:"tasks,omitempty"`
	All    bool     `json:"all,omitempty"`
}

// State is durable maintenance state.
type State struct {
	ID        string    `json:"id"`
	Reason    string    `json:"reason"`
	Scope     Scope     `json:"scope"`
	EnteredAt time.Time `json:"entered_at"`
	ExpiresAt time.Time `json:"expires_at"`
	ActorID   string    `json:"actor_id"`
	Active    bool      `json:"active"`
}

// EnterRequest configures Enter.
type EnterRequest struct {
	ID       string
	Reason   string
	Scope    Scope
	Duration time.Duration
	ActorID  string
	// CapabilityID is recorded for audit (verification done by caller).
	CapabilityID string
}

// Manager tracks maintenance with optional file durability.
type Manager struct {
	mu       sync.Mutex
	state    State
	path     string
	clock    func() time.Time
	maxDur   time.Duration
}

// Config configures Manager.
type Config struct {
	DurablePath string
	MaxDuration time.Duration
	Clock       func() time.Time
}

// New creates a Manager and loads durable state if present.
func New(cfg Config) (*Manager, error) {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.MaxDuration <= 0 {
		cfg.MaxDuration = 24 * time.Hour
	}
	m := &Manager{path: cfg.DurablePath, clock: cfg.Clock, maxDur: cfg.MaxDuration}
	if cfg.DurablePath != "" {
		if err := m.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return m, nil
}

// Enter starts maintenance.
func (m *Manager) Enter(req EnterRequest) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	if m.state.Active {
		return m.state, ErrActive
	}
	if req.Reason == "" {
		return State{}, errors.New("maintenance: reason required")
	}
	dur := req.Duration
	if dur <= 0 {
		dur = time.Hour
	}
	if dur > m.maxDur {
		dur = m.maxDur
	}
	now := m.clock()
	id := req.ID
	if id == "" {
		id = now.UTC().Format("20060102T150405")
	}
	m.state = State{
		ID: id, Reason: req.Reason, Scope: req.Scope,
		EnteredAt: now, ExpiresAt: now.Add(dur), ActorID: req.ActorID, Active: true,
	}
	if err := m.persistLocked(); err != nil {
		m.state = State{}
		return State{}, err
	}
	return m.state, nil
}

// Exit ends maintenance.
func (m *Manager) Exit(actorID string) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	if !m.state.Active {
		return State{}, ErrInactive
	}
	prev := m.state
	m.state = State{}
	_ = actorID
	if err := m.persistLocked(); err != nil {
		m.state = prev
		return State{}, err
	}
	prev.Active = false
	return prev, nil
}

// Active reports whether maintenance is in effect (auto-expires).
func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	return m.state.Active
}

// State returns a snapshot.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	return m.state
}

func (m *Manager) expireLocked() {
	if m.state.Active && m.clock().After(m.state.ExpiresAt) {
		m.state = State{}
		_ = m.persistLocked()
	}
}

func (m *Manager) persistLocked() error {
	if m.path == "" {
		return nil
	}
	b, err := json.Marshal(m.state)
	if err != nil {
		return err
	}
	return os.WriteFile(m.path, b, 0o600)
}

func (m *Manager) load() error {
	b, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	m.state = st
	m.expireLocked()
	return nil
}
