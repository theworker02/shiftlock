// Example maintenance-mode enters and exits a durable maintenance window.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/theworker02/shiftlock/control/maintenance"
)

func main() {
	m, err := maintenance.New(maintenance.Config{})
	if err != nil {
		fail(err)
	}
	st, err := m.Enter(maintenance.EnterRequest{
		ID: "mnt_demo", Reason: "rolling restart", Duration: 5 * time.Minute,
		ActorID: "operator", Scope: maintenance.Scope{Claims: []string{"worker"}},
	})
	if err != nil {
		fail(err)
	}
	fmt.Printf("maintenance active id=%s expires=%s\n", st.ID, st.ExpiresAt.Format(time.RFC3339))
	if _, err := m.Exit("operator"); err != nil {
		fail(err)
	}
	fmt.Println("maintenance exited")
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
