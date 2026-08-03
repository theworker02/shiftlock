package maintenance_test

import (
	"testing"
	"time"

	"github.com/theworker02/shiftlock/control/maintenance"
)

func TestAutoExpire(t *testing.T) {
	now := time.Now()
	m, err := maintenance.New(maintenance.Config{
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Enter(maintenance.EnterRequest{Reason: "x", Duration: time.Second, ActorID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Active() {
		t.Fatal("expected active")
	}
	now = now.Add(2 * time.Second)
	if m.Active() {
		t.Fatal("expected expired")
	}
}
