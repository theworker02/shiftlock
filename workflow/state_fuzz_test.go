package workflow_test

import (
	"testing"

	"github.com/theworker02/shiftlock/workflow"
)

func FuzzCanTransition(f *testing.F) {
	states := []string{
		string(workflow.StateCreated), string(workflow.StateValidating),
		string(workflow.StateWaiting), string(workflow.StateRunning),
		string(workflow.StatePaused), string(workflow.StateCompensating),
		string(workflow.StateCompleted), string(workflow.StateFailed),
		string(workflow.StateCancelled), string(workflow.StateBlocked),
		string(workflow.StateLockedDown), string(workflow.StateRequiresIntervention),
		string(workflow.StateRequiresReconciliation),
		"not-a-state", "",
	}
	for _, a := range states {
		for _, b := range states {
			f.Add(a, b)
		}
	}
	f.Fuzz(func(t *testing.T, from, to string) {
		ok := workflow.CanTransition(workflow.State(from), workflow.State(to))
		if workflow.State(from).Terminal() && from != to && ok {
			t.Fatalf("terminal %q must not transition to %q", from, to)
		}
		if from == to && !ok && isKnownState(from) {
			t.Fatalf("identity transition must be allowed for %q", from)
		}
	})
}

func isKnownState(s string) bool {
	switch workflow.State(s) {
	case workflow.StateCreated, workflow.StateValidating, workflow.StateWaiting,
		workflow.StateRunning, workflow.StatePaused, workflow.StateCompensating,
		workflow.StateCompleted, workflow.StateFailed, workflow.StateCancelled,
		workflow.StateBlocked, workflow.StateLockedDown,
		workflow.StateRequiresIntervention, workflow.StateRequiresReconciliation:
		return true
	default:
		return false
	}
}
