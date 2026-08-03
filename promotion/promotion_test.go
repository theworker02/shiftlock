package promotion_test

import (
	"context"
	"testing"

	"github.com/theworker02/shiftlock/promotion"
	"github.com/theworker02/shiftlock/workflow"
)

func TestBuildAndRun(t *testing.T) {
	def, err := promotion.BuildDefinition(promotion.Config{
		FromEnv: "staging", ToEnv: "prod",
		Hooks: promotion.Hooks{
			Validate: func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
				return workflow.Result{}, nil
			},
			Prepare: func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
				return workflow.Result{}, nil
			},
			Promote: func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
				return workflow.Result{}, nil
			},
			Verify: func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
				return workflow.Result{}, nil
			},
			Rollback: func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
				return workflow.Result{}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	eng := workflow.NewEngine(workflow.EngineConfig{})
	_ = eng.Register(def)
	inst, err := eng.Run(context.Background(), "promote", workflow.RunOptions{OperationID: "promo-1"})
	if err != nil {
		t.Fatal(err)
	}
	if inst.State != workflow.StateCompleted {
		t.Fatalf("%s", inst.State)
	}
}
