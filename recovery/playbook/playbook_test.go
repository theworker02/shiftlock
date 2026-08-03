package playbook_test

import (
	"context"
	"errors"
	"testing"

	"github.com/theworker02/shiftlock/recovery/playbook"
)

func TestPlaybookValidateDryRunConfirm(t *testing.T) {
	reg := playbook.NewRegistry(0)
	executed := false
	err := reg.Register(playbook.Playbook{
		Name: "claim-recovery", Version: "1", Summary: "inspect then force-release",
		Steps: []playbook.Step{
			{
				ID: "validate", Title: "check quarantine",
				Validate: func(ctx context.Context) error { return nil },
				DryRun:   func(ctx context.Context) error { return nil },
			},
			{
				ID: "force-release", Title: "force release", Destructive: true,
				Validate: func(ctx context.Context) error { return nil },
				DryRun:   func(ctx context.Context) error { return nil },
				Execute:  func(ctx context.Context) error { executed = true; return nil },
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := reg.Run(context.Background(), "claim-recovery", "1", playbook.RunOptions{DryRun: true})
	if err != nil || !res.Validated || res.StepsOK != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if executed {
		t.Fatal("dry-run executed")
	}
	_, err = reg.Run(context.Background(), "claim-recovery", "1", playbook.RunOptions{})
	if !errors.Is(err, playbook.ErrInvalid) {
		t.Fatalf("expected confirm required, got %v", err)
	}
	res, err = reg.Run(context.Background(), "claim-recovery", "1", playbook.RunOptions{Confirm: true})
	if err != nil || !executed {
		t.Fatalf("%+v %v", res, err)
	}
}
