package otel

import (
	"context"
	"sync/atomic"

	"github.com/theworker02/shiftlock"
)

// Tracer is a minimal span interface so this package has no hard OpenTelemetry dependency.
type Tracer interface {
	Start(ctx context.Context, name string, attrs map[string]string) (context.Context, Span)
}

// Span ends an observation.
type Span interface {
	End()
	RecordError(err string)
}

// Observer records high-level ShiftLock events. When Tracer is nil, events are counted only.
type Observer struct {
	Tracer Tracer
	spans  atomic.Uint64
	events atomic.Uint64
}

// OnEvent implements shiftlock.Observer.
func (o *Observer) OnEvent(e shiftlock.Event) {
	o.events.Add(1)
	if o.Tracer == nil {
		return
	}
	attrs := map[string]string{
		"shiftlock.type":       string(e.Type),
		"shiftlock.service":    e.Service,
		"shiftlock.generation": e.Generation,
		"shiftlock.claim":      e.Claim,
		"shiftlock.reason":     string(e.Reason),
	}
	_, span := o.Tracer.Start(context.Background(), "shiftlock."+string(e.Type), attrs)
	o.spans.Add(1)
	if e.Err != "" {
		span.RecordError(e.Err)
	}
	span.End()
}

// Events returns total observed events.
func (o *Observer) Events() uint64 { return o.events.Load() }

var _ shiftlock.Observer = (*Observer)(nil)
