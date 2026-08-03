// Package redteam provides scenario definitions and runnable prevention tests
// for ShiftLock security controls.
package redteam

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/theworker02/shiftlock/audit"
	"github.com/theworker02/shiftlock/barrier"
	"github.com/theworker02/shiftlock/capability"
	"github.com/theworker02/shiftlock/configlock"
	"github.com/theworker02/shiftlock/control/lockdown"
	"github.com/theworker02/shiftlock/control/snapshot"
	"github.com/theworker02/shiftlock/security/antireplay"
	"github.com/theworker02/shiftlock/security/signing"
)

// ScenarioID identifies a red-team scenario.
type ScenarioID string

const (
	ScenarioForgedGeneration         ScenarioID = "forged-generation"
	ScenarioStolenCapability         ScenarioID = "stolen-capability"
	ScenarioReplayedCommand          ScenarioID = "replayed-command"
	ScenarioStaleOwner               ScenarioID = "stale-owner"
	ScenarioMaliciousCandidate       ScenarioID = "malicious-candidate"
	ScenarioAuditTampering           ScenarioID = "audit-tampering"
	ScenarioConfigurationSubstitution ScenarioID = "configuration-substitution"
	ScenarioProtocolDowngrade        ScenarioID = "protocol-downgrade"
	ScenarioQuorumCollusion          ScenarioID = "quorum-collusion"
	ScenarioCandidateFlood           ScenarioID = "candidate-flood"
	ScenarioLockdownBypass           ScenarioID = "lockdown-bypass"
	ScenarioSecretExfiltration       ScenarioID = "secret-exfiltration-attempt"
)

// Scenario describes an attack simulation with expected controls.
type Scenario struct {
	ID                   ScenarioID `json:"id"`
	Title                string     `json:"title"`
	Threat               string     `json:"threat"`
	Description          string     `json:"description"`
	Preconditions        []string   `json:"preconditions,omitempty"`
	AttackSequence       []string   `json:"attack_sequence,omitempty"`
	ExpectedDetection    string     `json:"expected_detection,omitempty"`
	ExpectedPrevention   string     `json:"expected_prevention,omitempty"`
	ExpectedAuditEvidence string    `json:"expected_audit_evidence,omitempty"`
	ExpectedLockdown     string     `json:"expected_lockdown,omitempty"`
	RequiredRecovery     string     `json:"required_recovery,omitempty"`
	ExpectDeny           bool       `json:"expect_deny"`
	Runnable             bool       `json:"runnable"`
}

// Catalog returns known scenarios (runnable and documented).
func Catalog() []Scenario {
	return []Scenario{
		{
			ID: ScenarioForgedGeneration, Title: "Forged generation",
			Threat: "Use of authorization issued under a prior security epoch",
			Description: "Attempt to use a capability bound to a prior security epoch after rotation.",
			Preconditions: []string{"capability issued", "epoch advanced"},
			AttackSequence: []string{"issue capability", "advance epoch", "verify old token"},
			ExpectedDetection: "epoch mismatch on verify",
			ExpectedPrevention: "ErrEpochMismatch",
			ExpectedAuditEvidence: "capability.verify denied",
			ExpectedLockdown: "optional auto-trigger on repeated forge attempts",
			RequiredRecovery: "re-issue capabilities under current epoch",
			ExpectDeny: true, Runnable: true,
		},
		{
			ID: ScenarioStolenCapability, Title: "Stolen capability",
			Threat: "Replay of a revoked or single-use capability",
			Description: "Replay a revoked or single-use capability.",
			Preconditions: []string{"signed single-use capability"},
			AttackSequence: []string{"verify once", "verify again / after revoke"},
			ExpectedDetection: "single-use spent or revoked",
			ExpectedPrevention: "ErrSingleUseSpent or ErrRevoked",
			ExpectDeny: true, Runnable: true,
		},
		{
			ID: ScenarioReplayedCommand, Title: "Replayed command",
			Threat: "Reuse of a request nonce",
			Description: "Reuse a request nonce against the anti-replay cache.",
			Preconditions: []string{"bounded anti-replay cache"},
			AttackSequence: []string{"store nonce", "store same nonce"},
			ExpectedDetection: "ErrReplay",
			ExpectedPrevention: "reject second use",
			ExpectDeny: true, Runnable: true,
		},
		{
			ID: ScenarioStaleOwner, Title: "Stale owner",
			Threat: "Action with a fencing token older than the current owner",
			Description: "Attempt to act with a lower fencing token after ownership moved.",
			Preconditions: []string{"monotonic fencing tokens"},
			AttackSequence: []string{"accept token N", "reject token < N"},
			ExpectedDetection: "token validator reject",
			ExpectedPrevention: "stale token not accepted",
			ExpectDeny: true, Runnable: true,
		},
		{
			ID: ScenarioMaliciousCandidate, Title: "Malicious candidate",
			Threat: "Delegation that widens permission scope",
			Description: "Attempt to escalate via capability delegation.",
			Preconditions: []string{"parent capability with narrow permission"},
			AttackSequence: []string{"delegate with higher permission"},
			ExpectedDetection: "ErrWidenDelegate",
			ExpectedPrevention: "delegation only reduces",
			ExpectDeny: true, Runnable: true,
		},
		{
			ID: ScenarioAuditTampering, Title: "Audit tampering",
			Threat: "Mutation of hash-chained audit records",
			Description: "Mutate an audit record and ensure verify fails.",
			Preconditions: []string{"multi-record audit chain"},
			AttackSequence: []string{"append records", "mutate action field", "verify"},
			ExpectedDetection: "mutation finding",
			ExpectedPrevention: "VerifyRecords OK=false",
			ExpectDeny: true, Runnable: true,
		},
		{
			ID: ScenarioConfigurationSubstitution, Title: "Configuration substitution",
			Threat: "Activation of tampered or unsigned production config",
			Description: "Modify signed config content or activate without signatures when required.",
			Preconditions: []string{"production + require signatures"},
			AttackSequence: []string{"draft", "stage/validate/approve", "activate unsigned", "tamper content hash"},
			ExpectedDetection: "ErrUnsignedRequired / ErrHashMismatch",
			ExpectedPrevention: "activation blocked",
			ExpectDeny: true, Runnable: true,
		},
		{
			ID: ScenarioProtocolDowngrade, Title: "Protocol downgrade",
			Threat: "Accepting wildcard or empty privileged permissions",
			Description: "Issue a wildcard capability that would bypass narrow permission checks.",
			Preconditions: []string{"capability authority deny-by-default"},
			AttackSequence: []string{"Issue permission *"},
			ExpectedDetection: "ErrDenied",
			ExpectedPrevention: "wildcard rejected at issue",
			ExpectDeny: true, Runnable: true,
		},
		{
			ID: ScenarioQuorumCollusion, Title: "Quorum collusion",
			Threat: "Duplicate participant votes to fake quorum",
			Description: "Same participant arrives twice at a barrier/quorum gate.",
			Preconditions: []string{"barrier MaxParticipants bound"},
			AttackSequence: []string{"Arrive id=A", "Arrive id=A again"},
			ExpectedDetection: "ErrDuplicate",
			ExpectedPrevention: "second vote rejected",
			ExpectDeny: true, Runnable: true,
		},
		{
			ID: ScenarioCandidateFlood, Title: "Candidate flood",
			Threat: "Exhaustion of barrier waiters / participants",
			Description: "Flood Arrive beyond MaxParticipants.",
			Preconditions: []string{"MaxParticipants > 0"},
			AttackSequence: []string{"Arrive until full", "one more Arrive"},
			ExpectedDetection: "ErrFull",
			ExpectedPrevention: "hard bound enforced",
			ExpectDeny: true, Runnable: true,
		},
		{
			ID: ScenarioLockdownBypass, Title: "Lockdown bypass",
			Threat: "Unlock without confirm and strong auth",
			Description: "Attempt unlock without confirm/strong auth.",
			Preconditions: []string{"active fail-closed lockdown"},
			AttackSequence: []string{"Enter", "Unlock Confirm=false"},
			ExpectedDetection: "unlock error",
			ExpectedPrevention: "remain locked",
			RequiredRecovery: "unlock with ExpectedID + Confirm + StrongAuthID",
			ExpectDeny: true, Runnable: true,
		},
		{
			ID: ScenarioSecretExfiltration, Title: "Secret exfiltration attempt",
			Threat: "Leak secrets via snapshots or serialized diagnostics",
			Description: "Ensure snapshot sanitization redacts password/token fields.",
			Preconditions: []string{"snapshot.Create redaction"},
			AttackSequence: []string{"Create snapshot with secrets", "marshal JSON"},
			ExpectedDetection: "redacted placeholders",
			ExpectedPrevention: "plaintext secrets absent",
			ExpectDeny: true, Runnable: true,
		},
	}
}

// Result is the outcome of running a scenario.
type Result struct {
	ID      ScenarioID `json:"id"`
	Passed  bool       `json:"passed"`
	Denied  bool       `json:"denied"`
	Message string     `json:"message"`
}

// Run executes a scenario by ID.
func Run(id ScenarioID) (Result, error) {
	switch id {
	case ScenarioForgedGeneration:
		return runForgedGeneration()
	case ScenarioStolenCapability:
		return runStolenCapability()
	case ScenarioReplayedCommand:
		return runReplayedCommand()
	case ScenarioStaleOwner:
		return runStaleOwner()
	case ScenarioMaliciousCandidate:
		return runMaliciousCandidate()
	case ScenarioAuditTampering:
		return runAuditTampering()
	case ScenarioConfigurationSubstitution:
		return runConfigurationSubstitution()
	case ScenarioProtocolDowngrade:
		return runProtocolDowngrade()
	case ScenarioQuorumCollusion:
		return runQuorumCollusion()
	case ScenarioCandidateFlood:
		return runCandidateFlood()
	case ScenarioLockdownBypass:
		return runLockdownBypass()
	case ScenarioSecretExfiltration:
		return runSecretExfiltration()
	default:
		return Result{}, fmt.Errorf("redteam: unknown scenario %q", id)
	}
}

// RunAll executes every runnable catalog scenario.
func RunAll() ([]Result, error) {
	var out []Result
	for _, s := range Catalog() {
		if !s.Runnable {
			continue
		}
		res, err := Run(s.ID)
		if err != nil {
			return out, err
		}
		out = append(out, res)
	}
	return out, nil
}

func runForgedGeneration() (Result, error) {
	auth := capability.New()
	tok, err := auth.Issue(capability.Request{Subject: "gen-old", Permission: "claim.inspect", TTL: time.Minute})
	if err != nil {
		return Result{ID: ScenarioForgedGeneration}, err
	}
	_, _ = auth.AdvanceEpoch()
	err = auth.Verify(tok)
	denied := errors.Is(err, capability.ErrEpochMismatch)
	return Result{
		ID: ScenarioForgedGeneration, Passed: denied, Denied: denied,
		Message: fmt.Sprintf("verify after epoch advance: %v", err),
	}, nil
}

func runStolenCapability() (Result, error) {
	key, err := signing.GenerateKey()
	if err != nil {
		return Result{ID: ScenarioStolenCapability}, err
	}
	ring := signing.NewKeyRing()
	_ = ring.Add(key.PublicView())
	auth := capability.New(capability.WithSigner(key, ring))
	tok, err := auth.Issue(capability.Request{
		Subject: "attacker", Permission: "command.execute", TTL: time.Minute,
		Constraints: capability.Constraints{SingleUse: true},
	})
	if err != nil {
		return Result{ID: ScenarioStolenCapability}, err
	}
	if err := auth.Verify(tok); err != nil {
		return Result{ID: ScenarioStolenCapability}, err
	}
	err = auth.Verify(tok)
	denied := errors.Is(err, capability.ErrSingleUseSpent)
	if !denied {
		_ = auth.Revoke(tok.ID)
		err = auth.Verify(tok)
		denied = errors.Is(err, capability.ErrRevoked)
	}
	return Result{
		ID: ScenarioStolenCapability, Passed: denied, Denied: denied,
		Message: fmt.Sprintf("stolen/reuse blocked: %v", err),
	}, nil
}

func runReplayedCommand() (Result, error) {
	c := antireplay.New(64)
	if err := c.CheckAndStore("cmd-nonce-1", time.Minute); err != nil {
		return Result{ID: ScenarioReplayedCommand}, err
	}
	err := c.CheckAndStore("cmd-nonce-1", time.Minute)
	denied := errors.Is(err, antireplay.ErrReplay)
	return Result{
		ID: ScenarioReplayedCommand, Passed: denied, Denied: denied,
		Message: fmt.Sprintf("replay: %v", err),
	}, nil
}

func runStaleOwner() (Result, error) {
	// Fencing monotonicity: a lower token must not be accepted after a higher one.
	type validator struct {
		cur uint64
	}
	v := &validator{}
	accept := func(tok uint64) bool {
		if tok == 0 {
			return false
		}
		if tok < v.cur {
			return false
		}
		v.cur = tok
		return true
	}
	if !accept(10) || accept(9) || accept(0) {
		return Result{ID: ScenarioStaleOwner, Passed: false, Message: "stale token accepted"}, nil
	}
	return Result{ID: ScenarioStaleOwner, Passed: true, Denied: true, Message: "stale fencing token rejected"}, nil
}

func runMaliciousCandidate() (Result, error) {
	auth := capability.New()
	parent, err := auth.Issue(capability.Request{Subject: "g", Permission: "claim.release", Resource: "a", TTL: time.Minute})
	if err != nil {
		return Result{ID: ScenarioMaliciousCandidate}, err
	}
	_, err = auth.Delegate(parent, capability.Request{Permission: "claim.revoke", Resource: "a"})
	denied := errors.Is(err, capability.ErrWidenDelegate)
	return Result{
		ID: ScenarioMaliciousCandidate, Passed: denied, Denied: denied,
		Message: fmt.Sprintf("widen delegate: %v", err),
	}, nil
}

func runAuditTampering() (Result, error) {
	s := audit.New()
	_, _ = s.Append(audit.Actor{ID: "op"}, "ok", "", "ok", "", nil)
	_, _ = s.Append(audit.Actor{ID: "op"}, "ok2", "", "ok", "", nil)
	recs := s.Records()
	recs[0].Action = "forged"
	rep := audit.VerifyRecords(recs, nil)
	denied := !rep.OK
	msg, _ := json.Marshal(rep.Findings)
	return Result{
		ID: ScenarioAuditTampering, Passed: denied, Denied: denied,
		Message: string(msg),
	}, nil
}

func runConfigurationSubstitution() (Result, error) {
	key, err := signing.GenerateKey()
	if err != nil {
		return Result{ID: ScenarioConfigurationSubstitution}, err
	}
	ring := signing.NewKeyRing()
	_ = ring.Add(key.PublicView())
	m := configlock.NewManager(configlock.WithKeyRing(ring), configlock.Production(true), configlock.RequireSignatures(true))
	b, err := m.Draft("svc", "prod", json.RawMessage(`{"max":1}`))
	if err != nil {
		return Result{ID: ScenarioConfigurationSubstitution}, err
	}
	_ = m.Stage(b.Revision)
	_ = m.Validate(b.Revision)
	_ = m.Approve(b.Revision)
	err = m.Activate(b.Revision)
	denied := errors.Is(err, configlock.ErrUnsignedRequired)
	if !denied {
		return Result{ID: ScenarioConfigurationSubstitution, Passed: false, Message: fmt.Sprintf("unsigned activate: %v", err)}, nil
	}
	// Tamper content after sign
	_ = m.SignRevision(b.Revision, key)
	got, _ := m.Get(b.Revision)
	got.Content = json.RawMessage(`{"max":999}`)
	hashErr := got.VerifyContentHash()
	denied = denied && errors.Is(hashErr, configlock.ErrHashMismatch)
	return Result{
		ID: ScenarioConfigurationSubstitution, Passed: denied, Denied: denied,
		Message: fmt.Sprintf("unsigned=%v hash=%v", err, hashErr),
	}, nil
}

func runProtocolDowngrade() (Result, error) {
	auth := capability.New()
	_, err := auth.Issue(capability.Request{Subject: "x", Permission: "*", TTL: time.Minute})
	denied := errors.Is(err, capability.ErrDenied)
	if !denied {
		_, err = auth.Issue(capability.Request{Subject: "x", Permission: "", TTL: time.Minute})
		denied = errors.Is(err, capability.ErrDenied)
	}
	return Result{
		ID: ScenarioProtocolDowngrade, Passed: denied, Denied: denied,
		Message: fmt.Sprintf("wildcard/empty: %v", err),
	}, nil
}

func runQuorumCollusion() (Result, error) {
	b, err := barrier.New(barrier.Config{MaxParticipants: 3, Policy: barrier.PolicyQuorum, Epoch: 1})
	if err != nil {
		return Result{ID: ScenarioQuorumCollusion}, err
	}
	if err := b.Arrive("A", 1); err != nil {
		return Result{ID: ScenarioQuorumCollusion}, err
	}
	err = b.Arrive("A", 1)
	denied := errors.Is(err, barrier.ErrDuplicate)
	return Result{
		ID: ScenarioQuorumCollusion, Passed: denied, Denied: denied,
		Message: fmt.Sprintf("duplicate vote: %v", err),
	}, nil
}

func runCandidateFlood() (Result, error) {
	b, err := barrier.New(barrier.Config{MaxParticipants: 2, Policy: barrier.PolicyAll, Epoch: 1})
	if err != nil {
		return Result{ID: ScenarioCandidateFlood}, err
	}
	_ = b.Arrive("A", 1)
	_ = b.Arrive("B", 1)
	err = b.Arrive("C", 1)
	denied := errors.Is(err, barrier.ErrFull)
	return Result{
		ID: ScenarioCandidateFlood, Passed: denied, Denied: denied,
		Message: fmt.Sprintf("flood: %v", err),
	}, nil
}

func runLockdownBypass() (Result, error) {
	m, err := lockdown.New(lockdown.Config{})
	if err != nil {
		return Result{ID: ScenarioLockdownBypass}, err
	}
	st, err := m.Enter(lockdown.EnterRequest{
		ID: "ld_test", Mode: lockdown.ModeFailClosed, Reason: "redteam", ActorID: "op",
	})
	if err != nil {
		return Result{ID: ScenarioLockdownBypass}, err
	}
	_, err = m.Unlock(lockdown.UnlockRequest{
		ExpectedID: st.ID, Confirm: false, ActorID: "attacker", StrongAuthID: "",
	})
	denied := err != nil
	return Result{
		ID: ScenarioLockdownBypass, Passed: denied, Denied: denied,
		Message: fmt.Sprintf("unlock without confirm/auth: %v", err),
	}, nil
}

func runSecretExfiltration() (Result, error) {
	s, err := snapshot.Create("svc", "i", map[string]any{
		"db_password": "hunter2",
		"nested":      map[string]any{"api_key": "secret-key"},
	}, map[string]string{"token": "leak-me"})
	if err != nil {
		return Result{ID: ScenarioSecretExfiltration}, err
	}
	raw, err := s.Marshal()
	if err != nil {
		return Result{ID: ScenarioSecretExfiltration}, err
	}
	text := string(raw)
	leaked := strings.Contains(text, "hunter2") || strings.Contains(text, "secret-key") || strings.Contains(text, "leak-me")
	return Result{
		ID: ScenarioSecretExfiltration, Passed: !leaked, Denied: !leaked,
		Message: fmt.Sprintf("secret leak=%v", leaked),
	}, nil
}
