package shiftlock

import (
	"time"
)

// Generation identifies a process generation participating in ownership.
type Generation struct {
	ID         string           `json:"id"`
	Service    string           `json:"service"`
	InstanceID string           `json:"instance_id"`
	State      GenerationState  `json:"state"`
	StartedAt  time.Time        `json:"started_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
	Reason     TransitionReason `json:"reason,omitempty"`
}

// Clone returns a shallow copy.
func (g Generation) Clone() Generation { return g }
