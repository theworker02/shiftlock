package guard_test

import (
	"testing"

	"github.com/theworker02/shiftlock/guard"
)

func TestRequireApprovalAndConstraints(t *testing.T) {
	e := guard.New()
	_ = e.AddRule(guard.Rule{
		Name: "approve-maint", Permission: "maintenance.enter", Decision: guard.RequireApproval, Priority: 5,
	})
	_ = e.AddRule(guard.Rule{
		Name: "constrained", Permission: "task.start", Decision: guard.AllowWithConstraints, Priority: 5,
		Constraints: &guard.Constraint{MaxTTLSeconds: 60},
	})
	ex := e.Explain(guard.Request{Permission: "maintenance.enter"})
	if ex.Decision != guard.RequireApproval {
		t.Fatalf("got %s", ex.Decision)
	}
	ex2 := e.Explain(guard.Request{Permission: "task.start"})
	if ex2.Decision != guard.AllowWithConstraints || ex2.Constraints == nil || ex2.Constraints.MaxTTLSeconds != 60 {
		t.Fatalf("%+v", ex2)
	}
}
