// Command shiftlock is the unified ShiftLock operator CLI.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/audit"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/control/snapshot"
	"github.com/theworker02/shiftlock/security/redteam"
	"github.com/theworker02/shiftlock/security/scanner"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "help", "-h", "--help":
		printHelp()
	case "version":
		err = cmdVersion()
	case "status":
		err = cmdStatus(args)
	case "claims":
		err = cmdClaims(args)
	case "generations":
		err = cmdGenerations(args)
	case "tasks":
		err = cmdTasks(args)
	case "maintenance":
		err = cmdMaintenance(args)
	case "lockdown":
		err = cmdLockdown(args)
	case "capabilities":
		err = cmdCapabilities(args)
	case "security":
		err = cmdSecurity(args)
	case "audit":
		err = cmdAudit(args)
	case "incident":
		err = cmdIncident(args)
	case "snapshot":
		err = cmdSnapshot(args)
	case "redteam":
		err = cmdRedteam(args)
	case "resources":
		err = cmdResources(args)
	case "workflows":
		err = cmdWorkflows(args)
	case "migrations":
		err = cmdMigrations(args)
	case "failover":
		err = cmdFailover(args)
	case "tui":
		err = cmdTUI(args)
	case "inspect":
		// thin alias toward shiftlock-inspect behavior
		err = cmdStatus(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		printHelp()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Fprintf(os.Stderr, `shiftlock — security-first runtime control CLI

Usage:
  shiftlock <command> [flags]

Commands:
  status              runtime / coordinator diagnostics
  claims              list claim diagnostics (memory demo backend)
  generations         show local generation
  tasks               list supervisor tasks (stub without live runtime)
  maintenance         show maintenance help / local state file
  lockdown            show lockdown help / local state file
  capabilities        describe capability model
  security scan       scan configuration for unsafe settings
  audit verify|export verify or export an audit NDJSON/JSON chain
  incident            create sanitized incident notes
  snapshot create|diff  sanitized snapshot create/diff
  redteam run|catalog   run security red-team scenarios
  resources list|inspect|health   demo fabric resource ops (-state-dir DIR)
  workflows list|inspect|run      demo fabric workflows (-dry-run|-confirm|-state-dir)
  migrations list|start|pause     demo migrations (-dry-run|-confirm|-state-dir)
  failover status                 demo failover groups (-state-dir DIR)
  tui                 stub (full TUI deferred; see docs/site/tui)
  version             print version
  inspect             alias of status (prefer shiftlock-inspect for journals)

`)
}

func cmdVersion() error {
	info := map[string]any{"version": version, "module": "github.com/theworker02/shiftlock"}
	if bi, ok := debug.ReadBuildInfo(); ok {
		info["go"] = bi.GoVersion
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info["module_version"] = bi.Main.Version
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(info)
}

func openDemoCoord(service, instance string) (*shiftlock.Coordinator, func(), error) {
	be := memory.New()
	coord, err := shiftlock.New(shiftlock.Config{
		Service:    service,
		InstanceID: instance,
		Backend:    be,
		LeaseTTL:   10 * time.Second,
	})
	if err != nil {
		_ = be.Close()
		return nil, nil, err
	}
	return coord, func() { _ = coord.Close(); _ = be.Close() }, nil
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	service := fs.String("service", "shiftlock", "service name")
	instance := fs.String("instance", "cli", "instance id")
	_ = fs.Parse(args)
	coord, closer, err := openDemoCoord(*service, *instance)
	if err != nil {
		return err
	}
	defer closer()
	d := coord.Diagnostics()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(d)
}

func cmdClaims(args []string) error {
	fs := flag.NewFlagSet("claims", flag.ExitOnError)
	service := fs.String("service", "shiftlock", "service")
	instance := fs.String("instance", "cli", "instance")
	_ = fs.Parse(args)
	coord, closer, err := openDemoCoord(*service, *instance)
	if err != nil {
		return err
	}
	defer closer()
	d := coord.Diagnostics()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"service":  d.Service,
		"claims":   d.Claims,
		"note":     "demo memory backend; attach a live backend for production inspection",
	})
}

func cmdGenerations(args []string) error {
	fs := flag.NewFlagSet("generations", flag.ExitOnError)
	service := fs.String("service", "shiftlock", "service")
	instance := fs.String("instance", "cli", "instance")
	_ = fs.Parse(args)
	coord, closer, err := openDemoCoord(*service, *instance)
	if err != nil {
		return err
	}
	defer closer()
	d := coord.Diagnostics()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(d.Generation)
}

func cmdTasks(args []string) error {
	_ = args
	fmt.Println(`{"tasks":[],"note":"connect via Runtime.Supervisor() or shiftlock-agent for live task lists"}`)
	return nil
}

func cmdMaintenance(args []string) error {
	fs := flag.NewFlagSet("maintenance", flag.ExitOnError)
	path := fs.String("state", "", "optional durable maintenance state JSON")
	_ = fs.Parse(args)
	if *path == "" {
		fmt.Println(`{"active":false,"note":"provide -state PATH to inspect durable maintenance state"}`)
		return nil
	}
	b, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	_, _ = os.Stdout.Write(b)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

func cmdLockdown(args []string) error {
	fs := flag.NewFlagSet("lockdown", flag.ExitOnError)
	path := fs.String("state", "", "optional durable lockdown state JSON")
	_ = fs.Parse(args)
	if *path == "" {
		fmt.Println(`{"active":false,"note":"provide -state PATH; unlock requires expected id + confirm + strong auth"}`)
		return nil
	}
	b, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	_, _ = os.Stdout.Write(b)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

func cmdCapabilities(args []string) error {
	_ = args
	fmt.Println(`{"model":"capability","rules":["deny-by-default","short-TTL","delegate-only-reduces","epoch-bound","optional-ed25519"],"package":"github.com/theworker02/shiftlock/capability"}`)
	return nil
}

func cmdSecurity(args []string) error {
	if len(args) == 0 || args[0] != "scan" {
		return fmt.Errorf("usage: shiftlock security scan [-format text|json|sarif] [-production] ...")
	}
	fs := flag.NewFlagSet("security scan", flag.ExitOnError)
	format := fs.String("format", "text", "text|json|sarif")
	production := fs.Bool("production", false, "treat target as production")
	unsigned := fs.Bool("unsigned-config", false, "unsigned config currently active")
	sigRequired := fs.Bool("signatures-required", false, "signatures required flag")
	skipTLS := fs.Bool("insecure-skip-verify", false, "TLS skip verify enabled")
	tcpAgent := fs.Bool("tcp-agent", false, "agent TCP listener enabled")
	shell := fs.Bool("shell-exec", false, "shell execution enabled")
	unbounded := fs.Bool("unbounded-queue", false, "unbounded queue configured")
	auditVerify := fs.Bool("audit-verify-on-start", true, "audit verified on start")
	denyDefault := fs.Bool("deny-by-default", true, "policy denies by default")
	profile := fs.String("profile", "standard", "security profile name")
	leaseTTL := fs.Int("lease-ttl-seconds", 15, "lease TTL seconds")
	capsUnsigned := fs.Bool("capabilities-unsigned", false, "capabilities issued unsigned")
	_ = fs.Parse(args[1:])

	findings := scanner.Scan(scanner.Input{
		Production:              *production,
		SignaturesRequired:      *sigRequired,
		UnsignedConfigActive:    *unsigned,
		AllowInsecureSkipVerify: *skipTLS,
		TCPAgentListener:        *tcpAgent,
		ShellExecEnabled:        *shell,
		UnboundedQueue:          *unbounded,
		AuditVerifyOnStart:      *auditVerify,
		DenyByDefault:           *denyDefault,
		SecurityProfile:         *profile,
		LeaseTTLSeconds:         *leaseTTL,
		CapabilitiesUnsigned:    *capsUnsigned,
	})
	if err := scanner.Format(os.Stdout, *format, findings); err != nil {
		return err
	}
	for _, f := range findings {
		if f.Severity == scanner.SeverityCritical || f.Severity == scanner.SeverityHigh {
			os.Exit(2)
		}
	}
	return nil
}

func cmdAudit(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: shiftlock audit verify|export -file PATH")
	}
	sub := args[0]
	fs := flag.NewFlagSet("audit "+sub, flag.ExitOnError)
	file := fs.String("file", "", "audit NDJSON or JSON array file")
	_ = fs.Parse(args[1:])
	if *file == "" {
		return fmt.Errorf("-file required")
	}
	recs, err := loadAuditFile(*file)
	if err != nil {
		return err
	}
	switch sub {
	case "verify":
		rep := audit.VerifyRecords(recs, nil)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
		if !rep.OK {
			os.Exit(2)
		}
		return nil
	case "export":
		b, err := audit.ExportJSON(recs)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		if err == nil {
			fmt.Println()
		}
		return err
	default:
		return fmt.Errorf("unknown audit subcommand %q", sub)
	}
}

func loadAuditFile(path string) ([]audit.Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	trim := strings.TrimSpace(string(data))
	if strings.HasPrefix(trim, "[") {
		var recs []audit.Record
		if err := json.Unmarshal(data, &recs); err != nil {
			return nil, err
		}
		return recs, nil
	}
	return audit.LoadNDJSON(path)
}

func cmdIncident(args []string) error {
	fs := flag.NewFlagSet("incident", flag.ExitOnError)
	reason := fs.String("reason", "unspecified", "incident reason")
	out := fs.String("out", "", "optional output JSON path")
	_ = fs.Parse(args)
	doc := map[string]any{
		"created_at": time.Now().UTC(),
		"reason":     *reason,
		"note":       "sanitized stub; use shiftlock-inspect incident create for journal bundles",
		"secrets":    "[redacted]",
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if *out != "" {
		return os.WriteFile(*out, append(b, '\n'), 0o600)
	}
	fmt.Println(string(b))
	return nil
}

func cmdSnapshot(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: shiftlock snapshot create|diff ...")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("snapshot create", flag.ExitOnError)
		service := fs.String("service", "shiftlock", "service")
		instance := fs.String("instance", "cli", "instance")
		out := fs.String("out", "", "output path (default stdout)")
		_ = fs.Parse(args[1:])
		coord, closer, err := openDemoCoord(*service, *instance)
		if err != nil {
			return err
		}
		defer closer()
		d := coord.Diagnostics()
		raw, _ := json.Marshal(d)
		var data map[string]any
		_ = json.Unmarshal(raw, &data)
		// inject a secret-looking field to prove redaction
		if data == nil {
			data = map[string]any{}
		}
		snap, err := snapshot.Create(*service, *instance, data, nil)
		if err != nil {
			return err
		}
		b, err := snap.Marshal()
		if err != nil {
			return err
		}
		if *out == "" {
			fmt.Println(string(b))
			return nil
		}
		return os.WriteFile(*out, append(b, '\n'), 0o600)
	case "diff":
		fs := flag.NewFlagSet("snapshot diff", flag.ExitOnError)
		aPath := fs.String("a", "", "snapshot A JSON")
		bPath := fs.String("b", "", "snapshot B JSON")
		_ = fs.Parse(args[1:])
		if *aPath == "" || *bPath == "" {
			return fmt.Errorf("-a and -b required")
		}
		var a, b snapshot.Snapshot
		if err := readJSON(*aPath, &a); err != nil {
			return err
		}
		if err := readJSON(*bPath, &b); err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(snapshot.Diff(a, b))
	default:
		return fmt.Errorf("unknown snapshot subcommand %q", args[0])
	}
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func cmdRedteam(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: shiftlock redteam run|catalog")
	}
	switch args[0] {
	case "catalog":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(redteam.Catalog())
	case "run":
		results, err := redteam.RunAll()
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return err
		}
		for _, r := range results {
			if !r.Passed {
				os.Exit(2)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown redteam subcommand %q", args[0])
	}
}

func cmdTUI(args []string) error {
	_ = args
	fmt.Println(`{"tui":"deferred","note":"Full interactive TUI is not shipped yet. Use shiftlock CLI + shiftlock-inspect. See docs/site/tui/."}`)
	return nil
}

// ensure filepath used on windows path tips in help expansions
var _ = filepath.Separator
