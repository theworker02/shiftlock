// Example emergency-lockdown demonstrates fail-closed lockdown enter/unlock rules.
package main

import (
	"fmt"
	"os"

	"github.com/theworker02/shiftlock/control/lockdown"
)

func main() {
	m, err := lockdown.New(lockdown.Config{})
	if err != nil {
		fail(err)
	}
	st, err := m.Enter(lockdown.EnterRequest{
		ID:      "ld_demo",
		Mode:    lockdown.ModeFailClosed,
		Reason:  "suspected credential compromise",
		ActorID: "operator",
	})
	if err != nil {
		fail(err)
	}
	fmt.Printf("lockdown active id=%s mode=%s\n", st.ID, st.Mode)

	_, err = m.Unlock(lockdown.UnlockRequest{
		ExpectedID: st.ID, Confirm: false, ActorID: "operator", StrongAuthID: "cap_strong",
	})
	if err != nil {
		fmt.Printf("unlock without confirm denied: %v\n", err)
	}

	st2, err := m.Unlock(lockdown.UnlockRequest{
		ExpectedID: st.ID, Confirm: true, ActorID: "operator", StrongAuthID: "cap_strong",
	})
	if err != nil {
		fail(err)
	}
	fmt.Printf("unlocked id=%s evidence_retained=true active=%v\n", st2.ID, m.Active())
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
