package shiftlock

import (
	"time"
)

// OperationID is a stable client-generated identifier for idempotent mutations.
// Retries with the same OperationID must return the prior successful result
// without advancing the fencing token or emitting duplicate history.
type OperationID string

// Empty reports whether the ID is unset (legacy non-idempotent path).
func (id OperationID) Empty() bool { return id == "" }

// Capabilities describes backend safety features. The coordinator validates
// Config.Policy against backend capabilities and refuses silent degradation.
type Capabilities struct {
	// AtomicCAS guarantees fencing-token CAS under concurrency.
	AtomicCAS bool `json:"atomic_cas"`

	// IdempotentMutations supports OperationID dedupe on state-changing ops.
	IdempotentMutations bool `json:"idempotent_mutations"`

	// WatchSupported indicates WatchClaim is meaningful (not poll-only stub).
	WatchSupported bool `json:"watch_supported"`

	// DurableStorage indicates ownership survives process restart.
	DurableStorage bool `json:"durable_storage"`

	// ExpireBeforeMutate clears expired leases before prepare/commit/release.
	ExpireBeforeMutate bool `json:"expire_before_mutate"`

	// RenewDuringReserved allows heartbeats while transfer is reserved.
	RenewDuringReserved bool `json:"renew_during_reserved"`

	// GlobalExclusive can guarantee single-owner across all clients of the store.
	GlobalExclusive bool `json:"global_exclusive"`

	// MaxFencingToken is the highest token the backend will issue (0 = MaxUint64-1).
	MaxFencingToken FencingToken `json:"max_fencing_token,omitempty"`
}

// Capabler is optionally implemented by backends that advertise capabilities.
type Capabler interface {
	Capabilities() Capabilities
}

// DefaultMemoryCapabilities are the in-process memory backend capabilities.
func DefaultMemoryCapabilities() Capabilities {
	return Capabilities{
		AtomicCAS:           true,
		IdempotentMutations: true,
		WatchSupported:      true,
		DurableStorage:      false,
		ExpireBeforeMutate:  true,
		RenewDuringReserved: true,
		GlobalExclusive:     true, // within one process
		MaxFencingToken:     FencingToken(^uint64(0) - 1),
	}
}

// ValidateCapabilities checks policy requirements against backend capabilities.
// Returns ErrPolicy on unsafe mismatch — never silently degrades.
func ValidateCapabilities(cfg Config, caps Capabilities) error {
	if cfg.Policy.RequireDurable && !caps.DurableStorage {
		return &Error{Op: "capabilities", Err: ErrPolicy, Message: "policy requires durable storage; backend is not durable"}
	}
	if cfg.Policy.RequireIdempotent && !caps.IdempotentMutations {
		return &Error{Op: "capabilities", Err: ErrPolicy, Message: "policy requires idempotent mutations; backend lacks OperationID support"}
	}
	if cfg.Policy.RequireGlobalExclusive && !caps.GlobalExclusive {
		return &Error{Op: "capabilities", Err: ErrPolicy, Message: "policy requires global exclusive ownership; backend cannot guarantee it"}
	}
	if !caps.AtomicCAS {
		return &Error{Op: "capabilities", Err: ErrPolicy, Message: "backend lacks atomic CAS; refusing to start"}
	}
	return nil
}

// MaxSafeFencingToken is the last token that may be issued before terminal overflow.
const MaxSafeFencingToken FencingToken = FencingToken(^uint64(0) - 1)

// reservationTTL returns TTL used for prepare/commit so reserved claims outlive TransferTimeout.
func reservationTTL(leaseTTL, transferTimeout time.Duration) time.Duration {
	if transferTimeout > leaseTTL {
		return transferTimeout + leaseTTL/3
	}
	return leaseTTL
}
