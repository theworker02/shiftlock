package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/theworker02/shiftlock/health"
)

func runImpactPlan(args []string) {
	fs := flag.NewFlagSet("impact-plan", flag.ExitOnError)
	failed := fs.String("failed", "", "node that failed (required)")
	jsonOut := fs.Bool("json", true, "emit JSON plan")
	_ = fs.Parse(args)

	if *failed == "" {
		fmt.Fprintln(os.Stderr, "usage: shiftlock-inspect impact-plan -failed NODE [-json]")
		os.Exit(2)
	}

	plan := sampleHealthReport().PlanImpact(*failed)
	if *jsonOut {
		payload := struct {
			health.ImpactPlan
			Waves []health.Wave `json:"waves"`
		}{ImpactPlan: plan, Waves: plan.Waves()}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(payload)
		return
	}

	fmt.Printf("failed=%s blast_radius=%v actions=%d issues=%d waves=%d\n",
		plan.Failed, plan.BlastRadius, len(plan.Actions), len(plan.Issues), len(plan.Waves()))
	for _, wave := range plan.Waves() {
		fmt.Printf("wave %d (priority %d)\n", wave.Index, wave.Priority)
		for _, action := range wave.Actions {
			fmt.Printf("  [%d] %s %s — %s\n", action.Priority, action.Node, action.Kind, action.Reason)
		}
	}
}

func sampleHealthReport() health.Report {
	b := health.NewBuilder()
	b.Set(health.Node{Name: "api", Status: health.Healthy})
	b.Set(health.Node{Name: "database", Status: health.Healthy})
	b.Set(health.Node{Name: "worker", Status: health.Healthy})
	b.Link("api", "database")
	b.Link("worker", "database")
	return b.Build(time.Now().UTC())
}
