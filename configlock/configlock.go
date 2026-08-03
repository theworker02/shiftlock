// Package configlock protects runtime configuration with signed bundles
// and an explicit draft→…→active lifecycle.
package configlock

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/security/signing"
)

var (
	ErrInvalidState     = errors.New("configlock: invalid state transition")
	ErrHashMismatch     = errors.New("configlock: content hash mismatch")
	ErrUnsignedRequired = errors.New("configlock: signatures required for production activation")
	ErrBadSignature     = errors.New("configlock: invalid signature")
	ErrNotFound         = errors.New("configlock: bundle not found")
	ErrExpired          = errors.New("configlock: bundle expired")
)

// State is the configuration lifecycle state.
type State string

const (
	StateDraft      State = "draft"
	StateStaged     State = "staged"
	StateValidated  State = "validated"
	StateApproved   State = "approved"
	StateActive     State = "active"
	StateSuperseded State = "superseded"
	StateRevoked    State = "revoked"
	StateRejected   State = "rejected"
)

// Bundle is a signed configuration payload.
type Bundle struct {
	Version     uint32              `json:"version"`
	Revision    uint64              `json:"revision"`
	Service     string              `json:"service"`
	Environment string              `json:"environment"`
	CreatedAt   time.Time           `json:"created_at"`
	ActivatesAt *time.Time          `json:"activates_at,omitempty"`
	ExpiresAt   *time.Time          `json:"expires_at,omitempty"`
	Content     json.RawMessage     `json:"content"`
	ContentHash [32]byte            `json:"content_hash"`
	Signatures  []signing.Signature `json:"signatures,omitempty"`
	State       State               `json:"state"`
}

// signBody is the canonical payload covered by signatures (excludes Signatures/State).
type signBody struct {
	Version     uint32          `json:"version"`
	Revision    uint64          `json:"revision"`
	Service     string          `json:"service"`
	Environment string          `json:"environment"`
	CreatedAt   time.Time       `json:"created_at"`
	ActivatesAt *time.Time      `json:"activates_at,omitempty"`
	ExpiresAt   *time.Time      `json:"expires_at,omitempty"`
	ContentHash string          `json:"content_hash"`
	Content     json.RawMessage `json:"content"`
}

// ComputeContentHash sets ContentHash from Content bytes.
func (b *Bundle) ComputeContentHash() {
	b.ContentHash = sha256.Sum256(b.Content)
}

// VerifyContentHash checks ContentHash matches Content.
func (b Bundle) VerifyContentHash() error {
	sum := sha256.Sum256(b.Content)
	if sum != b.ContentHash {
		return ErrHashMismatch
	}
	return nil
}

func (b Bundle) body() signBody {
	return signBody{
		Version:     b.Version,
		Revision:    b.Revision,
		Service:     b.Service,
		Environment: b.Environment,
		CreatedAt:   b.CreatedAt.UTC(),
		ActivatesAt: b.ActivatesAt,
		ExpiresAt:   b.ExpiresAt,
		ContentHash: fmt.Sprintf("%x", b.ContentHash),
		Content:     b.Content,
	}
}

// Sign appends an Ed25519 signature over the canonical bundle body.
func (b *Bundle) Sign(key signing.PrivateKey) error {
	if b.ContentHash == ([32]byte{}) {
		b.ComputeContentHash()
	}
	sig, _, err := signing.SignCanonical(key, b.body())
	if err != nil {
		return err
	}
	b.Signatures = append(b.Signatures, sig)
	return nil
}

// VerifySignatures checks all signatures against the key ring.
func (b Bundle) VerifySignatures(keys *signing.KeyRing) error {
	if keys == nil || keys.Len() == 0 {
		return signing.ErrNoTrustedKeys
	}
	if len(b.Signatures) == 0 {
		return ErrBadSignature
	}
	for _, sig := range b.Signatures {
		if err := signing.VerifyCanonical(keys, b.body(), sig); err != nil {
			return fmt.Errorf("%w: %v", ErrBadSignature, err)
		}
	}
	return nil
}

// Manager tracks configuration lifecycle.
type Manager struct {
	mu               sync.Mutex
	bundles          map[uint64]*Bundle
	active           uint64
	nextRev          uint64
	keys             *signing.KeyRing
	requireSignature bool
	production       bool
}

// ManagerOption configures Manager.
type ManagerOption func(*Manager)

// WithKeyRing sets trusted verification keys.
func WithKeyRing(keys *signing.KeyRing) ManagerOption {
	return func(m *Manager) { m.keys = keys }
}

// RequireSignatures forces signatures for activation when production is set.
func RequireSignatures(v bool) ManagerOption {
	return func(m *Manager) { m.requireSignature = v }
}

// Production marks the environment as production (unsigned activation rejected when required).
func Production(v bool) ManagerOption {
	return func(m *Manager) { m.production = v }
}

// NewManager creates a configuration manager.
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{bundles: make(map[uint64]*Bundle)}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Draft creates a new draft bundle.
func (m *Manager) Draft(service, env string, content json.RawMessage) (*Bundle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextRev++
	b := &Bundle{
		Version:     1,
		Revision:    m.nextRev,
		Service:     service,
		Environment: env,
		CreatedAt:   time.Now().UTC(),
		Content:     append(json.RawMessage(nil), content...),
		State:       StateDraft,
	}
	b.ComputeContentHash()
	cp := *b
	m.bundles[b.Revision] = b
	return &cp, nil
}

func (m *Manager) get(rev uint64) (*Bundle, error) {
	b, ok := m.bundles[rev]
	if !ok {
		return nil, ErrNotFound
	}
	return b, nil
}

func transition(from, to State) error {
	allowed := map[State][]State{
		StateDraft:     {StateStaged, StateRejected, StateRevoked},
		StateStaged:    {StateValidated, StateRejected, StateRevoked},
		StateValidated: {StateApproved, StateRejected, StateRevoked},
		StateApproved:  {StateActive, StateRejected, StateRevoked},
		StateActive:    {StateSuperseded, StateRevoked},
	}
	for _, a := range allowed[from] {
		if a == to {
			return nil
		}
	}
	return fmt.Errorf("%w: %s → %s", ErrInvalidState, from, to)
}

// Stage moves draft → staged.
func (m *Manager) Stage(rev uint64) error {
	return m.move(rev, StateStaged)
}

// Validate moves staged → validated after hash check.
func (m *Manager) Validate(rev uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := m.get(rev)
	if err != nil {
		return err
	}
	if err := transition(b.State, StateValidated); err != nil {
		return err
	}
	if err := b.VerifyContentHash(); err != nil {
		return err
	}
	b.State = StateValidated
	return nil
}

// Approve moves validated → approved.
func (m *Manager) Approve(rev uint64) error {
	return m.move(rev, StateApproved)
}

// Activate moves approved → active. Production + RequireSignatures rejects unsigned.
func (m *Manager) Activate(rev uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := m.get(rev)
	if err != nil {
		return err
	}
	if err := transition(b.State, StateActive); err != nil {
		return err
	}
	if err := b.VerifyContentHash(); err != nil {
		return err
	}
	if b.ExpiresAt != nil && time.Now().UTC().After(*b.ExpiresAt) {
		return ErrExpired
	}
	needSig := m.requireSignature || m.production
	if needSig {
		if len(b.Signatures) == 0 {
			return ErrUnsignedRequired
		}
		if err := b.VerifySignatures(m.keys); err != nil {
			return err
		}
	}
	if m.active != 0 {
		if prev, ok := m.bundles[m.active]; ok && prev.State == StateActive {
			prev.State = StateSuperseded
		}
	}
	b.State = StateActive
	m.active = rev
	return nil
}

// Active returns the active bundle copy, if any.
func (m *Manager) Active() (*Bundle, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == 0 {
		return nil, false
	}
	b := m.bundles[m.active]
	cp := *b
	return &cp, true
}

// SignRevision signs a stored bundle.
func (m *Manager) SignRevision(rev uint64, key signing.PrivateKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := m.get(rev)
	if err != nil {
		return err
	}
	return b.Sign(key)
}

func (m *Manager) move(rev uint64, to State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := m.get(rev)
	if err != nil {
		return err
	}
	if err := transition(b.State, to); err != nil {
		return err
	}
	b.State = to
	return nil
}

// Get returns a copy of a revision.
func (m *Manager) Get(rev uint64) (*Bundle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := m.get(rev)
	if err != nil {
		return nil, err
	}
	cp := *b
	return &cp, nil
}
