// Package capability implements narrow, expiring, optionally signed
// authorization tokens. Delegation may only reduce scope. Deny by default.
package capability

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/security/antireplay"
	"github.com/theworker02/shiftlock/security/signing"
)

var (
	ErrDenied         = errors.New("capability: denied")
	ErrExpired        = errors.New("capability: expired")
	ErrRevoked        = errors.New("capability: revoked")
	ErrEpochMismatch  = errors.New("capability: security epoch mismatch")
	ErrInvalidSig     = errors.New("capability: invalid signature")
	ErrWidenDelegate  = errors.New("capability: delegation cannot widen scope")
	ErrSingleUseSpent = errors.New("capability: single-use already spent")
	ErrNotFound       = errors.New("capability: not found")
)

// ID uniquely identifies a capability token.
type ID string

// Permission is a stable permission string (e.g. claim.revoke).
type Permission string

// Constraints narrow how a capability may be used.
type Constraints struct {
	SingleUse        bool     `json:"single_use,omitempty"`
	Environment      string   `json:"environment,omitempty"`
	MaxUses          int      `json:"max_uses,omitempty"`
	AllowedResources []string `json:"allowed_resources,omitempty"`
}

// Token is a capability grant. Contents must not include secrets.
type Token struct {
	ID          ID                       `json:"id"`
	Subject     string                   `json:"subject"`
	Permission  Permission               `json:"permission"`
	Resource    string                   `json:"resource,omitempty"`
	IssuedAt    time.Time                `json:"issued_at"`
	ExpiresAt   time.Time                `json:"expires_at"`
	Constraints Constraints              `json:"constraints"`
	Nonce       string                   `json:"nonce"`
	Epoch       antireplay.SecurityEpoch `json:"epoch"`
	ParentID    ID                       `json:"parent_id,omitempty"`
	Signature   []byte                   `json:"signature,omitempty"`
	KeyID       string                   `json:"key_id,omitempty"`
}

type signBody struct {
	ID          ID                       `json:"id"`
	Subject     string                   `json:"subject"`
	Permission  Permission               `json:"permission"`
	Resource    string                   `json:"resource,omitempty"`
	IssuedAt    time.Time                `json:"issued_at"`
	ExpiresAt   time.Time                `json:"expires_at"`
	Constraints Constraints              `json:"constraints"`
	Nonce       string                   `json:"nonce"`
	Epoch       antireplay.SecurityEpoch `json:"epoch"`
	ParentID    ID                       `json:"parent_id,omitempty"`
}

func (t Token) body() signBody {
	return signBody{
		ID: t.ID, Subject: t.Subject, Permission: t.Permission, Resource: t.Resource,
		IssuedAt: t.IssuedAt.UTC(), ExpiresAt: t.ExpiresAt.UTC(),
		Constraints: t.Constraints, Nonce: t.Nonce, Epoch: t.Epoch, ParentID: t.ParentID,
	}
}

// Request describes a capability to issue.
type Request struct {
	Subject     string
	Permission  Permission
	Resource    string
	TTL         time.Duration
	Constraints Constraints
}

// Authority issues and verifies capabilities.
type Authority struct {
	mu         sync.Mutex
	signer     *signing.PrivateKey
	keys       *signing.KeyRing
	epoch      antireplay.SecurityEpoch
	replay     *antireplay.Cache
	revoked    map[ID]struct{}
	spent      map[ID]int
	issued     map[ID]Token
	now        func() time.Time
	requireSig bool
}

// Option configures Authority.
type Option func(*Authority)

// WithSigner enables Ed25519 signing of issued tokens.
func WithSigner(key signing.PrivateKey, ring *signing.KeyRing) Option {
	return func(a *Authority) {
		cp := key
		a.signer = &cp
		a.keys = ring
		a.requireSig = true
	}
}

// WithKeyRing sets verification keys.
func WithKeyRing(ring *signing.KeyRing) Option {
	return func(a *Authority) { a.keys = ring }
}

// WithReplayCache binds nonce checks to an anti-replay cache.
func WithReplayCache(c *antireplay.Cache) Option {
	return func(a *Authority) { a.replay = c }
}

// WithClock overrides time (tests).
func WithClock(now func() time.Time) Option {
	return func(a *Authority) { a.now = now }
}

// New creates an authority at epoch 0.
func New(opts ...Option) *Authority {
	a := &Authority{
		revoked: make(map[ID]struct{}),
		spent:   make(map[ID]int),
		issued:  make(map[ID]Token),
		replay:  antireplay.New(4096),
		now:     func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Epoch returns the current security epoch.
func (a *Authority) Epoch() antireplay.SecurityEpoch {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.epoch
}

// AdvanceEpoch invalidates prior capabilities by epoch mismatch.
func (a *Authority) AdvanceEpoch() (antireplay.SecurityEpoch, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.replay != nil {
		ep, err := a.replay.AdvanceEpoch()
		if err != nil {
			return a.epoch, err
		}
		a.epoch = ep
		return a.epoch, nil
	}
	if a.epoch == ^antireplay.SecurityEpoch(0) {
		return a.epoch, errors.New("capability: epoch overflow")
	}
	a.epoch++
	return a.epoch, nil
}

// Issue creates a new capability. Empty permission is denied.
func (a *Authority) Issue(req Request) (Token, error) {
	if req.Permission == "" || req.Permission == "*" {
		return Token{}, ErrDenied
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return Token{}, err
	}
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return Token{}, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	tok := Token{
		ID:          ID("cap_" + hex.EncodeToString(idBytes)),
		Subject:     req.Subject,
		Permission:  req.Permission,
		Resource:    req.Resource,
		IssuedAt:    now,
		ExpiresAt:   now.Add(ttl),
		Constraints: req.Constraints,
		Nonce:       hex.EncodeToString(nonce),
		Epoch:       a.epoch,
	}
	if err := a.signLocked(&tok); err != nil {
		return Token{}, err
	}
	a.issued[tok.ID] = tok
	return tok, nil
}

func (a *Authority) signLocked(tok *Token) error {
	if a.signer == nil {
		return nil
	}
	sig, _, err := signing.SignCanonical(*a.signer, tok.body())
	if err != nil {
		return err
	}
	tok.Signature = sig.Sig
	tok.KeyID = string(sig.KeyID)
	return nil
}

// Verify checks signature, expiry, revocation, epoch, and optional single-use.
func (a *Authority) Verify(tok Token) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.verifyLocked(tok, true)
}

func (a *Authority) verifyLocked(tok Token, consume bool) error {
	if _, ok := a.revoked[tok.ID]; ok {
		return ErrRevoked
	}
	if tok.Epoch != a.epoch {
		return ErrEpochMismatch
	}
	now := a.now()
	if now.After(tok.ExpiresAt) {
		return ErrExpired
	}
	if a.requireSig || len(tok.Signature) > 0 {
		if a.keys == nil {
			return ErrInvalidSig
		}
		sig := signing.Signature{
			KeyID:     signing.KeyID(tok.KeyID),
			Algorithm: signing.AlgorithmEd25519,
			Version:   1,
			Sig:       tok.Signature,
			SignedAt:  tok.IssuedAt,
		}
		if err := signing.VerifyCanonical(a.keys, tok.body(), sig); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSig, err)
		}
	}
	if tok.Constraints.SingleUse || tok.Constraints.MaxUses == 1 {
		if a.spent[tok.ID] > 0 {
			return ErrSingleUseSpent
		}
		if consume {
			a.spent[tok.ID] = 1
		}
	} else if tok.Constraints.MaxUses > 1 {
		if a.spent[tok.ID] >= tok.Constraints.MaxUses {
			return ErrSingleUseSpent
		}
		if consume {
			a.spent[tok.ID]++
		}
	}
	return nil
}

// Revoke marks a capability invalid.
func (a *Authority) Revoke(id ID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.issued[id]; !ok {
		return ErrNotFound
	}
	a.revoked[id] = struct{}{}
	return nil
}

// Delegate creates a child capability with equal or reduced scope.
func (a *Authority) Delegate(parent Token, req Request) (Token, error) {
	a.mu.Lock()
	if err := a.verifyLocked(parent, false); err != nil {
		a.mu.Unlock()
		return Token{}, err
	}
	a.mu.Unlock()

	if !permissionReduces(parent.Permission, req.Permission) {
		return Token{}, ErrWidenDelegate
	}
	if parent.Resource != "" && req.Resource != "" && parent.Resource != req.Resource {
		return Token{}, ErrWidenDelegate
	}
	if parent.Resource != "" && req.Resource == "" {
		req.Resource = parent.Resource
	}
	if req.TTL <= 0 || parent.ExpiresAt.Before(a.now().Add(req.TTL)) {
		// clamp to parent remaining TTL
		rem := time.Until(parent.ExpiresAt)
		if rem <= 0 {
			return Token{}, ErrExpired
		}
		if req.TTL <= 0 || req.TTL > rem {
			req.TTL = rem
		}
	}
	if req.Subject == "" {
		req.Subject = parent.Subject
	}
	child, err := a.Issue(req)
	if err != nil {
		return Token{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	child.ParentID = parent.ID
	child.Epoch = parent.Epoch
	if err := a.signLocked(&child); err != nil {
		return Token{}, err
	}
	a.issued[child.ID] = child
	return child, nil
}

func permissionReduces(parent, child Permission) bool {
	if parent == child {
		return true
	}
	ps, cs := string(parent), string(child)
	if strings.HasSuffix(ps, ".*") {
		prefix := strings.TrimSuffix(ps, ".*")
		return strings.HasPrefix(cs, prefix+".") && !strings.Contains(cs, "*")
	}
	return false
}
