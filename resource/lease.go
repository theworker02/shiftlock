package resource

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// Lease mode / conflict sentinels.
var (
	ErrLeaseHeld       = errors.New("resource: lease held")
	ErrLeaseNotHeld    = errors.New("resource: lease not held")
	ErrLeaseMode       = errors.New("resource: incompatible lease mode")
	ErrFenceRequired   = errors.New("resource: fencing token required")
	ErrStaleFence      = errors.New("resource: stale fencing token")
)

// LeaseMode selects exclusivity for a resource lease.
type LeaseMode string

const (
	LeaseExclusive    LeaseMode = "exclusive"
	LeaseShared       LeaseMode = "shared"
	LeaseReadOnly     LeaseMode = "read-only"
	LeaseMaintenance  LeaseMode = "maintenance"
	LeaseMigration    LeaseMode = "migration"
	LeaseAdministrative LeaseMode = "administrative"
)

// LeaseRequest asks for a resource lease.
type LeaseRequest struct {
	Purpose   string
	Mode      LeaseMode
	Owner     string
	TTL       time.Duration
	// Fence is required when the resource SupportsFencing and Mode is exclusive/maintenance/migration.
	Fence uint64
}

// Lease is a held resource lease.
type Lease struct {
	ID        ResourceID
	Mode      LeaseMode
	Owner     string
	Purpose   string
	Fence     uint64
	Epoch     ResourceEpoch
	ExpiresAt time.Time
	AcquiredAt time.Time
}

// LeaseManager tracks in-process leases for registered resources.
// Distributed lease enforcement remains adapter/backend-specific; this
// manager coordinates multi-resource acquisition ordering and fencing checks.
type LeaseManager struct {
	reg   *Registry
	mu    sync.Mutex
	held  map[string][]Lease // key = id.String()
	fence map[string]uint64  // last issued / accepted fence per resource
	clock func() time.Time
}

// NewLeaseManager binds to a registry.
func NewLeaseManager(reg *Registry) *LeaseManager {
	return &LeaseManager{
		reg:   reg,
		held:  make(map[string][]Lease),
		fence: make(map[string]uint64),
		clock: time.Now,
	}
}

// SetClock overrides the clock (tests).
func (m *LeaseManager) SetClock(c func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c != nil {
		m.clock = c
	}
}

// Lease acquires a single resource lease.
func (m *LeaseManager) Lease(ctx context.Context, id ResourceID, req LeaseRequest) (Lease, error) {
	_ = ctx
	if req.Owner == "" {
		return Lease{}, &Error{Op: "Lease", ID: id, Err: ErrInvalidArgument, Message: "owner required"}
	}
	if req.Mode == "" {
		req.Mode = LeaseExclusive
	}
	ent, err := m.reg.Get(id)
	if err != nil {
		return Lease{}, err
	}
	caps := ent.Resource.Capabilities()
	needsFence := caps.SupportsFencing && modeRequiresFence(req.Mode)
	if needsFence {
		if req.Fence == 0 {
			return Lease{}, &Error{Op: "Lease", ID: id, Err: ErrFenceRequired, Message: "fencing token required"}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.reg.checkMutationForLease(); err != nil {
		return Lease{}, err
	}
	key := id.String()
	m.expireLocked(key)
	if err := m.compatibleLocked(key, req.Mode); err != nil {
		return Lease{}, &Error{Op: "Lease", ID: id, Err: err, Message: err.Error()}
	}
	if needsFence {
		cur := m.fence[key]
		if req.Fence < cur {
			return Lease{}, &Error{Op: "Lease", ID: id, Err: ErrStaleFence, Message: "stale fencing token"}
		}
		if req.Fence > cur {
			m.fence[key] = req.Fence
		}
	} else if caps.SupportsFencing && req.Fence > m.fence[key] {
		// Accept optional fence advancement even for shared/read-only.
		m.fence[key] = req.Fence
	}

	now := m.clock().UTC()
	lease := Lease{
		ID:         id,
		Mode:       req.Mode,
		Owner:      req.Owner,
		Purpose:    req.Purpose,
		Fence:      req.Fence,
		Epoch:      ent.Epoch,
		AcquiredAt: now,
	}
	if req.TTL > 0 {
		lease.ExpiresAt = now.Add(req.TTL)
	}
	m.held[key] = append(m.held[key], lease)
	return lease, nil
}

// Release drops a matching lease for owner+mode.
func (m *LeaseManager) Release(id ResourceID, owner string, mode LeaseMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := id.String()
	list := m.held[key]
	for i, l := range list {
		if l.Owner == owner && (mode == "" || l.Mode == mode) {
			m.held[key] = append(list[:i], list[i+1:]...)
			if len(m.held[key]) == 0 {
				delete(m.held, key)
			}
			return nil
		}
	}
	return &Error{Op: "Release", ID: id, Err: ErrLeaseNotHeld, Message: "lease not held"}
}

// Held returns a copy of active leases for id.
func (m *LeaseManager) Held(id ResourceID) []Lease {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := id.String()
	m.expireLocked(key)
	return append([]Lease(nil), m.held[key]...)
}

// AcquireAll leases multiple resources in canonical order.
// On failure, releases the partial set. Fencing tokens are taken from reqs[id].
func (m *LeaseManager) AcquireAll(ctx context.Context, reqs map[string]LeaseRequest) (LeaseHandle, error) {
	if len(reqs) == 0 {
		return LeaseHandle{}, &Error{Op: "AcquireAll", Err: ErrInvalidArgument, Message: "empty request"}
	}
	ids := make([]ResourceID, 0, len(reqs))
	for k := range reqs {
		id, err := ParseResourceID(k)
		if err != nil {
			return LeaseHandle{}, err
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	held := make([]Lease, 0, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			m.releaseLeases(held)
			return LeaseHandle{}, &Error{Op: "AcquireAll", Err: err, Message: "context done"}
		}
		req := reqs[id.String()]
		lease, err := m.Lease(ctx, id, req)
		if err != nil {
			m.releaseLeases(held)
			return LeaseHandle{}, &Error{Op: "AcquireAll", ID: id, Err: ErrPartialAcquire, Message: err.Error()}
		}
		held = append(held, lease)
	}
	outIDs := make([]ResourceID, len(held))
	for i, l := range held {
		outIDs[i] = l.ID
	}
	return LeaseHandle{IDs: outIDs, Leases: held}, nil
}

// ReleaseHandle releases all leases in a handle.
func (m *LeaseManager) ReleaseHandle(h LeaseHandle) {
	m.releaseLeases(h.Leases)
	if len(h.Leases) == 0 {
		for _, id := range h.IDs {
			_ = m.Release(id, "", "")
		}
	}
}

func (m *LeaseManager) releaseLeases(leases []Lease) {
	for i := len(leases) - 1; i >= 0; i-- {
		l := leases[i]
		_ = m.Release(l.ID, l.Owner, l.Mode)
	}
}

func modeRequiresFence(m LeaseMode) bool {
	switch m {
	case LeaseExclusive, LeaseMaintenance, LeaseMigration, LeaseAdministrative:
		return true
	default:
		return false
	}
}

func (m *LeaseManager) compatibleLocked(key string, mode LeaseMode) error {
	list := m.held[key]
	if len(list) == 0 {
		return nil
	}
	for _, l := range list {
		if !modesCompatible(l.Mode, mode) {
			return ErrLeaseHeld
		}
	}
	return nil
}

func modesCompatible(a, b LeaseMode) bool {
	if a == LeaseExclusive || b == LeaseExclusive {
		return false
	}
	if a == LeaseMaintenance || b == LeaseMaintenance || a == LeaseMigration || b == LeaseMigration ||
		a == LeaseAdministrative || b == LeaseAdministrative {
		return false
	}
	// shared + shared, read-only + read-only, shared + read-only OK
	return true
}

func (m *LeaseManager) expireLocked(key string) {
	now := m.clock().UTC()
	list := m.held[key]
	if len(list) == 0 {
		return
	}
	kept := list[:0]
	for _, l := range list {
		if !l.ExpiresAt.IsZero() && !l.ExpiresAt.After(now) {
			continue
		}
		kept = append(kept, l)
	}
	if len(kept) == 0 {
		delete(m.held, key)
	} else {
		m.held[key] = kept
	}
}

// checkMutationForLease is used without holding registry mu — best-effort lockdown check.
func (r *Registry) checkMutationForLease() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.checkMutationLocked("Lease")
}
