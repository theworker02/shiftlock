package simulation

import (
	"fmt"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/internal/testclock"
	"github.com/theworker02/shiftlock/model"
)

// Scenario is a deterministic multi-step simulation over a test clock + memory backend.
type Scenario struct {
	name   string
	seed   int64
	clock  *testclock.Clock
	steps  []step
	t      *testing.T
}

type step struct {
	at   time.Duration
	name string
	fn   func(env *Env) error
}

// Env is the simulation environment.
type Env struct {
	Clock   *testclock.Clock
	Backend *memory.Backend
	Seed    int64
}

// NewScenario starts a named scenario.
func NewScenario(t *testing.T, name string, seed int64) *Scenario {
	t.Helper()
	start := time.Unix(1_000_000, 0).UTC()
	return &Scenario{name: name, seed: seed, clock: testclock.New(start), t: t}
}

// At schedules fn at an offset from scenario start.
func (s *Scenario) At(d time.Duration, name string, fn func(env *Env) error) *Scenario {
	s.steps = append(s.steps, step{at: d, name: name, fn: fn})
	return s
}

// Pause is syntactic sugar for At with a no-op advance marker.
func (s *Scenario) Pause(d time.Duration, name string) *Scenario {
	return s.At(d, "pause:"+name, func(env *Env) error { return nil })
}

// Run executes the scenario. On failure prints a copy-paste reproduction command.
func (s *Scenario) Run() {
	s.t.Helper()
	be := memory.New(memory.WithClock(s.clock))
	defer be.Close()
	env := &Env{Clock: s.clock, Backend: be, Seed: s.seed}

	// Sort steps by time (insertion order assumed sorted; bubble for safety)
	steps := append([]step(nil), s.steps...)
	for i := 0; i < len(steps); i++ {
		for j := i + 1; j < len(steps); j++ {
			if steps[j].at < steps[i].at {
				steps[i], steps[j] = steps[j], steps[i]
			}
		}
	}

	var last time.Duration
	for _, st := range steps {
		if st.at > last {
			s.clock.Advance(st.at - last)
			last = st.at
		}
		if err := st.fn(env); err != nil {
			s.t.Fatalf("scenario %q step %q: %v\nreproduce: go test ./internal/simulation -run %s -shiftlock.seed=%d",
				s.name, st.name, err, s.t.Name(), s.seed)
		}
	}
}

// NewCoordinator is a helper for scenarios.
func (e *Env) NewCoordinator(id string) (*shiftlock.Coordinator, error) {
	return shiftlock.New(shiftlock.Config{
		Service:         "sim",
		InstanceID:      id,
		GenerationID:    id,
		Backend:         e.Backend,
		Clock:           e.Clock,
		LeaseTTL:        time.Second,
		RenewInterval:   200 * time.Millisecond,
		AcquireInterval: 10 * time.Millisecond,
		TransferTimeout: 2 * time.Second,
		DrainTimeout:    time.Second,
	})
}

// ModelBridge runs model actions inside a simulation for cross-checking.
func RunModelActions(acts []model.Action) error {
	w := model.NewWorld(15, 30)
	prev := w.TokenSnapshot()
	for _, a := range acts {
		_ = w.Apply(a)
		if inv, detail := model.CheckTokenMonotonic(prev, w.TokenSnapshot()); inv != "" {
			return fmt.Errorf("%s: %s", inv, detail)
		}
		prev = w.TokenSnapshot()
		if inv, detail := w.CheckInvariants(); inv != "" {
			return fmt.Errorf("%s: %s", inv, detail)
		}
	}
	return nil
}
