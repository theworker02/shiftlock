// Package lockdown provides fail-closed service lockdown stronger than maintenance.
// Unlock requires separate stronger auth + expected ID + confirm. Evidence is never deleted.
package lockdown

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

var (
	ErrActive        = errors.New("lockdown: already active")
	ErrInactive      = errors.New("lockdown: not active")
	ErrDenied        = errors.New("lockdown: denied")
	ErrConfirm       = errors.New("lockdown: confirm required")
	ErrExpectedID    = errors.New("lockdown: expected id mismatch")
	ErrWeakAuth      = errors.New("lockdown: unlock requires stronger auth")
)

// Mode selects lockdown severity.
type Mode string

const (
	ModeObserveOnly   Mode = "observe-only"
	ModeRestricted    Mode = "restricted"
	ModeFailClosed    Mode = "fail-closed"
	ModeIsolateClaims Mode = "isolate-claims"
	ModeIsolateTasks  Mode = "isolate-tasks"
	ModeFullService   Mode = "full-service"
)

// State is durable lockdown state. Historical evidence fields are append-only via Evidence.
type State struct {
	ID        string    `json:"id"`
	Mode      Mode      `json:"mode"`
	Reason    string    `json:"reason"`
	EnteredAt time.Time `json:"entered_at"`
	ActorID   string    `json:"actor_id"`
	Active    bool      `json:"active"`
	Trigger   string    `json:"trigger,omitempty"`
}

// Evidence is never deleted by unlock.
type Evidence struct {
	Time    time.Time         `json:"time"`
	Event   string            `json:"event"`
	ActorID string            `json:"actor_id,omitempty"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// EnterRequest configures Enter.
type EnterRequest struct {
	ID      string
	Mode    Mode
	Reason  string
	ActorID string
	Trigger string
}

// UnlockRequest requires expected ID + confirm + strong auth token id.
type UnlockRequest struct {
	ExpectedID     string
	Confirm        bool
	ActorID        string
	StrongAuthID   string // separate stronger capability/auth id (required)
}

// Manager tracks lockdown.
type Manager struct {
	mu        sync.Mutex
	state     State
	evidence  []Evidence
	path      string
	evPath    string
	clock     func() time.Time
	lastAuto  time.Time
	autoEvery time.Duration
}

// Config configures Manager.
type Config struct {
	DurablePath  string
	EvidencePath string
	Clock        func() time.Time
	AutoRateLimit time.Duration // min interval between auto triggers
}

// New creates a Manager.
func New(cfg Config) (*Manager, error) {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.AutoRateLimit <= 0 {
		cfg.AutoRateLimit = time.Minute
	}
	m := &Manager{
		path: cfg.DurablePath, evPath: cfg.EvidencePath,
		clock: cfg.Clock, autoEvery: cfg.AutoRateLimit,
	}
	if cfg.DurablePath != "" {
		_ = m.load()
	}
	if cfg.EvidencePath != "" {
		_ = m.loadEvidence()
	}
	return m, nil
}

// Enter activates lockdown.
func (m *Manager) Enter(req EnterRequest) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Active {
		return m.state, ErrActive
	}
	if req.Mode == "" {
		req.Mode = ModeFailClosed
	}
	if req.Reason == "" {
		return State{}, errors.New("lockdown: reason required")
	}
	now := m.clock()
	id := req.ID
	if id == "" {
		id = now.UTC().Format("20060102T150405.000")
	}
	m.state = State{
		ID: id, Mode: req.Mode, Reason: req.Reason,
		EnteredAt: now, ActorID: req.ActorID, Active: true, Trigger: req.Trigger,
	}
	m.appendEvidenceLocked(Evidence{
		Time: now, Event: "enter", ActorID: req.ActorID,
		Attrs: map[string]string{"mode": string(req.Mode), "reason": req.Reason, "id": id, "trigger": req.Trigger},
	})
	if err := m.persistLocked(); err != nil {
		return State{}, err
	}
	return m.state, nil
}

// Unlock deactivates lockdown without deleting evidence.
func (m *Manager) Unlock(req UnlockRequest) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.state.Active {
		return State{}, ErrInactive
	}
	if !req.Confirm {
		return State{}, ErrConfirm
	}
	if req.ExpectedID == "" || req.ExpectedID != m.state.ID {
		return State{}, ErrExpectedID
	}
	if req.StrongAuthID == "" {
		return State{}, ErrWeakAuth
	}
	prev := m.state
	m.state = State{}
	m.appendEvidenceLocked(Evidence{
		Time: m.clock(), Event: "unlock", ActorID: req.ActorID,
		Attrs: map[string]string{"id": prev.ID, "strong_auth_id": req.StrongAuthID},
	})
	if err := m.persistLocked(); err != nil {
		m.state = prev
		return State{}, err
	}
	prev.Active = false
	return prev, nil
}

// TryAutoEnter rate-limits automatic lockdown triggers.
func (m *Manager) TryAutoEnter(trigger, reason string) (State, bool, error) {
	m.mu.Lock()
	now := m.clock()
	if m.state.Active {
		st := m.state
		m.mu.Unlock()
		return st, false, ErrActive
	}
	if now.Sub(m.lastAuto) < m.autoEvery {
		m.mu.Unlock()
		return State{}, false, nil
	}
	m.lastAuto = now
	m.mu.Unlock()
	st, err := m.Enter(EnterRequest{
		Mode: ModeFailClosed, Reason: reason, ActorID: "system", Trigger: trigger,
	})
	if err != nil {
		return State{}, false, err
	}
	return st, true, nil
}

// Active reports lockdown.
func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.Active
}

// State returns snapshot.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Evidence returns a copy (never cleared by unlock).
func (m *Manager) Evidence() []Evidence {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Evidence, len(m.evidence))
	copy(out, m.evidence)
	return out
}

func (m *Manager) appendEvidenceLocked(e Evidence) {
	m.evidence = append(m.evidence, e)
	if m.evPath == "" {
		return
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(m.evPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
	_ = f.Close()
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
	return json.Unmarshal(b, &m.state)
}

func (m *Manager) loadEvidence() error {
	b, err := os.ReadFile(m.evPath)
	if err != nil {
		return err
	}
	start := 0
	for start < len(b) {
		end := start
		for end < len(b) && b[end] != '\n' {
			end++
		}
		line := b[start:end]
		start = end + 1
		if len(line) == 0 {
			continue
		}
		var e Evidence
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		m.evidence = append(m.evidence, e)
	}
	return nil
}
