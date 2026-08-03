package resource

import (
	"context"
	"time"
)

// Description is static metadata returned by Resource.Describe.
type Description struct {
	DisplayName string            `json:"display_name,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Owner       string            `json:"owner,omitempty"`
}

// Resource is the fabric adapter contract.
type Resource interface {
	ID() ResourceID
	Kind() Kind
	Describe() Description
	Health(ctx context.Context) ResourceHealth
	Capabilities() ResourceCapabilities
}

// MutableResource is an optional extension for adapters that accept mutations.
// Workflow mutation steps and registry ops consult lockdown before calling Mutate.
type MutableResource interface {
	Resource
	// Mutate performs a named mutation. Implementations must refuse when
	// fencing/epoch checks fail. Returns evidence of the attempt.
	Mutate(ctx context.Context, op string, attrs map[string]string) (Evidence, error)
}

// Entry is a registry record wrapping a Resource with metadata and epoch.
type Entry struct {
	Resource Resource
	Meta     Metadata
	Epoch    ResourceEpoch
	RegisteredAt time.Time
}

// Metadata is operator-facing registry metadata (not secrets).
type Metadata struct {
	Labels   map[string]string `json:"labels,omitempty"`
	Tags     []string          `json:"tags,omitempty"`
	Notes    string            `json:"notes,omitempty"`
	Source   string            `json:"source,omitempty"`
}

// Clone returns a deep-ish copy of metadata maps/slices.
func (m Metadata) Clone() Metadata {
	out := Metadata{Notes: m.Notes, Source: m.Source}
	if m.Labels != nil {
		out.Labels = make(map[string]string, len(m.Labels))
		for k, v := range m.Labels {
			out.Labels[k] = v
		}
	}
	if m.Tags != nil {
		out.Tags = append([]string(nil), m.Tags...)
	}
	return out
}
