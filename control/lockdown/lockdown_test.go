package lockdown_test

import (
	"errors"
	"testing"

	"github.com/theworker02/shiftlock/control/lockdown"
)

func TestUnlockRequiresConfirmAndEvidenceRetained(t *testing.T) {
	m, err := lockdown.New(lockdown.Config{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := m.Enter(lockdown.EnterRequest{Mode: lockdown.ModeFullService, Reason: "x", ActorID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Unlock(lockdown.UnlockRequest{ExpectedID: st.ID, Confirm: false, StrongAuthID: "s"})
	if !errors.Is(err, lockdown.ErrConfirm) {
		t.Fatalf("got %v", err)
	}
	_, err = m.Unlock(lockdown.UnlockRequest{ExpectedID: st.ID, Confirm: true, StrongAuthID: "s", ActorID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Evidence()) < 2 {
		t.Fatalf("evidence=%d", len(m.Evidence()))
	}
}
