package workflow

import "github.com/theworker02/shiftlock/resource"

// LockdownGate is a soft dependency on Runtime lockdown.
type LockdownGate interface {
	BlocksMutations() bool
}

// Auditor is a soft dependency on Runtime audit.
type Auditor interface {
	Audit(actor, action, resource, result, operationID string)
}

// ResourceLookup resolves registered resources for capability/epoch checks.
type ResourceLookup interface {
	Get(id resource.ResourceID) (*resource.Entry, error)
}

// Hooks wires soft Runtime integrations without import cycles.
type Hooks struct {
	Lockdown LockdownGate
	Audit    Auditor
	Resources ResourceLookup
}
