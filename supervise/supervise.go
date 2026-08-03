// Package supervise runs ownership-aware tasks with bounded restart policies.
package supervise

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrClosed       = errors.New("supervise: closed")
	ErrExists       = errors.New("supervise: task exists")
	ErrNotFound     = errors.New("supervise: task not found")
	ErrMode         = errors.New("supervise: mode rejected")
	ErrRestartBound  = errors.New("supervise: restart bound exceeded")
	ErrQuarantined  = errors.New("supervise: quarantined")
)

// Mode selects task scheduling semantics.
type Mode string

const (
	ModeSingleton       Mode = "singleton"
	ModePerInstance     Mode = "per-instance"
	ModeLeaderOnly      Mode = "leader-only"
	ModeClaimBound      Mode = "claim-bound"
	ModeScheduled       Mode = "scheduled"
	ModeOneShot         Mode = "one-shot"
	ModeMaintenanceOnly Mode = "maintenance-only"
	ModeManual          Mode = "manual"
)

// RestartPolicy bounds automatic restarts. Default is NOT infinite.
type RestartPolicy struct {
	// MaxRestarts is the maximum restart attempts after failure (default 3).
	MaxRestarts int
	// Interval is the minimum delay between restarts (default 1s).
	Interval time.Duration
	// MaxInterval caps backoff (default 30s).
	MaxInterval time.Duration
}

func (p RestartPolicy) withDefaults() RestartPolicy {
	if p.MaxRestarts <= 0 {
		p.MaxRestarts = 3
	}
	if p.Interval <= 0 {
		p.Interval = time.Second
	}
	if p.MaxInterval <= 0 {
		p.MaxInterval = 30 * time.Second
	}
	return p
}

// FailurePolicy controls failure handling.
type FailurePolicy string

const (
	FailStop     FailurePolicy = "stop"
	FailRestart  FailurePolicy = "restart"
	FailIsolate  FailurePolicy = "isolate-zone"
)

// TaskFunc is the unit of work. ctx is canceled on ownership loss / stop / close.
type TaskFunc func(ctx context.Context) error

// Spec describes a supervised task.
type Spec struct {
	Name          string
	Mode          Mode
	Run           TaskFunc
	Restart       RestartPolicy
	Failure       FailurePolicy
	Zone          string
	ClaimName     string // for claim-bound / leader-only
	ScheduleEvery time.Duration
}

// Gate is consulted before start (maintenance / lockdown / quarantine / leader).
type Gate interface {
	AllowTask(spec Spec) error
}

// OwnershipCancel is canceled when claim ownership is lost (optional).
type OwnershipCancel interface {
	Done() <-chan struct{}
}

// Supervisor manages structured task concurrency.
type Supervisor struct {
	mu     sync.Mutex
	tasks  map[string]*task
	zones  map[string]*zone
	gate   Gate
	closed bool
	wg     sync.WaitGroup
	root   context.Context
	cancel context.CancelFunc
}

type zone struct {
	isolated bool
}

type task struct {
	spec   Spec
	cancel context.CancelFunc
	runs   int
}

// New creates a Supervisor.
func New(parent context.Context, gate Gate) *Supervisor {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &Supervisor{
		tasks:  make(map[string]*task),
		zones:  make(map[string]*zone),
		gate:   gate,
		root:   ctx,
		cancel: cancel,
	}
}

// Register validates and stores a task without starting it (manual mode default start).
func (s *Supervisor) Register(spec Spec) error {
	if spec.Name == "" || spec.Run == nil {
		return fmt.Errorf("%w: name and run required", ErrMode)
	}
	if spec.Mode == "" {
		spec.Mode = ModeManual
	}
	spec.Restart = spec.Restart.withDefaults()
	if spec.Failure == "" {
		spec.Failure = FailRestart
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if _, ok := s.tasks[spec.Name]; ok {
		return ErrExists
	}
	s.tasks[spec.Name] = &task{spec: spec}
	if spec.Zone != "" {
		if _, ok := s.zones[spec.Zone]; !ok {
			s.zones[spec.Zone] = &zone{}
		}
	}
	return nil
}

// Start runs a registered task (or registers+starts if using StartSpec).
func (s *Supervisor) Start(name string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	t, ok := s.tasks[name]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	spec := t.spec
	s.mu.Unlock()

	if s.gate != nil {
		if err := s.gate.AllowTask(spec); err != nil {
			return err
		}
	}
	return s.spawn(spec)
}

// StartSpec registers and starts.
func (s *Supervisor) StartSpec(spec Spec) error {
	if err := s.Register(spec); err != nil {
		return err
	}
	if spec.Mode == ModeManual {
		return nil
	}
	return s.Start(spec.Name)
}

// Stop cancels a task.
func (s *Supervisor) Stop(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[name]
	if !ok {
		return ErrNotFound
	}
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	return nil
}

// Close cancels all tasks and waits.
func (s *Supervisor) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.cancel()
	for _, t := range s.tasks {
		if t.cancel != nil {
			t.cancel()
		}
	}
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

func (s *Supervisor) spawn(spec Spec) error {
	s.mu.Lock()
	if spec.Zone != "" {
		if z, ok := s.zones[spec.Zone]; ok && z.isolated {
			s.mu.Unlock()
			return fmt.Errorf("%w: zone %s isolated", ErrMode, spec.Zone)
		}
	}
	t := s.tasks[spec.Name]
	if t == nil {
		s.mu.Unlock()
		return ErrNotFound
	}
	if t.cancel != nil {
		t.cancel()
	}
	ctx, cancel := context.WithCancel(s.root)
	t.cancel = cancel
	t.runs = 0
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { _ = recover() }()
		s.runLoop(ctx, spec)
	}()
	return nil
}

func (s *Supervisor) runLoop(ctx context.Context, spec Spec) {
	rp := spec.Restart.withDefaults()
	delay := rp.Interval
	restarts := 0
	for {
		if err := s.gateCheck(spec); err != nil {
			return
		}
		err := s.invoke(ctx, spec)
		if ctx.Err() != nil {
			return
		}
		if spec.Mode == ModeOneShot {
			return
		}
		if err == nil && (spec.Mode == ModeScheduled) {
			timer := time.NewTimer(spec.ScheduleEvery)
			if spec.ScheduleEvery <= 0 {
				timer.Reset(time.Minute)
			}
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		if err == nil && spec.Failure != FailRestart {
			return
		}
		if err == nil {
			return
		}
		switch spec.Failure {
		case FailStop:
			return
		case FailIsolate:
			s.isolate(spec.Zone)
			return
		default:
			restarts++
			if restarts > rp.MaxRestarts {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			delay *= 2
			if delay > rp.MaxInterval {
				delay = rp.MaxInterval
			}
		}
	}
}

func (s *Supervisor) invoke(ctx context.Context, spec Spec) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("supervise: panic: %v", r)
		}
	}()
	return spec.Run(ctx)
}

func (s *Supervisor) gateCheck(spec Spec) error {
	if s.gate == nil {
		return nil
	}
	return s.gate.AllowTask(spec)
}

func (s *Supervisor) isolate(zoneName string) {
	if zoneName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	z, ok := s.zones[zoneName]
	if !ok {
		z = &zone{}
		s.zones[zoneName] = z
	}
	z.isolated = true
}

// Tasks returns registered task names.
func (s *Supervisor) Tasks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.tasks))
	for n := range s.tasks {
		out = append(out, n)
	}
	return out
}
