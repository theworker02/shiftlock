package health

import "sort"

// ValidationIssue describes a structural problem in a health report.
type ValidationIssue struct {
	Code    string `json:"code"`
	Node    string `json:"node,omitempty"`
	Message string `json:"message"`
}

// Validate checks node identity, dependency endpoints, self-dependencies, and cycles.
func (r Report) Validate() []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	nodes := make(map[string]struct{}, len(r.Nodes))
	for _, node := range r.Nodes {
		if node.Name == "" {
			issues = append(issues, ValidationIssue{Code: "empty-node", Message: "health node name is empty"})
			continue
		}
		if _, exists := nodes[node.Name]; exists {
			issues = append(issues, ValidationIssue{Code: "duplicate-node", Node: node.Name, Message: "health node is declared more than once"})
		}
		nodes[node.Name] = struct{}{}
	}

	graph := make(map[string][]string, len(nodes))
	for _, edge := range r.Edges {
		if edge.From == edge.To && edge.From != "" {
			issues = append(issues, ValidationIssue{Code: "self-edge", Node: edge.From, Message: "health node depends on itself"})
		}
		if _, exists := nodes[edge.From]; !exists {
			issues = append(issues, ValidationIssue{Code: "missing-from", Node: edge.From, Message: "dependency source is not present in the report"})
		}
		if _, exists := nodes[edge.To]; !exists {
			issues = append(issues, ValidationIssue{Code: "missing-to", Node: edge.To, Message: "dependency target is not present in the report"})
		}
		graph[edge.From] = append(graph[edge.From], edge.To)
	}

	state := make(map[string]uint8, len(nodes))
	var visit func(string) bool
	visit = func(node string) bool {
		if state[node] == 1 {
			return true
		}
		if state[node] == 2 {
			return false
		}
		state[node] = 1
		for _, dependency := range graph[node] {
			if visit(dependency) {
				return true
			}
		}
		state[node] = 2
		return false
	}
	for node := range nodes {
		if state[node] == 0 && visit(node) {
			issues = append(issues, ValidationIssue{Code: "cycle", Node: node, Message: "dependency graph contains a cycle"})
			break
		}
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Code != issues[j].Code {
			return issues[i].Code < issues[j].Code
		}
		if issues[i].Node != issues[j].Node {
			return issues[i].Node < issues[j].Node
		}
		return issues[i].Message < issues[j].Message
	})
	return issues
}

// Dependents returns every node transitively affected when name becomes unavailable.
// Because Edge.From depends on Edge.To, the traversal follows reverse edges.
func (r Report) Dependents(name string) []string {
	reverse := make(map[string][]string)
	for _, edge := range r.Edges {
		reverse[edge.To] = append(reverse[edge.To], edge.From)
	}
	seen := map[string]bool{name: true}
	queue := []string{name}
	result := make([]string, 0)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, dependent := range reverse[current] {
			if seen[dependent] {
				continue
			}
			seen[dependent] = true
			result = append(result, dependent)
			queue = append(queue, dependent)
		}
	}
	sort.Strings(result)
	return result
}

// Node returns a named health node and whether it exists.
func (r Report) Node(name string) (Node, bool) {
	for _, node := range r.Nodes {
		if node.Name == name {
			return node, true
		}
	}
	return Node{}, false
}
