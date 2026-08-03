package shiftlock

import (
	"context"
	"sync/atomic"
	"time"
)

// EventType classifies coordinator lifecycle and ownership events.
type EventType string

const (
	EventGenerationRegistered EventType = "generation.registered"
	EventGenerationState      EventType = "generation.state"
	EventClaimAcquired        EventType = "claim.acquired"
	EventClaimRenewed         EventType = "claim.renewed"
	EventClaimLost            EventType = "claim.lost"
	EventClaimReleased        EventType = "claim.released"
	EventClaimExpired         EventType = "claim.expired"
	EventDrainStarted         EventType = "drain.started"
	EventDrainCompleted       EventType = "drain.completed"
	EventDrainFailed          EventType = "drain.failed"
	EventTransferPrepared     EventType = "transfer.prepared"
	EventTransferCommitted    EventType = "transfer.committed"
	EventTransferAborted      EventType = "transfer.aborted"
	EventHandoffStarted       EventType = "handoff.started"
	EventHandoffCompleted     EventType = "handoff.completed"
	EventHandoffFailed        EventType = "handoff.failed"
	EventReadinessStarted     EventType = "readiness.started"
	EventReadinessPassed      EventType = "readiness.passed"
	EventReadinessFailed      EventType = "readiness.failed"
	EventLeaseRevoked         EventType = "lease.revoked"
	EventBackendHeartbeat     EventType = "backend.heartbeat"
	EventError                EventType = "error"
	EventClosed               EventType = "closed"
)

// Event is a structured coordinator notification.
type Event struct {
	Type       EventType        `json:"type"`
	Time       time.Time        `json:"time"`
	Service    string           `json:"service"`
	Generation string           `json:"generation,omitempty"`
	InstanceID string           `json:"instance_id,omitempty"`
	Claim      string           `json:"claim,omitempty"`
	Token      FencingToken     `json:"fencing_token,omitempty"`
	FromState  GenerationState  `json:"from_state,omitempty"`
	ToState    GenerationState  `json:"to_state,omitempty"`
	Reason     TransitionReason `json:"reason,omitempty"`
	Err        string           `json:"error,omitempty"`
	Attrs      map[string]string `json:"attrs,omitempty"`
}

// Hook is a synchronous callback invoked inline (must not block long).
type Hook func(Event)

// Observer receives events asynchronously.
type Observer interface {
	OnEvent(Event)
}

// ObserverFunc adapts a function to Observer.
type ObserverFunc func(Event)

func (f ObserverFunc) OnEvent(e Event) { f(e) }

// EventFilter decides whether an event is delivered.
type EventFilter func(Event) bool

type eventBus struct {
	hooks     []Hook
	observers []Observer
	filter    EventFilter
	ch        chan Event
	dropped   atomic.Uint64
	clock     Clock
	service   string
	genID     string
	instance  string
}

func newEventBus(cfg Config) *eventBus {
	return &eventBus{
		hooks:     append([]Hook(nil), cfg.Hooks...),
		observers: append([]Observer(nil), cfg.Observers...),
		ch:        make(chan Event, cfg.EventBuffer),
		clock:     cfg.Clock,
		service:   cfg.Service,
		genID:     cfg.GenerationID,
		instance:  cfg.InstanceID,
	}
}

func (b *eventBus) SetFilter(f EventFilter) { b.filter = f }

func (b *eventBus) Dropped() uint64 { return b.dropped.Load() }

func (b *eventBus) emit(e Event) {
	if e.Time.IsZero() {
		e.Time = b.clock.Now()
	}
	if e.Service == "" {
		e.Service = b.service
	}
	if e.Generation == "" {
		e.Generation = b.genID
	}
	if e.InstanceID == "" {
		e.InstanceID = b.instance
	}
	if b.filter != nil && !b.filter(e) {
		return
	}
	for _, h := range b.hooks {
		func(hook Hook) {
			defer func() { _ = recover() }()
			hook(e)
		}(h)
	}
	select {
	case b.ch <- e:
	default:
		b.dropped.Add(1)
	}
}

func (b *eventBus) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			// drain remaining without blocking forever
			for {
				select {
				case e := <-b.ch:
					b.dispatch(e)
				default:
					return
				}
			}
		case e := <-b.ch:
			b.dispatch(e)
		}
	}
}

func (b *eventBus) dispatch(e Event) {
	for _, o := range b.observers {
		func(obs Observer) {
			defer func() { _ = recover() }()
			obs.OnEvent(e)
		}(o)
	}
}
