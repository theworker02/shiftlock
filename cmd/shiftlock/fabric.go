package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/failover"
	"github.com/theworker02/shiftlock/migration"
	"github.com/theworker02/shiftlock/resource"
	resmem "github.com/theworker02/shiftlock/resource/memory"
	"github.com/theworker02/shiftlock/workflow"
)

func openDemoRuntime(stateDir string) (*shiftlock.Runtime, func(), error) {
	be := memory.New()
	cfg := shiftlock.RuntimeConfig{
		Config: shiftlock.Config{
			Service: "shiftlock-cli", InstanceID: "cli", Backend: be, LeaseTTL: 10 * time.Second,
		},
		EnableResources: true,
		EnableWorkflows: true,
	}
	if stateDir != "" {
		shiftlock.WithLocalStateDir(stateDir)(&cfg)
	}
	rt, err := shiftlock.NewRuntime(cfg)
	if err != nil {
		_ = be.Close()
		return nil, nil, err
	}
	seedDemoFabric(rt)
	return rt, func() { _ = rt.Close(); _ = be.Close() }, nil
}

func parseStateDir(args []string) (stateDir string, rest []string) {
	rest = args
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "-state-dir" && i+1 < len(rest) {
			stateDir = rest[i+1]
			rest = append(rest[:i], rest[i+2:]...)
			return stateDir, rest
		}
		if len(a) > 11 && a[:11] == "-state-dir=" {
			stateDir = a[11:]
			rest = append(rest[:i], rest[i+1:]...)
			return stateDir, rest
		}
	}
	return "", rest
}

func seedDemoFabric(rt *shiftlock.Runtime) {
	reg := rt.Resources()
	if reg == nil {
		return
	}
	_ = mustReg(reg, resmem.Worker("demo", "cli", "worker-a"))
	_ = mustReg(reg, resmem.Feature("demo", "cli", "feature-x"))
	if fm := rt.Failover(); fm != nil {
		a := resource.MustParseResourceID("http-service/demo/cli/provider-a")
		b := resource.MustParseResourceID("http-service/demo/cli/provider-b")
		_ = mustReg(reg, resmem.New(a, resource.Description{DisplayName: "provider-a"},
			resource.ResourceCapabilities{SupportsHealth: true, SupportsFailover: true}))
		_ = mustReg(reg, resmem.New(b, resource.Description{DisplayName: "provider-b"},
			resource.ResourceCapabilities{SupportsHealth: true, SupportsFailover: true}))
		_ = fm.Register(failover.GroupConfig{
			Name: "demo-providers", Primary: a, Standbys: []resource.ResourceID{b}, Policy: failover.PolicyManual,
		})
	}
	if mig := rt.Migrations(); mig != nil {
		_ = mig.DefineSimple("demo-cutover", "db-a", "db-b")
	}
	if eng := rt.Workflows(); eng != nil {
		def, err := workflow.Define("demo-ping").
			Step("ping", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
				return workflow.Result{Evidence: workflow.Evidence{Event: "ping"}}, nil
			}).
			Build()
		if err == nil {
			_ = eng.Register(def)
		}
	}
}

func mustReg(reg *resource.Registry, res resource.Resource) error {
	_, err := reg.Register(res, resource.Metadata{Source: "cli-demo"})
	return err
}

func cmdResources(args []string) error {
	stateDir, args := parseStateDir(args)
	if len(args) == 0 {
		return fmt.Errorf("usage: shiftlock resources [-state-dir DIR] list|inspect|health")
	}
	sub := args[0]
	rt, closer, err := openDemoRuntime(stateDir)
	if err != nil {
		return err
	}
	defer closer()
	reg := rt.Resources()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	switch sub {
	case "list":
		entries := reg.List()
		out := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			out = append(out, map[string]any{
				"id": e.Resource.ID().String(), "kind": e.Resource.Kind(),
				"epoch": e.Epoch, "source": e.Meta.Source,
			})
		}
		note := "demo fabric seed"
		if stateDir != "" {
			note = "demo fabric seed; state-dir=" + stateDir
		}
		return enc.Encode(map[string]any{"resources": out, "count": len(out), "note": note, "local_state_dir": rt.LocalStateDir()})
	case "inspect":
		fs := flag.NewFlagSet("resources inspect", flag.ExitOnError)
		idStr := fs.String("id", "", "resource id kind/env/service/name")
		_ = fs.Parse(args[1:])
		if *idStr == "" {
			return fmt.Errorf("-id required")
		}
		id, err := resource.ParseResourceID(*idStr)
		if err != nil {
			return err
		}
		ent, err := reg.Get(id)
		if err != nil {
			return err
		}
		return enc.Encode(map[string]any{
			"id": ent.Resource.ID().String(), "kind": ent.Resource.Kind(),
			"description": ent.Resource.Describe(), "capabilities": ent.Resource.Capabilities(),
			"epoch": ent.Epoch, "meta": ent.Meta,
		})
	case "health":
		fs := flag.NewFlagSet("resources health", flag.ExitOnError)
		idStr := fs.String("id", "", "optional resource id; omit for all")
		_ = fs.Parse(args[1:])
		ctx := context.Background()
		if *idStr != "" {
			id, err := resource.ParseResourceID(*idStr)
			if err != nil {
				return err
			}
			ent, err := reg.Get(id)
			if err != nil {
				return err
			}
			return enc.Encode(ent.Resource.Health(ctx))
		}
		entries := reg.List()
		out := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			h := e.Resource.Health(ctx)
			out = append(out, map[string]any{"id": e.Resource.ID().String(), "overall": h.Overall, "message": h.Message})
		}
		return enc.Encode(map[string]any{"health": out})
	default:
		return fmt.Errorf("unknown resources subcommand %q", sub)
	}
}

func cmdWorkflows(args []string) error {
	stateDir, args := parseStateDir(args)
	if len(args) == 0 {
		return fmt.Errorf("usage: shiftlock workflows [-state-dir DIR] list|inspect|run")
	}
	sub := args[0]
	rt, closer, err := openDemoRuntime(stateDir)
	if err != nil {
		return err
	}
	defer closer()
	eng := rt.Workflows()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	switch sub {
	case "list":
		return enc.Encode(map[string]any{
			"definitions": eng.ListDefinitions(),
			"instances":   eng.ListInstances(),
			"note":        "demo fabric seed",
			"local_state_dir": rt.LocalStateDir(),
		})
	case "inspect":
		fs := flag.NewFlagSet("workflows inspect", flag.ExitOnError)
		id := fs.String("id", "", "instance id")
		_ = fs.Parse(args[1:])
		if *id == "" {
			return fmt.Errorf("-id required")
		}
		inst, err := eng.Get(*id)
		if err != nil {
			return err
		}
		return enc.Encode(inst)
	case "run":
		fs := flag.NewFlagSet("workflows run", flag.ExitOnError)
		name := fs.String("name", "demo-ping", "workflow name")
		dry := fs.Bool("dry-run", false, "dry-run mode")
		confirm := fs.Bool("confirm", false, "confirm mutating run")
		_ = fs.Parse(args[1:])
		if !*dry && !*confirm {
			return fmt.Errorf("refusing mutating run without -dry-run or -confirm")
		}
		inst, err := eng.Run(context.Background(), *name, workflow.RunOptions{DryRun: *dry})
		if err != nil {
			return err
		}
		return enc.Encode(inst)
	default:
		return fmt.Errorf("unknown workflows subcommand %q", sub)
	}
}

func cmdMigrations(args []string) error {
	stateDir, args := parseStateDir(args)
	if len(args) == 0 {
		return fmt.Errorf("usage: shiftlock migrations [-state-dir DIR] list|start|pause")
	}
	sub := args[0]
	rt, closer, err := openDemoRuntime(stateDir)
	if err != nil {
		return err
	}
	defer closer()
	mig := rt.Migrations()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	switch sub {
	case "list":
		return enc.Encode(map[string]any{"migrations": mig.List(), "note": "demo fabric seed"})
	case "start":
		fs := flag.NewFlagSet("migrations start", flag.ExitOnError)
		name := fs.String("name", "demo-cutover", "migration name")
		dry := fs.Bool("dry-run", false, "dry-run (skips cutover)")
		confirm := fs.Bool("confirm", false, "confirm live cutover")
		_ = fs.Parse(args[1:])
		if !*dry && !*confirm {
			return fmt.Errorf("refusing cutover without -dry-run or -confirm")
		}
		prog, err := mig.Start(context.Background(), *name, migration.StartOptions{DryRun: *dry})
		if err != nil {
			_ = enc.Encode(prog)
			return err
		}
		return enc.Encode(prog)
	case "pause":
		fs := flag.NewFlagSet("migrations pause", flag.ExitOnError)
		name := fs.String("name", "demo-cutover", "migration name")
		confirm := fs.Bool("confirm", false, "confirm pause")
		_ = fs.Parse(args[1:])
		if !*confirm {
			return fmt.Errorf("pause requires -confirm")
		}
		// Advance into copying then pause for demo observability.
		_, err := mig.Start(context.Background(), *name, migration.StartOptions{PauseAfter: migration.PhasePreparing})
		if err != nil && err != migration.ErrPaused {
			return err
		}
		prog, err := mig.Pause(*name, "operator pause")
		if err != nil {
			// Already paused by PauseAfter is acceptable for demo.
			if p, e := mig.ProgressOf(*name); e == nil && p.Phase == migration.PhasePaused {
				return enc.Encode(p)
			}
			return err
		}
		return enc.Encode(prog)
	default:
		return fmt.Errorf("unknown migrations subcommand %q", sub)
	}
}

func cmdFailover(args []string) error {
	stateDir, args := parseStateDir(args)
	if len(args) == 0 || args[0] != "status" {
		return fmt.Errorf("usage: shiftlock failover [-state-dir DIR] status")
	}
	rt, closer, err := openDemoRuntime(stateDir)
	if err != nil {
		return err
	}
	defer closer()
	fm := rt.Failover()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	groups := fm.List()
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		out = append(out, map[string]any{
			"name": g.Name, "policy": g.Policy,
			"primary": g.Primary.String(), "active": g.Active.String(),
			"standbys": idsStrings(g.Standbys), "epoch": g.Epoch,
		})
	}
	return enc.Encode(map[string]any{"groups": out, "history": fm.History(), "note": "demo fabric seed"})
}

func idsStrings(ids []resource.ResourceID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}
