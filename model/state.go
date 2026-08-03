package model

// GenState mirrors shiftlock generation states.
type GenState string

const (
	GenJoining      GenState = "joining"
	GenStandby      GenState = "standby"
	GenPreparing    GenState = "preparing"
	GenActive       GenState = "active"
	GenDraining     GenState = "draining"
	GenTransferring GenState = "transferring"
	GenRetired      GenState = "retired"
	GenFailed       GenState = "failed"
)

// ClaimPhase mirrors shiftlock claim phases.
type ClaimPhase string

const (
	PhaseUnowned  ClaimPhase = "unowned"
	PhaseOwned    ClaimPhase = "owned"
	PhaseReserved ClaimPhase = "reserved"
	PhaseDraining ClaimPhase = "draining"
)

// Generation is model state for one process generation.
type Generation struct {
	ID         string
	State      GenState
	Paused     bool
	Connected  bool
	Ready      bool
	Draining   bool
}

// Claim is model ownership state.
type Claim struct {
	Name             string
	Owner            string
	Token            uint64
	Phase            ClaimPhase
	PendingSuccessor string
	ExpiresAt        int64 // model time units
	TransferDeadline int64
	LastOpID         string
	OpResults        map[string]opSnap // idempotency
}

type opSnap struct {
	Owner string
	Token uint64
	Phase ClaimPhase
}

// World is the deterministic protocol world.
type World struct {
	Now            int64
	LeaseTTL       int64
	TransferTO     int64
	Gens           map[string]*Generation
	Claims         map[string]*Claim
	ProtectedEpoch map[string]uint64 // last accepted fencing token per claim (resource)
	History        []Action
}
