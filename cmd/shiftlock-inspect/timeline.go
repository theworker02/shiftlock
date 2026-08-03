package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/theworker02/shiftlock/journal"
)

func loadJournalEntries(path, claimFilter string) ([]journal.Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []journal.Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e journal.Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if claimFilter != "" && e.Claim != "" && e.Claim != claimFilter {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

func runTimeline(args []string) {
	fs := flag.NewFlagSet("timeline", flag.ExitOnError)
	journalPath := fs.String("journal", "", "NDJSON journal path")
	claim := fs.String("claim", "", "optional claim filter")
	_ = fs.Parse(args)
	if *journalPath == "" {
		fmt.Fprintln(os.Stderr, "missing -journal")
		os.Exit(2)
	}
	entries, err := loadJournalEntries(*journalPath, *claim)
	if err != nil {
		fatal(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{
		"claim":   *claim,
		"count":   len(entries),
		"entries": entries,
	})
}

func runExplain(args []string) {
	fs := flag.NewFlagSet("explain", flag.ExitOnError)
	journalPath := fs.String("journal", "", "NDJSON journal path")
	claim := fs.String("claim", "", "claim filter (recommended)")
	_ = fs.Parse(args)
	if *journalPath == "" {
		fmt.Fprintln(os.Stderr, "missing -journal")
		os.Exit(2)
	}
	entries, err := loadJournalEntries(*journalPath, *claim)
	if err != nil {
		fatal(err)
	}
	report := explainEntries(*claim, entries)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report)
}

// ExplainReport is a deterministic, rule-based operator summary.
type ExplainReport struct {
	Claim           string   `json:"claim"`
	Situation       string   `json:"situation"`
	LastEventType   string   `json:"last_event_type,omitempty"`
	LastToken       uint64   `json:"last_fencing_token,omitempty"`
	Findings        []string `json:"findings"`
	Recommendations []string `json:"recommendations"`
	RulesApplied    []string `json:"rules_applied"`
}

func explainEntries(claim string, entries []journal.Entry) ExplainReport {
	rep := ExplainReport{
		Claim:    claim,
		Findings: []string{},
		Recommendations: []string{
			"prefer abort-transfer / release with --expected-owner --expected-token --reason --confirm",
			"never blind force-unlock",
		},
		RulesApplied: []string{},
	}
	if len(entries) == 0 {
		rep.Situation = "no_events"
		rep.Findings = append(rep.Findings, "journal empty or claim filter matched nothing")
		rep.Recommendations = append(rep.Recommendations, "verify journal path and claim name")
		rep.RulesApplied = append(rep.RulesApplied, "empty_journal")
		return rep
	}
	last := entries[len(entries)-1]
	rep.LastEventType = last.Type
	rep.LastToken = uint64(last.Token)

	preparedWithoutCommit := false
	sawCommit := false
	sawAbort := false
	for _, e := range entries {
		t := strings.ToLower(e.Type + " " + string(e.Reason))
		if strings.Contains(t, "prepared") || strings.Contains(t, "transfer.prepared") {
			preparedWithoutCommit = true
			sawCommit = false
		}
		if strings.Contains(t, "committed") || strings.Contains(t, "handoff.completed") {
			sawCommit = true
			preparedWithoutCommit = false
		}
		if strings.Contains(t, "aborted") || strings.Contains(t, "handoff.failed") {
			sawAbort = true
			preparedWithoutCommit = false
		}
		if strings.Contains(t, "fenced") || strings.Contains(t, "split") {
			rep.Findings = append(rep.Findings, "possible fence-out / split-brain signal in journal")
			rep.Recommendations = append(rep.Recommendations, "compare local vs backend token; revoke stale local lease")
			rep.RulesApplied = append(rep.RulesApplied, "fence_or_split_signal")
		}
	}

	switch {
	case preparedWithoutCommit && !sawCommit:
		rep.Situation = "orphaned_or_pending_transfer"
		rep.Findings = append(rep.Findings, "transfer prepared without subsequent commit")
		rep.Recommendations = append(rep.Recommendations, "recovery abort-transfer with expected owner/token")
		rep.RulesApplied = append(rep.RulesApplied, "prepared_without_commit")
	case sawAbort && !sawCommit:
		rep.Situation = "transfer_aborted"
		rep.Findings = append(rep.Findings, "transfer aborted; previous owner should still control")
		rep.Recommendations = append(rep.Recommendations, "verify heartbeats restored; monitor ownership")
		rep.RulesApplied = append(rep.RulesApplied, "aborted_transfer")
	case sawCommit:
		rep.Situation = "transfer_committed"
		rep.Findings = append(rep.Findings, "commit observed; successor should own claim")
		rep.Recommendations = append(rep.Recommendations, "confirm successor heartbeats; fence-out predecessor")
		rep.RulesApplied = append(rep.RulesApplied, "committed_transfer")
	case strings.Contains(strings.ToLower(last.Type), "unowned") || string(last.Reason) == "expired" || string(last.Reason) == "released":
		rep.Situation = "unowned"
		rep.Findings = append(rep.Findings, "last signal suggests claim unowned")
		rep.Recommendations = append(rep.Recommendations, "promote-candidate or resume-previous-owner")
		rep.RulesApplied = append(rep.RulesApplied, "unowned_terminal")
	default:
		rep.Situation = "owned_or_unknown"
		rep.Findings = append(rep.Findings, "no orphaned-transfer pattern detected from journal alone")
		rep.Recommendations = append(rep.Recommendations, "cross-check backend GetClaim / PlanRecovery")
		rep.RulesApplied = append(rep.RulesApplied, "default_owned")
	}
	return rep
}
