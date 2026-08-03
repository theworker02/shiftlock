package resource

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultMaxResources is the default registry cardinality bound.
const DefaultMaxResources = 1024

// LockdownChecker is a soft dependency so resource never imports control/lockdown.
type LockdownChecker interface {
	// BlocksMutations reports whether protected resource mutations must stop.
	BlocksMutations() bool
}

// RegistryConfig configures a Runtime-owned resource registry.
type RegistryConfig struct {
	MaxResources int
	Clock        func() time.Time
	Lockdown     LockdownChecker
}

// Metrics are cheap atomic counters for operators.
type Metrics struct {
	Registered   atomic.Uint64
	Removed      atomic.Uint64
	Duplicates   atomic.Uint64
	BoundRejects atomic.Uint64
	EpochAdvances atomic.Uint64
	LockdownBlocks atomic.Uint64
}

// Snapshot is a point-in-time metrics view.
type MetricsSnapshot struct {
	Registered     uint64 `json:"registered"`
	Removed        uint64 `json:"removed"`
	Duplicates     uint64 `json:"duplicates"`
	BoundRejects   uint64 `json:"bound_rejects"`
	EpochAdvances  uint64 `json:"epoch_advances"`
	LockdownBlocks uint64 `json:"lockdown_blocks"`
	Count          int    `json:"count"`
}

// Registry is owned by Runtime — never a process-global singleton.
type Registry struct {
	mu       sync.RWMutex
	entries  map[string]*Entry
	max      int
	clock    func() time.Time
	lockdown LockdownChecker
	closed   bool
	deps     *DependencyGraph
	metrics  Metrics
}

// NewRegistry constructs an empty registry.
func NewRegistry(cfg RegistryConfig) *Registry {
	if cfg.MaxResources <= 0 {
		cfg.MaxResources = DefaultMaxResources
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Registry{
		entries:  make(map[string]*Entry),
		max:      cfg.MaxResources,
		clock:    cfg.Clock,
		lockdown: cfg.Lockdown,
		deps:     NewDependencyGraph(),
	}
}

// SetLockdown updates the lockdown checker (Runtime wiring).
func (r *Registry) SetLockdown(c LockdownChecker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lockdown = c
}

// Metrics returns a snapshot including current cardinality.
func (r *Registry) Metrics() MetricsSnapshot {
	r.mu.RLock()
	n := len(r.entries)
	r.mu.RUnlock()
	return MetricsSnapshot{
		Registered:     r.metrics.Registered.Load(),
		Removed:        r.metrics.Removed.Load(),
		Duplicates:     r.metrics.Duplicates.Load(),
		BoundRejects:   r.metrics.BoundRejects.Load(),
		EpochAdvances:  r.metrics.EpochAdvances.Load(),
		LockdownBlocks: r.metrics.LockdownBlocks.Load(),
		Count:          n,
	}
}

// Register adds a resource. Duplicate IDs are rejected.
func (r *Registry) Register(res Resource, meta Metadata) (*Entry, error) {
	if res == nil {
		return nil, &Error{Op: "Register", Err: ErrInvalidArgument, Message: "nil resource"}
	}
	id := res.ID()
	if err := id.Validate(); err != nil {
		return nil, wrap("Register", id, err, "invalid id")
	}
	if !ValidKind(id.Kind) {
		return nil, &Error{Op: "Register", ID: id, Err: ErrUnknownKind, Message: "unknown kind"}
	}
	if res.Kind() != id.Kind {
		return nil, &Error{Op: "Register", ID: id, Err: ErrInvalidArgument, Message: "kind mismatch between ID and Kind()"}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, &Error{Op: "Register", ID: id, Err: ErrClosed, Message: "registry closed"}
	}
	if err := r.checkMutationLocked("Register"); err != nil {
		return nil, err
	}
	key := id.String()
	if _, ok := r.entries[key]; ok {
		r.metrics.Duplicates.Add(1)
		return nil, &Error{Op: "Register", ID: id, Err: ErrDuplicate, Message: "already registered"}
	}
	if len(r.entries) >= r.max {
		r.metrics.BoundRejects.Add(1)
		return nil, &Error{Op: "Register", ID: id, Err: ErrBoundExceeded, Message: "max resources reached"}
	}
	ent := &Entry{
		Resource:     res,
		Meta:         meta.Clone(),
		Epoch:        0,
		RegisteredAt: r.clock().UTC(),
	}
	r.entries[key] = ent
	r.metrics.Registered.Add(1)
	return cloneEntry(ent), nil
}

// Remove deletes a resource and its dependency edges.
func (r *Registry) Remove(id ResourceID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return &Error{Op: "Remove", ID: id, Err: ErrClosed, Message: "registry closed"}
	}
	if err := r.checkMutationLocked("Remove"); err != nil {
		return err
	}
	key := id.String()
	if _, ok := r.entries[key]; !ok {
		return &Error{Op: "Remove", ID: id, Err: ErrNotFound, Message: "not found"}
	}
	delete(r.entries, key)
	r.deps.removeNode(key)
	r.metrics.Removed.Add(1)
	return nil
}

// Get returns a cloned entry.
func (r *Registry) Get(id ResourceID) (*Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ent, ok := r.entries[id.String()]
	if !ok {
		return nil, &Error{Op: "Get", ID: id, Err: ErrNotFound, Message: "not found"}
	}
	return cloneEntry(ent), nil
}

// List returns all entries sorted by canonical ID.
func (r *Registry) List() []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.entries))
	for k := range r.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*Entry, 0, len(keys))
	for _, k := range keys {
		out = append(out, cloneEntry(r.entries[k]))
	}
	return out
}

// Count returns current cardinality.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// MaxResources returns the configured bound.
func (r *Registry) MaxResources() int { return r.max }

// Advance advances the resource epoch with a required reason.
func (r *Registry) Advance(id ResourceID, reason string) (EpochAdvance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return EpochAdvance{}, &Error{Op: "Advance", ID: id, Err: ErrClosed, Message: "registry closed"}
	}
	if err := r.checkMutationLocked("Advance"); err != nil {
		return EpochAdvance{}, err
	}
	ent, ok := r.entries[id.String()]
	if !ok {
		return EpochAdvance{}, &Error{Op: "Advance", ID: id, Err: ErrNotFound, Message: "not found"}
	}
	next, adv, err := AdvanceEpoch(ent.Epoch, reason)
	if err != nil {
		return EpochAdvance{}, wrap("Advance", id, err, err.Error())
	}
	ent.Epoch = next
	r.metrics.EpochAdvances.Add(1)
	return adv, nil
}

// DefineDependency records that from depends on to (from → to).
func (r *Registry) DefineDependency(from, to ResourceID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return &Error{Op: "DefineDependency", Err: ErrClosed, Message: "registry closed"}
	}
	if err := r.checkMutationLocked("DefineDependency"); err != nil {
		return err
	}
	fk, tk := from.String(), to.String()
	if _, ok := r.entries[fk]; !ok {
		return &Error{Op: "DefineDependency", ID: from, Err: ErrNotFound, Message: "from not registered"}
	}
	if _, ok := r.entries[tk]; !ok {
		return &Error{Op: "DefineDependency", ID: to, Err: ErrNotFound, Message: "to not registered"}
	}
	return r.deps.Define(fk, tk)
}

// StartupOrder returns a deterministic topological order for activation.
func (r *Registry) StartupOrder() ([]ResourceID, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys, err := r.deps.StartupOrder(r.knownKeysLocked())
	if err != nil {
		return nil, err
	}
	return keysToIDs(keys)
}

// ShutdownOrder is the reverse of StartupOrder.
func (r *Registry) ShutdownOrder() ([]ResourceID, error) {
	order, err := r.StartupOrder()
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order, nil
}

// BlockedExplanation explains why id cannot activate (missing deps / unhealthy).
func (r *Registry) BlockedExplanation(ctx context.Context, id ResourceID) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := id.String()
	if _, ok := r.entries[key]; !ok {
		return "resource not registered"
	}
	missing := r.deps.MissingDependencies(key, r.entries)
	if len(missing) > 0 {
		return "blocked by missing or unhealthy dependencies: " + joinKeys(missing)
	}
	ent := r.entries[key]
	h := ent.Resource.Health(ctx)
	if h.Overall == HealthUnhealthy || h.Overall == HealthBlocked {
		return "resource health is " + string(h.Overall)
	}
	return ""
}

// Dependencies returns the dependency graph (copy-safe view).
func (r *Registry) Dependencies() *DependencyGraph {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.deps.Clone()
}

// Close prevents further mutations.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
}

func (r *Registry) checkMutationLocked(op string) error {
	if r.lockdown != nil && r.lockdown.BlocksMutations() {
		r.metrics.LockdownBlocks.Add(1)
		return &Error{Op: op, Err: ErrLockdown, Message: "lockdown active"}
	}
	return nil
}

func (r *Registry) knownKeysLocked() []string {
	keys := make([]string, 0, len(r.entries))
	for k := range r.entries {
		keys = append(keys, k)
	}
	return keys
}

func cloneEntry(e *Entry) *Entry {
	if e == nil {
		return nil
	}
	return &Entry{
		Resource:     e.Resource,
		Meta:         e.Meta.Clone(),
		Epoch:        e.Epoch,
		RegisteredAt: e.RegisteredAt,
	}
}

func keysToIDs(keys []string) ([]ResourceID, error) {
	out := make([]ResourceID, 0, len(keys))
	for _, k := range keys {
		id, err := ParseResourceID(k)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func joinKeys(keys []string) string {
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += k
	}
	return out
}
