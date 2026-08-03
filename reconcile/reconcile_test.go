package reconcile

import (
	"context"
	"errors"
	"testing"

	"github.com/theworker02/shiftlock/resource"
)

type lockdown struct{ block bool }

func (l lockdown) BlocksMutations() bool { return l.block }

func TestReconcileBackoffAndLockdown(t *testing.T) {
	reg := NewRegistry()
	var calls int
	errBoom := errors.New("boom")
	if err := reg.Register(Reconciler{
		Name:     "queue-pause-policy",
		Resource: resource.MustParseResourceID("queue/prod/pay/billing-events"),
		Reconcile: func(ctx context.Context, desired DesiredState, actual ActualState) error {
			calls++
			if calls < 3 {
				return errBoom
			}
			return nil
		},
		MaxAttempts:    5,
		InitialBackoff: 1,
		MaxBackoff:     1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Run(context.Background(), "queue-pause-policy", DesiredState{Version: 1}, ActualState{}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls=%d", calls)
	}

	reg.SetLockdown(lockdown{block: true})
	if err := reg.Run(context.Background(), "queue-pause-policy", DesiredState{}, ActualState{}); !errors.Is(err, ErrLockdown) {
		t.Fatalf("%v", err)
	}
	reg.SetLockdown(lockdown{block: false})
	reg.Pause()
	if err := reg.Run(context.Background(), "queue-pause-policy", DesiredState{}, ActualState{}); !errors.Is(err, ErrPaused) {
		t.Fatalf("%v", err)
	}
}
