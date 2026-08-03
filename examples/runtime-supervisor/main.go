package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/supervise"
)

// Example: NewRuntime + Supervisor (Phase 6).
func main() {
	be := memory.New()
	defer be.Close()

	rt, err := shiftlock.NewRuntime(shiftlock.RuntimeConfig{
		Config: shiftlock.Config{
			Service: "demo", InstanceID: "local", Backend: be, LeaseTTL: 10 * time.Second,
		},
		SecurityProfile:  shiftlock.ProfileStandard,
		EnableSupervisor: true,
		EnableAudit:      true,
		EnableGuard:      true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer rt.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err = rt.Supervisor().StartSpec(supervise.Spec{
		Name: "heartbeat-logger",
		Mode: supervise.ModeSingleton,
		Restart: supervise.RestartPolicy{MaxRestarts: 3, Interval: time.Second},
		Run: func(ctx context.Context) error {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-t.C:
					fmt.Println("tick", rt.Coordinator().Generation().ID, "features", rt.Features())
				}
			}
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	<-ctx.Done()
}
