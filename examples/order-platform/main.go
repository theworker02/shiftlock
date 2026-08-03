// Command order-platform demonstrates stale-write rejection during rolling handoff.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
	fencingmem "github.com/theworker02/shiftlock/fencing/memory"
)

func main() {
	be := memory.New()
	defer be.Close()
	resource := fencingmem.NewResource()

	old, err := shiftlock.New(shiftlock.Config{
		Service: "orders", InstanceID: "v1", GenerationID: "gen-v1", Backend: be, LeaseTTL: 10 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer old.Close()

	ctx := context.Background()
	claim, _ := old.Claim(ctx, "order-writer")
	lease, err := claim.WaitForOwnership(ctx)
	if err != nil {
		log.Fatal(err)
	}
	oldTok := lease.FencingToken()
	if err := resource.Write(oldTok, "order-100"); err != nil {
		log.Fatal(err)
	}
	fmt.Println("v1 wrote with token", oldTok)

	h, _ := old.PrepareHandoff(ctx)
	_ = h.Drain(ctx)
	_ = h.Transfer(ctx, "gen-v2")
	_ = h.Commit(ctx)

	newGen, err := shiftlock.New(shiftlock.Config{
		Service: "orders", InstanceID: "v2", GenerationID: "gen-v2", Backend: be, LeaseTTL: 10 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer newGen.Close()
	cl2, _ := newGen.Claim(ctx, "order-writer")
	lease2, err := cl2.WaitForOwnership(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if err := resource.Write(lease2.FencingToken(), "order-101"); err != nil {
		log.Fatal(err)
	}
	fmt.Println("v2 wrote with token", lease2.FencingToken())

	// Stale v1 write must fail against the newer fencing epoch.
	if err := resource.Write(oldTok, "order-stale"); err == nil {
		log.Fatal("expected stale write rejection")
	} else {
		fmt.Println("rejected stale write:", err)
	}
	val, tok := resource.Read()
	fmt.Printf("final value=%q token=%d\n", val, tok)
}
