package resource

import (
	"time"
)

// Severity classifies drift findings.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// DriftAction is a recommended response to drift.
type DriftAction string

const (
	DriftActionReport     DriftAction = "report"
	DriftActionBlock      DriftAction = "block"
	DriftActionDegrade    DriftAction = "degrade"
	DriftActionQuarantine DriftAction = "quarantine"
	DriftActionReconcile  DriftAction = "reconcile"
	DriftActionLockdown   DriftAction = "lockdown"
)

// DriftReport compares expected vs observed resource state.
type DriftReport struct {
	Resource   ResourceID `json:"resource"`
	Severity   Severity   `json:"severity"`
	Expected   any        `json:"expected"`
	Observed   any        `json:"observed"`
	Evidence   []Evidence `json:"evidence,omitempty"`
	DetectedAt time.Time  `json:"detected_at"`
	Action     DriftAction `json:"action,omitempty"`
	Field      string     `json:"field,omitempty"`
}

// DriftDetector compares desired maps to observed snapshots.
type DriftDetector struct {
	DefaultAction DriftAction
}

// Compare builds drift reports for keys that differ.
// Values must be sanitized (no secrets). Automatic reconciliation is opt-in elsewhere.
func (d DriftDetector) Compare(id ResourceID, expected, observed map[string]string) []DriftReport {
	action := d.DefaultAction
	if action == "" {
		action = DriftActionReport
	}
	now := time.Now().UTC()
	var out []DriftReport
	seen := map[string]struct{}{}
	for k, exp := range expected {
		seen[k] = struct{}{}
		obs, ok := observed[k]
		if !ok {
			out = append(out, DriftReport{
				Resource: id, Severity: SeverityWarning, Field: k,
				Expected: exp, Observed: nil, DetectedAt: now, Action: action,
				Evidence: []Evidence{{Time: now, Event: "drift.missing", Resource: id.String(), Summary: k}},
			})
			continue
		}
		if obs != exp {
			sev := SeverityWarning
			if k == "schema_version" || k == "primary" || k == "generation" {
				sev = SeverityCritical
			}
			out = append(out, DriftReport{
				Resource: id, Severity: sev, Field: k,
				Expected: exp, Observed: obs, DetectedAt: now, Action: action,
				Evidence: []Evidence{{Time: now, Event: "drift.mismatch", Resource: id.String(), Summary: k}},
			})
		}
	}
	for k, obs := range observed {
		if _, ok := seen[k]; ok {
			continue
		}
		out = append(out, DriftReport{
			Resource: id, Severity: SeverityInfo, Field: k,
			Expected: nil, Observed: obs, DetectedAt: now, Action: DriftActionReport,
			Evidence: []Evidence{{Time: now, Event: "drift.unexpected", Resource: id.String(), Summary: k}},
		})
	}
	return out
}
