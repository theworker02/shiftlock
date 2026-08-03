package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// ActionType enumerates protocol actions.
type ActionType string

const (
	ActRegisterGeneration ActionType = "register_generation"
	ActPassReadiness      ActionType = "pass_readiness"
	ActFailReadiness      ActionType = "fail_readiness"
	ActRequestClaim       ActionType = "request_claim"
	ActRenewClaim         ActionType = "renew_claim"
	ActBeginDrain         ActionType = "begin_drain"
	ActCompleteDrain      ActionType = "complete_drain"
	ActPrepareTransfer    ActionType = "prepare_transfer"
	ActCommitTransfer     ActionType = "commit_transfer"
	ActAbortTransfer      ActionType = "abort_transfer"
	ActPause              ActionType = "pause"
	ActResume             ActionType = "resume"
	ActDisconnect         ActionType = "disconnect"
	ActReconnect          ActionType = "reconnect"
	ActExpireLease        ActionType = "expire_lease"
	ActCrashOwner         ActionType = "crash_owner"
	ActCrashCandidate     ActionType = "crash_candidate"
	ActRestartBackend     ActionType = "restart_backend"
	ActForceRevoke        ActionType = "force_revoke"
	ActAdvanceTime        ActionType = "advance_time"
	ActReleaseClaim       ActionType = "release_claim"
)

// Action is a single protocol step.
type Action struct {
	Type       ActionType    `json:"type"`
	Generation string        `json:"generation,omitempty"`
	Claim      string        `json:"claim,omitempty"`
	Successor  string        `json:"successor,omitempty"`
	OpID       string        `json:"operation_id,omitempty"`
	Delta      time.Duration `json:"delta,omitempty"`
}

// FailureRecord is a minimal reproducible sequence.
type FailureRecord struct {
	Seed      int64    `json:"seed"`
	Claim     string   `json:"claim"`
	Actions   []Action `json:"actions"`
	Invariant string   `json:"invariant"`
	Detail    string   `json:"detail"`
}

func (f FailureRecord) JSON() string {
	b, _ := json.MarshalIndent(f, "", "  ")
	return string(b)
}

func (f FailureRecord) ReproduceCmd() string {
	return fmt.Sprintf("go test ./model -run TestRandomSequences -shiftlock.seed=%d", f.Seed)
}
