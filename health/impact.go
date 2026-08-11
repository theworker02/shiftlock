package health

import "sort"

// ActionKind is a recommended operator response for a health node.
type ActionKind string

const (
	ActionDrain        ActionKind = "drain"
	ActionQuarantine   ActionKind = "quarantine"
	ActionLockdown     ActionKind = "lockdown"
	ActionFailoverEval ActionKind = "failover-eval"
	ActionObserve      ActionKind = "observe"
)

// PlannedAction is one deterministic remediation step.
type PlannedAction struct {
	Node     string     `json:"node"`
	Kind     ActionKind `json:"kind"`
	Reason   string     `json:"reason"`
	Priority int        `json:"priority"`
}

// ImpactPlan describes blast radius and recommended actions when a node fails.
type ImpactPlan struct {
	Failed      string            `json:"failed"`
	BlastRadius []string          `json:"blast_radius"`
	Actions     []PlannedAction   `json:"actions"`
	Issues      []ValidationIssue `json:"issues,omitempty"`
}

// Empty reports whether the plan carries no failure context or work items.
func (p ImpactPlan) Empty() bool {
	return p.Failed == "" && len(p.BlastRadius) == 0 && len(p.Actions) == 0 && len(p.Issues) == 0
}

// PlanImpact derives blast radius and recommended actions for a failed node.
// Validation issues from the report are included; planning stops early when
// the failed node is missing or unnamed.
func (r Report) PlanImpact(failed string) ImpactPlan {
	plan := ImpactPlan{
		Failed: failed,
		Issues: r.Validate(),
	}
	if failed == "" {
		plan.Issues = append(plan.Issues, ValidationIssue{
			Code:    "empty-failed",
			Message: "failed node name is empty",
		})
		sortIssues(plan.Issues)
		return plan
	}

	node, ok := r.Node(failed)
	if !ok {
		plan.Issues = append(plan.Issues, ValidationIssue{
			Code:    "missing-node",
			Node:    failed,
			Message: "failed node is not present in the report",
		})
		sortIssues(plan.Issues)
		return plan
	}

	plan.BlastRadius = r.Dependents(failed)
	direct := directDependents(r, failed)
	actions := make([]PlannedAction, 0, 1+len(plan.BlastRadius)+len(r.dependencies(failed)))
	actions = append(actions, actionForFailed(failed, node.Status))

	for _, dependent := range plan.BlastRadius {
		depNode, _ := r.Node(dependent)
		actions = append(actions, actionForDependent(dependent, depNode.Status, direct[dependent]))
	}

	for _, upstream := range r.dependencies(failed) {
		actions = append(actions, PlannedAction{
			Node:     upstream,
			Kind:     ActionObserve,
			Reason:   "upstream dependency of failed node",
			Priority: 90,
		})
	}

	sort.Slice(actions, func(i, j int) bool {
		if actions[i].Priority != actions[j].Priority {
			return actions[i].Priority < actions[j].Priority
		}
		if actions[i].Node != actions[j].Node {
			return actions[i].Node < actions[j].Node
		}
		return actions[i].Kind < actions[j].Kind
	})
	plan.Actions = actions
	return plan
}

func directDependents(r Report, failed string) map[string]bool {
	out := make(map[string]bool)
	for _, edge := range r.Edges {
		if edge.To == failed {
			out[edge.From] = true
		}
	}
	return out
}

func (r Report) dependencies(name string) []string {
	deps := make([]string, 0)
	for _, edge := range r.Edges {
		if edge.From == name {
			deps = append(deps, edge.To)
		}
	}
	sort.Strings(deps)
	return deps
}

func actionForFailed(name string, status Status) PlannedAction {
	switch status {
	case LockedDown:
		return PlannedAction{Node: name, Kind: ActionObserve, Reason: "node already locked down", Priority: 0}
	case Quarantined:
		return PlannedAction{Node: name, Kind: ActionObserve, Reason: "node already quarantined", Priority: 0}
	case Unhealthy:
		return PlannedAction{Node: name, Kind: ActionQuarantine, Reason: "isolate unhealthy failed node", Priority: 0}
	case Degraded:
		return PlannedAction{Node: name, Kind: ActionDrain, Reason: "drain degraded failed node before replacement", Priority: 0}
	case Blocked:
		return PlannedAction{Node: name, Kind: ActionLockdown, Reason: "failed node is blocked; enforce lockdown", Priority: 0}
	default:
		return PlannedAction{Node: name, Kind: ActionQuarantine, Reason: "quarantine simulated failure", Priority: 0}
	}
}

func actionForDependent(name string, status Status, direct bool) PlannedAction {
	if status == LockedDown || status == Quarantined {
		return PlannedAction{Node: name, Kind: ActionObserve, Reason: "dependent already isolated", Priority: priorityForDependent(direct)}
	}
	if direct {
		if status == Unhealthy {
			return PlannedAction{Node: name, Kind: ActionFailoverEval, Reason: "direct dependent is unhealthy", Priority: priorityForDependent(true)}
		}
		return PlannedAction{Node: name, Kind: ActionDrain, Reason: "direct dependent of failed node", Priority: priorityForDependent(true)}
	}
	if status == Healthy || status == Unknown {
		return PlannedAction{Node: name, Kind: ActionObserve, Reason: "transitive dependent; monitor for symptoms", Priority: priorityForDependent(false)}
	}
	return PlannedAction{Node: name, Kind: ActionFailoverEval, Reason: "transitive dependent may need failover", Priority: priorityForDependent(false)}
}

func priorityForDependent(direct bool) int {
	if direct {
		return 10
	}
	return 20
}

func sortIssues(issues []ValidationIssue) {
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Code != issues[j].Code {
			return issues[i].Code < issues[j].Code
		}
		if issues[i].Node != issues[j].Node {
			return issues[i].Node < issues[j].Node
		}
		return issues[i].Message < issues[j].Message
	})
}
