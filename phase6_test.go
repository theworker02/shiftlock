package shiftlock_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/barrier"
	"github.com/theworker02/shiftlock/capability"
	"github.com/theworker02/shiftlock/control/command"
	"github.com/theworker02/shiftlock/control/lockdown"
	"github.com/theworker02/shiftlock/control/maintenance"
	"github.com/theworker02/shiftlock/guard"
	"github.com/theworker02/shiftlock/supervise"
	"github.com/theworker02/shiftlock/syncprim"
)

func testRuntime(t *testing.T, profile shiftlock.SecurityProfile) *shiftlock.Runtime {
	t.Helper()
	be := memory.New()
	t.Cleanup(func() { _ = be.Close() })
	rt, err := shiftlock.NewRuntime(shiftlock.RuntimeConfig{
		Config: shiftlock.Config{
			Service: "phase6", InstanceID: "test-1", Backend: be,
			LeaseTTL: 5 * time.Second,
		},
		SecurityProfile:    profile,
		EnableSupervisor:   true,
		EnableCommands:     true,
		EnableMaintenance:  true,
		EnableLockdown:     true,
		EnableCapabilities: true,
		EnableGuard:        true,
		EnableAudit:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func TestNewRuntimeFeaturesAndClose(t *testing.T) {
	rt := testRuntime(t, shiftlock.ProfileTesting)
	f := rt.Features()
	if !f.Supervisor || !f.Commands || !f.Audit || !f.Capabilities || !f.Guard {
		t.Fatalf("expected subsystems enabled: %+v", f)
	}
	sec := rt.Security()
	if sec.Profile != shiftlock.ProfileTesting {
		t.Fatalf("profile %s", sec.Profile)
	}
	if !sec.DenyPrivilegedByDefault {
		t.Fatal("expected deny privileged by default")
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSecurityEpochNeverDecreases(t *testing.T) {
	var e shiftlock.SecurityEpoch = shiftlock.MaxSecurityEpoch
	if _, err := e.Next(); !errors.Is(err, shiftlock.ErrSecurityEpochOverflow) {
		t.Fatalf("want overflow, got %v", err)
	}
}

func TestReplayCacheBounded(t *testing.T) {
	c := shiftlock.NewReplayCache(2, nil)
	if err := c.CheckAndStore("a", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckAndStore("a", time.Minute); !errors.Is(err, shiftlock.ErrReplay) {
		t.Fatalf("want replay, got %v", err)
	}
	_ = c.CheckAndStore("b", time.Minute)
	_ = c.CheckAndStore("c", time.Minute) // evicts oldest
	if c.Len() > 2 {
		t.Fatalf("unbounded cache len=%d", c.Len())
	}
}

func TestErrorTaxonomyPublicMessage(t *testing.T) {
	e := &shiftlock.Error{
		Op: "test", Err: shiftlock.ErrUnauthorized, Code: shiftlock.CodeUnauthorized,
		Category: shiftlock.CategoryAuthorization, Message: "denied",
	}
	if e.PublicMessage() != "denied" {
		t.Fatal(e.PublicMessage())
	}
	if !errors.Is(e, shiftlock.ErrUnauthorized) {
		t.Fatal("Is failed")
	}
}

func TestGuardDefaultDenyExplain(t *testing.T) {
	g := guard.New()
	ex := g.Explain(guard.Request{Permission: "lockdown.enter"})
	if ex.Decision != guard.Deny {
		t.Fatalf("got %s", ex.Decision)
	}
	_ = g.AddRule(guard.Rule{Name: "allow-ops", Principal: "ops", Permission: "lockdown.enter", Decision: guard.Allow, Priority: 10})
	if g.Evaluate(guard.Request{Principal: "ops", Permission: "lockdown.enter"}) != guard.Allow {
		t.Fatal("expected allow")
	}
}

func TestSupervisorBoundedRestart(t *testing.T) {
	rt := testRuntime(t, shiftlock.ProfileDevelopment)
	sup := rt.Supervisor()
	var n atomic.Int32
	err := sup.StartSpec(supervise.Spec{
		Name: "flaky2", Mode: supervise.ModePerInstance,
		Restart: supervise.RestartPolicy{MaxRestarts: 2, Interval: time.Millisecond},
		Failure: supervise.FailRestart,
		Run: func(ctx context.Context) error {
			n.Add(1)
			return errors.New("boom")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	_ = sup.Close()
	got := n.Load()
	if got < 1 || got > 4 {
		t.Fatalf("restarts not bounded: n=%d", got)
	}
}

func TestBarrierQuorumEpoch(t *testing.T) {
	rt := testRuntime(t, shiftlock.ProfileTesting)
	b, err := rt.Quorum().New("q1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Arrive("a", uint64(rt.SecurityEpoch())); err != nil {
		t.Fatal(err)
	}
	if err := b.Arrive("b", 999); !errors.Is(err, barrier.ErrEpoch) {
		t.Fatalf("want epoch err, got %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := b.Wait(ctx); err == nil {
		t.Fatal("expected wait timeout before quorum")
	}
	_ = b.Arrive("b", uint64(rt.SecurityEpoch()))
	// 2 of 3 is quorum
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := b.Wait(ctx2); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceAndLockdown(t *testing.T) {
	rt := testRuntime(t, shiftlock.ProfileHardened)
	st, err := rt.Maintenance().Enter(maintenance.EnterRequest{
		Reason: "deploy", Duration: time.Minute, ActorID: "ops", Scope: maintenance.Scope{All: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rt.Maintenance().Active() {
		t.Fatal("expected active")
	}
	if _, err := rt.Maintenance().Exit("ops"); err != nil {
		t.Fatal(err)
	}
	_ = st

	lst, err := rt.Lockdown().Enter(lockdown.EnterRequest{
		Mode: lockdown.ModeFailClosed, Reason: "incident", ActorID: "sec",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Lockdown().Unlock(lockdown.UnlockRequest{
		ExpectedID: lst.ID, Confirm: true, ActorID: "sec", StrongAuthID: "cap-strong",
	}); err != nil {
		t.Fatal(err)
	}
	if len(rt.Lockdown().Evidence()) == 0 {
		t.Fatal("evidence must survive unlock")
	}
}

func TestQuarantineBlocksCapabilityIssue(t *testing.T) {
	rt := testRuntime(t, shiftlock.ProfileMaximumSecurity)
	rt.EnterQuarantine("test")
	_, err := rt.IssueCapability(capability.Request{
		Subject: "ops", Permission: capability.Permission(shiftlock.PermCapabilityIssue), TTL: time.Minute,
	})
	if !errors.Is(err, shiftlock.ErrQuarantined) {
		t.Fatalf("want quarantined, got %v", err)
	}
	rep := rt.Health(context.Background())
	if rep.Overall != "quarantined" && rep.Overall != "locked-down" {
		// quarantined node should worsen overall
		found := false
		for _, n := range rep.Nodes {
			if n.Status == "quarantined" {
				found = true
			}
		}
		if !found {
			t.Fatalf("health missing quarantine: %+v", rep)
		}
	}
}

func TestCommandsNoShellDefault(t *testing.T) {
	rt := testRuntime(t, shiftlock.ProfileStandard)
	// Allow invoke for a non-privileged permission via guard
	_ = rt.Guard().AddRule(guard.Rule{
		Name: "allow-status", Permission: "command.invoke", Decision: guard.Allow, Priority: 50,
	})
	err := rt.Commands().Register(command.Spec{
		Name: "status", Permission: "command.invoke",
		Handler: func(ctx context.Context, req command.Request) (command.Result, error) {
			return command.Result{OK: true, Message: "ok"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := rt.Commands().Invoke(context.Background(), command.Request{Name: "status", ActorID: "ops"})
	if err != nil || !res.OK {
		t.Fatalf("invoke: %v %+v", err, res)
	}
}

func TestSyncprimOnceAndSemaphore(t *testing.T) {
	rt := testRuntime(t, shiftlock.ProfileDevelopment)
	fc := shiftlock.FencedClaims{RT: rt}
	once, err := syncprim.NewOnce(fc, "once-job")
	if err != nil {
		t.Fatal(err)
	}
	var ran int
	if err := once.Do(context.Background(), func(ctx context.Context, token uint64) error {
		ran++
		if token == 0 {
			t.Fatal("expected fencing token")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := once.Do(context.Background(), func(ctx context.Context, token uint64) error {
		ran++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if ran != 1 {
		t.Fatalf("once ran %d", ran)
	}

	sem, err := syncprim.NewSemaphore(fc, "sem", 2)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := sem.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := rel(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorStillWorksWithoutRuntime(t *testing.T) {
	be := memory.New()
	defer be.Close()
	c, err := shiftlock.New(shiftlock.Config{Service: "legacy", InstanceID: "a", Backend: be})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cl, err := c.Claim(context.Background(), "w")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := cl.TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if lease.FencingToken() == 0 {
		t.Fatal("token")
	}
}

func TestExpandSecurityProfiles(t *testing.T) {
	for _, p := range []shiftlock.SecurityProfile{
		shiftlock.ProfileDevelopment, shiftlock.ProfileTesting, shiftlock.ProfileStandard,
		shiftlock.ProfileHardened, shiftlock.ProfileMaximumSecurity,
	} {
		s := shiftlock.ExpandSecurityProfile(p)
		if !s.DenyPrivilegedByDefault {
			t.Fatalf("%s must deny privileged by default", p)
		}
		if s.AntiReplayMaxEntries <= 0 {
			t.Fatalf("%s unbounded replay", p)
		}
	}
}
