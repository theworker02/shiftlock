// Example secure-control-plane demonstrates a multi-step Phase 6 control plane:
// ownership, supervised worker, signed config, capability auth, forged-cap lockdown,
// audit verify, snapshot diff, and unlock recovery.
//
//	go run ./examples/secure-control-plane
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/audit"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/barrier"
	"github.com/theworker02/shiftlock/capability"
	"github.com/theworker02/shiftlock/configlock"
	"github.com/theworker02/shiftlock/control/lockdown"
	"github.com/theworker02/shiftlock/control/maintenance"
	"github.com/theworker02/shiftlock/control/snapshot"
	"github.com/theworker02/shiftlock/security/signing"
	"github.com/theworker02/shiftlock/supervise"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Println("== secure-control-plane demo ==")

	be := memory.New()
	defer be.Close()

	key, err := signing.GenerateKey()
	if err != nil {
		return err
	}
	ring := signing.NewKeyRing()
	if err := ring.Add(key.PublicView()); err != nil {
		return err
	}

	rt, err := shiftlock.NewRuntime(shiftlock.RuntimeConfig{
		Config: shiftlock.Config{
			Service: "payments", InstanceID: "gen-a", Backend: be, LeaseTTL: 10 * time.Second,
		},
		SecurityProfile:    shiftlock.ProfileStandard,
		EnableSupervisor:   true,
		EnableCapabilities: true,
		EnableGuard:        true,
		EnableAudit:        true,
		EnableMaintenance:  true,
		EnableLockdown:     true,
		CapabilityOptions:  []capability.Option{capability.WithSigner(key, ring)},
	})
	if err != nil {
		return err
	}
	defer rt.Close()

	ctx := context.Background()
	logAudit := func(action, result string) {
		if a := rt.Audit(); a != nil {
			_, _ = a.Append(audit.Actor{ID: "demo", Type: "example"}, action, "payments", result, "", nil)
		}
	}

	// 1) Claim ownership
	claim, err := rt.Coordinator().Claim(ctx, "billing-reconciler")
	if err != nil {
		return err
	}
	lease, err := claim.WaitForOwnership(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("1. ownership acquired token=%d generation=%s\n", lease.FencingToken(), rt.Coordinator().Generation().ID)
	logAudit("claim.acquire", "ok")

	// 2) Supervised singleton worker (short-lived)
	done := make(chan struct{})
	err = rt.Supervisor().StartSpec(supervise.Spec{
		Name: "billing-reconciler",
		Mode: supervise.ModeOneShot,
		Run: func(ctx context.Context) error {
			fmt.Println("2. supervised worker tick (one-shot)")
			close(done)
			return nil
		},
	})
	if err != nil {
		return err
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		return fmt.Errorf("worker did not run")
	}
	logAudit("supervise.oneshot", "ok")

	// 3) Signed configuration
	cm := configlock.NewManager(configlock.WithKeyRing(ring), configlock.Production(true), configlock.RequireSignatures(true))
	bundle, err := cm.Draft("payments", "prod", json.RawMessage(`{"feature":"reconcile","max_parallel":1}`))
	if err != nil {
		return err
	}
	_ = cm.Stage(bundle.Revision)
	_ = cm.Validate(bundle.Revision)
	_ = cm.Approve(bundle.Revision)
	if err := cm.Activate(bundle.Revision); err == nil {
		return fmt.Errorf("expected unsigned activation to fail")
	}
	if err := cm.SignRevision(bundle.Revision, key); err != nil {
		return err
	}
	if err := cm.Activate(bundle.Revision); err != nil {
		return err
	}
	fmt.Println("3. signed configuration activated")
	logAudit("config.activate", "ok")

	// 4) Capability authorization
	capAuth := rt.Capabilities()
	tok, err := capAuth.Issue(capability.Request{
		Subject: "operator", Permission: "maintenance.enter", TTL: time.Minute,
	})
	if err != nil {
		return err
	}
	if err := capAuth.Verify(tok); err != nil {
		return err
	}
	fmt.Printf("4. capability ok id=%s\n", tok.ID)
	logAudit("capability.verify", "ok")

	// 5) Quorum-style barrier (two of three)
	b, err := barrier.New(barrier.Config{MaxParticipants: 3, Policy: barrier.PolicyQuorum, Epoch: 1})
	if err != nil {
		return err
	}
	_ = b.Arrive("gen-a", 1)
	_ = b.Arrive("gen-b", 1)
	if !b.Released() {
		return fmt.Errorf("expected quorum release")
	}
	fmt.Println("5. quorum barrier released (2/3)")
	logAudit("quorum.release", "ok")

	// 6) Maintenance window
	if _, err := rt.Maintenance().Enter(maintenance.EnterRequest{
		ID: "mnt_1", Reason: "schema migrate", Duration: time.Minute, ActorID: "operator",
		CapabilityID: string(tok.ID), Scope: maintenance.Scope{All: true},
	}); err != nil {
		return err
	}
	fmt.Println("6. maintenance entered")
	logAudit("maintenance.enter", "ok")
	_, _ = rt.Maintenance().Exit("operator")

	snapBefore, err := snapshot.Create("payments", "gen-a", map[string]any{
		"phase": "pre-incident", "token": lease.FencingToken(), "password": "should-redact",
	}, nil)
	if err != nil {
		return err
	}

	// 7) Simulated forged capability → lockdown
	forged := tok
	forged.Permission = "lockdown.unlock"
	forged.Signature = append([]byte(nil), tok.Signature...)
	if len(forged.Signature) > 0 {
		forged.Signature[0] ^= 0xff
	}
	if err := capAuth.Verify(forged); err == nil {
		return fmt.Errorf("forged capability unexpectedly verified")
	}
	fmt.Println("7. forged capability rejected")
	logAudit("capability.forged", "denied")

	st, err := rt.Lockdown().Enter(lockdown.EnterRequest{
		ID: "ld_forge", Mode: lockdown.ModeFailClosed, Reason: "forged capability",
		ActorID: "runtime", Trigger: "forged-capability",
	})
	if err != nil {
		return err
	}
	fmt.Printf("8. lockdown active id=%s\n", st.ID)
	logAudit("lockdown.enter", "ok")

	if _, err := rt.Lockdown().Unlock(lockdown.UnlockRequest{
		ExpectedID: st.ID, Confirm: false, ActorID: "attacker", StrongAuthID: "",
	}); err == nil {
		return fmt.Errorf("lockdown bypass succeeded")
	}
	fmt.Println("9. lockdown bypass denied")

	strong, err := capAuth.Issue(capability.Request{
		Subject: "security-officer", Permission: "lockdown.unlock", TTL: time.Minute,
		Constraints: capability.Constraints{SingleUse: true},
	})
	if err != nil {
		return err
	}
	if _, err := rt.Lockdown().Unlock(lockdown.UnlockRequest{
		ExpectedID: st.ID, Confirm: true, ActorID: "security-officer", StrongAuthID: string(strong.ID),
	}); err != nil {
		return err
	}
	fmt.Println("10. recovered unlock with strong auth")
	logAudit("lockdown.unlock", "ok")

	// 11) Audit verify
	rep := rt.Audit().Verify()
	if !rep.OK {
		return fmt.Errorf("audit verify failed: %+v", rep.Findings)
	}
	fmt.Printf("11. audit verify ok records=%d\n", rep.Records)

	snapAfter, err := snapshot.Create("payments", "gen-a", map[string]any{
		"phase": "post-incident", "lockdown": false,
	}, nil)
	if err != nil {
		return err
	}
	diff := snapshot.Diff(snapBefore, snapAfter)
	rawBefore, _ := snapBefore.Marshal()
	if contains(string(rawBefore), "should-redact") {
		return fmt.Errorf("snapshot leaked secret")
	}
	fmt.Printf("12. snapshot diff entries=%d (secrets redacted)\n", len(diff))

	fmt.Println("== demo complete ==")
	return nil
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
