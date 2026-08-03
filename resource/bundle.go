package resource

import (
	"context"
	"fmt"
)

// BundleMode selects how a bundle evaluates activation readiness.
type BundleMode string

const (
	// BundleAllRequired — every member must be registered and healthy.
	BundleAllRequired BundleMode = "all-required"
	// BundleMinimumRequired — at least MinRequired members ready.
	BundleMinimumRequired BundleMode = "minimum-required"
	// BundleOptionalDependencies — listed members are optional; Ready if any/none ok.
	BundleOptionalDependencies BundleMode = "optional-dependencies"
	// BundlePrimaryWithFallbacks — Primary must be ready, or any Fallback.
	BundlePrimaryWithFallbacks BundleMode = "primary-with-fallbacks"
	// BundleOrdered — members must be ready in listed order (prefix readiness).
	BundleOrdered BundleMode = "ordered"
)

// Bundle groups resource IDs for activation checks.
type Bundle struct {
	Name         string       `json:"name"`
	Mode         BundleMode   `json:"mode"`
	IDs          []ResourceID `json:"ids"`
	MinRequired  int          `json:"min_required,omitempty"`
	Primary      ResourceID   `json:"primary,omitempty"`
	Fallbacks    []ResourceID `json:"fallbacks,omitempty"`
}

// Bundle constructs a named bundle with the given IDs (default all-required).
func NewBundle(name string, ids ...ResourceID) Bundle {
	cp := append([]ResourceID(nil), ids...)
	return Bundle{Name: name, Mode: BundleAllRequired, IDs: cp}
}

// WithMode sets the evaluation mode.
func (b Bundle) WithMode(m BundleMode) Bundle {
	b.Mode = m
	return b
}

// WithMinRequired sets minimum for BundleMinimumRequired.
func (b Bundle) WithMinRequired(n int) Bundle {
	b.MinRequired = n
	return b
}

// WithPrimaryFallbacks configures primary-with-fallbacks mode.
func (b Bundle) WithPrimaryFallbacks(primary ResourceID, fallbacks ...ResourceID) Bundle {
	b.Mode = BundlePrimaryWithFallbacks
	b.Primary = primary
	b.Fallbacks = append([]ResourceID(nil), fallbacks...)
	return b
}

// Readiness is the activation report for a bundle.
type Readiness struct {
	Name       string   `json:"name"`
	Ready      bool     `json:"ready"`
	Mode       BundleMode `json:"mode"`
	ReadyIDs   []string `json:"ready_ids,omitempty"`
	MissingIDs []string `json:"missing_ids,omitempty"`
	BlockedIDs []string `json:"blocked_ids,omitempty"`
	Message    string   `json:"message,omitempty"`
}

// EvaluateReadiness checks the bundle against the registry.
func (b Bundle) EvaluateReadiness(ctx context.Context, reg *Registry) Readiness {
	rep := Readiness{Name: b.Name, Mode: b.Mode}
	if reg == nil {
		rep.Message = "nil registry"
		return rep
	}
	switch b.Mode {
	case "", BundleAllRequired:
		return b.evalAll(ctx, reg, rep)
	case BundleMinimumRequired:
		return b.evalMin(ctx, reg, rep)
	case BundleOptionalDependencies:
		return b.evalOptional(ctx, reg, rep)
	case BundlePrimaryWithFallbacks:
		return b.evalPrimary(ctx, reg, rep)
	case BundleOrdered:
		return b.evalOrdered(ctx, reg, rep)
	default:
		rep.Message = fmt.Sprintf("unknown mode %q", b.Mode)
		return rep
	}
}

func (b Bundle) classify(ctx context.Context, reg *Registry, id ResourceID, rep *Readiness) bool {
	ent, err := reg.Get(id)
	if err != nil {
		rep.MissingIDs = append(rep.MissingIDs, id.String())
		return false
	}
	h := ent.Resource.Health(ctx)
	if h.Overall == HealthUnhealthy || h.Overall == HealthBlocked {
		rep.BlockedIDs = append(rep.BlockedIDs, id.String())
		return false
	}
	rep.ReadyIDs = append(rep.ReadyIDs, id.String())
	return true
}

func (b Bundle) evalAll(ctx context.Context, reg *Registry, rep Readiness) Readiness {
	ok := true
	for _, id := range b.IDs {
		if !b.classify(ctx, reg, id, &rep) {
			ok = false
		}
	}
	rep.Ready = ok && len(b.IDs) > 0
	if len(b.IDs) == 0 {
		rep.Message = "empty bundle"
	} else if !ok {
		rep.Message = "not all members ready"
	}
	return rep
}

func (b Bundle) evalMin(ctx context.Context, reg *Registry, rep Readiness) Readiness {
	min := b.MinRequired
	if min <= 0 {
		min = 1
	}
	for _, id := range b.IDs {
		b.classify(ctx, reg, id, &rep)
	}
	rep.Ready = len(rep.ReadyIDs) >= min
	if !rep.Ready {
		rep.Message = fmt.Sprintf("need %d ready, have %d", min, len(rep.ReadyIDs))
	}
	return rep
}

func (b Bundle) evalOptional(ctx context.Context, reg *Registry, rep Readiness) Readiness {
	for _, id := range b.IDs {
		b.classify(ctx, reg, id, &rep)
	}
	// Optional: always activation-ready; report which deps are present.
	rep.Ready = true
	rep.Message = "optional dependencies evaluated"
	return rep
}

func (b Bundle) evalPrimary(ctx context.Context, reg *Registry, rep Readiness) Readiness {
	ids := make([]ResourceID, 0, 1+len(b.Fallbacks))
	if !b.Primary.IsZero() {
		ids = append(ids, b.Primary)
	}
	ids = append(ids, b.Fallbacks...)
	if len(ids) == 0 {
		ids = b.IDs
	}
	for _, id := range ids {
		if b.classify(ctx, reg, id, &rep) {
			rep.Ready = true
			return rep
		}
	}
	rep.Message = "primary and fallbacks unavailable"
	return rep
}

func (b Bundle) evalOrdered(ctx context.Context, reg *Registry, rep Readiness) Readiness {
	for _, id := range b.IDs {
		if !b.classify(ctx, reg, id, &rep) {
			rep.Ready = false
			rep.Message = "ordered activation stopped at " + id.String()
			return rep
		}
	}
	rep.Ready = len(b.IDs) > 0
	if len(b.IDs) == 0 {
		rep.Message = "empty ordered bundle"
	}
	return rep
}
