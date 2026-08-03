// Package signing provides Ed25519 helpers for ShiftLock high-value records.
//
// It signs capabilities, config bundles, audit checkpoints, and quorum
// decisions using the Go standard library only — no custom cryptography.
package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const AlgorithmEd25519 = "ed25519"

var (
	ErrUnknownKey     = errors.New("signing: unknown key id")
	ErrExpiredKey     = errors.New("signing: key expired")
	ErrInvalidSig     = errors.New("signing: invalid signature")
	ErrEmptyPayload   = errors.New("signing: empty payload")
	ErrNoTrustedKeys  = errors.New("signing: no trusted keys")
	ErrDuplicateKeyID = errors.New("signing: duplicate key id")
)

// KeyID identifies a trusted signing key across rotation windows.
type KeyID string

// Signature is a versioned detached signature over canonical bytes.
type Signature struct {
	KeyID     KeyID     `json:"key_id"`
	Algorithm string    `json:"algorithm"`
	Version   uint32    `json:"version"`
	SignedAt  time.Time `json:"signed_at"`
	Sig       []byte    `json:"sig"`
}

// PublicKey is a trusted verification key with optional expiry.
type PublicKey struct {
	ID        KeyID             `json:"id"`
	Public    ed25519.PublicKey `json:"public"`
	CreatedAt time.Time         `json:"created_at"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
	Retired   bool              `json:"retired,omitempty"`
}

// ValidAt reports whether the key may verify signatures at t.
func (k PublicKey) ValidAt(t time.Time) bool {
	if k.Retired {
		return false
	}
	if k.ExpiresAt != nil && t.After(*k.ExpiresAt) {
		return false
	}
	return true
}

// PrivateKey holds a signing keypair. Never serialize private material to audit.
type PrivateKey struct {
	ID         KeyID
	Public     ed25519.PublicKey
	Private    ed25519.PrivateKey
	CreatedAt  time.Time
	ExpiresAt  *time.Time
}

// GenerateKey creates a new Ed25519 keypair with a random key ID.
func GenerateKey() (PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return PrivateKey{}, err
	}
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return PrivateKey{}, err
	}
	return PrivateKey{
		ID:        KeyID("ed25519:" + hex.EncodeToString(idBytes)),
		Public:    pub,
		Private:   priv,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// PublicView returns the verification half.
func (k PrivateKey) PublicView() PublicKey {
	return PublicKey{
		ID:        k.ID,
		Public:    append(ed25519.PublicKey(nil), k.Public...),
		CreatedAt: k.CreatedAt,
		ExpiresAt: k.ExpiresAt,
	}
}

// CanonicalJSON produces deterministic JSON: sorted object keys, no HTML escape.
func CanonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return canonicalizeJSON(raw)
}

func canonicalizeJSON(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	norm, err := normalize(v)
	if err != nil {
		return nil, err
	}
	buf, err := json.Marshal(norm)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func normalize(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			n, err := normalize(t[k])
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i := range t {
			n, err := normalize(t[i])
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	default:
		return t, nil
	}
}

// HashPayload returns SHA-256 of canonical JSON for v.
func HashPayload(v any) ([32]byte, error) {
	b, err := CanonicalJSON(v)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(b), nil
}

// SignBytes signs raw payload bytes with the private key.
func SignBytes(key PrivateKey, payload []byte) (Signature, error) {
	if len(payload) == 0 {
		return Signature{}, ErrEmptyPayload
	}
	now := time.Now().UTC()
	if key.ExpiresAt != nil && now.After(*key.ExpiresAt) {
		return Signature{}, ErrExpiredKey
	}
	sig := ed25519.Sign(key.Private, payload)
	return Signature{
		KeyID:     key.ID,
		Algorithm: AlgorithmEd25519,
		Version:   1,
		SignedAt:  now,
		Sig:       sig,
	}, nil
}

// SignCanonical marshals v canonically and signs the bytes.
func SignCanonical(key PrivateKey, v any) (Signature, []byte, error) {
	payload, err := CanonicalJSON(v)
	if err != nil {
		return Signature{}, nil, err
	}
	sig, err := SignBytes(key, payload)
	return sig, payload, err
}

// VerifyBytes verifies a detached signature over payload.
func VerifyBytes(keys *KeyRing, payload []byte, sig Signature) error {
	if keys == nil {
		return ErrUnknownKey
	}
	if len(payload) == 0 {
		return ErrEmptyPayload
	}
	if sig.Algorithm != "" && sig.Algorithm != AlgorithmEd25519 {
		return fmt.Errorf("signing: unsupported algorithm %q", sig.Algorithm)
	}
	pk, ok := keys.Lookup(sig.KeyID)
	if !ok {
		return ErrUnknownKey
	}
	at := sig.SignedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if !pk.ValidAt(at) {
		return ErrExpiredKey
	}
	if !ed25519.Verify(pk.Public, payload, sig.Sig) {
		return ErrInvalidSig
	}
	return nil
}

// VerifyCanonical verifies a signature over the canonical form of v.
func VerifyCanonical(keys *KeyRing, v any, sig Signature) error {
	payload, err := CanonicalJSON(v)
	if err != nil {
		return err
	}
	return VerifyBytes(keys, payload, sig)
}

// KeyRing holds multiple trusted public keys for rotation windows.
type KeyRing struct {
	mu   sync.RWMutex
	keys map[KeyID]PublicKey
}

// NewKeyRing creates an empty ring.
func NewKeyRing() *KeyRing {
	return &KeyRing{keys: make(map[KeyID]PublicKey)}
}

// Add trusts a public key. Duplicate IDs with different material are rejected.
func (r *KeyRing) Add(k PublicKey) error {
	if len(k.Public) != ed25519.PublicKeySize {
		return fmt.Errorf("signing: invalid public key length")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys == nil {
		r.keys = make(map[KeyID]PublicKey)
	}
	if existing, ok := r.keys[k.ID]; ok {
		if !publicEqual(existing.Public, k.Public) {
			return ErrDuplicateKeyID
		}
	}
	r.keys[k.ID] = k
	return nil
}

// Retire marks a key as untrusted for new verification (rotation).
func (r *KeyRing) Retire(id KeyID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k, ok := r.keys[id]; ok {
		k.Retired = true
		r.keys[id] = k
	}
}

// Remove drops a key entirely.
func (r *KeyRing) Remove(id KeyID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.keys, id)
}

// Lookup returns a trusted key by ID.
func (r *KeyRing) Lookup(id KeyID) (PublicKey, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.keys[id]
	return k, ok
}

// Len returns the number of keys in the ring.
func (r *KeyRing) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.keys)
}

// IDs returns sorted key IDs.
func (r *KeyRing) IDs() []KeyID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]KeyID, 0, len(r.keys))
	for id := range r.keys {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func publicEqual(a, b ed25519.PublicKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// EncodeSignature returns a compact base64url encoding for transport.
func EncodeSignature(sig Signature) string {
	raw, _ := json.Marshal(sig)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeSignature parses EncodeSignature output.
func DecodeSignature(s string) (Signature, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Signature{}, err
	}
	var sig Signature
	if err := json.Unmarshal(raw, &sig); err != nil {
		return Signature{}, err
	}
	return sig, nil
}
