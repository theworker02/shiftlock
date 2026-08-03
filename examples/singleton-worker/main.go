// Command singleton-worker demonstrates exclusive ownership of a named claim
// using the in-memory backend.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
)

func main() {
	be := memory.New()
	defer be.Close()

	coord, err := shiftlock.New(shiftlock.Config{
		Service:    "demo",
		InstanceID: hostname(),
		Backend:    be,
		LeaseTTL:   5 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer coord.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = coord.Run(ctx, shiftlock.Worker{
		Name: "billing-reconciler",
		Run: func(ctx context.Context, ownership *shiftlock.Lease) error {
			fmt.Printf("owned billing-reconciler fencing_token=%d generation=%s\n",
				ownership.FencingToken(), coord.Generation().ID)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					fmt.Println("lease lost or canceled; stopping work")
					return nil
				case <-ticker.C:
					fmt.Println("reconcile tick")
				}
			}
		},
	})
	if err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "local"
	}
	return h
}
