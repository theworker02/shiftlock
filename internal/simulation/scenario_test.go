package simulation_test

import (
	"context"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/internal/simulation"
	"github.com/theworker02/shiftlock/model"
)

func TestScenarioHandoff(t *testing.T) {
	simulation.NewScenario(t, "handoff", 42).
		At(0, "acquire", func(env *simulation.Env) error {
			a, err := env.NewCoordinator("gen-a")
			if err != nil {
				return err
			}
			t.Cleanup(func() { _ = a.Close() })
			cl, err := a.Claim(context.Background(), "job")
			if err != nil {
				return err
			}
			_, err = cl.WaitForOwnership(context.Background())
			return err
		}).
		At(50*time.Millisecond, "handoff", func(env *simulation.Env) error {
			// Re-open coordinators against same backend via new instances sharing backend
			a, err := shiftlock.New(shiftlock.Config{
				Service: "sim", InstanceID: "gen-a2", GenerationID: "gen-a2",
				Backend: env.Backend, Clock: env.Clock,
				LeaseTTL: time.Second, RenewInterval: 200 * time.Millisecond,
			})
			if err != nil {
				return err
			}
			t.Cleanup(func() { _ = a.Close() })
			// Verify claim still held by someone
			rec, err := env.Backend.GetClaim(context.Background(), "job")
			if err != nil {
				return err
			}
			if rec.FencingToken == 0 {
				return shiftlock.ErrNotOwner
			}
			return nil
		}).
		Run()
}

func TestModelBridge(t *testing.T) {
	acts := []model.Action{
		{Type: model.ActRegisterGeneration, Generation: "a"},
		{Type: model.ActRequestClaim, Generation: "a", Claim: "c", OpID: "1"},
		{Type: model.ActAdvanceTime, Delta: 5 * time.Millisecond},
	}
	if err := simulation.RunModelActions(acts); err != nil {
		t.Fatal(err)
	}
}
