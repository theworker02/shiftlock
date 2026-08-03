package health_test

import (
	"testing"
	"time"

	"github.com/theworker02/shiftlock/health"
)

func TestOverallWorstWins(t *testing.T) {
	b := health.NewBuilder()
	b.Set(health.Node{Name: "a", Status: health.Healthy})
	b.Set(health.Node{Name: "b", Status: health.LockedDown})
	rep := b.Build(time.Now())
	if rep.Overall != health.LockedDown {
		t.Fatalf("got %s", rep.Overall)
	}
}
