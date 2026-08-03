// Package guard provides a deterministic authorization policy engine.
// No scripting: decisions come from explicit rules. Default deny.
package guard

import (
	"fmt"
	"strings"
	"sync"
)

// Decision is the result of Evaluate.
type Decision string

const (
	Allow                 Decision = "allow"
	Deny                  Decision = "deny"
	RequireApproval       Decision = "require-approval"
	RequireQuorum         Decision = "require-quorum"
	AllowWithConstraints  Decision = "allow-with-constraints"
)

// Request is evaluated against rules.
type Request struct {
	Principal   string
	Permission  string
	Resource    string
	Action      string
	Attrs       map[string]string
}

// Constraint limits an AllowWithConstraints decision.
type Constraint struct {
	MaxTTLSeconds int               `json:"max_ttl_seconds,omitempty"`
	Resources     []string          `json:"resources,omitempty"`
	Attrs         map[string]string `json:"attrs,omitempty"`
}

// Explanation is a stable, inspectable reason trail.
type Explanation struct {
	Decision    Decision     `json:"decision"`
	MatchedRule string       `json:"matched_rule,omitempty"`
	Reason      string       `json:"reason"`
	Constraints *Constraint  `json:"constraints,omitempty"`
	Trail       []string     `json:"trail,omitempty"`
}

// Rule is a deterministic match → decision mapping.
// Empty Principal/Permission/Resource/Action means wildcard for that field.
type Rule struct {
	Name        string
	Principal   string
	Permission  string
	Resource    string
	Action      string
	Decision    Decision
	Constraints *Constraint
	Priority    int // higher wins
}

// Engine evaluates requests. Default decision is Deny.
type Engine struct {
	mu    sync.RWMutex
	rules []Rule
}

// New creates an empty deny-by-default engine.
func New() *Engine {
	return &Engine{rules: nil}
}

// AddRule appends a rule.
func (e *Engine) AddRule(r Rule) error {
	if r.Decision == "" {
		return fmt.Errorf("guard: rule decision required")
	}
	switch r.Decision {
	case Allow, Deny, RequireApproval, RequireQuorum, AllowWithConstraints:
	default:
		return fmt.Errorf("guard: unknown decision %q", r.Decision)
	}
	if r.Name == "" {
		r.Name = fmt.Sprintf("rule-%d", len(e.rules)+1)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = append(e.rules, r)
	return nil
}

// Evaluate returns the decision for req (default Deny).
func (e *Engine) Evaluate(req Request) Decision {
	return e.Explain(req).Decision
}

// Explain returns the decision and reason trail.
func (e *Engine) Explain(req Request) Explanation {
	e.mu.RLock()
	defer e.mu.RUnlock()
	trail := []string{fmt.Sprintf("evaluate principal=%q permission=%q resource=%q action=%q", req.Principal, req.Permission, req.Resource, req.Action)}
	var best *Rule
	bestPri := -1 << 30
	for i := range e.rules {
		r := &e.rules[i]
		if !match(r.Principal, req.Principal) ||
			!match(r.Permission, req.Permission) ||
			!match(r.Resource, req.Resource) ||
			!match(r.Action, req.Action) {
			continue
		}
		trail = append(trail, fmt.Sprintf("match %s priority=%d decision=%s", r.Name, r.Priority, r.Decision))
		if best == nil || r.Priority >= best.Priority {
			cp := *r
			best = &cp
			bestPri = r.Priority
		}
	}
	_ = bestPri
	if best == nil {
		trail = append(trail, "no rule matched; default deny")
		return Explanation{Decision: Deny, Reason: "default deny", Trail: trail}
	}
	return Explanation{
		Decision:    best.Decision,
		MatchedRule: best.Name,
		Reason:      fmt.Sprintf("matched rule %s", best.Name),
		Constraints: best.Constraints,
		Trail:       trail,
	}
}

// Rules returns a copy of rules for inspection.
func (e *Engine) Rules() []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Rule, len(e.rules))
	copy(out, e.rules)
	return out
}

func match(pattern, value string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(value, prefix)
	}
	return pattern == value
}
