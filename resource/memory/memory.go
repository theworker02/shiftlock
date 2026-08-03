// Package memory provides in-process resource adapters for tests and local-first demos.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

// Base is a simple Resource implementation.
type Base struct {
	id   resource.ResourceID
	desc resource.Description
	caps resource.ResourceCapabilities
	mu   sync.Mutex
	health resource.ResourceHealth
	epoch  resource.ResourceEpoch
}

// New constructs a Base resource. Capabilities default to SupportsHealth only.
func New(id resource.ResourceID, desc resource.Description, caps resource.ResourceCapabilities) *Base {
	if !caps.SupportsHealth {
		// Honest default: in-process adapters always support health probes.
		caps.SupportsHealth = true
	}
	return &Base{
		id: id, desc: desc, caps: caps,
		health: resource.HealthyReport("memory adapter"),
	}
}

func (b *Base) ID() resource.ResourceID             { return b.id }
func (b *Base) Kind() resource.Kind                 { return b.id.Kind }
func (b *Base) Describe() resource.Description      { return b.desc }
func (b *Base) Capabilities() resource.ResourceCapabilities { return b.caps }

func (b *Base) Health(ctx context.Context) resource.ResourceHealth {
	_ = ctx
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.health
	h.CheckedAt = time.Now().UTC()
	return h
}

// SetHealth overrides health for tests.
func (b *Base) SetHealth(h resource.ResourceHealth) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.health = h
}

// Epoch returns the in-memory epoch.
func (b *Base) Epoch() resource.ResourceEpoch {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.epoch
}

// AdvanceEpoch advances local epoch with reason.
func (b *Base) AdvanceEpoch(reason string) (resource.EpochAdvance, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	next, adv, err := resource.AdvanceEpoch(b.epoch, reason)
	if err != nil {
		return resource.EpochAdvance{}, err
	}
	b.epoch = next
	return adv, nil
}

// Mutate implements MutableResource when SupportsOwnership or fencing is claimed;
// otherwise returns ErrCapabilityClaimed.
func (b *Base) Mutate(ctx context.Context, op string, attrs map[string]string) (resource.Evidence, error) {
	_ = ctx
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.caps.SupportsOwnership && !b.caps.SupportsFencing {
		return resource.Evidence{}, &resource.Error{Op: "Mutate", ID: b.id, Err: resource.ErrCapabilityClaimed, Message: "mutation not supported"}
	}
	ev := resource.Evidence{
		Time: time.Now().UTC(), Event: op, Resource: b.id.String(),
		Summary: "memory mutate", Attrs: attrs,
	}
	return resource.SanitizeEvidence(ev)
}

// Worker is a KindWorker stub.
func Worker(env, service, name string) *Base {
	id := resource.ResourceID{Kind: resource.KindWorker, Environment: env, Service: service, Name: name}
	return New(id, resource.Description{DisplayName: name, Summary: "in-process worker"}, resource.ResourceCapabilities{
		SupportsHealth: true, SupportsDrain: true, SupportsOwnership: true, SupportsFencing: true,
	})
}

// Feature is a KindFeature stub.
func Feature(env, service, name string) *Base {
	id := resource.ResourceID{Kind: resource.KindFeature, Environment: env, Service: service, Name: name}
	return New(id, resource.Description{DisplayName: name, Summary: "feature flag reference"}, resource.ResourceCapabilities{
		SupportsHealth: true,
	})
}

// RateLimit is a KindRateLimit stub.
func RateLimit(env, service, name string) *Base {
	id := resource.ResourceID{Kind: resource.KindRateLimit, Environment: env, Service: service, Name: name}
	return New(id, resource.Description{DisplayName: name, Summary: "rate limit"}, resource.ResourceCapabilities{
		SupportsHealth: true, SupportsRateLimit: true,
	})
}
