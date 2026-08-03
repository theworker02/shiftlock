package shiftlock

import (
	"context"
	"time"
)

// HealthReport separates process/backend/coordinator/readiness/ownership health.
type HealthReport struct {
	Time         time.Time    `json:"time"`
	Process      HealthStatus `json:"process"`
	Backend      HealthStatus `json:"backend"`
	Coordinator  HealthStatus `json:"coordinator"`
	Readiness    HealthStatus `json:"readiness"`
	Ownership    HealthStatus `json:"ownership"`
	Degradation  DegradationPolicy `json:"degradation_policy"`
	Details      map[string]string `json:"details,omitempty"`
}

// HealthStatus is a coarse health signal.
type HealthStatus string

const (
	HealthOK       HealthStatus = "ok"
	HealthDegraded HealthStatus = "degraded"
	HealthFailing  HealthStatus = "failing"
	HealthUnknown  HealthStatus = "unknown"
)

// DegradationPolicy controls behavior when ownership cannot be proven.
type DegradationPolicy string

const (
	// DegradeFailClosed stops protected work immediately (default).
	DegradeFailClosed DegradationPolicy = "fail_closed"
	// DegradeContinueUntilMargin continues until lease expiry minus renew margin.
	DegradeContinueUntilMargin DegradationPolicy = "continue_until_lease_margin"
	// DegradeFinishCurrentOnly finishes in-flight DrainGroup ops then stops.
	DegradeFinishCurrentOnly DegradationPolicy = "finish_current_work_only"
	// DegradeImmediateStop cancels lease contexts immediately.
	DegradeImmediateStop DegradationPolicy = "immediate_stop"
)

// SplitBrainReport describes conflicting ownership observations.
type SplitBrainReport struct {
	Claim          string       `json:"claim"`
	LocalToken     FencingToken `json:"local_token"`
	BackendToken   FencingToken `json:"backend_token"`
	LocalOwner     string       `json:"local_owner"`
	BackendOwner   string       `json:"backend_owner"`
	DetectedAt     time.Time    `json:"detected_at"`
	ActionTaken    string       `json:"action_taken,omitempty"`
}

// Health returns a multi-axis health snapshot.
func (c *Coordinator) Health(_ context.Context) HealthReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	rep := HealthReport{
		Time:        c.clock.Now(),
		Process:     HealthOK,
		Backend:     HealthOK,
		Coordinator: HealthOK,
		Readiness:   HealthOK,
		Ownership:   HealthOK,
		Degradation: DegradeFailClosed,
		Details:     map[string]string{},
	}
	if c.closed {
		rep.Coordinator = HealthFailing
		rep.Details["coordinator"] = "closed"
	}
	if c.gen.State == StateFailed {
		rep.Coordinator = HealthFailing
	}
	if c.lastHeartbeat.IsZero() {
		rep.Backend = HealthUnknown
	} else if c.clock.Since(c.lastHeartbeat) > c.cfg.LeaseTTL {
		rep.Backend = HealthDegraded
		rep.Details["backend"] = "heartbeat stale"
	}
	owned := 0
	for _, cl := range c.claims {
		own := cl.Ownership()
		if own.OwnedBy(c.gen.ID) {
			owned++
		}
	}
	if owned == 0 && len(c.claims) > 0 {
		rep.Ownership = HealthUnknown
	}
	return rep
}

// DetectSplitBrain compares local claim view to backend.
func (c *Coordinator) DetectSplitBrain(ctx context.Context, claimName string) (*SplitBrainReport, error) {
	cl, err := c.Claim(ctx, claimName)
	if err != nil {
		return nil, err
	}
	local := cl.Ownership()
	rec, err := c.backend.GetClaim(ctx, claimName)
	if err != nil {
		return nil, err
	}
	remote := rec.ToOwnership()
	if local.FencingToken == remote.FencingToken && local.OwnerGeneration == remote.OwnerGeneration {
		return nil, nil
	}
	rep := &SplitBrainReport{
		Claim: claimName, LocalToken: local.FencingToken, BackendToken: remote.FencingToken,
		LocalOwner: local.OwnerGeneration, BackendOwner: remote.OwnerGeneration,
		DetectedAt: c.clock.Now(),
	}
	// Fail-closed: revoke local lease if backend disagrees and we are behind.
	if local.FencingToken.Less(remote.FencingToken) {
		cl.revokeLease(ReasonFencedOut)
		rep.ActionTaken = "revoked_local_lease"
		c.bus.emit(Event{Type: EventError, Claim: claimName, Err: ErrSplitBrain.Error(), Reason: ReasonFencedOut})
	}
	return rep, nil
}

// RecoveryPlan describes safe operator next steps (no auto-destructive actions).
type RecoveryPlan struct {
	Claim       string   `json:"claim"`
	Situation   string   `json:"situation"`
	Recommended []string `json:"recommended_actions"`
	RequiresConfirm bool `json:"requires_confirm"`
	ExpectedToken FencingToken `json:"expected_token,omitempty"`
	ExpectedOwner string `json:"expected_owner,omitempty"`
}

// PlanRecovery inspects claim state and proposes recovery steps.
func (c *Coordinator) PlanRecovery(ctx context.Context, claim string) (*RecoveryPlan, error) {
	rec, err := c.backend.GetClaim(ctx, claim)
	if err != nil {
		return &RecoveryPlan{
			Claim: claim, Situation: "claim_missing_or_error",
			Recommended: []string{"verify_backend", "inspect_journal"},
			RequiresConfirm: true,
		}, nil
	}
	plan := &RecoveryPlan{
		Claim: claim, ExpectedToken: rec.FencingToken, ExpectedOwner: rec.OwnerGeneration,
		RequiresConfirm: true,
	}
	switch {
	case rec.Phase == ClaimReserved:
		plan.Situation = "orphaned_or_pending_transfer"
		plan.Recommended = []string{"abort-transfer", "expire-candidate", "repair-orphaned-transfer"}
	case rec.Phase == ClaimUnowned:
		plan.Situation = "unowned"
		plan.Recommended = []string{"promote-candidate", "resume-previous-owner"}
	case rec.Phase == ClaimOwned:
		plan.Situation = "owned"
		plan.Recommended = []string{"monitor_heartbeats", "prepare_handoff_if_deploying"}
	default:
		plan.Situation = string(rec.Phase)
		plan.Recommended = []string{"inspect"}
	}
	return plan, nil
}
