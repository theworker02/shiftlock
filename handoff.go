package shiftlock

import (
	"context"
	"sync"
)

// HandoffStatus tracks handoff progress.
type HandoffStatus string

const (
	HandoffPending      HandoffStatus = "pending"
	HandoffDraining     HandoffStatus = "draining"
	HandoffTransferring HandoffStatus = "transferring"
	HandoffCommitted    HandoffStatus = "committed"
	HandoffAborted      HandoffStatus = "aborted"
	HandoffFailed       HandoffStatus = "failed"
)

// Handoff coordinates graceful ownership transfer from this generation
// to a successor. Sequence: Drain → Transfer → Commit (or Abort).
//
// Normal sequence:
//  1. Successor registers and passes readiness
//  2. Current owner Drain() finishes in-flight work
//  3. Transfer(successorID) reserves the claim
//  4. Commit() advances fencing token; successor becomes active
//  5. Previous owner observes stale token and retires
//
// Rollback: Abort() before Commit restores prior ownership without
// advancing the fencing token.
type Handoff struct {
	coord  *Coordinator
	claims []*Claim

	mu             sync.Mutex
	status         HandoffStatus
	successorID    string
	drainDone      bool
	transferDone   bool
	tokens         map[string]FencingToken
	watcherStarted bool
	err            error
}

// Status returns the current handoff status.
func (h *Handoff) Status() HandoffStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

// Drain stops accepting new work and waits for in-flight ops on all claims.
func (h *Handoff) Drain(ctx context.Context) error {
	h.mu.Lock()
	if h.status != HandoffPending && h.status != HandoffDraining {
		h.mu.Unlock()
		return &Error{Op: "Handoff.Drain", Err: ErrInvalidState, Message: string(h.status)}
	}
	h.status = HandoffDraining
	h.mu.Unlock()

	_ = h.coord.setState(StateDraining, ReasonDrainStarted)
	h.coord.bus.emit(Event{Type: EventDrainStarted, Reason: ReasonDrainStarted})

	deadlineCh := make(chan struct{})
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-h.coord.clock.After(h.coord.cfg.DrainTimeout):
			close(deadlineCh)
			cancel()
		case <-dctx.Done():
		}
	}()

	for _, cl := range h.claims {
		cl.drain.SetDeadline(deadlineCh)
		if err := cl.drain.Wait(dctx); err != nil {
			h.coord.bus.emit(Event{Type: EventDrainFailed, Claim: cl.name, Err: err.Error()})
			h.fail(err)
			return wrap("Handoff.Drain", err)
		}
	}

	h.mu.Lock()
	h.drainDone = true
	h.mu.Unlock()
	h.coord.bus.emit(Event{Type: EventDrainCompleted, Reason: ReasonDrainComplete})
	return nil
}

// Transfer reserves ownership transfer to successorGenerationID for all
// owned claims. Drain must complete first unless force is implied by empty claims.
func (h *Handoff) Transfer(ctx context.Context, successorGenerationID string) error {
	if successorGenerationID == "" {
		return &Error{Op: "Handoff.Transfer", Err: ErrPolicy, Message: "successor required"}
	}
	h.mu.Lock()
	if !h.drainDone && h.status != HandoffDraining {
		// allow transfer after explicit drain
	}
	if h.status == HandoffCommitted || h.status == HandoffAborted {
		h.mu.Unlock()
		return &Error{Op: "Handoff.Transfer", Err: ErrInvalidState, Message: string(h.status)}
	}
	if !h.drainDone {
		h.mu.Unlock()
		if err := h.Drain(ctx); err != nil {
			return err
		}
		h.mu.Lock()
	}
	h.status = HandoffTransferring
	h.successorID = successorGenerationID
	h.tokens = make(map[string]FencingToken)
	h.mu.Unlock()

	_ = h.coord.setState(StateTransferring, ReasonTransferPrepared)
	ttl := reservationTTL(h.coord.cfg.LeaseTTL, h.coord.cfg.TransferTimeout)

	for _, cl := range h.claims {
		token, ok := cl.currentLeaseToken()
		if !ok {
			continue // not owned — skip
		}
		rec, err := h.coord.backend.PrepareTransfer(ctx, TransferRequest{
			ClaimName:      cl.name,
			FromGeneration: h.coord.gen.ID,
			ToGeneration:   successorGenerationID,
			Token:          token,
			TTL:            ttl,
			OperationID:    h.coord.newOpID("prepare", cl.name),
		})
		if err != nil {
			h.fail(err)
			// Cover already-prepared claims with timeout + best-effort abort.
			h.startTimeoutWatcher()
			h.abortPrepared(ctx)
			h.coord.bus.emit(Event{Type: EventTransferAborted, Claim: cl.name, Err: err.Error()})
			return wrapClaim("Handoff.Transfer", cl.name, err)
		}
		cl.applyRecord(rec)
		h.mu.Lock()
		h.tokens[cl.name] = rec.FencingToken
		h.mu.Unlock()
		h.coord.bus.emit(Event{
			Type: EventTransferPrepared, Claim: cl.name, Token: rec.FencingToken,
			Reason: ReasonTransferPrepared,
			Attrs:  map[string]string{"successor": successorGenerationID},
		})
	}

	h.mu.Lock()
	h.transferDone = true
	h.mu.Unlock()
	h.startTimeoutWatcher()
	return nil
}

// Commit advances fencing tokens and assigns ownership to the successor.
func (h *Handoff) Commit(ctx context.Context) error {
	h.mu.Lock()
	if !h.transferDone {
		h.mu.Unlock()
		return &Error{Op: "Handoff.Commit", Err: ErrNoTransfer, Message: "call Transfer first"}
	}
	if h.status == HandoffCommitted {
		h.mu.Unlock()
		return nil
	}
	if h.status == HandoffAborted {
		h.mu.Unlock()
		return ErrHandoffAborted
	}
	successor := h.successorID
	tokens := copyTokens(h.tokens)
	h.mu.Unlock()

	committed := make(map[string]struct{})
	for _, cl := range h.claims {
		token, ok := tokens[cl.name]
		if !ok {
			continue
		}
		rec, err := h.coord.backend.CommitTransfer(ctx, CommitRequest{
			ClaimName:      cl.name,
			FromGeneration: h.coord.gen.ID,
			ToGeneration:   successor,
			ExpectedToken:  token,
			TTL:            h.coord.cfg.LeaseTTL,
			OperationID:    h.coord.newOpID("commit", cl.name),
		})
		if err != nil {
			h.fail(err)
			// Abort remaining reserved claims; already-committed stay with successor.
			h.startTimeoutWatcher()
			h.abortUncommitted(ctx, committed)
			return wrapClaim("Handoff.Commit", cl.name, err)
		}
		cl.applyRecord(rec)
		cl.revokeLease(ReasonTransferCommitted)
		committed[cl.name] = struct{}{}
		h.mu.Lock()
		delete(h.tokens, cl.name)
		h.mu.Unlock()
		h.coord.bus.emit(Event{
			Type: EventTransferCommitted, Claim: cl.name, Token: rec.FencingToken,
			Reason: ReasonTransferCommitted,
		})
	}

	h.mu.Lock()
	h.status = HandoffCommitted
	h.mu.Unlock()
	_ = h.coord.setState(StateRetired, ReasonRetired)
	h.coord.bus.emit(Event{Type: EventHandoffCompleted, Reason: ReasonTransferCommitted})
	return nil
}

// Abort cancels a pending transfer and restores prior ownership.
func (h *Handoff) Abort(ctx context.Context) error {
	h.mu.Lock()
	if h.status == HandoffCommitted {
		h.mu.Unlock()
		return &Error{Op: "Handoff.Abort", Err: ErrInvalidState, Message: "already committed"}
	}
	successor := h.successorID
	tokens := copyTokens(h.tokens)
	h.status = HandoffAborted
	h.mu.Unlock()

	for _, cl := range h.claims {
		token, ok := tokens[cl.name]
		if !ok {
			continue
		}
		rec, err := h.coord.backend.AbortTransfer(ctx, AbortRequest{
			ClaimName:      cl.name,
			FromGeneration: h.coord.gen.ID,
			ToGeneration:   successor,
			ExpectedToken:  token,
			OperationID:    h.coord.newOpID("abort", cl.name),
		})
		if err != nil {
			h.coord.bus.emit(Event{Type: EventTransferAborted, Claim: cl.name, Err: err.Error()})
			return wrapClaim("Handoff.Abort", cl.name, err)
		}
		cl.restoreLease(rec)
		h.coord.bus.emit(Event{
			Type: EventTransferAborted, Claim: cl.name, Token: rec.FencingToken,
			Reason: ReasonTransferAborted,
		})
	}

	_ = h.coord.setState(StateActive, ReasonRollback)
	h.coord.bus.emit(Event{Type: EventHandoffFailed, Reason: ReasonTransferAborted})
	return nil
}

func (h *Handoff) fail(err error) {
	h.mu.Lock()
	h.status = HandoffFailed
	h.err = err
	h.mu.Unlock()
	h.coord.bus.emit(Event{Type: EventHandoffFailed, Err: err.Error(), Reason: ReasonFailed})
}

// abortPrepared best-effort aborts claims already recorded in h.tokens.
func (h *Handoff) abortPrepared(ctx context.Context) {
	h.mu.Lock()
	successor := h.successorID
	tokens := copyTokens(h.tokens)
	h.mu.Unlock()
	h.abortTokens(ctx, successor, tokens)
}

// abortUncommitted aborts reserved claims that were not yet committed.
func (h *Handoff) abortUncommitted(ctx context.Context, committed map[string]struct{}) {
	h.mu.Lock()
	successor := h.successorID
	tokens := copyTokens(h.tokens)
	h.mu.Unlock()
	for name := range committed {
		delete(tokens, name)
	}
	h.abortTokens(ctx, successor, tokens)
}

func (h *Handoff) abortTokens(ctx context.Context, successor string, tokens map[string]FencingToken) {
	if len(tokens) == 0 {
		return
	}
	for _, cl := range h.claims {
		token, ok := tokens[cl.name]
		if !ok {
			continue
		}
		rec, err := h.coord.backend.AbortTransfer(ctx, AbortRequest{
			ClaimName:      cl.name,
			FromGeneration: h.coord.gen.ID,
			ToGeneration:   successor,
			ExpectedToken:  token,
			OperationID:    h.coord.newOpID("abort-compensate", cl.name),
		})
		if err != nil {
			h.coord.bus.emit(Event{Type: EventTransferAborted, Claim: cl.name, Err: err.Error()})
			continue
		}
		cl.restoreLease(rec)
		h.mu.Lock()
		delete(h.tokens, cl.name)
		h.mu.Unlock()
	}
}

func copyTokens(m map[string]FencingToken) map[string]FencingToken {
	out := make(map[string]FencingToken, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (h *Handoff) startTimeoutWatcher() {
	h.mu.Lock()
	if h.watcherStarted {
		h.mu.Unlock()
		return
	}
	h.watcherStarted = true
	h.mu.Unlock()

	h.coord.sup.GoNamed("handoff-timeout", func(ctx context.Context) {
		select {
		case <-ctx.Done():
			return
		case <-h.coord.clock.After(h.coord.cfg.TransferTimeout):
			h.mu.Lock()
			status := h.status
			hasPending := len(h.tokens) > 0
			h.mu.Unlock()
			// Cover transferring and failed-with-reservations paths.
			if status == HandoffTransferring || (status == HandoffFailed && hasPending) {
				_ = h.Abort(context.Background())
			}
		}
	})
}
