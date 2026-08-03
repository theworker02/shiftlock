package secrets

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrRotationNotFound = errors.New("secrets: rotation not found")
	ErrRotationDup      = errors.New("secrets: duplicate rotation")
	ErrRotationBound    = errors.New("secrets: rotation bound exceeded")
	ErrSecretValue      = errors.New("secrets: secret values must not be recorded")
)

// DefaultMaxRotations bounds tracked rotation records.
const DefaultMaxRotations = 256

// RotationPhase is a secret rotation lifecycle stage.
type RotationPhase string

const (
	RotationPlanned    RotationPhase = "planned"
	RotationIssued     RotationPhase = "issued"
	RotationPropagated RotationPhase = "propagated"
	RotationVerified   RotationPhase = "verified"
	RotationRetired    RotationPhase = "retired"
	RotationFailed     RotationPhase = "failed"
)

// RotationRecord tracks secret references only — never values.
type RotationRecord struct {
	Name       string        `json:"name"`
	OldRef     string        `json:"old_ref"`
	NewRef     string        `json:"new_ref"`
	Phase      RotationPhase `json:"phase"`
	Actor      string        `json:"actor,omitempty"`
	Message    string        `json:"message,omitempty"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

// RotationLog records rotation workflow steps by opaque Ref strings.
type RotationLog struct {
	mu   sync.Mutex
	recs map[string]*RotationRecord
	max  int
}

// NewRotationLog creates an empty rotation log.
func NewRotationLog(max int) *RotationLog {
	if max <= 0 {
		max = DefaultMaxRotations
	}
	return &RotationLog{recs: make(map[string]*RotationRecord), max: max}
}

// Plan registers a rotation using opaque references only.
func (l *RotationLog) Plan(name string, oldRef, newRef Ref, actor string) error {
	if name == "" || oldRef.String() == "" || newRef.String() == "" {
		return ErrEmptyRef
	}
	// Reject anything that looks like an inline secret value rather than a ref.
	if looksLikeValue(oldRef.String()) || looksLikeValue(newRef.String()) {
		return ErrSecretValue
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.recs) >= l.max {
		return ErrRotationBound
	}
	if _, ok := l.recs[name]; ok {
		return ErrRotationDup
	}
	l.recs[name] = &RotationRecord{
		Name: name, OldRef: oldRef.String(), NewRef: newRef.String(),
		Phase: RotationPlanned, Actor: actor, UpdatedAt: time.Now().UTC(),
	}
	return nil
}

// Advance moves a rotation to the next recorded phase (references only).
func (l *RotationLog) Advance(name string, phase RotationPhase, message string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.recs[name]
	if !ok {
		return ErrRotationNotFound
	}
	r.Phase = phase
	r.Message = message
	r.UpdatedAt = time.Now().UTC()
	return nil
}

// Get returns a copy of the rotation record.
func (l *RotationLog) Get(name string) (RotationRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.recs[name]
	if !ok {
		return RotationRecord{}, ErrRotationNotFound
	}
	return *r, nil
}

// List returns all rotation records (references only).
func (l *RotationLog) List() []RotationRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]RotationRecord, 0, len(l.recs))
	for _, r := range l.recs {
		out = append(out, *r)
	}
	return out
}

func looksLikeValue(s string) bool {
	// Refs must use env:// or file://; anything else is treated as a value leak attempt.
	_, err := ParseRef(s)
	return err != nil
}
