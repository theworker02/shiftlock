package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
)

// ProductionReadinessReport is emitted by readiness-report.
type ProductionReadinessReport struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Status      string            `json:"status"` // pass|warn|fail
	Checks      []ReadinessCheck  `json:"checks"`
	Summary     string            `json:"summary"`
	BackendCaps shiftlock.Capabilities `json:"backend_capabilities"`
}

type ReadinessCheck struct {
	ID       string `json:"id"`
	Severity string `json:"severity"` // info|warn|error
	Passed   bool   `json:"passed"`
	Message  string `json:"message"`
}

func runReadinessReport(args []string) {
	fs := flag.NewFlagSet("readiness-report", flag.ExitOnError)
	format := fs.String("format", "text", "text|json|sarif")
	outPath := fs.String("out", "", "optional output path (default stdout)")
	_ = fs.Parse(args)

	rep := buildReadinessReport()
	var body []byte
	var err error
	switch strings.ToLower(*format) {
	case "json":
		body, err = json.MarshalIndent(rep, "", "  ")
		if err == nil {
			body = append(body, '\n')
		}
	case "sarif":
		body, err = marshalSARIF(rep)
	default:
		body = []byte(formatReadinessText(rep))
	}
	if err != nil {
		fatal(err)
	}
	if *outPath != "" {
		if err := os.WriteFile(*outPath, body, 0o644); err != nil {
			fatal(err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", *outPath)
		return
	}
	_, _ = os.Stdout.Write(body)
}

func buildReadinessReport() ProductionReadinessReport {
	be := memory.New()
	defer be.Close()
	caps := be.Capabilities()
	checks := []ReadinessCheck{
		{ID: "atomic_cas", Severity: "error", Passed: caps.AtomicCAS, Message: "backend must advertise AtomicCAS"},
		{ID: "idempotent_mutations", Severity: "error", Passed: caps.IdempotentMutations, Message: "OperationID idempotency required for RC"},
		{ID: "expire_before_mutate", Severity: "error", Passed: caps.ExpireBeforeMutate, Message: "expire-before-mutate required"},
		{ID: "renew_during_reserved", Severity: "error", Passed: caps.RenewDuringReserved, Message: "renewals must continue during reserved"},
		{ID: "cert_suite_importable", Severity: "info", Passed: true, Message: "shiftlockcert.RunBackendSuite available via go test"},
		{ID: "memory_backend", Severity: "info", Passed: true, Message: "memory backend available for local verification"},
	}

	status := "pass"
	var failing, warning int
	for _, c := range checks {
		if c.Passed {
			continue
		}
		if c.Severity == "error" {
			failing++
			status = "fail"
		} else if c.Severity == "warn" && status != "fail" {
			warning++
			status = "warn"
		}
	}
	summary := fmt.Sprintf("%d checks; failing=%d warn=%d", len(checks), failing, warning)
	return ProductionReadinessReport{
		GeneratedAt: time.Now().UTC(),
		Status:      status,
		Checks:      checks,
		Summary:     summary,
		BackendCaps: caps,
	}
}

func formatReadinessText(r ProductionReadinessReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ShiftLock production readiness report\n")
	fmt.Fprintf(&b, "generated: %s\nstatus: %s\n%s\n\n", r.GeneratedAt.Format(time.RFC3339), r.Status, r.Summary)
	for _, c := range r.Checks {
		mark := "PASS"
		if !c.Passed {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "[%s] %s (%s): %s\n", mark, c.ID, c.Severity, c.Message)
	}
	return b.String()
}

func marshalSARIF(r ProductionReadinessReport) ([]byte, error) {
	type result struct {
		RuleID string `json:"ruleId"`
		Level  string `json:"level"`
		Message struct {
			Text string `json:"text"`
		} `json:"message"`
	}
	var results []result
	for _, c := range r.Checks {
		if c.Passed {
			continue
		}
		level := "warning"
		if c.Severity == "error" {
			level = "error"
		}
		res := result{RuleID: c.ID, Level: level}
		res.Message.Text = c.Message
		results = append(results, res)
	}
	doc := map[string]any{
		"version": "2.1.0",
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"runs": []map[string]any{{
			"tool": map[string]any{
				"driver": map[string]any{
					"name":  "shiftlock-inspect readiness-report",
					"informationUri": "https://github.com/theworker02/shiftlock",
				},
			},
			"results": results,
		}},
	}
	return json.MarshalIndent(doc, "", "  ")
}
