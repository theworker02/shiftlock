// Command kubernetes-worker demonstrates the Kubernetes Lease backend with an
// in-memory LeaseClient stand-in. Swap in a real client for cluster use.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/kubernetes"
)

func main() {
	client := kubernetes.NewMemoryLeaseClient()
	be := kubernetes.New(client, "default")

	coord, err := shiftlock.New(shiftlock.Config{
		Service: "demo", InstanceID: "pod-a", Backend: be, LeaseTTL: 15 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer coord.Close()

	ctx := context.Background()
	claim, _ := coord.Claim(ctx, "leader")
	lease, err := claim.WaitForOwnership(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("k8s-lease owner token=%d\n", lease.FencingToken())
}
