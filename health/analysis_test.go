package health

import (
	"reflect"
	"testing"
	"time"
)

func TestBuildIsDeterministic(t *testing.T) {
	builder := NewBuilder()
	builder.Set(Node{Name: "worker", Status: Healthy})
	builder.Set(Node{Name: "database", Status: Degraded})
	builder.Link("worker", "database")
	builder.Link("api", "worker")
	builder.Set(Node{Name: "api", Status: Healthy})

	report := builder.Build(time.Unix(10, 0))
	if got := []string{report.Nodes[0].Name, report.Nodes[1].Name, report.Nodes[2].Name}; !reflect.DeepEqual(got, []string{"api", "database", "worker"}) {
		t.Fatalf("node order = %#v", got)
	}
	if report.Overall != Degraded {
		t.Fatalf("overall = %q, want %q", report.Overall, Degraded)
	}
}

func TestDependents(t *testing.T) {
	report := Report{Edges: []Edge{
		{From: "worker", To: "database"},
		{From: "api", To: "worker"},
		{From: "metrics", To: "database"},
	}}
	want := []string{"api", "metrics", "worker"}
	if got := report.Dependents("database"); !reflect.DeepEqual(got, want) {
		t.Fatalf("Dependents() = %#v, want %#v", got, want)
	}
}

func TestValidate(t *testing.T) {
	report := Report{
		Nodes: []Node{{Name: "api"}, {Name: "database"}, {Name: "api"}},
		Edges: []Edge{
			{From: "api", To: "database"},
			{From: "database", To: "api"},
			{From: "missing", To: "database"},
			{From: "api", To: "api"},
		},
	}
	issues := report.Validate()
	codes := make(map[string]bool)
	for _, issue := range issues {
		codes[issue.Code] = true
	}
	for _, code := range []string{"cycle", "duplicate-node", "missing-from", "self-edge"} {
		if !codes[code] {
			t.Fatalf("missing validation issue %q in %#v", code, issues)
		}
	}
}

func TestNode(t *testing.T) {
	report := Report{Nodes: []Node{{Name: "api", Status: Healthy}}}
	node, ok := report.Node("api")
	if !ok || node.Status != Healthy {
		t.Fatalf("Node() = %#v, %v", node, ok)
	}
	if _, ok := report.Node("missing"); ok {
		t.Fatal("missing node reported as present")
	}
}
