// Example quorum-deployment waits for a quorum barrier before proceeding.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/theworker02/shiftlock/barrier"
)

func main() {
	b, err := barrier.New(barrier.Config{
		MaxParticipants: 3,
		Policy:          barrier.PolicyQuorum,
		Epoch:           1,
	})
	if err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = b.Arrive("node-a", 1)
		_ = b.Arrive("node-b", 1) // majority of 3
	}()

	if err := b.Wait(ctx); err != nil {
		fail(err)
	}
	fmt.Printf("quorum released count=%d\n", b.Count())
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
