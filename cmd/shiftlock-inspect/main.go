// Command shiftlock-inspect is the ShiftLock operator toolkit.
//
// Subcommands:
//
//	timeline          print sanitized journal timeline
//	explain           rule-based incident explanation
//	incident create   pack sanitized evidence into tar.gz
//	recovery          guarded abort-transfer / release (never blind force-unlock)
//	readiness-report  production readiness report (text|json|sarif)
//	rehearse-handoff  dry-run handoff against memory backend
//
// Default (no subcommand): emit coordinator diagnostics JSON.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
)

func main() {
	if maybeDelegateToShiftlock() {
		runShiftlockAlias()
		return
	}
	if len(os.Args) < 2 {
		runDiagnostics(os.Args[1:])
		return
	}
	switch os.Args[1] {
	case "timeline":
		runTimeline(os.Args[2:])
	case "explain":
		runExplain(os.Args[2:])
	case "incident":
		if len(os.Args) < 3 || os.Args[2] != "create" {
			fmt.Fprintln(os.Stderr, "usage: shiftlock-inspect incident create -journal PATH -out FILE.tar.gz [-claim NAME]")
			os.Exit(2)
		}
		runIncidentCreate(os.Args[3:])
	case "recovery":
		runRecovery(os.Args[2:])
	case "readiness-report":
		runReadinessReport(os.Args[2:])
	case "rehearse-handoff":
		runRehearseHandoff(os.Args[2:])
	case "-h", "-help", "--help", "help":
		printHelp()
	default:
		runDiagnostics(os.Args[1:])
	}
}

func printHelp() {
	fmt.Fprintf(os.Stderr, `shiftlock-inspect — ShiftLock operator toolkit

Usage:
  shiftlock-inspect [flags]                     diagnostics JSON (memory backend)
  shiftlock-inspect timeline -journal PATH [-claim NAME]
  shiftlock-inspect explain -journal PATH [-claim NAME]
  shiftlock-inspect incident create -journal PATH -out FILE.tar.gz [-claim NAME]
  shiftlock-inspect recovery abort-transfer|release [flags]
  shiftlock-inspect readiness-report [-format text|json|sarif] [-out PATH]
  shiftlock-inspect rehearse-handoff [-claim NAME]

Recovery flags (required for mutate):
  --expected-owner  --expected-token  --reason  --confirm  [--dry-run]

Never uses blind force-unlock.
`)
}

func runDiagnostics(args []string) {
	fs := flag.NewFlagSet("shiftlock-inspect", flag.ExitOnError)
	service := fs.String("service", "inspect", "service name")
	instance := fs.String("instance", "cli", "instance id")
	claim := fs.String("claim", "", "optional claim name to acquire briefly for inspection")
	jsonOut := fs.Bool("json", true, "emit JSON diagnostics")
	_ = fs.Parse(args)

	be := memory.New()
	defer be.Close()

	coord, err := shiftlock.New(shiftlock.Config{
		Service:    *service,
		InstanceID: *instance,
		Backend:    be,
		LeaseTTL:   10 * time.Second,
	})
	if err != nil {
		fatal(err)
	}
	defer coord.Close()

	ctx := context.Background()
	if *claim != "" {
		cl, err := coord.Claim(ctx, *claim)
		if err != nil {
			fatal(err)
		}
		lease, err := cl.WaitForOwnership(ctx)
		if err != nil {
			fatal(err)
		}
		fmt.Fprintf(os.Stderr, "owned %s token=%d\n", *claim, lease.FencingToken())
	}

	d := coord.Diagnostics()
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(d)
		return
	}
	fmt.Printf("service=%s instance=%s gen=%s state=%s caps.cas=%v\n",
		d.Service, d.InstanceID, d.Generation.ID, d.Generation.State, d.Capabilities.AtomicCAS)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func wantConfirm(confirm bool, dryRun bool, action string) bool {
	if dryRun {
		fmt.Printf("dry-run: would %s\n", action)
		return false
	}
	if !confirm {
		fmt.Fprintln(os.Stderr, "refusing: pass --confirm (and preferably --dry-run first). Never blind force-unlock.")
		os.Exit(2)
	}
	return true
}

func parseToken(s string) (shiftlock.FencingToken, error) {
	var n uint64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	return shiftlock.FencingToken(n), err
}
