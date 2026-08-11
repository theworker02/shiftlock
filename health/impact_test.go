package health

import (
	"reflect"
	"testing"
	"time"
)

func sampleReport() Report {
	builder := NewBuilder()
	builder.Set(Node{Name: "api", Status: Healthy})
	builder.Set(Node{Name: "database", Status: Unhealthy})
	builder.Set(Node{Name: "metrics", Status: Healthy})
	builder.Set(Node{Name: "worker", Status: Degraded})
	builder.Link("worker", "database")
	builder.Link("api", "worker")
	builder.Link("metrics", "database")
	return builder.Build(testTime())
}

func testTime() time.Time {
	return time.Unix(42, 0)
}

func TestPlanImpactBlastRadius(t *testing.T) {
	plan := sampleReport().PlanImpact("database")
	want := []string{"api", "metrics", "worker"}
	if !reflect.DeepEqual(plan.BlastRadius, want) {
		t.Fatalf("BlastRadius = %#v, want %#v", plan.BlastRadius, want)
	}
	if plan.Failed != "database" {
		t.Fatalf("Failed = %q", plan.Failed)
	}
	if plan.Empty() {
		t.Fatal("expected non-empty plan")
	}
}

func TestPlanImpactActionsAreDeterministic(t *testing.T) {
	report := sampleReport()
	first := report.PlanImpact("database")
	second := report.PlanImpact("database")
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("plans differ:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if len(first.Actions) == 0 {
		t.Fatal("expected actions")
	}
	if first.Actions[0].Node != "database" || first.Actions[0].Kind != ActionQuarantine {
		t.Fatalf("first action = %#v", first.Actions[0])
	}
}

func TestPlanImpactMissingNode(t *testing.T) {
	plan := sampleReport().PlanImpact("missing")
	if len(plan.BlastRadius) != 0 {
		t.Fatalf("BlastRadius = %#v", plan.BlastRadius)
	}
	found := false
	for _, issue := range plan.Issues {
		if issue.Code == "missing-node" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing-node issue not found in %#v", plan.Issues)
	}
}

func TestPlanImpactEmptyFailed(t *testing.T) {
	plan := sampleReport().PlanImpact("")
	if plan.Failed != "" || len(plan.BlastRadius) != 0 || len(plan.Actions) != 0 {
		t.Fatalf("expected no failure context, got %#v", plan)
	}
	found := false
	for _, issue := range plan.Issues {
		if issue.Code == "empty-failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected empty-failed issue in %#v", plan.Issues)
	}
}

func TestImpactPlanEmpty(t *testing.T) {
	var plan ImpactPlan
	if !plan.Empty() {
		t.Fatal("zero plan should be empty")
	}
}

func TestPlanImpactUpstreamObserve(t *testing.T) {
	report := Report{
		Nodes: []Node{{Name: "cache", Status: Healthy}, {Name: "api", Status: Unhealthy}},
		Edges: []Edge{{From: "api", To: "cache"}},
	}
	plan := report.PlanImpact("api")
	found := false
	for _, action := range plan.Actions {
		if action.Node == "cache" && action.Kind == ActionObserve {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected upstream observe action in %#v", plan.Actions)
	}
}
