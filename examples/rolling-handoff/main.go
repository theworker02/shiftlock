// Command rolling-handoff demonstrates Drain → Transfer → Commit between two generations.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
)

func main() {
	be := memory.New()
	defer be.Close()

	old, err := shiftlock.New(shiftlock.Config{
		Service: "demo", InstanceID: "v1", GenerationID: "gen-v1", Backend: be, LeaseTTL: 10 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer old.Close()

	ctx := context.Background()
	claim, _ := old.Claim(ctx, "queue-consumer")
	lease, err := claim.WaitForOwnership(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("v1 active token=%d\n", lease.FencingToken())

	// In-flight work
	release, _ := claim.DrainGroup().BeginNamed("msg-1")
	go func() {
		time.Sleep(50 * time.Millisecond)
		release()
	}()

	newGen, err := shiftlock.New(shiftlock.Config{
		Service: "demo", InstanceID: "v2", GenerationID: "gen-v2", Backend: be, LeaseTTL: 10 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer newGen.Close()

	h, err := old.PrepareHandoff(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if err := h.Drain(ctx); err != nil {
		log.Fatal(err)
	}
	if err := h.Transfer(ctx, "gen-v2"); err != nil {
		log.Fatal(err)
	}
	if err := h.Commit(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("handoff committed; old state=%s\n", old.Generation().State)

	rec, _ := be.GetClaim(ctx, "queue-consumer")
	fmt.Printf("new owner=%s token=%d\n", rec.OwnerGeneration, rec.FencingToken)
}
