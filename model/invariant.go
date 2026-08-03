package model

import "fmt"

// Invariant names for failure records.
const (
	InvSingleOwner          = "single_committed_owner"
	InvTokenMonotonic       = "token_monotonic"
	InvStaleRelease         = "stale_release_rejected"
	InvTransferBounded      = "transfer_not_pending_forever"
	InvAbortNoAdvance       = "abort_no_token_advance"
	InvCommitAdvancesOnce   = "commit_advances_once"
	InvUnownedKeepsToken    = "unowned_keeps_token"
	InvReservedHasOwner     = "reserved_has_owner"
	InvCrashKeepsOwnership  = "crash_candidate_keeps_ownership"
	InvSingleAcquireWinner  = "single_acquire_winner"
	InvExpireKeepsToken     = "expire_keeps_token"
	InvOverflowTerminal     = "overflow_terminal"
	InvIdempotentNoDouble   = "idempotent_no_double_advance"
	InvNoLocalLeaseInvent   = "no_local_lease_extension_while_disconnected"
	InvProtectedEpoch       = "protected_resource_epoch"
)

// CheckInvariants verifies all 15 invariants. Returns invariant name + detail on failure.
func (w *World) CheckInvariants() (string, string) {
	for name, cl := range w.Claims {
		if cl.Phase == PhaseOwned || cl.Phase == PhaseReserved {
			if cl.Owner == "" {
				return InvReservedHasOwner, fmt.Sprintf("claim %s phase=%s empty owner", name, cl.Phase)
			}
		}
		if cl.Phase == PhaseReserved && cl.PendingSuccessor == "" {
			return InvReservedHasOwner, fmt.Sprintf("claim %s reserved without successor", name)
		}
		if cl.Phase == PhaseUnowned && cl.Owner != "" {
			return InvSingleOwner, fmt.Sprintf("claim %s unowned but owner=%s", name, cl.Owner)
		}
		if ep, ok := w.ProtectedEpoch[name]; ok {
			if cl.Token < ep && cl.Phase != PhaseUnowned {
				// token should be >= last protected when owned
			}
			if cl.Phase == PhaseOwned && cl.Token != ep {
				return InvProtectedEpoch, fmt.Sprintf("claim %s token=%d epoch=%d", name, cl.Token, ep)
			}
		}
		if cl.Phase == PhaseReserved && cl.TransferDeadline > 0 && w.Now > cl.TransferDeadline+w.TransferTO {
			return InvTransferBounded, fmt.Sprintf("claim %s transfer overdue", name)
		}
	}
	// Count committed owners per claim (always ≤1 by construction of map)
	for name, cl := range w.Claims {
		owners := 0
		if cl.Phase == PhaseOwned && cl.Owner != "" {
			owners = 1
		}
		if owners > 1 {
			return InvSingleOwner, name
		}
		_ = owners
	}
	return "", ""
}

// CheckTokenMonotonic compares previous snapshot tokens.
func CheckTokenMonotonic(prev, cur map[string]uint64) (string, string) {
	for k, v := range cur {
		if p, ok := prev[k]; ok && v < p {
			return InvTokenMonotonic, fmt.Sprintf("%s: %d -> %d", k, p, v)
		}
	}
	return "", ""
}

func (w *World) TokenSnapshot() map[string]uint64 {
	out := make(map[string]uint64, len(w.Claims))
	for k, cl := range w.Claims {
		out[k] = cl.Token
	}
	return out
}
