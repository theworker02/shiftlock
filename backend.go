package shiftlock

import (
	"context"
	"time"
)

// Clock provides time for deterministic tests.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	NewTicker(d time.Duration) Ticker
	Since(t time.Time) time.Duration
}

// Ticker is a cancelable periodic timer.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// realClock uses the wall clock.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (realClock) Since(t time.Time) time.Duration        { return time.Since(t) }
func (realClock) NewTicker(d time.Duration) Ticker {
	t := time.NewTicker(d)
	return &realTicker{t: t}
}

type realTicker struct{ t *time.Ticker }

func (r *realTicker) C() <-chan time.Time { return r.t.C }
func (r *realTicker) Stop()               { r.t.Stop() }

// Backend stores generation and claim ownership state.
// All ownership mutations MUST be atomic under concurrency: at most one
// generation may hold a committed fencing-token epoch for a claim.
type Backend interface {
	// RegisterGeneration records a new generation in joining/standby state.
	RegisterGeneration(ctx context.Context, gen Generation) error

	// AcquireClaim attempts to obtain ownership of claimName for generationID.
	// On success the returned record includes a new or existing fencing token.
	AcquireClaim(ctx context.Context, req AcquireRequest) (*ClaimRecord, error)

	// RenewClaim extends the lease for the current owner. Must fail with
	// ErrStaleToken / ErrNotOwner if the caller no longer owns the claim.
	RenewClaim(ctx context.Context, req RenewRequest) (*ClaimRecord, error)

	// PrepareTransfer reserves ownership transfer to successor without
	// advancing the fencing token yet.
	PrepareTransfer(ctx context.Context, req TransferRequest) (*ClaimRecord, error)

	// CommitTransfer atomically advances the fencing token and assigns
	// ownership to the successor.
	CommitTransfer(ctx context.Context, req CommitRequest) (*ClaimRecord, error)

	// AbortTransfer cancels a pending transfer and restores prior ownership.
	AbortTransfer(ctx context.Context, req AbortRequest) (*ClaimRecord, error)

	// ReleaseClaim releases ownership. Must refuse if fencing token is stale
	// so a partitioned former owner cannot release a newer owner's claim.
	ReleaseClaim(ctx context.Context, req ReleaseRequest) error

	// WatchClaim emits ownership change notifications. The channel is closed
	// when ctx is canceled or Close is called.
	WatchClaim(ctx context.Context, claimName string) (<-chan ClaimEvent, error)

	// UpdateGeneration persists generation state transitions.
	UpdateGeneration(ctx context.Context, gen Generation) error

	// GetClaim returns the current claim record or ErrClaimNotFound.
	GetClaim(ctx context.Context, claimName string) (*ClaimRecord, error)

	// GetGeneration returns a generation or ErrGenerationNotFound.
	GetGeneration(ctx context.Context, generationID string) (*Generation, error)

	// Close releases backend resources.
	Close() error
}

// AcquireRequest is the input to Backend.AcquireClaim.
type AcquireRequest struct {
	ClaimName      string
	GenerationID   string
	TTL            time.Duration
	AllowEmptyOnly bool // if true, only acquire when unowned
	OperationID    OperationID
}

// RenewRequest is the input to Backend.RenewClaim.
type RenewRequest struct {
	ClaimName    string
	GenerationID string
	Token        FencingToken
	TTL          time.Duration
	OperationID  OperationID
}

// TransferRequest is the input to Backend.PrepareTransfer.
type TransferRequest struct {
	ClaimName      string
	FromGeneration string
	ToGeneration   string
	Token          FencingToken
	TTL            time.Duration
	OperationID    OperationID
}

// CommitRequest is the input to Backend.CommitTransfer.
type CommitRequest struct {
	ClaimName      string
	FromGeneration string
	ToGeneration   string
	ExpectedToken  FencingToken
	TTL            time.Duration
	OperationID    OperationID
}

// AbortRequest is the input to Backend.AbortTransfer.
type AbortRequest struct {
	ClaimName      string
	FromGeneration string
	ToGeneration   string
	ExpectedToken  FencingToken
	OperationID    OperationID
}

// ReleaseRequest is the input to Backend.ReleaseClaim.
type ReleaseRequest struct {
	ClaimName    string
	GenerationID string
	Token        FencingToken
	OperationID  OperationID
}

// ClaimRecord is the durable claim state stored by a backend.
type ClaimRecord struct {
	Name             string
	OwnerGeneration  string
	FencingToken     FencingToken
	Phase            ClaimPhase
	AcquiredAt       time.Time
	ExpiresAt        time.Time
	PreviousOwner    string
	PendingSuccessor string
	DrainStatus      string
	TransferStatus   string
	LastHeartbeat    time.Time
	Reason           TransitionReason
	Version          uint64 // opaque CAS version for backends that need it
}

// ToOwnership converts a claim record to a public Ownership snapshot.
func (r *ClaimRecord) ToOwnership() Ownership {
	if r == nil {
		return Ownership{}
	}
	return Ownership{
		ClaimName:        r.Name,
		OwnerGeneration:  r.OwnerGeneration,
		FencingToken:     r.FencingToken,
		Phase:            r.Phase,
		AcquiredAt:       r.AcquiredAt,
		ExpiresAt:        r.ExpiresAt,
		PreviousOwner:    r.PreviousOwner,
		PendingSuccessor: r.PendingSuccessor,
		DrainStatus:      r.DrainStatus,
		TransferStatus:   r.TransferStatus,
		LastHeartbeat:    r.LastHeartbeat,
		Reason:           r.Reason,
	}
}

// ClaimEvent is emitted by WatchClaim.
type ClaimEvent struct {
	Claim  ClaimRecord
	Time   time.Time
	Reason TransitionReason
}
