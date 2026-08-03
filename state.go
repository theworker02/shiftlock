package shiftlock

// GenerationState is the lifecycle state of a process generation.
type GenerationState string

const (
	// StateJoining is the initial state after registration.
	StateJoining GenerationState = "joining"
	// StateStandby means the generation is registered and waiting.
	StateStandby GenerationState = "standby"
	// StatePreparing means the generation is running readiness gates.
	StatePreparing GenerationState = "preparing"
	// StateActive means the generation holds committed ownership.
	StateActive GenerationState = "active"
	// StateDraining means the generation is finishing in-flight work.
	StateDraining GenerationState = "draining"
	// StateTransferring means ownership transfer is reserved but not committed.
	StateTransferring GenerationState = "transferring"
	// StateRetired means the generation has permanently released ownership.
	StateRetired GenerationState = "retired"
	// StateFailed means the generation failed and must not reclaim work.
	StateFailed GenerationState = "failed"
)

// Valid reports whether s is a known generation state.
func (s GenerationState) Valid() bool {
	switch s {
	case StateJoining, StateStandby, StatePreparing, StateActive,
		StateDraining, StateTransferring, StateRetired, StateFailed:
		return true
	default:
		return false
	}
}

// Terminal reports whether the generation can no longer become active.
func (s GenerationState) Terminal() bool {
	return s == StateRetired || s == StateFailed
}

// ClaimPhase describes the ownership status of a named claim.
type ClaimPhase string

const (
	// ClaimUnowned means no generation currently owns the claim.
	ClaimUnowned ClaimPhase = "unowned"
	// ClaimOwned means a generation holds a committed fencing token.
	ClaimOwned ClaimPhase = "owned"
	// ClaimReserved means a transfer is prepared but not yet committed.
	ClaimReserved ClaimPhase = "reserved"
	// ClaimDraining means the owner is draining before transfer or release.
	ClaimDraining ClaimPhase = "draining"
)

// TransitionReason explains why a generation or claim changed state.
type TransitionReason string

const (
	ReasonRegistered       TransitionReason = "registered"
	ReasonReadinessPassed  TransitionReason = "readiness_passed"
	ReasonReadinessFailed  TransitionReason = "readiness_failed"
	ReasonAcquired         TransitionReason = "acquired"
	ReasonRenewed          TransitionReason = "renewed"
	ReasonDrainStarted     TransitionReason = "drain_started"
	ReasonDrainComplete    TransitionReason = "drain_complete"
	ReasonTransferPrepared TransitionReason = "transfer_prepared"
	ReasonTransferCommitted TransitionReason = "transfer_committed"
	ReasonTransferAborted  TransitionReason = "transfer_aborted"
	ReasonReleased         TransitionReason = "released"
	ReasonExpired          TransitionReason = "expired"
	ReasonFencedOut        TransitionReason = "fenced_out"
	ReasonRetired          TransitionReason = "retired"
	ReasonFailed           TransitionReason = "failed"
	ReasonClosed           TransitionReason = "closed"
	ReasonRollback         TransitionReason = "rollback"
	ReasonTimeout          TransitionReason = "timeout"
	ReasonPartition        TransitionReason = "partition"
)
