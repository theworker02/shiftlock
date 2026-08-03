package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
)

func runRecovery(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: shiftlock-inspect recovery abort-transfer|release ...")
		os.Exit(2)
	}
	action := args[0]
	fs := flag.NewFlagSet("recovery "+action, flag.ExitOnError)
	claim := fs.String("claim", "", "claim name (required)")
	expectedOwner := fs.String("expected-owner", "", "expected owner generation (required)")
	expectedToken := fs.String("expected-token", "", "expected fencing token (required)")
	reason := fs.String("reason", "", "operator reason (required)")
	confirm := fs.Bool("confirm", false, "required to mutate")
	dryRun := fs.Bool("dry-run", false, "print intended action only")
	backendName := fs.String("backend", "memory", "backend: memory (default; wire others via library)")
	successor := fs.String("to-generation", "", "pending successor for abort-transfer (optional)")
	_ = fs.Parse(args[1:])

	if *claim == "" || *expectedOwner == "" || *expectedToken == "" || strings.TrimSpace(*reason) == "" {
		fmt.Fprintln(os.Stderr, "recovery requires --claim --expected-owner --expected-token --reason")
		os.Exit(2)
	}
	tok, err := parseToken(*expectedToken)
	if err != nil {
		fatal(fmt.Errorf("expected-token: %w", err))
	}

	desc := fmt.Sprintf("%s claim=%s owner=%s token=%d reason=%q", action, *claim, *expectedOwner, tok, *reason)
	if !wantConfirm(*confirm, *dryRun, desc) {
		return
	}

	be, err := openRecoveryBackend(*backendName)
	if err != nil {
		fatal(err)
	}
	defer be.Close()

	ctx := context.Background()
	rec, err := be.GetClaim(ctx, *claim)
	if err != nil {
		fatal(err)
	}
	if rec.OwnerGeneration != *expectedOwner {
		fatal(fmt.Errorf("refusing: owner mismatch have=%q want=%q (never blind force-unlock)", rec.OwnerGeneration, *expectedOwner))
	}
	if rec.FencingToken != tok {
		fatal(fmt.Errorf("refusing: token mismatch have=%d want=%d", rec.FencingToken, tok))
	}

	switch action {
	case "abort-transfer":
		to := *successor
		if to == "" {
			to = rec.PendingSuccessor
		}
		out, err := be.AbortTransfer(ctx, shiftlock.AbortRequest{
			ClaimName: *claim, FromGeneration: *expectedOwner, ToGeneration: to,
			ExpectedToken: tok, OperationID: shiftlock.OperationID("inspect-abort-" + time.Now().UTC().Format("20060102T150405")),
		})
		if err != nil {
			fatal(err)
		}
		fmt.Printf("aborted transfer; phase=%s owner=%s token=%d\n", out.Phase, out.OwnerGeneration, out.FencingToken)
	case "release":
		err := be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{
			ClaimName: *claim, GenerationID: *expectedOwner, Token: tok,
			OperationID: shiftlock.OperationID("inspect-release-" + time.Now().UTC().Format("20060102T150405")),
		})
		if err != nil {
			fatal(err)
		}
		fmt.Println("released claim")
	default:
		fmt.Fprintf(os.Stderr, "unknown recovery action %q (supported: abort-transfer, release)\n", action)
		os.Exit(2)
	}
}

func openRecoveryBackend(name string) (shiftlock.Backend, error) {
	switch name {
	case "memory", "":
		return memory.New(), nil
	default:
		return nil, fmt.Errorf("recovery CLI ships with -backend=memory only; use library APIs for postgres/redis/k8s")
	}
}
