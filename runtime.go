package shiftlock

import (
	"context"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/audit"
	"github.com/theworker02/shiftlock/barrier"
	"github.com/theworker02/shiftlock/capability"
	"github.com/theworker02/shiftlock/control/command"
	"github.com/theworker02/shiftlock/control/execguard"
	"github.com/theworker02/shiftlock/control/lockdown"
	"github.com/theworker02/shiftlock/control/maintenance"
	"github.com/theworker02/shiftlock/election"
	"github.com/theworker02/shiftlock/failover"
	"github.com/theworker02/shiftlock/guard"
	"github.com/theworker02/shiftlock/health"
	"github.com/theworker02/shiftlock/migration"
	"github.com/theworker02/shiftlock/resource"
	"github.com/theworker02/shiftlock/security/antireplay"
	"github.com/theworker02/shiftlock/supervise"
	syncpkg "github.com/theworker02/shiftlock/sync"
	"github.com/theworker02/shiftlock/workflow"
)

// RuntimeConfig configures an opt-in security-aware Runtime.
// Existing Coordinator Config remains valid; security subsystems are additive.
type RuntimeConfig struct {
	Config

	// SecurityProfile expands to inspectable SecuritySettings.
	SecurityProfile SecurityProfile

	// SecurityOverrides overlays duration/numeric fields from the profile.
	SecurityOverrides *SecuritySettings

	// ApplySecurityOverridesBooleans when true copies boolean fields from SecurityOverrides.
	ApplySecurityOverridesBooleans bool

	EnableSupervisor   bool
	EnableCommands     bool
	EnableMaintenance  bool
	EnableLockdown     bool
	EnableCapabilities bool
	EnableGuard        bool
	EnableAudit        bool

	// Phase 7 fabric (opt-in; Coordinator APIs unchanged).
	EnableResources bool
	EnableWorkflows bool
	MaxResources    int
	LocalState      *LocalStateConfig

	// AuditStore overrides the default in-memory audit store.
	AuditStore *audit.Store

	MaintenancePath      string
	LockdownPath         string
	LockdownEvidencePath string

	// Capability options applied when EnableCapabilities is set.
	CapabilityOptions []capability.Option

	SecurityEpoch SecurityEpoch
}

// FeatureFlags reports which Runtime subsystems are active.
type FeatureFlags struct {
	Supervisor   bool `json:"supervisor"`
	Commands     bool `json:"commands"`
	Maintenance  bool `json:"maintenance"`
	Lockdown     bool `json:"lockdown"`
	Capabilities bool `json:"capabilities"`
	Guard        bool `json:"guard"`
	Audit        bool `json:"audit"`
	AntiReplay   bool `json:"anti_replay"`
	Exec         bool `json:"exec"`
	Resources    bool `json:"resources"`
	Workflows    bool `json:"workflows"`
}

// Runtime composes Coordinator with optional security and control-plane services.
type Runtime struct {
	coord *Coordinator
	cfg   RuntimeConfig
	sec   SecuritySettings
	epoch SecurityEpoch

	replay *ReplayCache
	ar     *antireplay.Cache

	caps  *capability.Authority
	guard *guard.Engine
	audit *audit.Store

	sup   *supervise.Supervisor
	cmds  *command.Registry
	maint *maintenance.Manager
	lock  *lockdown.Manager
	exec  *execguard.Guard

	resources  *resource.Registry
	workflows  *workflow.Engine
	migrations *migration.Coordinator
	failoverMgr *failover.Manager
	syncEngine *syncpkg.Engine
	localStateDir string

	mu          sync.Mutex
	quarantined bool
	closed      bool
	floodHits   []time.Time
	elections   map[string]*election.Election
	barriers    map[string]*barrier.Barrier
	features    FeatureFlags
}

// NewRuntime constructs a Runtime. defer runtime.Close() stops all subsystems.
func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
	profile := cfg.SecurityProfile
	if profile == "" {
		profile = ProfileStandard
	}
	sec := ExpandSecurityProfile(profile)
	if cfg.SecurityOverrides != nil {
		sec = MergeSecurity(sec, *cfg.SecurityOverrides)
		if cfg.ApplySecurityOverridesBooleans {
			o := cfg.SecurityOverrides
			sec.DenyPrivilegedByDefault = o.DenyPrivilegedByDefault
			sec.RequireCapabilityForPrivileged = o.RequireCapabilityForPrivileged
			sec.AuditEnabled = o.AuditEnabled
			sec.AuditFailClosed = o.AuditFailClosed
			sec.AntiReplayEnabled = o.AntiReplayEnabled
			sec.RequireSingleUseCapabilities = o.RequireSingleUseCapabilities
			sec.LockdownOnAuditTamper = o.LockdownOnAuditTamper
			sec.LockdownOnSplitBrain = o.LockdownOnSplitBrain
			sec.LockdownOnCommandFlood = o.LockdownOnCommandFlood
			sec.AllowExec = o.AllowExec
			sec.QuarantineOnTamper = o.QuarantineOnTamper
		}
	}
	sec.Profile = profile

	coord, err := New(cfg.Config)
	if err != nil {
		return nil, err
	}

	rt := &Runtime{
		coord:     coord,
		cfg:       cfg,
		sec:       sec,
		epoch:     cfg.SecurityEpoch,
		elections: make(map[string]*election.Election),
		barriers:  make(map[string]*barrier.Barrier),
	}

	if sec.AntiReplayEnabled {
		rt.replay = NewReplayCache(sec.AntiReplayMaxEntries, cfg.Clock)
		rt.ar = antireplay.New(sec.AntiReplayMaxEntries)
		rt.features.AntiReplay = true
	}

	enableAudit := cfg.EnableAudit || sec.AuditEnabled
	if enableAudit {
		if cfg.AuditStore != nil {
			rt.audit = cfg.AuditStore
		} else {
			rt.audit = audit.New()
		}
		rt.features.Audit = true
	}

	if cfg.EnableGuard || sec.DenyPrivilegedByDefault {
		rt.guard = guard.New()
		_ = rt.guard.AddRule(guard.Rule{
			Name: "deny-force-release", Permission: "claim.force_release", Decision: guard.Deny, Priority: 0,
		})
		_ = rt.guard.AddRule(guard.Rule{
			Name: "deny-lockdown", Permission: "lockdown.*", Decision: guard.Deny, Priority: 0,
		})
		_ = rt.guard.AddRule(guard.Rule{
			Name: "deny-exec", Permission: "exec.*", Decision: guard.Deny, Priority: 100,
		})
		rt.features.Guard = true
	}

	if cfg.EnableCapabilities || sec.RequireCapabilityForPrivileged {
		opts := append([]capability.Option{}, cfg.CapabilityOptions...)
		if rt.ar != nil {
			opts = append(opts, capability.WithReplayCache(rt.ar))
		}
		if cfg.Clock != nil {
			clock := cfg.Clock
			opts = append(opts, capability.WithClock(func() time.Time { return clock.Now() }))
		}
		rt.caps = capability.New(opts...)
		// Align authority epoch with configured security epoch.
		for uint64(rt.caps.Epoch()) < uint64(cfg.SecurityEpoch) {
			if _, err := rt.caps.AdvanceEpoch(); err != nil {
				_ = coord.Close()
				return nil, err
			}
		}
		rt.features.Capabilities = true
	}

	// Exec is deny-all by default (empty allowlist). Callers may replace via ExecGuard.
	eg, err := execguard.New(execguard.Policy{DryRun: !sec.AllowExec})
	if err != nil {
		_ = coord.Close()
		return nil, err
	}
	rt.exec = eg
	if sec.AllowExec {
		rt.features.Exec = true
	}

	if cfg.EnableMaintenance {
		m, err := maintenance.New(maintenance.Config{DurablePath: cfg.MaintenancePath})
		if err != nil {
			_ = rt.partialClose()
			return nil, err
		}
		rt.maint = m
		rt.features.Maintenance = true
	}

	if cfg.EnableLockdown {
		l, err := lockdown.New(lockdown.Config{
			DurablePath: cfg.LockdownPath, EvidencePath: cfg.LockdownEvidencePath,
		})
		if err != nil {
			_ = rt.partialClose()
			return nil, err
		}
		rt.lock = l
		rt.features.Lockdown = true
	}

	if cfg.EnableSupervisor {
		rt.sup = supervise.New(context.Background(), rt)
		rt.features.Supervisor = true
	}

	if cfg.EnableCommands {
		rt.cmds = command.New(command.Config{
			Authorizer:      rt,
			Auditor:         rt,
			MaxBodyBytes:    sec.MaxCommandBodyBytes,
			DefaultDeadline: sec.DefaultCommandDeadline,
		})
		rt.features.Commands = true
	}

	rt.initFabric()

	return rt, nil
}

func (r *Runtime) partialClose() error {
	return r.coord.Close()
}

// Close stops all subsystems then the coordinator.
func (r *Runtime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	elecs := make([]*election.Election, 0, len(r.elections))
	for _, e := range r.elections {
		elecs = append(elecs, e)
	}
	r.mu.Unlock()

	for _, e := range elecs {
		_ = e.Close(context.Background())
	}
	if r.sup != nil {
		_ = r.sup.Close()
	}
	if r.cmds != nil {
		_ = r.cmds.Close()
	}
	if r.workflows != nil {
		r.workflows.Close()
	}
	if r.resources != nil {
		r.resources.Close()
	}
	return r.coord.Close()
}

// Coordinator returns the underlying Phase 5 coordinator.
func (r *Runtime) Coordinator() *Coordinator { return r.coord }

// Security returns inspectable expanded settings.
func (r *Runtime) Security() SecuritySettings { return r.sec }

// SecurityEpoch returns the current epoch.
func (r *Runtime) SecurityEpoch() SecurityEpoch { return r.epoch }

// Features returns active subsystem flags.
func (r *Runtime) Features() FeatureFlags { return r.features }

// Claims is a convenience alias for the coordinator (claim access).
func (r *Runtime) Claims() *Coordinator { return r.coord }

// Supervisor returns the task supervisor (may be nil).
func (r *Runtime) Supervisor() *supervise.Supervisor { return r.sup }

// Election returns the election façade.
func (r *Runtime) Election() *ElectionFacade { return &ElectionFacade{rt: r} }

// Commands returns the command registry (may be nil).
func (r *Runtime) Commands() *command.Registry { return r.cmds }

// Maintenance returns the maintenance manager (may be nil).
func (r *Runtime) Maintenance() *maintenance.Manager { return r.maint }

// Lockdown returns the lockdown manager (may be nil).
func (r *Runtime) Lockdown() *lockdown.Manager { return r.lock }

// Health returns an extended health graph report.
func (r *Runtime) Health(ctx context.Context) health.Report {
	b := health.NewBuilder()
	base := r.coord.Health(ctx)
	b.Set(health.Node{Name: "process", Status: mapLegacyHealth(base.Process)})
	b.Set(health.Node{Name: "backend", Status: mapLegacyHealth(base.Backend)})
	b.Set(health.Node{Name: "coordinator", Status: mapLegacyHealth(base.Coordinator)})
	b.Set(health.Node{Name: "ownership", Status: mapLegacyHealth(base.Ownership)})
	b.Link("coordinator", "backend")
	b.Link("ownership", "coordinator")

	r.mu.Lock()
	q := r.quarantined
	r.mu.Unlock()
	if q {
		b.Set(health.Node{Name: "quarantine", Status: health.Quarantined, Message: "generation quarantined"})
	}
	if r.lock != nil && r.lock.Active() {
		b.Set(health.Node{Name: "lockdown", Status: health.LockedDown, Message: r.lock.State().Reason})
	}
	if r.maint != nil && r.maint.Active() {
		b.Set(health.Node{Name: "maintenance", Status: health.Blocked, Message: r.maint.State().Reason})
	}
	now := time.Now()
	if r.coord.clock != nil {
		now = r.coord.clock.Now()
	}
	return b.Build(now)
}

// Audit returns the audit store (may be nil).
func (r *Runtime) Audit() *audit.Store { return r.audit }

// Recovery returns recovery planning façade.
func (r *Runtime) Recovery() *RecoveryFacade { return &RecoveryFacade{rt: r} }

// Capabilities returns the capability authority (may be nil).
func (r *Runtime) Capabilities() *capability.Authority { return r.caps }

// Guard returns the policy engine (may be nil).
func (r *Runtime) Guard() *guard.Engine { return r.guard }

// Barrier returns the barrier façade.
func (r *Runtime) Barrier() *BarrierFacade { return &BarrierFacade{rt: r} }

// Quorum returns a quorum helper façade (barrier policy-based).
func (r *Runtime) Quorum() *QuorumFacade { return &QuorumFacade{rt: r} }

// ExecGuard returns the exec allowlist guard.
func (r *Runtime) ExecGuard() *execguard.Guard { return r.exec }

// Replay returns the root anti-replay cache (may be nil).
func (r *Runtime) Replay() *ReplayCache { return r.replay }

// Quarantined reports generation quarantine.
func (r *Runtime) Quarantined() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.quarantined
}

// EnterQuarantine marks the generation unable to acquire claims / vote / issue caps.
func (r *Runtime) EnterQuarantine(reason string) {
	r.mu.Lock()
	r.quarantined = true
	r.mu.Unlock()
	r.auditAction("system", "quarantine.enter", reason, "ok", "")
}

// IssueCapability issues a capability when the authority is enabled and not quarantined.
func (r *Runtime) IssueCapability(req capability.Request) (capability.Token, error) {
	if err := r.checkControlGates("Capability.Issue"); err != nil {
		return capability.Token{}, err
	}
	if r.caps == nil {
		return capability.Token{}, &Error{Op: "Capability.Issue", Err: ErrCapabilityToken, Code: CodeCapabilityInvalid, Category: CategoryCapability, Message: "capabilities not enabled"}
	}
	if r.sec.RequireSingleUseCapabilities {
		req.Constraints.SingleUse = true
	}
	if req.TTL <= 0 {
		req.TTL = r.sec.CapabilityDefaultTTL
	}
	if req.TTL > r.sec.CapabilityMaxTTL {
		req.TTL = r.sec.CapabilityMaxTTL
	}
	tok, err := r.caps.Issue(req)
	if err != nil {
		return capability.Token{}, err
	}
	r.auditAction(req.Subject, "capability.issue", string(req.Permission), "ok", string(tok.ID))
	return tok, nil
}

func (r *Runtime) checkControlGates(op string) error {
	r.mu.Lock()
	closed := r.closed
	q := r.quarantined
	r.mu.Unlock()
	if closed {
		return &Error{Op: op, Err: ErrRuntimeClosed, Code: CodeForbidden, Category: CategorySecurity, Message: "runtime closed"}
	}
	if q {
		return &Error{Op: op, Err: ErrQuarantined, Code: CodeQuarantined, Category: CategoryQuarantine, Message: "generation quarantined"}
	}
	if r.lock != nil && r.lock.Active() {
		st := r.lock.State()
		if st.Mode == lockdown.ModeFailClosed || st.Mode == lockdown.ModeFullService || st.Mode == lockdown.ModeIsolateClaims {
			return &Error{Op: op, Err: ErrLockdown, Code: CodeLockdownActive, Category: CategoryLockdown, Message: "lockdown active"}
		}
	}
	return nil
}

// AllowTask implements supervise.Gate.
func (r *Runtime) AllowTask(spec supervise.Spec) error {
	if err := r.checkControlGates("Supervise"); err != nil {
		return err
	}
	if r.lock != nil && r.lock.Active() {
		mode := r.lock.State().Mode
		if mode == lockdown.ModeIsolateTasks || mode == lockdown.ModeFullService || mode == lockdown.ModeFailClosed {
			return ErrLockdown
		}
	}
	if r.maint != nil && r.maint.Active() && spec.Mode != supervise.ModeMaintenanceOnly {
		st := r.maint.State()
		if st.Scope.All {
			return ErrMaintenance
		}
	}
	return nil
}

// AllowElection implements election.Gate.
func (r *Runtime) AllowElection(_ string) error {
	return r.checkControlGates("Election")
}

// AuthorizeCommand implements command.Authorizer.
func (r *Runtime) AuthorizeCommand(actor, name, permission string) error {
	if err := r.checkControlGates("Command"); err != nil {
		r.noteUnauthorized()
		return err
	}
	if r.guard != nil {
		dec := r.guard.Evaluate(guard.Request{Principal: actor, Permission: permission, Resource: name, Action: "invoke"})
		if dec == guard.Deny {
			r.noteUnauthorized()
			return ErrGuardDenied
		}
		if dec == guard.RequireApproval || dec == guard.RequireQuorum {
			r.noteUnauthorized()
			return ErrForbidden
		}
	} else if r.sec.DenyPrivilegedByDefault && IsPrivileged(Permission(permission)) {
		r.noteUnauthorized()
		return ErrForbidden
	}
	return nil
}

// AuditCommand implements command.Auditor.
func (r *Runtime) AuditCommand(actor, name, decision, outcome string) {
	r.auditAction(actor, "command."+name, name, decision, outcome)
	if decision == "deny" && outcome == "unauthorized" {
		r.noteUnauthorized()
	}
}

func (r *Runtime) noteUnauthorized() {
	if r.lock == nil || !r.sec.LockdownOnCommandFlood {
		return
	}
	r.mu.Lock()
	now := time.Now()
	cut := now.Add(-r.sec.CommandFloodWindow)
	hits := r.floodHits[:0]
	for _, t := range r.floodHits {
		if t.After(cut) {
			hits = append(hits, t)
		}
	}
	hits = append(hits, now)
	r.floodHits = hits
	n := len(hits)
	r.mu.Unlock()
	if n >= r.sec.CommandFloodThreshold {
		_, _, _ = r.lock.TryAutoEnter("unauthorized_command_flood", "unauthorized command flood")
		r.auditAction("system", "lockdown.auto", "command_flood", "entered", "")
	}
}

func (r *Runtime) auditAction(actor, action, resource, result, operationID string) {
	if r.audit == nil {
		return
	}
	_, err := r.audit.Append(audit.Actor{ID: actor, Type: "principal"}, action, resource, result, operationID, nil)
	if err != nil && r.sec.AuditFailClosed {
		if r.sec.QuarantineOnTamper {
			r.EnterQuarantine("audit_append_failed")
		}
	}
}

// VerifyAudit runs chain verification and may auto-lockdown / quarantine.
func (r *Runtime) VerifyAudit() error {
	if r.audit == nil {
		return nil
	}
	rep := r.audit.Verify()
	if !rep.OK {
		if r.sec.LockdownOnAuditTamper && r.lock != nil {
			_, _, _ = r.lock.TryAutoEnter("audit_tamper", "audit chain tamper detected")
		}
		if r.sec.QuarantineOnTamper {
			r.EnterQuarantine("audit_tamper")
		}
		return &Error{Op: "VerifyAudit", Err: ErrAuditTamper, Code: CodeAuditTamper, Category: CategoryAudit, Message: "audit tamper detected"}
	}
	return nil
}

// TriggerSplitBrainLockdown is invoked after DetectSplitBrain when profiles request it.
func (r *Runtime) TriggerSplitBrainLockdown(claim string) {
	if !r.sec.LockdownOnSplitBrain || r.lock == nil {
		return
	}
	_, _, _ = r.lock.TryAutoEnter("split_brain", "split-brain on "+claim)
	if r.sec.QuarantineOnTamper {
		r.EnterQuarantine("split_brain")
	}
}

// FencedClaims adapts Coordinator claims for election/syncprim.
type FencedClaims struct{ RT *Runtime }

// Acquire obtains a claim lease token.
func (f FencedClaims) Acquire(ctx context.Context, claim string) (uint64, error) {
	if err := f.RT.checkControlGates("Acquire"); err != nil {
		return 0, err
	}
	cl, err := f.RT.coord.Claim(ctx, claim)
	if err != nil {
		return 0, err
	}
	lease, err := cl.TryAcquire(ctx)
	if err != nil {
		return 0, err
	}
	return uint64(lease.FencingToken()), nil
}

// Release releases a claim.
func (f FencedClaims) Release(ctx context.Context, claim string, _ uint64) error {
	cl, err := f.RT.coord.Claim(ctx, claim)
	if err != nil {
		return err
	}
	return cl.Release(ctx)
}

// Renew checks continued ownership (coordinator renew loop handles heartbeats).
func (f FencedClaims) Renew(_ context.Context, claim string, token uint64) error {
	cl, err := f.RT.coord.Claim(context.Background(), claim)
	if err != nil {
		return err
	}
	own := cl.Ownership()
	if !own.Controls(f.RT.coord.Generation().ID) || uint64(own.FencingToken) != token {
		return ErrNotOwner
	}
	return nil
}

// ElectionFacade exposes Join.
type ElectionFacade struct{ rt *Runtime }

// Join starts participating in a named election.
func (f *ElectionFacade) Join(ctx context.Context, name string) (*election.Election, error) {
	if err := f.rt.checkControlGates("Election.Join"); err != nil {
		return nil, err
	}
	e, err := election.Join(ctx, election.Config{
		Name: name, ParticipantID: f.rt.coord.cfg.InstanceID,
		Lock: FencedClaims{RT: f.rt}, Gate: f.rt,
	})
	if err != nil {
		return nil, err
	}
	f.rt.mu.Lock()
	f.rt.elections[name] = e
	f.rt.mu.Unlock()
	return e, nil
}

// BarrierFacade creates barriers.
type BarrierFacade struct{ rt *Runtime }

// New creates a named barrier.
func (f *BarrierFacade) New(name string, cfg barrier.Config) (*barrier.Barrier, error) {
	cfg.Name = name
	if cfg.Epoch == 0 {
		cfg.Epoch = uint64(f.rt.epoch)
	}
	b, err := barrier.New(cfg)
	if err != nil {
		return nil, err
	}
	f.rt.mu.Lock()
	f.rt.barriers[name] = b
	f.rt.mu.Unlock()
	return b, nil
}

// QuorumFacade creates quorum barriers.
type QuorumFacade struct{ rt *Runtime }

// New creates a quorum barrier (majority of max).
func (f *QuorumFacade) New(name string, maxParticipants int) (*barrier.Barrier, error) {
	return f.rt.Barrier().New(name, barrier.Config{
		MaxParticipants: maxParticipants,
		Policy:          barrier.PolicyQuorum,
		Epoch:           uint64(f.rt.epoch),
	})
}

// RecoveryFacade wraps recovery planning.
type RecoveryFacade struct{ rt *Runtime }

// Plan inspects claim state for operator recovery.
func (f *RecoveryFacade) Plan(ctx context.Context, claim string) (*RecoveryPlan, error) {
	if f.rt.Quarantined() {
		return &RecoveryPlan{
			Claim: claim, Situation: "quarantined",
			Recommended:     []string{"inspect_audit", "clear_quarantine_with_auth"},
			RequiresConfirm: true,
		}, nil
	}
	return f.rt.coord.PlanRecovery(ctx, claim)
}

func mapLegacyHealth(s HealthStatus) health.Status {
	switch s {
	case HealthOK:
		return health.Healthy
	case HealthDegraded:
		return health.Degraded
	case HealthFailing:
		return health.Unhealthy
	default:
		return health.Unknown
	}
}
