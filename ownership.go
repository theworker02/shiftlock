package shiftlock

import (
	"context"
	"time"
)

// Ownership describes committed or reserved ownership of a claim.
type Ownership struct {
	ClaimName        string           `json:"claim_name"`
	OwnerGeneration  string           `json:"owner_generation,omitempty"`
	FencingToken     FencingToken     `json:"fencing_token"`
	Phase            ClaimPhase       `json:"phase"`
	AcquiredAt       time.Time        `json:"acquired_at,omitempty"`
	ExpiresAt        time.Time        `json:"expires_at,omitempty"`
	PreviousOwner    string           `json:"previous_owner,omitempty"`
	PendingSuccessor string           `json:"pending_successor,omitempty"`
	DrainStatus      string           `json:"drain_status,omitempty"`
	TransferStatus   string           `json:"transfer_status,omitempty"`
	LastHeartbeat    time.Time        `json:"last_heartbeat,omitempty"`
	Reason           TransitionReason `json:"reason,omitempty"`
}

// Clone returns a shallow copy.
func (o Ownership) Clone() Ownership { return o }

// OwnedBy reports whether genID is the committed owner (phase owned).
// During a reserved transfer this returns false so workers stop accepting new
// protected work. Use Controls for heartbeat/renew eligibility.
func (o Ownership) OwnedBy(genID string) bool {
	return o.Phase == ClaimOwned && o.OwnerGeneration == genID && !o.FencingToken.Zero()
}

// Controls reports whether genID still controls the claim for renewals
// (owned or reserved as current owner with a non-zero token).
func (o Ownership) Controls(genID string) bool {
	if o.OwnerGeneration != genID || o.FencingToken.Zero() {
		return false
	}
	return o.Phase == ClaimOwned || o.Phase == ClaimReserved || o.Phase == ClaimDraining
}

// Lease is a live ownership grant. Canceling lease.Context() means
// ownership was lost or the coordinator is shutting down.
type Lease struct {
	claim  *Claim
	token  FencingToken
	ctx    context.Context
	cancel context.CancelFunc
}

// Context is canceled when ownership is lost, the claim is closed,
// or the coordinator shuts down.
func (l *Lease) Context() context.Context {
	if l == nil {
		return context.Background()
	}
	return l.ctx
}

// FencingToken returns the fencing token for this lease epoch.
func (l *Lease) FencingToken() FencingToken {
	if l == nil {
		return 0
	}
	return l.token
}

// Ownership returns a snapshot of the current ownership record.
func (l *Lease) Ownership() Ownership {
	if l == nil || l.claim == nil {
		return Ownership{}
	}
	return l.claim.Ownership()
}

// Valid reports whether the lease context is still active.
func (l *Lease) Valid() bool {
	if l == nil {
		return false
	}
	select {
	case <-l.ctx.Done():
		return false
	default:
		return true
	}
}

func (l *Lease) revoke() {
	if l != nil && l.cancel != nil {
		l.cancel()
	}
}
