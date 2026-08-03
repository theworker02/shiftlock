package redis

import (
	"context"
	"sync"
	"time"

	"github.com/theworker02/shiftlock"
)

// Local is an in-process Redis-semantics backend for tests (mutex + CAS),
// mirroring Lua script behavior without requiring a Redis server.
type Local struct {
	mu     sync.Mutex
	claims map[string]*shiftlock.ClaimRecord
	gens   map[string]*shiftlock.Generation
	ops    map[shiftlock.OperationID]*localOp
}

type localOp struct {
	rec *shiftlock.ClaimRecord
	err error
}

// NewLocal returns a local CAS backend with Redis-equivalent ownership rules.
func NewLocal() *Local {
	return &Local{
		claims: make(map[string]*shiftlock.ClaimRecord),
		gens:   make(map[string]*shiftlock.Generation),
		ops:    make(map[shiftlock.OperationID]*localOp),
	}
}

// Capabilities advertises Redis-local safety features.
func (l *Local) Capabilities() shiftlock.Capabilities {
	return shiftlock.Capabilities{
		AtomicCAS:           true,
		IdempotentMutations: true,
		WatchSupported:      false,
		DurableStorage:      false,
		ExpireBeforeMutate:  true,
		RenewDuringReserved: true,
		GlobalExclusive:     true,
		MaxFencingToken:     shiftlock.MaxSafeFencingToken,
	}
}

func (l *Local) recall(id shiftlock.OperationID) (*shiftlock.ClaimRecord, error, bool) {
	if id.Empty() {
		return nil, nil, false
	}
	r, ok := l.ops[id]
	if !ok {
		return nil, nil, false
	}
	if r.rec != nil {
		cp := *r.rec
		return &cp, r.err, true
	}
	return nil, r.err, true
}

func (l *Local) store(id shiftlock.OperationID, rec *shiftlock.ClaimRecord, err error) {
	if id.Empty() {
		return
	}
	var cp *shiftlock.ClaimRecord
	if rec != nil {
		x := *rec
		cp = &x
	}
	l.ops[id] = &localOp{rec: cp, err: err}
}

func advanceLocal(cur shiftlock.FencingToken) (shiftlock.FencingToken, error) {
	if cur >= shiftlock.MaxSafeFencingToken {
		return cur, shiftlock.ErrTokenOverflow
	}
	return cur + 1, nil
}

func (l *Local) RegisterGeneration(_ context.Context, gen shiftlock.Generation) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := gen
	l.gens[gen.ID] = &cp
	return nil
}

func (l *Local) UpdateGeneration(ctx context.Context, gen shiftlock.Generation) error {
	return l.RegisterGeneration(ctx, gen)
}

func (l *Local) GetGeneration(_ context.Context, id string) (*shiftlock.Generation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	g, ok := l.gens[id]
	if !ok {
		return nil, shiftlock.ErrGenerationNotFound
	}
	cp := *g
	return &cp, nil
}

func (l *Local) expire(cl *shiftlock.ClaimRecord) {
	if cl.ExpiresAt.IsZero() || time.Now().Before(cl.ExpiresAt) {
		return
	}
	if cl.Phase == shiftlock.ClaimOwned || cl.Phase == shiftlock.ClaimReserved || cl.Phase == shiftlock.ClaimDraining {
		cl.PreviousOwner = cl.OwnerGeneration
		cl.OwnerGeneration = ""
		cl.PendingSuccessor = ""
		cl.Phase = shiftlock.ClaimUnowned
		cl.Reason = shiftlock.ReasonExpired
		cl.Version++
	}
}

func (l *Local) GetClaim(_ context.Context, name string) (*shiftlock.ClaimRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cl, ok := l.claims[name]
	if !ok {
		return nil, shiftlock.ErrClaimNotFound
	}
	l.expire(cl)
	cp := *cl
	return &cp, nil
}

func (l *Local) AcquireClaim(_ context.Context, req shiftlock.AcquireRequest) (*shiftlock.ClaimRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec, err, ok := l.recall(req.OperationID); ok {
		return rec, err
	}
	cl, ok := l.claims[req.ClaimName]
	if !ok {
		cl = &shiftlock.ClaimRecord{Name: req.ClaimName, Phase: shiftlock.ClaimUnowned}
		l.claims[req.ClaimName] = cl
	}
	l.expire(cl)
	now := time.Now()
	if cl.Phase == shiftlock.ClaimOwned || cl.Phase == shiftlock.ClaimReserved || cl.Phase == shiftlock.ClaimDraining {
		if cl.OwnerGeneration == req.GenerationID {
			cl.ExpiresAt = now.Add(req.TTL)
			cl.LastHeartbeat = now
			cl.Reason = shiftlock.ReasonRenewed
			cl.Version++
			cp := *cl
			l.store(req.OperationID, &cp, nil)
			return &cp, nil
		}
		cp := *cl
		l.store(req.OperationID, &cp, shiftlock.ErrClaimHeld)
		return &cp, shiftlock.ErrClaimHeld
	}
	next, err := advanceLocal(cl.FencingToken)
	if err != nil {
		l.store(req.OperationID, nil, err)
		return nil, err
	}
	cl.OwnerGeneration = req.GenerationID
	cl.FencingToken = next
	cl.Phase = shiftlock.ClaimOwned
	cl.AcquiredAt = now
	cl.ExpiresAt = now.Add(req.TTL)
	cl.LastHeartbeat = now
	cl.Reason = shiftlock.ReasonAcquired
	cl.Version++
	cp := *cl
	l.store(req.OperationID, &cp, nil)
	return &cp, nil
}

func (l *Local) RenewClaim(_ context.Context, req shiftlock.RenewRequest) (*shiftlock.ClaimRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec, err, ok := l.recall(req.OperationID); ok {
		return rec, err
	}
	cl, ok := l.claims[req.ClaimName]
	if !ok {
		return nil, shiftlock.ErrClaimNotFound
	}
	l.expire(cl)
	if cl.OwnerGeneration != req.GenerationID {
		return nil, shiftlock.ErrNotOwner
	}
	if cl.FencingToken != req.Token {
		return nil, shiftlock.ErrStaleToken
	}
	if cl.Phase != shiftlock.ClaimOwned && cl.Phase != shiftlock.ClaimDraining && cl.Phase != shiftlock.ClaimReserved {
		return nil, shiftlock.ErrNotOwner
	}
	now := time.Now()
	cl.ExpiresAt = now.Add(req.TTL)
	cl.LastHeartbeat = now
	cl.Reason = shiftlock.ReasonRenewed
	cl.Version++
	cp := *cl
	l.store(req.OperationID, &cp, nil)
	return &cp, nil
}

func (l *Local) PrepareTransfer(_ context.Context, req shiftlock.TransferRequest) (*shiftlock.ClaimRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec, err, ok := l.recall(req.OperationID); ok {
		return rec, err
	}
	cl, ok := l.claims[req.ClaimName]
	if !ok {
		return nil, shiftlock.ErrClaimNotFound
	}
	l.expire(cl)
	if cl.OwnerGeneration != req.FromGeneration {
		return nil, shiftlock.ErrNotOwner
	}
	if cl.FencingToken != req.Token {
		return nil, shiftlock.ErrStaleToken
	}
	if cl.Phase == shiftlock.ClaimReserved && cl.PendingSuccessor != "" && cl.PendingSuccessor != req.ToGeneration {
		return nil, shiftlock.ErrConcurrentTransfer
	}
	if cl.Phase == shiftlock.ClaimReserved && cl.PendingSuccessor == req.ToGeneration {
		cp := *cl
		l.store(req.OperationID, &cp, nil)
		return &cp, nil
	}
	now := time.Now()
	cl.Phase = shiftlock.ClaimReserved
	cl.PendingSuccessor = req.ToGeneration
	cl.TransferStatus = "prepared"
	cl.DrainStatus = "complete"
	cl.ExpiresAt = now.Add(req.TTL)
	cl.LastHeartbeat = now
	cl.Reason = shiftlock.ReasonTransferPrepared
	cl.Version++
	cp := *cl
	l.store(req.OperationID, &cp, nil)
	return &cp, nil
}

func (l *Local) CommitTransfer(_ context.Context, req shiftlock.CommitRequest) (*shiftlock.ClaimRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec, err, ok := l.recall(req.OperationID); ok {
		return rec, err
	}
	cl, ok := l.claims[req.ClaimName]
	if !ok {
		return nil, shiftlock.ErrClaimNotFound
	}
	l.expire(cl)
	if cl.Phase == shiftlock.ClaimOwned && cl.OwnerGeneration == req.ToGeneration &&
		cl.FencingToken == req.ExpectedToken+1 && cl.TransferStatus == "committed" {
		cp := *cl
		l.store(req.OperationID, &cp, nil)
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
	next, err := advanceLocal(cl.FencingToken)
	if err != nil {
		l.store(req.OperationID, nil, err)
		return nil, err
	}
	now := time.Now()
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
	l.store(req.OperationID, &cp, nil)
	return &cp, nil
}

func (l *Local) AbortTransfer(_ context.Context, req shiftlock.AbortRequest) (*shiftlock.ClaimRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec, err, ok := l.recall(req.OperationID); ok {
		return rec, err
	}
	cl, ok := l.claims[req.ClaimName]
	if !ok {
		return nil, shiftlock.ErrClaimNotFound
	}
	l.expire(cl)
	if cl.Phase != shiftlock.ClaimReserved {
		if cl.OwnerGeneration == req.FromGeneration && cl.Phase == shiftlock.ClaimOwned {
			cp := *cl
			l.store(req.OperationID, &cp, nil)
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
	l.store(req.OperationID, &cp, nil)
	return &cp, nil
}

func (l *Local) ReleaseClaim(_ context.Context, req shiftlock.ReleaseRequest) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err, ok := l.recall(req.OperationID); ok {
		return err
	}
	cl, ok := l.claims[req.ClaimName]
	if !ok {
		return shiftlock.ErrClaimNotFound
	}
	l.expire(cl)
	if cl.FencingToken != req.Token {
		return shiftlock.ErrStaleToken
	}
	if cl.OwnerGeneration != req.GenerationID {
		if cl.Phase == shiftlock.ClaimUnowned {
			l.store(req.OperationID, nil, nil)
			return nil
		}
		return shiftlock.ErrNotOwner
	}
	cl.PreviousOwner = cl.OwnerGeneration
	cl.OwnerGeneration = ""
	cl.PendingSuccessor = ""
	cl.Phase = shiftlock.ClaimUnowned
	cl.Reason = shiftlock.ReasonReleased
	cl.Version++
	cp := *cl
	l.store(req.OperationID, &cp, nil)
	return nil
}

func (l *Local) WatchClaim(ctx context.Context, _ string) (<-chan shiftlock.ClaimEvent, error) {
	// Local stub: bounded buffer; closes when ctx ends (no event stream).
	ch := make(chan shiftlock.ClaimEvent, 1)
	go func() {
		defer close(ch)
		<-ctx.Done()
	}()
	return ch, nil
}

func (l *Local) Close() error { return nil }

// ForceSetToken supports certification overflow tests.
func (l *Local) ForceSetToken(claimName string, tok shiftlock.FencingToken) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cl, ok := l.claims[claimName]
	if !ok {
		cl = &shiftlock.ClaimRecord{Name: claimName, Phase: shiftlock.ClaimUnowned}
		l.claims[claimName] = cl
	}
	cl.FencingToken = tok
	cl.Phase = shiftlock.ClaimUnowned
	cl.OwnerGeneration = ""
}

var _ shiftlock.Backend = (*Local)(nil)
var _ shiftlock.Capabler = (*Local)(nil)
