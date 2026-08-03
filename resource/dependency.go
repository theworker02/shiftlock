package resource

import (
	"context"
	"sort"
)

// DependencyGraph stores directed edges from → to meaning "from depends on to".
type DependencyGraph struct {
	// edges[from] = set of tos
	edges map[string]map[string]struct{}
}

// NewDependencyGraph creates an empty graph.
func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{edges: make(map[string]map[string]struct{})}
}

// Define adds an edge; detects cycles involving the new edge.
func (g *DependencyGraph) Define(from, to string) error {
	if from == "" || to == "" {
		return &Error{Op: "DefineDependency", Err: ErrInvalidArgument, Message: "empty dependency endpoint"}
	}
	if from == to {
		return &Error{Op: "DefineDependency", Err: ErrCycle, Message: "self-dependency"}
	}
	if g.edges[from] == nil {
		g.edges[from] = make(map[string]struct{})
	}
	g.edges[from][to] = struct{}{}
	if g.hasCycle() {
		delete(g.edges[from], to)
		if len(g.edges[from]) == 0 {
			delete(g.edges, from)
		}
		return &Error{Op: "DefineDependency", Err: ErrCycle, Message: "dependency would create a cycle"}
	}
	return nil
}

func (g *DependencyGraph) removeNode(key string) {
	delete(g.edges, key)
	for from, tos := range g.edges {
		delete(tos, key)
		if len(tos) == 0 {
			delete(g.edges, from)
		}
	}
}

// Clone returns a deep copy.
func (g *DependencyGraph) Clone() *DependencyGraph {
	out := NewDependencyGraph()
	for from, tos := range g.edges {
		out.edges[from] = make(map[string]struct{}, len(tos))
		for to := range tos {
			out.edges[from][to] = struct{}{}
		}
	}
	return out
}

// DependenciesOf returns sorted dependency keys for from.
func (g *DependencyGraph) DependenciesOf(from string) []string {
	tos := g.edges[from]
	out := make([]string, 0, len(tos))
	for to := range tos {
		out = append(out, to)
	}
	sort.Strings(out)
	return out
}

// StartupOrder returns a deterministic topological order over nodes.
// Dependencies appear before dependents. Nodes with no edges are included
// from known (sorted lexicographically for stability).
func (g *DependencyGraph) StartupOrder(known []string) ([]string, error) {
	nodes := make(map[string]struct{})
	for _, k := range known {
		nodes[k] = struct{}{}
	}
	for from, tos := range g.edges {
		nodes[from] = struct{}{}
		for to := range tos {
			nodes[to] = struct{}{}
		}
	}
	// Kahn: edge from→to means from depends on to, so to must come first.
	// indegree[from] counts unmet dependencies.
	indeg := make(map[string]int, len(nodes))
	for n := range nodes {
		indeg[n] = 0
	}
	for from, tos := range g.edges {
		for range tos {
			indeg[from]++
		}
	}
	ready := make([]string, 0)
	for n, d := range indeg {
		if d == 0 {
			ready = append(ready, n)
		}
	}
	sort.Strings(ready)
	order := make([]string, 0, len(nodes))
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		// n satisfied: reduce indegree of nodes that depend on n
		dependents := make([]string, 0)
		for from, tos := range g.edges {
			if _, ok := tos[n]; ok {
				dependents = append(dependents, from)
			}
		}
		sort.Strings(dependents)
		for _, from := range dependents {
			indeg[from]--
			if indeg[from] == 0 {
				ready = append(ready, from)
			}
		}
		sort.Strings(ready)
	}
	if len(order) != len(nodes) {
		return nil, &Error{Op: "StartupOrder", Err: ErrCycle, Message: "dependency cycle detected"}
	}
	return order, nil
}

func (g *DependencyGraph) hasCycle() bool {
	nodes := make([]string, 0)
	seen := map[string]struct{}{}
	for from, tos := range g.edges {
		if _, ok := seen[from]; !ok {
			seen[from] = struct{}{}
			nodes = append(nodes, from)
		}
		for to := range tos {
			if _, ok := seen[to]; !ok {
				seen[to] = struct{}{}
				nodes = append(nodes, to)
			}
		}
	}
	_, err := g.StartupOrder(nodes)
	return err != nil
}

// MissingDependencies returns dependency keys that are absent or unhealthy.
func (g *DependencyGraph) MissingDependencies(from string, entries map[string]*Entry) []string {
	var missing []string
	for _, to := range g.DependenciesOf(from) {
		ent, ok := entries[to]
		if !ok || ent == nil || ent.Resource == nil {
			missing = append(missing, to)
			continue
		}
		// Health check without context: adapters should be cheap for this path.
		h := ent.Resource.Health(context.Background())
		if h.Overall == HealthUnhealthy || h.Overall == HealthBlocked {
			missing = append(missing, to)
		}
	}
	return missing
}
