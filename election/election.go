// Package election provides leader election built on claim + fencing semantics.
package election

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrClosed   = errors.New("election: closed")
	ErrNotLeader = errors.New("election: not leader")
	ErrDenied   = errors.New("election: denied")
)

// EventType classifies election events.
type EventType string

const (
	EventJoined    EventType = "joined"
	EventLeading   EventType = "leading"
	EventFollow   EventType = "following"
	EventResigned  EventType = "resigned"
	EventTransferred EventType = "transferred"
	EventLost      EventType = "lost"
)

// Event is emitted on leadership changes.
type Event struct {
	Type      EventType
	Election  string
	Leader    string
	Token     uint64
	Time      time.Time
	Err       string
}

// FencedLock is the claim/fencing adapter (implemented by Runtime).
type FencedLock interface {
	Acquire(ctx context.Context, claim string) (token uint64, err error)
	Release(ctx context.Context, claim string, token uint64) error
	Renew(ctx context.Context, claim string, token uint64) error
}

// Gate may deny join (quarantine / lockdown).
type Gate interface {
	AllowElection(name string) error
}

// Config configures an Election.
type Config struct {
	Name          string
	ParticipantID string
	Lock          FencedLock
	Gate          Gate
	RenewEvery    time.Duration
	EventBuffer   int // bounded; default 16
	Clock         func() time.Time
}

// Election is a single named leadership contest.
type Election struct {
	cfg    Config
	mu     sync.Mutex
	leader bool
	token  uint64
	closed bool
	events chan Event
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Join starts participating; may become leader when the claim is acquired.
func Join(parent context.Context, cfg Config) (*Election, error) {
	if cfg.Name == "" || cfg.ParticipantID == "" || cfg.Lock == nil {
		return nil, errors.New("election: name, participant, and lock required")
	}
	if cfg.Gate != nil {
		if err := cfg.Gate.AllowElection(cfg.Name); err != nil {
			return nil, err
		}
	}
	if cfg.RenewEvery <= 0 {
		cfg.RenewEvery = 5 * time.Second
	}
	if cfg.EventBuffer <= 0 {
		cfg.EventBuffer = 16
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	ctx, cancel := context.WithCancel(parent)
	e := &Election{
		cfg:    cfg,
		events: make(chan Event, cfg.EventBuffer),
		cancel: cancel,
	}
	e.emit(Event{Type: EventJoined, Election: cfg.Name, Time: cfg.Clock()})
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.loop(ctx)
	}()
	return e, nil
}

// Events returns a bounded receive-only channel (drops on overflow).
func (e *Election) Events() <-chan Event { return e.events }

// IsLeader reports local leadership belief (always confirm via token fencing for work).
func (e *Election) IsLeader() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.leader
}

// Token returns the fencing token when leading.
func (e *Election) Token() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.token
}

// Resign releases leadership if held.
func (e *Election) Resign(ctx context.Context) error {
	e.mu.Lock()
	if !e.leader {
		e.mu.Unlock()
		return ErrNotLeader
	}
	tok := e.token
	e.mu.Unlock()
	if err := e.cfg.Lock.Release(ctx, e.cfg.Name, tok); err != nil {
		return err
	}
	e.mu.Lock()
	e.leader = false
	e.token = 0
	e.mu.Unlock()
	e.emit(Event{Type: EventResigned, Election: e.cfg.Name, Time: e.cfg.Clock()})
	return nil
}

// Transfer resigns so another participant may acquire (cooperative).
func (e *Election) Transfer(ctx context.Context, _ string) error {
	if err := e.Resign(ctx); err != nil {
		return err
	}
	e.emit(Event{Type: EventTransferred, Election: e.cfg.Name, Time: e.cfg.Clock()})
	return nil
}

// Close stops the election loop and resigns if leading.
func (e *Election) Close(ctx context.Context) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	leading := e.leader
	tok := e.token
	e.mu.Unlock()
	e.cancel()
	e.wg.Wait()
	if leading {
		_ = e.cfg.Lock.Release(ctx, e.cfg.Name, tok)
	}
	close(e.events)
	return nil
}

func (e *Election) loop(ctx context.Context) {
	ticker := time.NewTicker(e.cfg.RenewEvery)
	defer ticker.Stop()
	for {
		e.mu.Lock()
		leading := e.leader
		tok := e.token
		e.mu.Unlock()

		if !leading {
			token, err := e.cfg.Lock.Acquire(ctx, e.cfg.Name)
			if err == nil {
				e.mu.Lock()
				e.leader = true
				e.token = token
				e.mu.Unlock()
				e.emit(Event{Type: EventLeading, Election: e.cfg.Name, Leader: e.cfg.ParticipantID, Token: token, Time: e.cfg.Clock()})
			} else {
				e.emit(Event{Type: EventFollow, Election: e.cfg.Name, Time: e.cfg.Clock(), Err: err.Error()})
			}
		} else {
			if err := e.cfg.Lock.Renew(ctx, e.cfg.Name, tok); err != nil {
				e.mu.Lock()
				e.leader = false
				e.token = 0
				e.mu.Unlock()
				e.emit(Event{Type: EventLost, Election: e.cfg.Name, Time: e.cfg.Clock(), Err: err.Error()})
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (e *Election) emit(ev Event) {
	select {
	case e.events <- ev:
	default:
		// bounded: drop
	}
}
