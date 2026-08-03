package stategraph

import "fmt"

// State is a generation lifecycle state (mirrors shiftlock.GenerationState).
type State string

const (
	Joining      State = "joining"
	Standby      State = "standby"
	Preparing    State = "preparing"
	Active       State = "active"
	Draining     State = "draining"
	Transferring State = "transferring"
	Retired      State = "retired"
	Failed       State = "failed"
)

var allowed = map[State][]State{
	Joining:      {Standby, Preparing, Failed, Retired},
	Standby:      {Preparing, Active, Failed, Retired},
	Preparing:    {Standby, Active, Failed, Retired},
	Active:       {Draining, Transferring, Retired, Failed},
	Draining:     {Transferring, Active, Retired, Failed},
	Transferring: {Retired, Active, Failed, Standby},
	Retired:      {},
	Failed:       {},
}

// CanTransition reports whether from→to is a legal generation transition.
func CanTransition(from, to State) bool {
	for _, s := range allowed[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Transition validates a state change.
func Transition(from, to State) error {
	if CanTransition(from, to) {
		return nil
	}
	return fmt.Errorf("invalid state transition: %s -> %s", from, to)
}

// AllStates returns every generation state.
func AllStates() []State {
	return []State{Joining, Standby, Preparing, Active, Draining, Transferring, Retired, Failed}
}
