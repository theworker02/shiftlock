// Package kubernetes implements a ShiftLock backend using Kubernetes Lease objects.
//
// This package is intentionally separate so the core module does not depend on
// kubernetes.io client libraries. Import github.com/theworker02/shiftlock/backend/kubernetes
// only in binaries that already talk to a cluster.
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/theworker02/shiftlock"
)

// LeaseObject is the subset of coordination/v1.Lease fields we persist.
type LeaseObject struct {
	Name              string
	HolderIdentity    string
	LeaseDurationSecs int32
	AcquireTime       time.Time
	RenewTime         time.Time
	ResourceVersion   string
	// Annotations carry fencing token and transfer metadata.
	Annotations map[string]string
}

// LeaseClient abstracts the Kubernetes Lease API.
type LeaseClient interface {
	Get(ctx context.Context, name string) (*LeaseObject, error)
	Create(ctx context.Context, lease *LeaseObject) (*LeaseObject, error)
	Update(ctx context.Context, lease *LeaseObject) (*LeaseObject, error)
	Delete(ctx context.Context, name string) error
}

const (
	annToken      = "shiftlock.theworker02.io/fencing-token"
	annPhase      = "shiftlock.theworker02.io/phase"
	annPrev       = "shiftlock.theworker02.io/previous-owner"
	annSuccessor  = "shiftlock.theworker02.io/pending-successor"
	annTransfer   = "shiftlock.theworker02.io/transfer-status"
	annDrain      = "shiftlock.theworker02.io/drain-status"
	annReason     = "shiftlock.theworker02.io/reason"
	annVersion    = "shiftlock.theworker02.io/version"
	annAcquired   = "shiftlock.theworker02.io/acquired-at"
)

// Backend coordinates ownership via Kubernetes Leases.
type Backend struct {
	client    LeaseClient
	namespace string
	prefix    string
	mu        sync.Mutex
	gens      map[string]*shiftlock.Generation
	ops       map[shiftlock.OperationID]*k8sOp
}

type k8sOp struct {
	rec *shiftlock.ClaimRecord
	err error
}

// Option configures the kubernetes backend.
type Option func(*Backend)

// WithPrefix prefixes lease names (default "shiftlock-").
func WithPrefix(p string) Option {
	return func(b *Backend) { b.prefix = p }
}

// New creates a Kubernetes Lease backend.
func New(client LeaseClient, namespace string, opts ...Option) *Backend {
	b := &Backend{
		client:    client,
		namespace: namespace,
		prefix:    "shiftlock-",
		gens:      make(map[string]*shiftlock.Generation),
		ops:       make(map[shiftlock.OperationID]*k8sOp),
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Capabilities implements shiftlock.Capabler.
func (b *Backend) Capabilities() shiftlock.Capabilities {
	return shiftlock.Capabilities{
		AtomicCAS:           true,
		IdempotentMutations: true,
		WatchSupported:      true,
		DurableStorage:      true,
		ExpireBeforeMutate:  true,
		RenewDuringReserved: true,
		GlobalExclusive:     true,
		MaxFencingToken:     shiftlock.MaxSafeFencingToken,
	}
}

func (b *Backend) recallOp(id shiftlock.OperationID) (*shiftlock.ClaimRecord, error, bool) {
	if id.Empty() {
		return nil, nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.ops[id]
	if !ok {
		return nil, nil, false
	}
	if r.rec != nil {
		cp := *r.rec
		return &cp, r.err, true
	}
	return nil, r.err, true
}

func (b *Backend) storeOp(id shiftlock.OperationID, rec *shiftlock.ClaimRecord, err error) {
	if id.Empty() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var cp *shiftlock.ClaimRecord
	if rec != nil {
		x := *rec
		cp = &x
	}
	b.ops[id] = &k8sOp{rec: cp, err: err}
}

func (b *Backend) leaseName(claim string) string {
	return b.prefix + claim
}

func isConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "conflict") || strings.Contains(msg, "Conflict")
}

func (b *Backend) RegisterGeneration(_ context.Context, gen shiftlock.Generation) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := gen
	b.gens[gen.ID] = &cp
	return nil
}

func (b *Backend) UpdateGeneration(_ context.Context, gen shiftlock.Generation) error {
	return b.RegisterGeneration(context.Background(), gen)
}

func (b *Backend) GetGeneration(_ context.Context, id string) (*shiftlock.Generation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.gens[id]
	if !ok {
		return nil, shiftlock.ErrGenerationNotFound
	}
	cp := *g
	return &cp, nil
}

func recordFromLease(claim string, l *LeaseObject) *shiftlock.ClaimRecord {
	ann := l.Annotations
	if ann == nil {
		ann = map[string]string{}
	}
	var token shiftlock.FencingToken
	fmt.Sscanf(ann[annToken], "%d", &token)
	var ver uint64
	fmt.Sscanf(ann[annVersion], "%d", &ver)
	phase := shiftlock.ClaimPhase(ann[annPhase])
	if phase == "" {
		if l.HolderIdentity == "" {
			phase = shiftlock.ClaimUnowned
		} else {
			phase = shiftlock.ClaimOwned
		}
	}
	rec := &shiftlock.ClaimRecord{
		Name:             claim,
		OwnerGeneration:  l.HolderIdentity,
		FencingToken:     token,
		Phase:            phase,
		PreviousOwner:    ann[annPrev],
		PendingSuccessor: ann[annSuccessor],
		TransferStatus:   ann[annTransfer],
		DrainStatus:      ann[annDrain],
		Reason:           shiftlock.TransitionReason(ann[annReason]),
		Version:          ver,
		LastHeartbeat:    l.RenewTime,
		ExpiresAt:        l.RenewTime.Add(time.Duration(l.LeaseDurationSecs) * time.Second),
	}
	if s := ann[annAcquired]; s != "" {
		var ns int64
		fmt.Sscanf(s, "%d", &ns)
		rec.AcquiredAt = time.Unix(0, ns)
	}
	return rec
}

func applyRecord(l *LeaseObject, rec *shiftlock.ClaimRecord, ttl time.Duration) {
	if l.Annotations == nil {
		l.Annotations = map[string]string{}
	}
	l.HolderIdentity = rec.OwnerGeneration
	l.LeaseDurationSecs = int32(ttl / time.Second)
	if l.LeaseDurationSecs <= 0 {
		l.LeaseDurationSecs = 15
	}
	now := time.Now()
	if rec.AcquiredAt.IsZero() {
		rec.AcquiredAt = now
	}
	l.AcquireTime = rec.AcquiredAt
	l.RenewTime = now
	l.Annotations[annToken] = fmt.Sprintf("%d", rec.FencingToken)
	l.Annotations[annPhase] = string(rec.Phase)
	l.Annotations[annPrev] = rec.PreviousOwner
	l.Annotations[annSuccessor] = rec.PendingSuccessor
	l.Annotations[annTransfer] = rec.TransferStatus
	l.Annotations[annDrain] = rec.DrainStatus
	l.Annotations[annReason] = string(rec.Reason)
	l.Annotations[annVersion] = fmt.Sprintf("%d", rec.Version)
	l.Annotations[annAcquired] = fmt.Sprintf("%d", rec.AcquiredAt.UnixNano())
}

func (b *Backend) getOrCreate(ctx context.Context, claim string) (*LeaseObject, *shiftlock.ClaimRecord, error) {
	name := b.leaseName(claim)
	l, err := b.client.Get(ctx, name)
	if err != nil {
		// create empty
		l = &LeaseObject{Name: name, Annotations: map[string]string{
			annPhase: string(shiftlock.ClaimUnowned),
			annToken: "0",
		}}
		l, err = b.client.Create(ctx, l)
		if err != nil {
			// race: get again
			l, err = b.client.Get(ctx, name)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	rec := recordFromLease(claim, l)
	// expire
	if !rec.ExpiresAt.IsZero() && time.Now().After(rec.ExpiresAt) &&
		(rec.Phase == shiftlock.ClaimOwned || rec.Phase == shiftlock.ClaimReserved || rec.Phase == shiftlock.ClaimDraining) {
		rec.PreviousOwner = rec.OwnerGeneration
		rec.OwnerGeneration = ""
		rec.PendingSuccessor = ""
		rec.Phase = shiftlock.ClaimUnowned
		rec.Reason = shiftlock.ReasonExpired
		rec.Version++
		applyRecord(l, rec, 15*time.Second)
		l, err = b.client.Update(ctx, l)
		if err != nil {
			return nil, nil, err
		}
		rec = recordFromLease(claim, l)
	}
	return l, rec, nil
}

func (b *Backend) GetClaim(ctx context.Context, claimName string) (*shiftlock.ClaimRecord, error) {
	_, rec, err := b.getOrCreate(ctx, claimName)
	return rec, err
}

func (b *Backend) AcquireClaim(ctx context.Context, req shiftlock.AcquireRequest) (*shiftlock.ClaimRecord, error) {
	if rec, err, ok := b.recallOp(req.OperationID); ok {
		return rec, err
	}
	l, rec, err := b.getOrCreate(ctx, req.ClaimName)
	if err != nil {
		return nil, err
	}
	if rec.Phase == shiftlock.ClaimOwned || rec.Phase == shiftlock.ClaimReserved || rec.Phase == shiftlock.ClaimDraining {
		if rec.OwnerGeneration == req.GenerationID {
			rec.Reason = shiftlock.ReasonRenewed
			rec.Version++
			applyRecord(l, rec, req.TTL)
			l, err = b.client.Update(ctx, l)
			if err != nil {
				if isConflict(err) {
					return nil, shiftlock.ErrClaimHeld
				}
				return nil, err
			}
			out := recordFromLease(req.ClaimName, l)
			b.storeOp(req.OperationID, out, nil)
			return out, nil
		}
		b.storeOp(req.OperationID, rec, shiftlock.ErrClaimHeld)
		return rec, shiftlock.ErrClaimHeld
	}
	if rec.FencingToken >= shiftlock.MaxSafeFencingToken {
		b.storeOp(req.OperationID, nil, shiftlock.ErrTokenOverflow)
		return nil, shiftlock.ErrTokenOverflow
	}
	rec.PreviousOwner = rec.OwnerGeneration
	rec.OwnerGeneration = req.GenerationID
	rec.FencingToken++
	rec.Phase = shiftlock.ClaimOwned
	rec.AcquiredAt = time.Now()
	rec.PendingSuccessor = ""
	rec.TransferStatus = ""
	rec.DrainStatus = ""
	rec.Reason = shiftlock.ReasonAcquired
	rec.Version++
	applyRecord(l, rec, req.TTL)
	l, err = b.client.Update(ctx, l)
	if err != nil {
		if isConflict(err) {
			return nil, shiftlock.ErrClaimHeld
		}
		return nil, err
	}
	out := recordFromLease(req.ClaimName, l)
	b.storeOp(req.OperationID, out, nil)
	return out, nil
}

func (b *Backend) RenewClaim(ctx context.Context, req shiftlock.RenewRequest) (*shiftlock.ClaimRecord, error) {
	if rec, err, ok := b.recallOp(req.OperationID); ok {
		return rec, err
	}
	l, rec, err := b.getOrCreate(ctx, req.ClaimName)
	if err != nil {
		return nil, err
	}
	if rec.OwnerGeneration != req.GenerationID {
		return nil, shiftlock.ErrNotOwner
	}
	if rec.FencingToken != req.Token {
		return nil, shiftlock.ErrStaleToken
	}
	if rec.Phase != shiftlock.ClaimOwned && rec.Phase != shiftlock.ClaimDraining && rec.Phase != shiftlock.ClaimReserved {
		return nil, shiftlock.ErrNotOwner
	}
	rec.Reason = shiftlock.ReasonRenewed
	rec.Version++
	applyRecord(l, rec, req.TTL)
	l, err = b.client.Update(ctx, l)
	if err != nil {
		return nil, err
	}
	out := recordFromLease(req.ClaimName, l)
	b.storeOp(req.OperationID, out, nil)
	return out, nil
}

func (b *Backend) PrepareTransfer(ctx context.Context, req shiftlock.TransferRequest) (*shiftlock.ClaimRecord, error) {
	if rec, err, ok := b.recallOp(req.OperationID); ok {
		return rec, err
	}
	l, rec, err := b.getOrCreate(ctx, req.ClaimName)
	if err != nil {
		return nil, err
	}
	if rec.OwnerGeneration != req.FromGeneration {
		return nil, shiftlock.ErrNotOwner
	}
	if rec.FencingToken != req.Token {
		return nil, shiftlock.ErrStaleToken
	}
	if rec.Phase == shiftlock.ClaimReserved && rec.PendingSuccessor != "" && rec.PendingSuccessor != req.ToGeneration {
		return nil, shiftlock.ErrConcurrentTransfer
	}
	if rec.Phase == shiftlock.ClaimReserved && rec.PendingSuccessor == req.ToGeneration {
		b.storeOp(req.OperationID, rec, nil)
		return rec, nil
	}
	rec.Phase = shiftlock.ClaimReserved
	rec.PendingSuccessor = req.ToGeneration
	rec.TransferStatus = "prepared"
	rec.DrainStatus = "complete"
	rec.Reason = shiftlock.ReasonTransferPrepared
	rec.Version++
	applyRecord(l, rec, req.TTL)
	l, err = b.client.Update(ctx, l)
	if err != nil {
		return nil, err
	}
	out := recordFromLease(req.ClaimName, l)
	b.storeOp(req.OperationID, out, nil)
	return out, nil
}

func (b *Backend) CommitTransfer(ctx context.Context, req shiftlock.CommitRequest) (*shiftlock.ClaimRecord, error) {
	if rec, err, ok := b.recallOp(req.OperationID); ok {
		return rec, err
	}
	l, rec, err := b.getOrCreate(ctx, req.ClaimName)
	if err != nil {
		return nil, err
	}
	if rec.Phase == shiftlock.ClaimOwned && rec.OwnerGeneration == req.ToGeneration &&
		rec.FencingToken == req.ExpectedToken+1 && rec.TransferStatus == "committed" {
		b.storeOp(req.OperationID, rec, nil)
		return rec, nil
	}
	if rec.Phase != shiftlock.ClaimReserved {
		return nil, shiftlock.ErrNoTransfer
	}
	if rec.OwnerGeneration != req.FromGeneration || rec.PendingSuccessor != req.ToGeneration {
		return nil, shiftlock.ErrConcurrentTransfer
	}
	if rec.FencingToken != req.ExpectedToken {
		return nil, shiftlock.ErrStaleToken
	}
	if rec.FencingToken >= shiftlock.MaxSafeFencingToken {
		b.storeOp(req.OperationID, nil, shiftlock.ErrTokenOverflow)
		return nil, shiftlock.ErrTokenOverflow
	}
	rec.PreviousOwner = rec.OwnerGeneration
	rec.OwnerGeneration = req.ToGeneration
	rec.FencingToken++
	rec.Phase = shiftlock.ClaimOwned
	rec.PendingSuccessor = ""
	rec.TransferStatus = "committed"
	rec.DrainStatus = ""
	rec.AcquiredAt = time.Now()
	rec.Reason = shiftlock.ReasonTransferCommitted
	rec.Version++
	applyRecord(l, rec, req.TTL)
	l, err = b.client.Update(ctx, l)
	if err != nil {
		return nil, err
	}
	out := recordFromLease(req.ClaimName, l)
	b.storeOp(req.OperationID, out, nil)
	return out, nil
}

func (b *Backend) AbortTransfer(ctx context.Context, req shiftlock.AbortRequest) (*shiftlock.ClaimRecord, error) {
	if rec, err, ok := b.recallOp(req.OperationID); ok {
		return rec, err
	}
	l, rec, err := b.getOrCreate(ctx, req.ClaimName)
	if err != nil {
		return nil, err
	}
	if rec.Phase != shiftlock.ClaimReserved {
		if rec.OwnerGeneration == req.FromGeneration && rec.Phase == shiftlock.ClaimOwned {
			b.storeOp(req.OperationID, rec, nil)
			return rec, nil
		}
		return nil, shiftlock.ErrNoTransfer
	}
	if rec.FencingToken != req.ExpectedToken {
		return nil, shiftlock.ErrStaleToken
	}
	if rec.OwnerGeneration != req.FromGeneration {
		return nil, shiftlock.ErrNotOwner
	}
	rec.PendingSuccessor = ""
	rec.Phase = shiftlock.ClaimOwned
	rec.TransferStatus = "aborted"
	rec.Reason = shiftlock.ReasonTransferAborted
	rec.Version++
	applyRecord(l, rec, 15*time.Second)
	l, err = b.client.Update(ctx, l)
	if err != nil {
		return nil, err
	}
	out := recordFromLease(req.ClaimName, l)
	b.storeOp(req.OperationID, out, nil)
	return out, nil
}

func (b *Backend) ReleaseClaim(ctx context.Context, req shiftlock.ReleaseRequest) error {
	if _, err, ok := b.recallOp(req.OperationID); ok {
		return err
	}
	l, rec, err := b.getOrCreate(ctx, req.ClaimName)
	if err != nil {
		return err
	}
	if rec.FencingToken != req.Token {
		return shiftlock.ErrStaleToken
	}
	if rec.OwnerGeneration != req.GenerationID {
		if rec.Phase == shiftlock.ClaimUnowned {
			b.storeOp(req.OperationID, nil, nil)
			return nil
		}
		return shiftlock.ErrNotOwner
	}
	rec.PreviousOwner = rec.OwnerGeneration
	rec.OwnerGeneration = ""
	rec.PendingSuccessor = ""
	rec.Phase = shiftlock.ClaimUnowned
	rec.TransferStatus = ""
	rec.DrainStatus = ""
	rec.Reason = shiftlock.ReasonReleased
	rec.Version++
	applyRecord(l, rec, 15*time.Second)
	_, err = b.client.Update(ctx, l)
	b.storeOp(req.OperationID, nil, err)
	return err
}

func (b *Backend) WatchClaim(ctx context.Context, claimName string) (<-chan shiftlock.ClaimEvent, error) {
	ch := make(chan shiftlock.ClaimEvent, 8)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		var last uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rec, err := b.GetClaim(ctx, claimName)
				if err != nil {
					continue
				}
				if rec.Version != last {
					last = rec.Version
					select {
					case ch <- shiftlock.ClaimEvent{Claim: *rec, Time: time.Now(), Reason: rec.Reason}:
					default:
					}
				}
			}
		}
	}()
	return ch, nil
}

func (b *Backend) Close() error { return nil }

// MemoryLeaseClient is an in-process LeaseClient for tests.
type MemoryLeaseClient struct {
	mu     sync.Mutex
	leases map[string]*LeaseObject
	rv     int
}

// NewMemoryLeaseClient returns a fake Lease client.
func NewMemoryLeaseClient() *MemoryLeaseClient {
	return &MemoryLeaseClient{leases: make(map[string]*LeaseObject)}
}

func (m *MemoryLeaseClient) Get(_ context.Context, name string) (*LeaseObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[name]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return cloneLease(l), nil
}

func (m *MemoryLeaseClient) Create(_ context.Context, lease *LeaseObject) (*LeaseObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.leases[lease.Name]; ok {
		return nil, fmt.Errorf("exists")
	}
	m.rv++
	cp := cloneLease(lease)
	cp.ResourceVersion = fmt.Sprintf("%d", m.rv)
	m.leases[lease.Name] = cp
	return cloneLease(cp), nil
}

func (m *MemoryLeaseClient) Update(_ context.Context, lease *LeaseObject) (*LeaseObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.leases[lease.Name]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	if lease.ResourceVersion != "" && lease.ResourceVersion != cur.ResourceVersion {
		return nil, fmt.Errorf("conflict")
	}
	m.rv++
	cp := cloneLease(lease)
	cp.ResourceVersion = fmt.Sprintf("%d", m.rv)
	m.leases[lease.Name] = cp
	return cloneLease(cp), nil
}

func (m *MemoryLeaseClient) Delete(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.leases, name)
	return nil
}

func cloneLease(l *LeaseObject) *LeaseObject {
	cp := *l
	if l.Annotations != nil {
		cp.Annotations = make(map[string]string, len(l.Annotations))
		for k, v := range l.Annotations {
			cp.Annotations[k] = v
		}
	}
	return &cp
}

var _ = json.Marshal

// ForceSetToken supports certification overflow tests (creates an unowned lease at the given token).
func (b *Backend) ForceSetToken(claimName string, tok shiftlock.FencingToken) {
	ctx := context.Background()
	name := b.leaseName(claimName)
	l := &LeaseObject{Name: name, Annotations: map[string]string{}}
	rec := &shiftlock.ClaimRecord{
		Name: claimName, Phase: shiftlock.ClaimUnowned, FencingToken: tok,
	}
	applyRecord(l, rec, 15*time.Second)
	if existing, err := b.client.Get(ctx, name); err == nil {
		l.ResourceVersion = existing.ResourceVersion
		_, _ = b.client.Update(ctx, l)
		return
	}
	_, _ = b.client.Create(ctx, l)
}

var _ shiftlock.Backend = (*Backend)(nil)
var _ shiftlock.Capabler = (*Backend)(nil)
var _ LeaseClient = (*MemoryLeaseClient)(nil)
