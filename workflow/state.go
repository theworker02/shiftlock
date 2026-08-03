package workflow

import "time"

// State is the lifecycle state of a workflow instance.
type State string

const (
	StateCreated                 State = "created"
	StateValidating              State = "validating"
	StateWaiting                 State = "waiting"
	StateRunning                 State = "running"
	StatePaused                  State = "paused"
	StateCompensating            State = "compensating"
	StateCompleted               State = "completed"
	StateFailed                  State = "failed"
	StateCancelled               State = "cancelled"
	StateBlocked                 State = "blocked"
	StateLockedDown              State = "locked-down"
	StateRequiresIntervention    State = "requires-intervention"
	StateRequiresReconciliation  State = "requires-reconciliation"
)

// Terminal reports whether s is a terminal state.
func (s State) Terminal() bool {
	switch s {
	case StateCompleted, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}

// CanTransition reports whether an engine may move from -> to.
// This is a conservative allow-list for validation/fuzzing; the engine may
// use a subset of these edges.
func CanTransition(from, to State) bool {
	if from == to {
		return true
	}
	if from.Terminal() {
		return false
	}
	switch from {
	case StateCreated:
		return to == StateValidating || to == StateCancelled || to == StateLockedDown
	case StateValidating:
		return to == StateWaiting || to == StateRunning || to == StateFailed || to == StateLockedDown || to == StateCancelled
	case StateWaiting:
		return to == StateRunning || to == StatePaused || to == StateCancelled || to == StateLockedDown || to == StateBlocked
	case StateRunning:
		return to == StatePaused || to == StateCompensating || to == StateCompleted || to == StateFailed ||
			to == StateCancelled || to == StateLockedDown || to == StateRequiresIntervention || to == StateRequiresReconciliation || to == StateBlocked
	case StatePaused:
		return to == StateRunning || to == StateCancelled || to == StateLockedDown || to == StateFailed
	case StateCompensating:
		return to == StateFailed || to == StateCompleted || to == StateRequiresIntervention || to == StateRequiresReconciliation
	case StateBlocked, StateLockedDown, StateRequiresIntervention, StateRequiresReconciliation:
		return to == StateRunning || to == StatePaused || to == StateCancelled || to == StateFailed || to == StateCompensating
	default:
		return false
	}
}

// StepStatus is the status of one step within an instance.
type StepStatus string

const (
	StepPending       StepStatus = "pending"
	StepRunning       StepStatus = "running"
	StepCompleted     StepStatus = "completed"
	StepFailed        StepStatus = "failed"
	StepCompensating  StepStatus = "compensating"
	StepCompensated   StepStatus = "compensated"
	StepSkipped       StepStatus = "skipped"
	StepReconcile     StepStatus = "requires-reconciliation"
)

// Evidence is a size-bounded observation (aligned with resource.Evidence).
type Evidence struct {
	Time    time.Time         `json:"time"`
	Event   string            `json:"event"`
	Summary string            `json:"summary,omitempty"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// StepState is runtime progress for one step.
type StepState struct {
	Name       string     `json:"name"`
	Status     StepStatus `json:"status"`
	Attempt    int        `json:"attempt"`
	StartedAt  time.Time  `json:"started_at,omitempty"`
	FinishedAt time.Time  `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
	Evidence   []Evidence `json:"evidence,omitempty"`
}

// IdempotencyMode controls retry semantics.
type IdempotencyMode string

const (
	// Idempotent — safe to retry on transient failure.
	Idempotent IdempotencyMode = "idempotent"
	// RequiresOperationID — retries only with a stable operation id.
	RequiresOperationID IdempotencyMode = "requires-operation-id"
	// NotRetryable — never retry; fail the step.
	NotRetryable IdempotencyMode = "not-retryable"
	// RequiresReconciliation — ambiguous outcomes must not be blindly retried.
	RequiresReconciliation IdempotencyMode = "requires-reconciliation"
)
