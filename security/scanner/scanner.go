// Package scanner finds unsafe ShiftLock configurations and emits findings
// in text, JSON, or SARIF form.
package scanner

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Severity ranks finding impact.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// Finding is one scanner result.
type Finding struct {
	RuleID      string   `json:"rule_id"`
	Severity    Severity `json:"severity"`
	Title       string   `json:"title"`
	Message     string   `json:"message"`
	Remediation string   `json:"remediation"`
	Path        string   `json:"path,omitempty"`
}

// Input is a sanitized configuration view for scanning.
type Input struct {
	Production              bool
	SignaturesRequired      bool
	UnsignedConfigActive    bool
	AllowInsecureSkipVerify bool
	TCPAgentListener        bool
	ShellExecEnabled        bool
	UnboundedQueue          bool
	AuditVerifyOnStart      bool
	DenyByDefault           bool
	SecurityProfile         string
	LeaseTTLSeconds         int
	CapabilitiesUnsigned    bool
}

// Scan evaluates Input against built-in rules.
func Scan(in Input) []Finding {
	var out []Finding
	add := func(f Finding) { out = append(out, f) }

	if in.Production && in.UnsignedConfigActive {
		add(Finding{
			RuleID: "CFG-UNSIGNED-PROD", Severity: SeverityCritical,
			Title: "Unsigned configuration active in production",
			Message: "Production has an active configuration bundle without required signatures.",
			Remediation: "Sign the bundle with a trusted Ed25519 key and set RequireSignatures before Activate.",
			Path: "configlock.active",
		})
	}
	if in.Production && !in.SignaturesRequired {
		add(Finding{
			RuleID: "CFG-SIG-OPTIONAL", Severity: SeverityHigh,
			Title: "Configuration signatures not required",
			Message: "Production environment does not require signed configuration activation.",
			Remediation: "Enable configlock.RequireSignatures(true) for production.",
		})
	}
	if in.AllowInsecureSkipVerify {
		add(Finding{
			RuleID: "TLS-SKIP-VERIFY", Severity: SeverityCritical,
			Title: "TLS certificate verification disabled",
			Message: "InsecureSkipVerify (or equivalent) is enabled.",
			Remediation: "Remove insecure TLS options from production configuration.",
		})
	}
	if in.TCPAgentListener {
		add(Finding{
			RuleID: "AGENT-TCP", Severity: SeverityHigh,
			Title: "Agent TCP listener enabled",
			Message: "ShiftLock agent should not expose a default TCP control listener.",
			Remediation: "Use Unix sockets or Windows named pipes; disable TCP.",
		})
	}
	if in.ShellExecEnabled {
		add(Finding{
			RuleID: "EXEC-SHELL", Severity: SeverityCritical,
			Title: "Shell execution enabled",
			Message: "Arbitrary shell execution is enabled on the control plane.",
			Remediation: "Use control/execguard allowlists only; never enable sh -c.",
		})
	}
	if in.UnboundedQueue {
		add(Finding{
			RuleID: "QUEUE-UNBOUNDED", Severity: SeverityHigh,
			Title: "Unbounded queue configured",
			Message: "An unbounded queue can exhaust memory under abuse.",
			Remediation: "Set explicit buffer/queue limits on events, commands, and audit fanout.",
		})
	}
	if in.Production && !in.AuditVerifyOnStart {
		add(Finding{
			RuleID: "AUDIT-NO-VERIFY", Severity: SeverityMedium,
			Title: "Audit chain not verified on start",
			Message: "Production should verify the audit hash chain during startup.",
			Remediation: "Call audit.Store.Verify() (or CLI shiftlock audit verify) before serving.",
		})
	}
	if !in.DenyByDefault {
		add(Finding{
			RuleID: "POLICY-PERMIT-DEFAULT", Severity: SeverityHigh,
			Title: "Policy does not deny by default",
			Message: "Guard/policy engine allows actions without an explicit allow rule.",
			Remediation: "Configure deny-by-default in the security profile / guard engine.",
		})
	}
	if in.CapabilitiesUnsigned && (in.Production || strings.EqualFold(in.SecurityProfile, "hardened") || strings.EqualFold(in.SecurityProfile, "maximum-security")) {
		add(Finding{
			RuleID: "CAP-UNSIGNED", Severity: SeverityHigh,
			Title: "Capabilities issued without signatures",
			Message: "Hardened profiles should sign capabilities with Ed25519.",
			Remediation: "Configure capability.WithSigner and verify signatures on use.",
		})
	}
	if in.LeaseTTLSeconds > 0 && in.LeaseTTLSeconds > 300 && in.Production {
		add(Finding{
			RuleID: "LEASE-TTL-LONG", Severity: SeverityLow,
			Title: "Long lease TTL in production",
			Message: fmt.Sprintf("LeaseTTL of %ds widens the unowned/stale window after crashes.", in.LeaseTTLSeconds),
			Remediation: "Prefer shorter TTLs with reliable renewals unless backends require longer leases.",
		})
	}
	if strings.EqualFold(in.SecurityProfile, "development") && in.Production {
		add(Finding{
			RuleID: "PROFILE-DEV-IN-PROD", Severity: SeverityCritical,
			Title: "Development security profile in production",
			Message: "SecurityProfile development is active while Production=true.",
			Remediation: "Use standard, hardened, or maximum-security profiles in production.",
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Severity == out[j].Severity {
			return out[i].RuleID < out[j].RuleID
		}
		return severityRank(out[i].Severity) > severityRank(out[j].Severity)
	})
	return out
}

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 5
	case SeverityHigh:
		return 4
	case SeverityMedium:
		return 3
	case SeverityLow:
		return 2
	default:
		return 1
	}
}

// Format selects text, json, or sarif output.
func Format(w io.Writer, format string, findings []Finding) error {
	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(findings)
	case "sarif":
		return writeSARIF(w, findings)
	default:
		if len(findings) == 0 {
			_, err := fmt.Fprintln(w, "No findings.")
			return err
		}
		for _, f := range findings {
			if _, err := fmt.Fprintf(w, "[%s] %s (%s)\n  %s\n  Remediation: %s\n",
				strings.ToUpper(string(f.Severity)), f.RuleID, f.Title, f.Message, f.Remediation); err != nil {
				return err
			}
		}
		return nil
	}
}

func writeSARIF(w io.Writer, findings []Finding) error {
	type rule struct {
		ID               string `json:"id"`
		ShortDescription struct {
			Text string `json:"text"`
		} `json:"shortDescription"`
		Help struct {
			Text string `json:"text"`
		} `json:"help"`
		DefaultConfiguration struct {
			Level string `json:"level"`
		} `json:"defaultConfiguration"`
	}
	type result struct {
		RuleID string `json:"ruleId"`
		Level  string `json:"level"`
		Message struct {
			Text string `json:"text"`
		} `json:"message"`
	}
	rules := make([]rule, 0, len(findings))
	results := make([]result, 0, len(findings))
	seen := map[string]struct{}{}
	for _, f := range findings {
		if _, ok := seen[f.RuleID]; !ok {
			seen[f.RuleID] = struct{}{}
			r := rule{ID: f.RuleID}
			r.ShortDescription.Text = f.Title
			r.Help.Text = f.Remediation
			r.DefaultConfiguration.Level = sarifLevel(f.Severity)
			rules = append(rules, r)
		}
		res := result{RuleID: f.RuleID, Level: sarifLevel(f.Severity)}
		res.Message.Text = f.Message
		results = append(results, res)
	}
	doc := map[string]any{
		"version": "2.1.0",
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"runs": []map[string]any{{
			"tool": map[string]any{
				"driver": map[string]any{
					"name":            "shiftlock-security-scan",
					"informationUri":  "https://github.com/theworker02/shiftlock",
					"rules":           rules,
					"version":         "0.1.0",
					"semanticVersion": "0.1.0",
				},
			},
			"results":   results,
			"invocations": []map[string]any{{
				"executionSuccessful": true,
				"startTimeUtc":        time.Now().UTC().Format(time.RFC3339),
			}},
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func sarifLevel(s Severity) string {
	switch s {
	case SeverityCritical, SeverityHigh:
		return "error"
	case SeverityMedium:
		return "warning"
	default:
		return "note"
	}
}
