package shiftlock

import (
	"context"
	"sync"
)

// Claim is a named ownership unit coordinated across generations.
type Claim struct {
	name  string
	coord *Coordinator

	mu        sync.Mutex
	record    ClaimRecord
	lease     *Lease
	drain     *DrainGroup
	closed    bool
	acquiring bool
	renewing  bool
	acquireCh chan struct{} // closed when an in-flight acquire finishes
}

// Name returns the claim name.
func (c *Claim) Name() string { return c.name }

// Ownership returns a snapshot of current ownership.
func (c *Claim) Ownership() Ownership {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.record.ToOwnership()
}

// FencingToken returns the current known fencing token.
func (c *Claim) FencingToken() FencingToken {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.record.FencingToken
}

// DrainGroup returns the claim's drain group for in-flight work tracking.
func (c *Claim) DrainGroup() *DrainGroup {
	return c.drain
}

// TryAcquire attempts a single non-blocking acquire. Returns ErrClaimHeld if
// another generation owns the claim or a local acquire is already in flight.
func (c *Claim) TryAcquire(ctx context.Context) (*Lease, error) {
	return c.tryAcquire(ctx)
}

// WaitForOwnership blocks until this generation owns the claim or ctx ends.
func (c *Claim) WaitForOwnership(ctx context.Context) (*Lease, error) {
	if err := c.coord.checkOpen(); err != nil {
		return nil, wrapClaim("WaitForOwnership", c.name, err)
	}

	for {
		lease, err := c.tryAcquire(ctx)
		if err == nil {
			return lease, nil
		}
		if err != ErrClaimHeld && err != ErrNotOwner {
			return nil, wrapClaim("WaitForOwnership", c.name, err)
		}

		c.mu.Lock()
		if c.lease != nil && c.lease.Valid() {
			lease := c.lease
			c.mu.Unlock()
			return lease, nil
		}
		waitCh := c.acquireCh
		c.mu.Unlock()

		timer := c.coord.clock.After(c.coord.cfg.AcquireInterval)
		if waitCh != nil {
			select {
			case <-ctx.Done():
				return nil, wrapClaim("WaitForOwnership", c.name, ctx.Err())
			case <-c.coord.sup.Context().Done():
				return nil, wrapClaim("WaitForOwnership", c.name, ErrClosed)
			case <-waitCh:
				continue
			case <-timer:
			}
		} else {
			select {
			case <-ctx.Done():
				return nil, wrapClaim("WaitForOwnership", c.name, ctx.Err())
			case <-c.coord.sup.Context().Done():
				return nil, wrapClaim("WaitForOwnership", c.name, ErrClosed)
			case <-timer:
			}
		}
	}
}

func (c *Claim) tryAcquire(ctx context.Context) (*Lease, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	if c.lease != nil && c.lease.Valid() {
		lease := c.lease
		c.mu.Unlock()
		return lease, nil
	}
	// Serialize concurrent acquires so only one local lease is installed.
	if c.acquiring {
		c.mu.Unlock()
		return nil, ErrClaimHeld
	}
	c.acquiring = true
	if c.acquireCh == nil {
		c.acquireCh = make(chan struct{})
	}
	done := c.acquireCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.acquiring = false
		c.acquireCh = make(chan struct{})
		close(done)
		c.mu.Unlock()
	}()

	rec, err := c.coord.backend.AcquireClaim(ctx, AcquireRequest{
		ClaimName:    c.name,
		GenerationID: c.coord.gen.ID,
		TTL:          c.coord.cfg.LeaseTTL,
		OperationID:  c.coord.newOpID("acquire", c.name),
	})
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.closed {
		genID := c.coord.gen.ID
		token := rec.FencingToken
		c.mu.Unlock()
		// Best-effort release so backend ownership is not leaked.
		_ = c.coord.backend.ReleaseClaim(context.Background(), ReleaseRequest{
			ClaimName:    c.name,
			GenerationID: genID,
			Token:        token,
			OperationID:  c.coord.newOpID("release-compensate", c.name),
		})
		return nil, ErrClosed
	}
	if c.lease != nil && c.lease.Valid() {
		lease := c.lease
		c.mu.Unlock()
		return lease, nil
	}
	// Revoke any previous (invalid) local lease before installing a new one.
	if c.lease != nil {
		c.lease.revoke()
		c.lease = nil
	}
	c.record = *rec
	if !rec.ToOwnership().OwnedBy(c.coord.gen.ID) {
		c.mu.Unlock()
		return nil, ErrClaimHeld
	}

	lctx, cancel := context.WithCancel(c.coord.sup.Context())
	lease := &Lease{claim: c, token: rec.FencingToken, ctx: lctx, cancel: cancel}
	c.lease = lease
	c.mu.Unlock()

	c.coord.setState(StateActive, ReasonAcquired)
	c.coord.bus.emit(Event{
		Type:   EventClaimAcquired,
		Claim:  c.name,
		Token:  rec.FencingToken,
		Reason: ReasonAcquired,
	})
	c.coord.startRenewal(c)
	return lease, nil
}

// Release voluntarily releases ownership if this generation holds it.
func (c *Claim) Release(ctx context.Context) error {
	c.mu.Lock()
	if c.lease == nil {
		c.mu.Unlock()
		return wrapClaim("Release", c.name, ErrNotOwner)
	}
	token := c.lease.token
	genID := c.coord.gen.ID
	c.mu.Unlock()

	err := c.coord.backend.ReleaseClaim(ctx, ReleaseRequest{
		ClaimName:    c.name,
		GenerationID: genID,
		Token:        token,
		OperationID:  c.coord.newOpID("release", c.name),
	})
	if err != nil {
		return wrapClaim("Release", c.name, err)
	}
	c.revokeLease(ReasonReleased)
	c.coord.bus.emit(Event{
		Type:   EventClaimReleased,
		Claim:  c.name,
		Token:  token,
		Reason: ReasonReleased,
	})
	return nil
}

func (c *Claim) revokeLease(reason TransitionReason) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.renewing = false
	if c.lease != nil {
		c.lease.revoke()
		c.lease = nil
		c.coord.bus.emit(Event{
			Type:   EventLeaseRevoked,
			Claim:  c.name,
			Reason: reason,
		})
	}
}

func (c *Claim) markRenewing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.renewing {
		return false
	}
	c.renewing = true
	return true
}

func (c *Claim) clearRenewing() {
	c.mu.Lock()
	c.renewing = false
	c.mu.Unlock()
}

func (c *Claim) applyRecord(rec *ClaimRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.record = *rec
	own := rec.ToOwnership()
	if c.lease == nil {
		return
	}
	// Keep local lease during reserved/draining while this generation still Controls.
	if !own.Controls(c.coord.gen.ID) {
		c.renewing = false
		c.lease.revoke()
		c.lease = nil
		c.coord.bus.emit(Event{
			Type:   EventClaimLost,
			Claim:  c.name,
			Token:  rec.FencingToken,
			Reason: ReasonFencedOut,
		})
		return
	}
	c.lease.token = rec.FencingToken
}

// restoreLease applies a backend record and ensures a local lease exists (Abort path).
func (c *Claim) restoreLease(rec *ClaimRecord) {
	c.mu.Lock()
	c.record = *rec
	c.mu.Unlock()
	c.ensureLocalLease()
}

// ensureLocalLease re-arms a lease and renewal when this generation still
// controls the claim but the local lease was revoked (e.g. after Abort).
func (c *Claim) ensureLocalLease() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	own := c.record.ToOwnership()
	if !own.Controls(c.coord.gen.ID) {
		c.mu.Unlock()
		return
	}
	if c.lease != nil && c.lease.Valid() {
		c.lease.token = c.record.FencingToken
		needRenew := !c.renewing
		c.mu.Unlock()
		if needRenew {
			c.coord.startRenewal(c)
		}
		return
	}
	if c.lease != nil {
		c.lease.revoke()
		c.lease = nil
	}
	lctx, cancel := context.WithCancel(c.coord.sup.Context())
	lease := &Lease{claim: c, token: c.record.FencingToken, ctx: lctx, cancel: cancel}
	c.lease = lease
	c.mu.Unlock()
	c.coord.startRenewal(c)
}

func (c *Claim) currentLeaseToken() (FencingToken, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lease != nil && c.lease.Valid() {
		return c.lease.token, true
	}
	if c.record.ToOwnership().Controls(c.coord.gen.ID) {
		return c.record.FencingToken, true
	}
	return 0, false
}

func (c *Claim) close() {
	c.mu.Lock()
	c.closed = true
	c.renewing = false
	if c.lease != nil {
		c.lease.revoke()
		c.lease = nil
	}
	c.mu.Unlock()
	c.drain.Close()
}
