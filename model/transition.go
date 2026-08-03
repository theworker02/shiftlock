package model

import "fmt"

// Apply executes one action against the world. Returns error on illegal transition.
func (w *World) Apply(a Action) error {
	w.History = append(w.History, a)
	switch a.Type {
	case ActRegisterGeneration:
		if _, ok := w.Gens[a.Generation]; ok {
			return nil
		}
		w.Gens[a.Generation] = &Generation{ID: a.Generation, State: GenStandby, Connected: true}
	case ActPassReadiness:
		g := w.Gens[a.Generation]
		if g == nil {
			return fmt.Errorf("unknown gen")
		}
		g.Ready = true
		g.State = GenStandby
	case ActFailReadiness:
		g := w.Gens[a.Generation]
		if g == nil {
			return fmt.Errorf("unknown gen")
		}
		g.Ready = false
		g.State = GenFailed
	case ActRequestClaim:
		return w.requestClaim(a)
	case ActRenewClaim:
		return w.renewClaim(a)
	case ActReleaseClaim:
		return w.releaseClaim(a)
	case ActBeginDrain:
		g := w.Gens[a.Generation]
		if g == nil {
			return fmt.Errorf("unknown gen")
		}
		g.Draining = true
		g.State = GenDraining
	case ActCompleteDrain:
		g := w.Gens[a.Generation]
		if g != nil {
			g.Draining = false
		}
	case ActPrepareTransfer:
		return w.prepare(a)
	case ActCommitTransfer:
		return w.commit(a)
	case ActAbortTransfer:
		return w.abort(a)
	case ActPause:
		if g := w.Gens[a.Generation]; g != nil {
			g.Paused = true
		}
	case ActResume:
		if g := w.Gens[a.Generation]; g != nil {
			g.Paused = false
		}
	case ActDisconnect:
		if g := w.Gens[a.Generation]; g != nil {
			g.Connected = false
		}
	case ActReconnect:
		if g := w.Gens[a.Generation]; g != nil {
			g.Connected = true
		}
	case ActExpireLease:
		return w.expire(a.Claim)
	case ActCrashOwner:
		return w.crashOwner(a.Claim)
	case ActCrashCandidate:
		cl := w.Claims[a.Claim]
		if cl != nil && cl.Phase == PhaseReserved {
			cl.TransferDeadline = w.Now // force timeout path
		}
	case ActRestartBackend:
		// Durable model: claims survive; in-memory watchers cleared (noop here).
	case ActForceRevoke:
		return w.forceRevoke(a)
	case ActAdvanceTime:
		w.Now += int64(a.Delta)
		w.sweepExpired()
	default:
		return fmt.Errorf("unknown action %s", a.Type)
	}
	return nil
}

func (w *World) claim(name string) *Claim {
	cl, ok := w.Claims[name]
	if !ok {
		cl = &Claim{Name: name, Phase: PhaseUnowned, OpResults: map[string]opSnap{}}
		w.Claims[name] = cl
	}
	if cl.OpResults == nil {
		cl.OpResults = map[string]opSnap{}
	}
	return cl
}

func (w *World) requestClaim(a Action) error {
	cl := w.claim(a.Claim)
	if a.OpID != "" {
		if s, ok := cl.OpResults[a.OpID]; ok {
			cl.Owner, cl.Token, cl.Phase = s.Owner, s.Token, s.Phase
			return nil
		}
	}
	w.expireIfNeeded(cl)
	if cl.Phase == PhaseOwned || cl.Phase == PhaseReserved {
		if cl.Owner == a.Generation {
			cl.ExpiresAt = w.Now + w.LeaseTTL
			return nil
		}
		return fmt.Errorf("held")
	}
	if cl.Token >= ^uint64(0)-1 {
		return fmt.Errorf("overflow")
	}
	cl.Token++
	cl.Owner = a.Generation
	cl.Phase = PhaseOwned
	cl.ExpiresAt = w.Now + w.LeaseTTL
	cl.PendingSuccessor = ""
	if g := w.Gens[a.Generation]; g != nil {
		g.State = GenActive
	}
	w.ProtectedEpoch[a.Claim] = cl.Token
	if a.OpID != "" {
		cl.OpResults[a.OpID] = opSnap{Owner: cl.Owner, Token: cl.Token, Phase: cl.Phase}
	}
	return nil
}

func (w *World) renewClaim(a Action) error {
	cl := w.claim(a.Claim)
	if !cl.controls(a.Generation) {
		return fmt.Errorf("not owner")
	}
	if !w.Gens[a.Generation].Connected || w.Gens[a.Generation].Paused {
		return fmt.Errorf("disconnected")
	}
	cl.ExpiresAt = w.Now + w.LeaseTTL
	return nil
}

func (w *World) releaseClaim(a Action) error {
	cl := w.claim(a.Claim)
	if a.OpID != "" {
		if _, ok := cl.OpResults[a.OpID]; ok {
			return nil
		}
	}
	if cl.Owner != a.Generation {
		return fmt.Errorf("not owner")
	}
	// Stale token check via ProtectedEpoch
	if cl.Token != w.ProtectedEpoch[a.Claim] && w.ProtectedEpoch[a.Claim] > cl.Token {
		return fmt.Errorf("stale")
	}
	cl.Owner = ""
	cl.Phase = PhaseUnowned
	cl.PendingSuccessor = ""
	if a.OpID != "" {
		cl.OpResults[a.OpID] = opSnap{Phase: PhaseUnowned, Token: cl.Token}
	}
	return nil
}

func (w *World) prepare(a Action) error {
	cl := w.claim(a.Claim)
	w.expireIfNeeded(cl)
	if cl.Owner != a.Generation || cl.Phase == PhaseUnowned {
		return fmt.Errorf("not owner")
	}
	cl.Phase = PhaseReserved
	cl.PendingSuccessor = a.Successor
	cl.ExpiresAt = w.Now + w.LeaseTTL
	if w.TransferTO > w.LeaseTTL {
		cl.ExpiresAt = w.Now + w.TransferTO
	}
	cl.TransferDeadline = w.Now + w.TransferTO
	if g := w.Gens[a.Generation]; g != nil {
		g.State = GenTransferring
	}
	return nil
}

func (w *World) commit(a Action) error {
	cl := w.claim(a.Claim)
	if a.OpID != "" {
		if s, ok := cl.OpResults[a.OpID]; ok {
			cl.Owner, cl.Token, cl.Phase = s.Owner, s.Token, s.Phase
			return nil
		}
	}
	if cl.Phase != PhaseReserved || cl.Owner != a.Generation || cl.PendingSuccessor != a.Successor {
		// Idempotent success if already committed
		if cl.Phase == PhaseOwned && cl.Owner == a.Successor {
			return nil
		}
		return fmt.Errorf("no transfer")
	}
	if cl.Token >= ^uint64(0)-1 {
		return fmt.Errorf("overflow")
	}
	cl.Token++
	cl.Owner = a.Successor
	cl.Phase = PhaseOwned
	cl.PendingSuccessor = ""
	cl.ExpiresAt = w.Now + w.LeaseTTL
	w.ProtectedEpoch[a.Claim] = cl.Token
	if g := w.Gens[a.Generation]; g != nil {
		g.State = GenRetired
	}
	if g := w.Gens[a.Successor]; g != nil {
		g.State = GenActive
	}
	if a.OpID != "" {
		cl.OpResults[a.OpID] = opSnap{Owner: cl.Owner, Token: cl.Token, Phase: cl.Phase}
	}
	return nil
}

func (w *World) abort(a Action) error {
	cl := w.claim(a.Claim)
	if cl.Phase != PhaseReserved {
		if cl.Owner == a.Generation && cl.Phase == PhaseOwned {
			return nil
		}
		return fmt.Errorf("no transfer")
	}
	if cl.Owner != a.Generation {
		return fmt.Errorf("not owner")
	}
	tok := cl.Token
	cl.PendingSuccessor = ""
	cl.Phase = PhaseOwned
	cl.Token = tok // no advance
	if g := w.Gens[a.Generation]; g != nil {
		g.State = GenActive
	}
	return nil
}

func (w *World) expire(name string) error {
	cl := w.claim(name)
	return w.expireIfNeeded(cl)
}

func (w *World) expireIfNeeded(cl *Claim) error {
	if cl.Phase == PhaseUnowned {
		return nil
	}
	if cl.ExpiresAt > 0 && w.Now > cl.ExpiresAt {
		cl.Owner = ""
		cl.PendingSuccessor = ""
		cl.Phase = PhaseUnowned
	}
	if cl.Phase == PhaseReserved && cl.TransferDeadline > 0 && w.Now > cl.TransferDeadline {
		// auto-abort semantics: restore owner if we still have Previous — model keeps Owner during reserved
		cl.PendingSuccessor = ""
		cl.Phase = PhaseOwned
	}
	return nil
}

func (w *World) sweepExpired() {
	for _, cl := range w.Claims {
		_ = w.expireIfNeeded(cl)
	}
}

func (w *World) crashOwner(claim string) error {
	cl := w.claim(claim)
	if g := w.Gens[cl.Owner]; g != nil {
		g.Connected = false
		g.State = GenFailed
	}
	// Lease will expire via time advance; do not clear immediately.
	return nil
}

func (w *World) forceRevoke(a Action) error {
	cl := w.claim(a.Claim)
	// Requires matching expected owner — never blind unlock.
	if cl.Owner != a.Generation {
		return fmt.Errorf("expected owner mismatch")
	}
	cl.Owner = ""
	cl.Phase = PhaseUnowned
	cl.PendingSuccessor = ""
	return nil
}

func (c *Claim) controls(gen string) bool {
	if c.Owner != gen {
		return false
	}
	return c.Phase == PhaseOwned || c.Phase == PhaseReserved || c.Phase == PhaseDraining
}
