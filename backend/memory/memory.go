package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/theworker02/shiftlock"
)

// Option configures the memory backend.
type Option func(*Backend)

// WithClock sets a custom clock.
func WithClock(c shiftlock.Clock) Option {
	return func(b *Backend) { b.clock = c }
}

// WithPartitionSimulation enables network partition: when partitioned,
// mutating calls return ErrBackend.
func WithPartitionSimulation() Option {
	return func(b *Backend) { b.partitionEnabled = true }
}

// Backend is an in-memory ownership store for tests and single-process use.
type Backend struct {
	mu sync.Mutex

	clock shiftlock.Clock

	gens   map[string]*shiftlock.Generation
	claims map[string]*shiftlock.ClaimRecord

	watchers map[string][]*watchChan

	// Idempotent operation results keyed by OperationID.
	ops map[shiftlock.OperationID]*opResult

	// Fault injection
	partitionEnabled bool
	partitioned      bool
	failNext         error
	delay            time.Duration
	crashOnCommit bool
	failPrepareAt int // 1-based PrepareTransfer call to fail; 0 = off
	prepareCount  int

	closed bool
}

type watchChan struct {
	ch     chan shiftlock.ClaimEvent
	closed bool
}

type opResult struct {
	rec *shiftlock.ClaimRecord
	err error
}

// New creates a memory backend.
func New(opts ...Option) *Backend {
	b := &Backend{
		clock:    realClock{},
		gens:     make(map[string]*shiftlock.Generation),
		claims:   make(map[string]*shiftlock.ClaimRecord),
		watchers: make(map[string][]*watchChan),
		ops:      make(map[shiftlock.OperationID]*opResult),
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Capabilities implements shiftlock.Capabler.
func (b *Backend) Capabilities() shiftlock.Capabilities {
	return shiftlock.DefaultMemoryCapabilities()
}

func (b *Backend) recallOp(id shiftlock.OperationID) (*shiftlock.ClaimRecord, error, bool) {
	if id.Empty() {
		return nil, nil, false
	}
	if r, ok := b.ops[id]; ok {
		if r.rec != nil {
			cp := *r.rec
			return &cp, r.err, true
		}
		return nil, r.err, true
	}
	return nil, nil, false
}

func (b *Backend) storeOp(id shiftlock.OperationID, rec *shiftlock.ClaimRecord, err error) {
	if id.Empty() {
		return
	}
	var cp *shiftlock.ClaimRecord
	if rec != nil {
		x := *rec
		cp = &x
	}
	b.ops[id] = &opResult{rec: cp, err: err}
}

func advanceToken(cur shiftlock.FencingToken) (shiftlock.FencingToken, error) {
	if cur >= shiftlock.MaxSafeFencingToken {
		return cur, shiftlock.ErrTokenOverflow
	}
	return cur + 1, nil
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (realClock) Since(t time.Time) time.Duration        { return time.Since(t) }
func (realClock) NewTicker(d time.Duration) shiftlock.Ticker {
	t := time.NewTicker(d)
	return realTicker{t}
}

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

// SetPartition toggles partition mode (mutations fail).
func (b *Backend) SetPartition(on bool) {
	b.mu.Lock()
	b.partitioned = on
	b.mu.Unlock()
}

// FailNext causes the next mutating call to return err.
func (b *Backend) FailNext(err error) {
	b.mu.Lock()
	b.failNext = err
	b.mu.Unlock()
}

// SetDelay adds artificial delay to mutating calls.
func (b *Backend) SetDelay(d time.Duration) {
	b.mu.Lock()
	b.delay = d
	b.mu.Unlock()
}

// CrashOnCommit simulates a crash after prepare but before durable commit visibility.
func (b *Backend) CrashOnCommit(v bool) {
	b.mu.Lock()
	b.crashOnCommit = v
	b.mu.Unlock()
}

// FailPrepareAt fails the n-th PrepareTransfer call (1-based) with ErrBackend.
// Useful for multi-claim partial Transfer tests. Pass 0 to disable.
func (b *Backend) FailPrepareAt(n int) {
	b.mu.Lock()
	b.failPrepareAt = n
	b.prepareCount = 0
	b.mu.Unlock()
}

func (b *Backend) beforeMutate(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return shiftlock.ErrClosed
	}
	if b.partitionEnabled && b.partitioned {
		b.mu.Unlock()
		return &shiftlock.Error{Op: "memory", Err: shiftlock.ErrBackend, Message: "partitioned", Reason: shiftlock.ReasonPartition}
	}
	err := b.failNext
	b.failNext = nil
	delay := b.delay
	b.mu.Unlock()
	if err != nil {
		return err
	}
	if delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.clock.After(delay):
		}
	}
	return nil
}

func (b *Backend) RegisterGeneration(ctx context.Context, gen shiftlock.Generation) error {
	if err := b.beforeMutate(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := gen
	b.gens[gen.ID] = &cp
	return nil
}

func (b *Backend) UpdateGeneration(ctx context.Context, gen shiftlock.Generation) error {
	if err := b.beforeMutate(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := gen
	b.gens[gen.ID] = &cp
	return nil
}

func (b *Backend) GetGeneration(ctx context.Context, generationID string) (*shiftlock.Generation, error) {
	_ = ctx
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.gens[generationID]
	if !ok {
		return nil, shiftlock.ErrGenerationNotFound
	}
	cp := *g
	return &cp, nil
}

func (b *Backend) GetClaim(ctx context.Context, claimName string) (*shiftlock.ClaimRecord, error) {
	_ = ctx
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(claimName)
	cl, ok := b.claims[claimName]
	if !ok {
		return nil, shiftlock.ErrClaimNotFound
	}
	cp := *cl
	return &cp, nil
}

func (b *Backend) expireLocked(claimName string) {
	cl, ok := b.claims[claimName]
	if !ok {
		return
	}
	now := b.clock.Now()
	if cl.Phase == shiftlock.ClaimOwned || cl.Phase == shiftlock.ClaimDraining || cl.Phase == shiftlock.ClaimReserved {
		if !cl.ExpiresAt.IsZero() && now.After(cl.ExpiresAt) {
			// Expire: clear owner but keep fencing token (never decrease).
			cl.PreviousOwner = cl.OwnerGeneration
			cl.OwnerGeneration = ""
			cl.PendingSuccessor = ""
			cl.Phase = shiftlock.ClaimUnowned
			cl.DrainStatus = ""
			cl.TransferStatus = ""
			cl.Reason = shiftlock.ReasonExpired
			cl.Version++
		}
	}
}

func (b *Backend) AcquireClaim(ctx context.Context, req shiftlock.AcquireRequest) (*shiftlock.ClaimRecord, error) {
	if err := b.beforeMutate(ctx); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if rec, err, ok := b.recallOp(req.OperationID); ok {
		return rec, err
	}
	b.expireLocked(req.ClaimName)

	now := b.clock.Now()
	cl, ok := b.claims[req.ClaimName]
	if !ok {
		cl = &shiftlock.ClaimRecord{
			Name:         req.ClaimName,
			Phase:        shiftlock.ClaimUnowned,
			FencingToken: 0,
		}
		b.claims[req.ClaimName] = cl
	}

	if cl.Phase == shiftlock.ClaimOwned || cl.Phase == shiftlock.ClaimReserved || cl.Phase == shiftlock.ClaimDraining {
		if cl.OwnerGeneration == req.GenerationID {
			cl.ExpiresAt = now.Add(req.TTL)
			cl.LastHeartbeat = now
			cl.Reason = shiftlock.ReasonRenewed
			cp := *cl
			b.storeOp(req.OperationID, &cp, nil)
			return &cp, nil
		}
		cp := *cl
		b.storeOp(req.OperationID, &cp, shiftlock.ErrClaimHeld)
		return &cp, shiftlock.ErrClaimHeld
	}

	next, err := advanceToken(cl.FencingToken)
	if err != nil {
		b.storeOp(req.OperationID, nil, err)
		return nil, &shiftlock.Error{Op: "AcquireClaim", Err: err, Message: "claim unavailable due to token overflow"}
	}
	cl.PreviousOwner = cl.OwnerGeneration
	cl.OwnerGeneration = req.GenerationID
	cl.FencingToken = next
	cl.Phase = shiftlock.ClaimOwned
	cl.AcquiredAt = now
	cl.ExpiresAt = now.Add(req.TTL)
	cl.LastHeartbeat = now
	cl.PendingSuccessor = ""
	cl.TransferStatus = ""
	cl.DrainStatus = ""
	cl.Reason = shiftlock.ReasonAcquired
	cl.Version++
	cp := *cl
	b.notifyLocked(cp, shiftlock.ReasonAcquired)
	b.storeOp(req.OperationID, &cp, nil)
	return &cp, nil
}

func (b *Backend) RenewClaim(ctx context.Context, req shiftlock.RenewRequest) (*shiftlock.ClaimRecord, error) {
	if err := b.beforeMutate(ctx); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if rec, err, ok := b.recallOp(req.OperationID); ok {
		return rec, err
	}
	b.expireLocked(req.ClaimName)

	cl, ok := b.claims[req.ClaimName]
	if !ok {
		return nil, shiftlock.ErrClaimNotFound
	}
	if cl.OwnerGeneration != req.GenerationID {
		return nil, shiftlock.ErrNotOwner
	}
	if cl.FencingToken != req.Token {
		return nil, shiftlock.ErrStaleToken
	}
	if cl.Phase != shiftlock.ClaimOwned && cl.Phase != shiftlock.ClaimDraining && cl.Phase != shiftlock.ClaimReserved {
		return nil, shiftlock.ErrNotOwner
	}
	now := b.clock.Now()
	cl.ExpiresAt = now.Add(req.TTL)
	cl.LastHeartbeat = now
	cl.Reason = shiftlock.ReasonRenewed
	cl.Version++
	cp := *cl
	b.storeOp(req.OperationID, &cp, nil)
	return &cp, nil
}

func (b *Backend) PrepareTransfer(ctx context.Context, req shiftlock.TransferRequest) (*shiftlock.ClaimRecord, error) {
	if err := b.beforeMutate(ctx); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prepareCount++
	if b.failPrepareAt > 0 && b.prepareCount == b.failPrepareAt {
		b.failPrepareAt = 0
		return nil, &shiftlock.Error{Op: "PrepareTransfer", Err: shiftlock.ErrBackend, Message: "injected prepare failure"}
	}
	if rec, err, ok := b.recallOp(req.OperationID); ok {
		return rec, err
	}
	b.expireLocked(req.ClaimName)

	cl, ok := b.claims[req.ClaimName]
	if !ok {
		return nil, shiftlock.ErrClaimNotFound
	}
	if cl.OwnerGeneration != req.FromGeneration {
		return nil, shiftlock.ErrNotOwner
	}
	if cl.FencingToken != req.Token {
		return nil, shiftlock.ErrStaleToken
	}
	if cl.Phase == shiftlock.ClaimReserved && cl.PendingSuccessor != "" && cl.PendingSuccessor != req.ToGeneration {
		return nil, shiftlock.ErrConcurrentTransfer
	}
	// Idempotent re-prepare same successor
	if cl.Phase == shiftlock.ClaimReserved && cl.PendingSuccessor == req.ToGeneration {
		cp := *cl
		b.storeOp(req.OperationID, &cp, nil)
		return &cp, nil
	}
	now := b.clock.Now()
	cl.Phase = shiftlock.ClaimReserved
	cl.PendingSuccessor = req.ToGeneration
	cl.TransferStatus = "prepared"
	cl.DrainStatus = "complete"
	cl.ExpiresAt = now.Add(req.TTL)
	cl.LastHeartbeat = now
	cl.Reason = shiftlock.ReasonTransferPrepared
	cl.Version++
	cp := *cl
	b.notifyLocked(cp, shiftlock.ReasonTransferPrepared)
	b.storeOp(req.OperationID, &cp, nil)
	return &cp, nil
}

func (b *Backend) CommitTransfer(ctx context.Context, req shiftlock.CommitRequest) (*shiftlock.ClaimRecord, error) {
	if err := b.beforeMutate(ctx); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if rec, err, ok := b.recallOp(req.OperationID); ok {
		return rec, err
	}

	if b.crashOnCommit {
		b.crashOnCommit = false
		err := &shiftlock.Error{Op: "CommitTransfer", Err: shiftlock.ErrBackend, Message: "simulated crash"}
		// Do not store — ambiguous; client must re-read
		return nil, err
	}

	b.expireLocked(req.ClaimName)
	cl, ok := b.claims[req.ClaimName]
	if !ok {
		return nil, shiftlock.ErrClaimNotFound
	}
	// Idempotent: already committed to successor with advanced token
	if cl.Phase == shiftlock.ClaimOwned && cl.OwnerGeneration == req.ToGeneration &&
		cl.FencingToken == req.ExpectedToken+1 && cl.TransferStatus == "committed" {
		cp := *cl
		b.storeOp(req.OperationID, &cp, nil)
		return &cp, nil
	}
	if cl.Phase != shiftlock.ClaimReserved {
		return nil, shiftlock.ErrNoTransfer
	}
	if cl.OwnerGeneration != req.FromGeneration || cl.PendingSuccessor != req.ToGeneration {
		return nil, shiftlock.ErrConcurrentTransfer
	}
	if cl.FencingToken != req.ExpectedToken {
		return nil, shiftlock.ErrStaleToken
	}

	next, err := advanceToken(cl.FencingToken)
	if err != nil {
		b.storeOp(req.OperationID, nil, err)
		return nil, err
	}
	now := b.clock.Now()
	cl.PreviousOwner = cl.OwnerGeneration
	cl.OwnerGeneration = req.ToGeneration
	cl.FencingToken = next
	cl.Phase = shiftlock.ClaimOwned
	cl.PendingSuccessor = ""
	cl.TransferStatus = "committed"
	cl.DrainStatus = ""
	cl.AcquiredAt = now
	cl.ExpiresAt = now.Add(req.TTL)
	cl.LastHeartbeat = now
	cl.Reason = shiftlock.ReasonTransferCommitted
	cl.Version++
	cp := *cl
	b.notifyLocked(cp, shiftlock.ReasonTransferCommitted)
	b.storeOp(req.OperationID, &cp, nil)
	return &cp, nil
}

func (b *Backend) AbortTransfer(ctx context.Context, req shiftlock.AbortRequest) (*shiftlock.ClaimRecord, error) {
	if err := b.beforeMutate(ctx); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if rec, err, ok := b.recallOp(req.OperationID); ok {
		return rec, err
	}
	b.expireLocked(req.ClaimName)

	cl, ok := b.claims[req.ClaimName]
	if !ok {
		return nil, shiftlock.ErrClaimNotFound
	}
	if cl.Phase != shiftlock.ClaimReserved {
		if cl.OwnerGeneration == req.FromGeneration && cl.Phase == shiftlock.ClaimOwned {
			cp := *cl
			b.storeOp(req.OperationID, &cp, nil)
			return &cp, nil
		}
		return nil, shiftlock.ErrNoTransfer
	}
	if cl.FencingToken != req.ExpectedToken {
		return nil, shiftlock.ErrStaleToken
	}
	if cl.OwnerGeneration != req.FromGeneration {
		return nil, shiftlock.ErrNotOwner
	}

	cl.PendingSuccessor = ""
	cl.Phase = shiftlock.ClaimOwned
	cl.TransferStatus = "aborted"
	cl.Reason = shiftlock.ReasonTransferAborted
	cl.Version++
	cp := *cl
	b.notifyLocked(cp, shiftlock.ReasonTransferAborted)
	b.storeOp(req.OperationID, &cp, nil)
	return &cp, nil
}

func (b *Backend) ReleaseClaim(ctx context.Context, req shiftlock.ReleaseRequest) error {
	if err := b.beforeMutate(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err, ok := b.recallOp(req.OperationID); ok {
		return err
	}
	b.expireLocked(req.ClaimName)

	cl, ok := b.claims[req.ClaimName]
	if !ok {
		return shiftlock.ErrClaimNotFound
	}
	if cl.FencingToken != req.Token {
		return shiftlock.ErrStaleToken
	}
	if cl.OwnerGeneration != req.GenerationID {
		// Idempotent: already released
		if cl.Phase == shiftlock.ClaimUnowned {
			b.storeOp(req.OperationID, nil, nil)
			return nil
		}
		return shiftlock.ErrNotOwner
	}

	cl.PreviousOwner = cl.OwnerGeneration
	cl.OwnerGeneration = ""
	cl.PendingSuccessor = ""
	cl.Phase = shiftlock.ClaimUnowned
	cl.TransferStatus = ""
	cl.DrainStatus = ""
	cl.Reason = shiftlock.ReasonReleased
	cl.Version++
	cp := *cl
	b.notifyLocked(cp, shiftlock.ReasonReleased)
	b.storeOp(req.OperationID, &cp, nil)
	return nil
}

func (b *Backend) WatchClaim(ctx context.Context, claimName string) (<-chan shiftlock.ClaimEvent, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, shiftlock.ErrClosed
	}
	w := &watchChan{ch: make(chan shiftlock.ClaimEvent, 16)}
	b.watchers[claimName] = append(b.watchers[claimName], w)
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		ws := b.watchers[claimName]
		for i, x := range ws {
			if x == w {
				b.watchers[claimName] = append(ws[:i], ws[i+1:]...)
				break
			}
		}
		if !w.closed {
			w.closed = true
			close(w.ch)
		}
	}()
	return w.ch, nil
}

func (b *Backend) notifyLocked(rec shiftlock.ClaimRecord, reason shiftlock.TransitionReason) {
	ev := shiftlock.ClaimEvent{Claim: rec, Time: b.clock.Now(), Reason: reason}
	for _, w := range b.watchers[rec.Name] {
		if w.closed {
			continue
		}
		select {
		case w.ch <- ev:
		default:
		}
	}
}

func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for name, ws := range b.watchers {
		for _, w := range ws {
			if !w.closed {
				w.closed = true
				close(w.ch)
			}
		}
		delete(b.watchers, name)
	}
	return nil
}

// ForceExpire expires a claim immediately (test helper).
func (b *Backend) ForceExpire(claimName string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cl, ok := b.claims[claimName]
	if !ok {
		return
	}
	cl.ExpiresAt = b.clock.Now().Add(-time.Second)
	b.expireLocked(claimName)
}

// ForceSetToken sets the claim token for overflow tests (creates unowned claim).
func (b *Backend) ForceSetToken(claimName string, tok shiftlock.FencingToken) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cl, ok := b.claims[claimName]
	if !ok {
		cl = &shiftlock.ClaimRecord{Name: claimName, Phase: shiftlock.ClaimUnowned}
		b.claims[claimName] = cl
	}
	cl.FencingToken = tok
	cl.Phase = shiftlock.ClaimUnowned
	cl.OwnerGeneration = ""
}

// Snapshot returns a copy of all claims (test helper).
func (b *Backend) Snapshot() map[string]shiftlock.ClaimRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]shiftlock.ClaimRecord, len(b.claims))
	for k, v := range b.claims {
		out[k] = *v
	}
	return out
}

var _ shiftlock.Backend = (*Backend)(nil)

// Ensure errors package used for potential wrapping.
var _ = errors.New
