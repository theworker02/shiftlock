// Package health provides an extended health status graph for Runtime.
package health

import (
	"time"
)

// Status is an extended health signal.
type Status string

const (
	Healthy     Status = "healthy"
	Degraded    Status = "degraded"
	Unhealthy   Status = "unhealthy"
	Blocked     Status = "blocked"
	Unknown     Status = "unknown"
	Quarantined Status = "quarantined"
	LockedDown  Status = "locked-down"
)

// Node is one node in the health graph.
type Node struct {
	Name    string            `json:"name"`
	Status  Status            `json:"status"`
	Message string            `json:"message,omitempty"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// Report is a graph-oriented health snapshot.
type Report struct {
	Time     time.Time `json:"time"`
	Overall  Status    `json:"overall"`
	Nodes    []Node    `json:"nodes"`
	Edges    []Edge    `json:"edges,omitempty"`
}

// Edge relates nodes (dependency).
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Builder assembles a Report.
type Builder struct {
	nodes map[string]Node
	edges []Edge
}

// NewBuilder creates a Builder.
func NewBuilder() *Builder {
	return &Builder{nodes: make(map[string]Node)}
}

// Set adds or replaces a node.
func (b *Builder) Set(n Node) {
	b.nodes[n.Name] = n
}

// Link adds a dependency edge.
func (b *Builder) Link(from, to string) {
	b.edges = append(b.edges, Edge{From: from, To: to})
}

// Build computes overall status (worst-wins with lockdown/quarantine priority).
func (b *Builder) Build(now time.Time) Report {
	rep := Report{Time: now, Edges: append([]Edge(nil), b.edges...)}
	worst := Healthy
	for _, n := range b.nodes {
		rep.Nodes = append(rep.Nodes, n)
		worst = worse(worst, n.Status)
	}
	if len(b.nodes) == 0 {
		worst = Unknown
	}
	rep.Overall = worst
	return rep
}

func worse(a, b Status) Status {
	return Status(maxRank(a, b))
}

func rank(s Status) int {
	switch s {
	case Healthy:
		return 0
	case Degraded:
		return 1
	case Unknown:
		return 2
	case Blocked:
		return 3
	case Unhealthy:
		return 4
	case Quarantined:
		return 5
	case LockedDown:
		return 6
	default:
		return 2
	}
}

func maxRank(a, b Status) Status {
	if rank(a) >= rank(b) {
		return a
	}
	return b
}
