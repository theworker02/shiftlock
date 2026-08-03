// Package failover provides primary/standby resource groups with manual and
// health-based failover policy skeletons. Failback is always a separate workflow.
package failover

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

var (
	ErrNotFound      = errors.New("failover: group not found")
	ErrDuplicate     = errors.New("failover: duplicate group")
	ErrNoStandby     = errors.New("failover: no healthy standby")
	ErrInvalidState  = errors.New("failover: invalid state")
	ErrManualOnly    = errors.New("failover: policy is manual")
)

// Policy selects how failover decisions are made.
type Policy string

const (
	PolicyManual      Policy = "manual"
	PolicyHealthBased Policy = "health-based"
)

// GroupConfig configures a failover group.
type GroupConfig struct {
	Name    string
	Primary resource.ResourceID
	Standbys []resource.ResourceID
	Policy  Policy
}

// Group is a primary/standby set.
type Group struct {
	Name     string
	Policy   Policy
	Primary  resource.ResourceID
	Standbys []resource.ResourceID
	Active   resource.ResourceID
	Epoch    resource.ResourceEpoch
}

// Decision records a failover or failback.
type Decision struct {
	Group     string
	From      resource.ResourceID
	To        resource.ResourceID
	Reason    string
	At        time.Time
	EpochAdv  resource.EpochAdvance
	Failback  bool
}

// DefaultMaxHistory bounds decision history.
const DefaultMaxHistory = 256

// Manager owns failover groups and advances resource epochs via a registry.
type Manager struct {
	reg       *resource.Registry
	mu        sync.Mutex
	groups    map[string]*Group
	log       []Decision
	maxHistory int
}

// NewManager creates a failover manager.
func NewManager(reg *resource.Registry) *Manager {
	return &Manager{
		reg:        reg,
		groups:     make(map[string]*Group),
		maxHistory: DefaultMaxHistory,
	}
}

// List returns copies of all groups.
func (m *Manager) List() []Group {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Group, 0, len(m.groups))
	for _, g := range m.groups {
		out = append(out, cloneGroup(g))
	}
	return out
}

// Register adds a failover group.
func (m *Manager) Register(cfg GroupConfig) error {
	if cfg.Name == "" || cfg.Primary.IsZero() {
		return errors.New("failover: name and primary required")
	}
	if cfg.Policy == "" {
		cfg.Policy = PolicyManual
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.groups[cfg.Name]; ok {
		return ErrDuplicate
	}
	m.groups[cfg.Name] = &Group{
		Name:     cfg.Name,
		Policy:   cfg.Policy,
		Primary:  cfg.Primary,
		Standbys: append([]resource.ResourceID(nil), cfg.Standbys...),
		Active:   cfg.Primary,
	}
	return nil
}

// Get returns a copy of the group.
func (m *Manager) Get(name string) (Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[name]
	if !ok {
		return Group{}, ErrNotFound
	}
	return cloneGroup(g), nil
}

// ExecuteFailover promotes a standby (manual or health-based).
// Advances the newly active resource epoch in the registry.
func (m *Manager) ExecuteFailover(ctx context.Context, name, reason string, target *resource.ResourceID) (Decision, error) {
	if reason == "" {
		return Decision{}, errors.New("failover: reason required")
	}
	m.mu.Lock()
	g, ok := m.groups[name]
	if !ok {
		m.mu.Unlock()
		return Decision{}, ErrNotFound
	}
	if g.Policy == PolicyManual && target == nil {
		m.mu.Unlock()
		return Decision{}, ErrManualOnly
	}
	to, err := m.pickStandby(ctx, g, target)
	if err != nil {
		m.mu.Unlock()
		return Decision{}, err
	}
	from := g.Active
	g.Active = to
	m.mu.Unlock()

	adv, err := m.reg.Advance(to, "failover:"+reason)
	if err != nil {
		// Best-effort rollback of in-memory active pointer.
		m.mu.Lock()
		if gg := m.groups[name]; gg != nil {
			gg.Active = from
		}
		m.mu.Unlock()
		return Decision{}, err
	}
	m.mu.Lock()
	m.groups[name].Epoch = adv.To
	d := Decision{
		Group: name, From: from, To: to, Reason: reason,
		At: time.Now().UTC(), EpochAdv: adv, Failback: false,
	}
	m.appendLog(d)
	m.mu.Unlock()
	return d, nil
}

// ExecuteFailback is a separate controlled workflow back toward the configured primary.
// It never runs automatically from health policy.
func (m *Manager) ExecuteFailback(ctx context.Context, name, reason string) (Decision, error) {
	_ = ctx
	if reason == "" {
		return Decision{}, errors.New("failover: reason required")
	}
	m.mu.Lock()
	g, ok := m.groups[name]
	if !ok {
		m.mu.Unlock()
		return Decision{}, ErrNotFound
	}
	if g.Active.Equal(g.Primary) {
		m.mu.Unlock()
		return Decision{}, ErrInvalidState
	}
	from := g.Active
	to := g.Primary
	g.Active = to
	m.mu.Unlock()

	adv, err := m.reg.Advance(to, "failback:"+reason)
	if err != nil {
		m.mu.Lock()
		if gg := m.groups[name]; gg != nil {
			gg.Active = from
		}
		m.mu.Unlock()
		return Decision{}, err
	}
	m.mu.Lock()
	m.groups[name].Epoch = adv.To
	d := Decision{
		Group: name, From: from, To: to, Reason: reason,
		At: time.Now().UTC(), EpochAdv: adv, Failback: true,
	}
	m.appendLog(d)
	m.mu.Unlock()
	return d, nil
}

func (m *Manager) appendLog(d Decision) {
	m.log = append(m.log, d)
	if len(m.log) > m.maxHistory {
		m.log = append([]Decision(nil), m.log[len(m.log)-m.maxHistory:]...)
	}
}

// EvaluateHealth is a health-based policy skeleton: returns a recommended standby
// when the active resource is unhealthy. Does not execute failover.
func (m *Manager) EvaluateHealth(ctx context.Context, name string) (resource.ResourceID, bool, error) {
	m.mu.Lock()
	g, ok := m.groups[name]
	if !ok {
		m.mu.Unlock()
		return resource.ResourceID{}, false, ErrNotFound
	}
	if g.Policy != PolicyHealthBased {
		m.mu.Unlock()
		return resource.ResourceID{}, false, nil
	}
	active := g.Active
	standbys := append([]resource.ResourceID(nil), g.Standbys...)
	m.mu.Unlock()

	ent, err := m.reg.Get(active)
	if err != nil {
		return resource.ResourceID{}, false, err
	}
	h := ent.Resource.Health(ctx)
	if h.Overall != resource.HealthUnhealthy && h.Overall != resource.HealthBlocked {
		return resource.ResourceID{}, false, nil
	}
	for _, s := range standbys {
		se, err := m.reg.Get(s)
		if err != nil {
			continue
		}
		sh := se.Resource.Health(ctx)
		if sh.Overall == resource.HealthHealthy || sh.Overall == resource.HealthDegraded {
			return s, true, nil
		}
	}
	return resource.ResourceID{}, false, ErrNoStandby
}

// History returns recent decisions (copy).
func (m *Manager) History() []Decision {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Decision(nil), m.log...)
}

func (m *Manager) pickStandby(ctx context.Context, g *Group, target *resource.ResourceID) (resource.ResourceID, error) {
	if target != nil {
		for _, s := range g.Standbys {
			if s.Equal(*target) {
				return s, nil
			}
		}
		return resource.ResourceID{}, ErrNoStandby
	}
	for _, s := range g.Standbys {
		ent, err := m.reg.Get(s)
		if err != nil {
			continue
		}
		h := ent.Resource.Health(ctx)
		if h.Overall == resource.HealthHealthy || h.Overall == resource.HealthDegraded {
			return s, nil
		}
	}
	if len(g.Standbys) == 0 {
		return resource.ResourceID{}, ErrNoStandby
	}
	// Manual without target already rejected; health-based may still pick first standby.
	return g.Standbys[0], nil
}

func cloneGroup(g *Group) Group {
	return Group{
		Name: g.Name, Policy: g.Policy, Primary: g.Primary,
		Standbys: append([]resource.ResourceID(nil), g.Standbys...),
		Active: g.Active, Epoch: g.Epoch,
	}
}
