package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
)

func runRehearseHandoff(args []string) {
	fs := flag.NewFlagSet("rehearse-handoff", flag.ExitOnError)
	claim := fs.String("claim", "rehearse-claim", "claim name")
	_ = fs.Parse(args)

	be := memory.New()
	defer be.Close()

	old, err := shiftlock.New(shiftlock.Config{
		Service: "rehearse", InstanceID: "v1", GenerationID: "gen-v1",
		Backend: be, LeaseTTL: 10 * time.Second,
	})
	if err != nil {
		fatal(err)
	}
	defer old.Close()

	ctx := context.Background()
	cl, err := old.Claim(ctx, *claim)
	if err != nil {
		fatal(err)
	}
	lease, err := cl.WaitForOwnership(ctx)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("owner gen-v1 token=%d\n", lease.FencingToken())

	h, err := old.PrepareHandoff(ctx)
	if err != nil {
		fatal(err)
	}
	if err := h.Drain(ctx); err != nil {
		fatal(err)
	}
	if err := h.Transfer(ctx, "gen-v2"); err != nil {
		fatal(err)
	}

	neu, err := shiftlock.New(shiftlock.Config{
		Service: "rehearse", InstanceID: "v2", GenerationID: "gen-v2",
		Backend: be, LeaseTTL: 10 * time.Second,
	})
	if err != nil {
		fatal(err)
	}
	defer neu.Close()

	if err := h.Commit(ctx); err != nil {
		fatal(err)
	}
	cl2, err := neu.Claim(ctx, *claim)
	if err != nil {
		fatal(err)
	}
	lease2, err := cl2.WaitForOwnership(ctx)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("rehearse ok: successor gen-v2 token=%d (advanced from %d)\n", lease2.FencingToken(), lease.FencingToken())
	plan, _ := neu.PlanRecovery(ctx, *claim)
	if plan != nil {
		fmt.Printf("plan_recovery situation=%s recommended=%v\n", plan.Situation, plan.Recommended)
	}
}
