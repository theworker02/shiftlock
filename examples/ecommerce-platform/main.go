// Command ecommerce-platform stubs an e-commerce resource fabric using
// in-memory adapters (queue, cache, HTTP-shaped services) and a small workflow.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/theworker02/shiftlock/budget"
	"github.com/theworker02/shiftlock/failover"
	"github.com/theworker02/shiftlock/resource"
	cachemem "github.com/theworker02/shiftlock/resource/cache/memory"
	"github.com/theworker02/shiftlock/resource/memory"
	"github.com/theworker02/shiftlock/resource/queue"
	"github.com/theworker02/shiftlock/resource/ratelimit"
	"github.com/theworker02/shiftlock/workflow"
)

func main() {
	reg := resource.NewRegistry(resource.RegistryConfig{})
	defer reg.Close()

	orders := memory.New(
		resource.MustParseResourceID("database/prod/checkout/orders"),
		resource.Description{DisplayName: "orders", Summary: "memory stand-in for postgres"},
		resource.ResourceCapabilities{SupportsHealth: true, SupportsSnapshots: true, SupportsDrain: true},
	)
	qMem := queue.NewMemory(256)
	billingQ, err := queue.New(queue.Config{
		ID:      resource.MustParseResourceID("queue/prod/checkout/billing-events"),
		Backend: qMem,
	})
	if err != nil {
		log.Fatal(err)
	}
	cache, err := cachemem.New(cachemem.Config{
		ID: resource.MustParseResourceID("cache/prod/checkout/customer-balances"),
	})
	if err != nil {
		log.Fatal(err)
	}
	providerA := memory.New(
		resource.MustParseResourceID("http-service/prod/checkout/provider-a"),
		resource.Description{DisplayName: "provider-a"},
		resource.ResourceCapabilities{SupportsHealth: true, SupportsFailover: true},
	)
	providerB := memory.New(
		resource.MustParseResourceID("http-service/prod/checkout/provider-b"),
		resource.Description{DisplayName: "provider-b"},
		resource.ResourceCapabilities{SupportsHealth: true, SupportsFailover: true},
	)
	rl, err := ratelimit.New(ratelimit.Config{
		ID:       resource.MustParseResourceID("rate-limit/prod/checkout/payment-provider"),
		Capacity: 100,
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, res := range []resource.Resource{orders, billingQ, cache, providerA, providerB, rl} {
		if _, err := reg.Register(res, resource.Metadata{Source: "ecommerce-demo"}); err != nil {
			log.Fatal(err)
		}
	}

	fm := failover.NewManager(reg)
	_ = fm.Register(failover.GroupConfig{
		Name:     "payment-providers",
		Primary:  providerA.ID(),
		Standbys: []resource.ResourceID{providerB.ID()},
		Policy:   failover.PolicyManual,
	})

	bud := budget.New(budget.Config{
		Name: "invoice-batch", MaxBytes: 50 << 20, MaxRetries: 10,
		OnExhausted: budget.BehaviorPause,
	})

	ctx := context.Background()
	_ = billingQ.Pause(ctx)
	_ = billingQ.Resume(ctx)
	_ = qMem.Publish("invoice-1")

	target := providerB.ID()
	dec, err := fm.ExecuteFailover(ctx, "payment-providers", "provider-a maintenance", &target)
	if err != nil {
		log.Fatal(err)
	}

	def, err := workflow.Define("checkout-maintenance").
		Step("pause-queue", func(ctx context.Context, _ *workflow.ExecContext) (workflow.Result, error) {
			return workflow.Result{}, billingQ.Pause(ctx)
		}).
		Step("resume-queue", func(ctx context.Context, _ *workflow.ExecContext) (workflow.Result, error) {
			return workflow.Result{}, billingQ.Resume(ctx)
		}).
		Depend("resume-queue", "pause-queue").
		Build()
	if err != nil {
		log.Fatal(err)
	}
	eng := workflow.NewEngine(workflow.EngineConfig{})
	if err := eng.Register(def); err != nil {
		log.Fatal(err)
	}
	inst, err := eng.Run(ctx, "checkout-maintenance", workflow.RunOptions{})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("registered:", reg.Count())
	fmt.Println("failover active:", dec.To.Name, "epoch", dec.EpochAdv.To)
	fmt.Println("budget:", bud.Snapshot().Name)
	fmt.Println("workflow state:", inst.State)
	fmt.Println("ecommerce stub OK")
}
