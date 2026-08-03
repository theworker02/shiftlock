package shiftlock

import (
	"context"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/internal/stategraph"
	"github.com/theworker02/shiftlock/internal/supervisor"
)

// Worker runs under exclusive ownership of a named claim.
type Worker struct {
	// Name is the claim name to own before Run is invoked.
	Name string
	// Run executes work. ownership.Context() is canceled when the lease is lost.
	Run func(ctx context.Context, ownership *Lease) error
	// Readiness optional gates evaluated before acquiring ownership.
	Readiness *Readiness
}

// Coordinator manages a process generation and its claims.
type Coordinator struct {
	cfg     Config
	backend Backend
	clock   Clock
	bus     *eventBus
	sup     *supervisor.Supervisor
	caps    Capabilities

	mu     sync.Mutex
	gen    Generation
	claims map[string]*Claim
	closed bool
	opSeq  uint64

	lastHeartbeat time.Time
}

// New creates and registers a Coordinator generation.
func New(cfg Config) (*Coordinator, error) {
	cfg, err := cfg.defaults()
	if err != nil {
		return nil, err
	}
	now := cfg.Clock.Now()
	gen := Generation{
		ID:         cfg.GenerationID,
		Service:    cfg.Service,
		InstanceID: cfg.InstanceID,
		State:      StateJoining,
		StartedAt:  now,
		UpdatedAt:  now,
		Reason:     ReasonRegistered,
	}
	c := &Coordinator{
		cfg:     cfg,
		backend: cfg.Backend,
		clock:   cfg.Clock,
		bus:     newEventBus(cfg),
		sup:     supervisor.New(context.Background()),
		gen:     gen,
		claims:  make(map[string]*Claim),
	}
	caps := resolveCapabilities(cfg.Backend)
	if err := ValidateCapabilities(cfg, caps); err != nil {
		return nil, err
	}
	c.caps = caps
	if err := c.backend.RegisterGeneration(context.Background(), gen); err != nil {
		return nil, wrap("New", err)
	}
	_ = c.setState(StateStandby, ReasonRegistered)
	c.bus.emit(Event{Type: EventGenerationRegistered, Reason: ReasonRegistered, ToState: StateStandby})
	c.sup.GoNamed("events", c.bus.run)
	return c, nil
}

// Generation returns the current generation snapshot.
func (c *Coordinator) Generation() Generation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen.Clone()
}

// Config returns a copy of the effective configuration (backend omitted from safety? keep full).
func (c *Coordinator) Config() Config { return c.cfg }

// LastHeartbeat returns the last successful backend renew/heartbeat time.
func (c *Coordinator) LastHeartbeat() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastHeartbeat
}

// Capabilities returns negotiated backend capabilities.
func (c *Coordinator) Capabilities() Capabilities { return c.caps }

func (c *Coordinator) newOpID(op, claim string) OperationID {
	c.mu.Lock()
	c.opSeq++
	n := c.opSeq
	id := c.gen.ID
	c.mu.Unlock()
	return OperationID(id + ":" + op + ":" + claim + ":" + itoa64(n))
}

func itoa64(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func resolveCapabilities(b Backend) Capabilities {
	if c, ok := b.(Capabler); ok {
		return c.Capabilities()
	}
	// Unknown backends: assume minimal CAS only — require explicit Capabler for production.
	return Capabilities{AtomicCAS: true, GlobalExclusive: true}
}

// EventDropped returns count of async events dropped due to full buffer.
func (c *Coordinator) EventDropped() uint64 { return c.bus.Dropped() }

// Claim obtains a handle for a named ownership unit.
func (c *Coordinator) Claim(ctx context.Context, name string) (*Claim, error) {
	if name == "" {
		return nil, &Error{Op: "Claim", Err: ErrPolicy, Message: "claim name required"}
	}
	if err := c.checkOpen(); err != nil {
		return nil, wrap("Claim", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.Policy.MaxConcurrentClaims > 0 && len(c.claims) >= c.cfg.Policy.MaxConcurrentClaims {
		if _, ok := c.claims[name]; !ok {
			return nil, &Error{Op: "Claim", Err: ErrPolicy, Message: "MaxConcurrentClaims exceeded"}
		}
	}
	if cl, ok := c.claims[name]; ok {
		return cl, nil
	}
	cl := &Claim{
		name:  name,
		coord: c,
		drain: NewDrainGroup(0),
		record: ClaimRecord{Name: name, Phase: ClaimUnowned},
	}
	c.claims[name] = cl
	_ = ctx
	return cl, nil
}

// Run acquires ownership for worker.Name and invokes worker.Run.
// It returns when Run returns or ownership is lost.
func (c *Coordinator) Run(ctx context.Context, worker Worker) error {
	if worker.Name == "" || worker.Run == nil {
		return &Error{Op: "Run", Err: ErrPolicy, Message: "worker Name and Run required"}
	}
	if worker.Readiness != nil {
		_ = c.setState(StatePreparing, ReasonRegistered)
		c.bus.emit(Event{Type: EventReadinessStarted, Claim: worker.Name})
		r := *worker.Readiness
		if r.Clock == nil {
			r.Clock = c.clock
		}
		if r.Timeout <= 0 {
			r.Timeout = c.cfg.ReadinessTimeout
		}
		rep, err := r.Run(ctx)
		if err != nil {
			_ = c.setState(StateFailed, ReasonReadinessFailed)
			c.bus.emit(Event{Type: EventReadinessFailed, Claim: worker.Name, Err: err.Error(), Reason: ReasonReadinessFailed})
			return wrap("Run", err)
		}
		_ = rep
		c.bus.emit(Event{Type: EventReadinessPassed, Claim: worker.Name, Reason: ReasonReadinessPassed})
		_ = c.setState(StateStandby, ReasonReadinessPassed)
	}

	claim, err := c.Claim(ctx, worker.Name)
	if err != nil {
		return wrap("Run", err)
	}
	lease, err := claim.WaitForOwnership(ctx)
	if err != nil {
		return wrap("Run", err)
	}
	return worker.Run(lease.Context(), lease)
}

// PrepareHandoff starts a graceful ownership handoff as the current owner.
func (c *Coordinator) PrepareHandoff(ctx context.Context) (*Handoff, error) {
	if err := c.checkOpen(); err != nil {
		return nil, wrap("PrepareHandoff", err)
	}
	c.mu.Lock()
	claims := make([]*Claim, 0, len(c.claims))
	for _, cl := range c.claims {
		claims = append(claims, cl)
	}
	c.mu.Unlock()

	h := &Handoff{
		coord:  c,
		claims: claims,
		status: HandoffPending,
	}
	if err := c.setState(StateDraining, ReasonDrainStarted); err != nil {
		return nil, wrap("PrepareHandoff", err)
	}
	c.bus.emit(Event{Type: EventHandoffStarted, Reason: ReasonDrainStarted})
	_ = ctx
	return h, nil
}

// Close stops all internal goroutines and releases resources.
// Ownership is not forcibly released (successor may take over via expiry);
// use PrepareHandoff for graceful transfer.
func (c *Coordinator) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	claims := make([]*Claim, 0, len(c.claims))
	for _, cl := range c.claims {
		claims = append(claims, cl)
	}
	c.mu.Unlock()

	for _, cl := range claims {
		cl.close()
	}
	_ = c.setState(StateRetired, ReasonClosed)
	c.bus.emit(Event{Type: EventClosed, Reason: ReasonClosed})
	c.sup.Shutdown()
	return nil
}

func (c *Coordinator) checkOpen() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	return nil
}

func (c *Coordinator) setState(to GenerationState, reason TransitionReason) error {
	c.mu.Lock()
	from := c.gen.State
	if from == to {
		c.gen.Reason = reason
		c.gen.UpdatedAt = c.clock.Now()
		c.mu.Unlock()
		return nil
	}
	if !stategraph.CanTransition(stategraph.State(from), stategraph.State(to)) {
		c.mu.Unlock()
		c.bus.emit(Event{
			Type: EventError, FromState: from, ToState: to, Reason: reason,
			Err: ErrInvalidState.Error(),
		})
		return &Error{
			Op:      "setState",
			Err:     ErrInvalidState,
			Reason:  reason,
			Message: string(from) + " -> " + string(to),
		}
	}
	c.gen.State = to
	c.gen.Reason = reason
	c.gen.UpdatedAt = c.clock.Now()
	gen := c.gen.Clone()
	c.mu.Unlock()

	_ = c.backend.UpdateGeneration(context.Background(), gen)
	c.bus.emit(Event{
		Type:      EventGenerationState,
		FromState: from,
		ToState:   to,
		Reason:    reason,
	})
	return nil
}

func (c *Coordinator) startRenewal(claim *Claim) {
	if !claim.markRenewing() {
		return
	}
	c.sup.GoNamed("renew:"+claim.name, func(ctx context.Context) {
		defer claim.clearRenewing()
		ticker := c.clock.NewTicker(c.cfg.RenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C():
				token, ok := claim.currentLeaseToken()
				if !ok {
					return
				}
				ttl := c.cfg.LeaseTTL
				own := claim.Ownership()
				if own.Phase == ClaimReserved {
					ttl = reservationTTL(c.cfg.LeaseTTL, c.cfg.TransferTimeout)
				}
				rec, err := c.backend.RenewClaim(ctx, RenewRequest{
					ClaimName:    claim.name,
					GenerationID: c.gen.ID,
					Token:        token,
					TTL:          ttl,
					OperationID:  c.newOpID("renew", claim.name),
				})
				if err != nil {
					claim.revokeLease(ReasonExpired)
					c.bus.emit(Event{
						Type: EventClaimLost, Claim: claim.name, Token: token,
						Err: err.Error(), Reason: ReasonExpired,
					})
					return
				}
				claim.applyRecord(rec)
				c.mu.Lock()
				c.lastHeartbeat = c.clock.Now()
				c.mu.Unlock()
				c.bus.emit(Event{
					Type: EventClaimRenewed, Claim: claim.name, Token: rec.FencingToken,
					Reason: ReasonRenewed,
				})
				c.bus.emit(Event{Type: EventBackendHeartbeat, Claim: claim.name})
			}
		}
	})
}

// Diagnostics returns a sanitized snapshot for HTTP/CLI inspection.
func (c *Coordinator) Diagnostics() Diagnostics {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := Diagnostics{
		Service:       c.cfg.Service,
		InstanceID:    c.cfg.InstanceID,
		Generation:    c.gen.Clone(),
		LastHeartbeat: c.lastHeartbeat,
		EventDropped:  c.bus.Dropped(),
		Capabilities:  c.caps,
		Claims:        make([]Ownership, 0, len(c.claims)),
	}
	for _, cl := range c.claims {
		d.Claims = append(d.Claims, cl.Ownership())
	}
	return d
}

// Diagnostics is a sanitized coordinator snapshot (no secrets).
type Diagnostics struct {
	Service       string       `json:"service"`
	InstanceID    string       `json:"instance_id"`
	Generation    Generation   `json:"generation"`
	LastHeartbeat time.Time    `json:"last_heartbeat,omitempty"`
	EventDropped  uint64       `json:"event_dropped"`
	Capabilities  Capabilities `json:"capabilities"`
	Claims        []Ownership  `json:"claims"`
}
