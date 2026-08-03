package migration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/theworker02/shiftlock/migration"
	"github.com/theworker02/shiftlock/resource"
)

type ownGate struct{ ok bool }

func (o ownGate) Owns(resource.ResourceID, string) bool { return o.ok }

type fenceGate struct{ err error }

func (f fenceGate) CheckFence(resource.ResourceID, uint64) error { return f.err }

type lockGate struct{ block bool }

func (l lockGate) BlocksMutations() bool { return l.block }

func TestMigrationDryRunAndVerify(t *testing.T) {
	verified := false
	c := migration.New(migration.Config{})
	err := c.Define(migration.Definition{
		Name: "orders", Source: "db-a", Target: "db-b",
		VerifySteps: []migration.VerifyStep{
			func(ctx context.Context, run *migration.Run) error {
				verified = true
				if run.Name != "orders" {
					return errors.New("bad name")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prog, err := c.Start(context.Background(), "orders", migration.StartOptions{DryRun: true, Total: 100})
	if err != nil {
		t.Fatal(err)
	}
	if prog.Phase != migration.PhaseCompleted || !prog.DryRun {
		t.Fatalf("%+v", prog)
	}
	if !verified {
		t.Fatal("expected verify hook")
	}
}

func TestMigrationPauseResume(t *testing.T) {
	c := migration.NewCoordinator()
	if err := c.DefineSimple("x", "s", "t"); err != nil {
		t.Fatal(err)
	}
	prog, err := c.Start(context.Background(), "x", migration.StartOptions{PauseAfter: migration.PhaseCopying})
	if !errors.Is(err, migration.ErrPaused) {
		t.Fatalf("err=%v prog=%+v", err, prog)
	}
	if prog.Phase != migration.PhasePaused {
		t.Fatalf("%+v", prog)
	}
	_ = c.ReportCopy("x", 50, 100)
	prog, err = c.Resume(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if prog.Phase != migration.PhaseCompleted {
		t.Fatalf("%+v", prog)
	}
}

func TestMigrationOwnershipAndLockdown(t *testing.T) {
	id := resource.MustParseResourceID("database/dev/pay/orders")
	c := migration.New(migration.Config{
		Hooks: migration.Hooks{Ownership: ownGate{ok: false}, Fence: fenceGate{}, Lockdown: lockGate{block: false}},
	})
	if err := c.Define(migration.Definition{
		Name: "m", Source: "a", Target: "b", Owner: "op", SourceID: id, Fence: 1,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := c.Start(context.Background(), "m", migration.StartOptions{})
	if !errors.Is(err, migration.ErrOwnership) {
		t.Fatalf("got %v", err)
	}

	c.SetHooks(migration.Hooks{Ownership: ownGate{ok: true}, Lockdown: lockGate{block: true}})
	_, err = c.Start(context.Background(), "m", migration.StartOptions{})
	if !errors.Is(err, migration.ErrLockdown) {
		t.Fatalf("got %v", err)
	}
}
