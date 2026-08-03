package shiftlock

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Gate is a readiness check that must pass before becoming active.
type Gate struct {
	// Name identifies the gate in reports.
	Name string
	// Check performs the readiness probe. Return nil on success.
	Check func(ctx context.Context) error
	// Required means failure blocks readiness (default true).
	Required bool
	// Optional is the inverse of Required for convenience.
	Optional bool
	// Timeout bounds a single Check invocation (0 = use group default).
	Timeout time.Duration
	// Retries is the number of additional attempts after the first failure.
	Retries int
	// RetryDelay waits between retries.
	RetryDelay time.Duration
	// StableSuccesses requires N consecutive successes (default 1).
	StableSuccesses int
}

func (g Gate) required() bool {
	if g.Optional {
		return false
	}
	if !g.Required && g.Optional {
		return false
	}
	// Default required unless Optional is set.
	return !g.Optional
}

func (g Gate) stableNeed() int {
	if g.StableSuccesses <= 0 {
		return 1
	}
	return g.StableSuccesses
}

// GateMode controls concurrency of gate evaluation.
type GateMode int

const (
	// GateSequential evaluates gates one at a time in order.
	GateSequential GateMode = iota
	// GateParallel evaluates all gates concurrently.
	GateParallel
)

// GateReport is the result of evaluating a single gate.
type GateReport struct {
	Name     string        `json:"name"`
	Passed   bool          `json:"passed"`
	Required bool          `json:"required"`
	Attempts int           `json:"attempts"`
	Duration time.Duration `json:"duration"`
	Err      string        `json:"error,omitempty"`
}

// ReadinessReport summarizes gate evaluation.
type ReadinessReport struct {
	Passed   bool         `json:"passed"`
	Duration time.Duration `json:"duration"`
	Gates    []GateReport `json:"gates"`
}

// Readiness evaluates a set of gates.
type Readiness struct {
	Gates   []Gate
	Mode    GateMode
	Timeout time.Duration
	Clock   Clock
}

// Run evaluates all gates and returns a report.
func (r Readiness) Run(ctx context.Context) (ReadinessReport, error) {
	clock := r.Clock
	if clock == nil {
		clock = realClock{}
	}
	start := clock.Now()
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var reports []GateReport
	var err error
	switch r.Mode {
	case GateParallel:
		reports, err = r.runParallel(ctx, clock)
	default:
		reports, err = r.runSequential(ctx, clock)
	}
	rep := ReadinessReport{
		Passed:   err == nil,
		Duration: clock.Since(start),
		Gates:    reports,
	}
	if err != nil {
		return rep, &Error{Op: "readiness", Err: ErrNotReady, Message: err.Error()}
	}
	return rep, nil
}

func (r Readiness) runSequential(ctx context.Context, clock Clock) ([]GateReport, error) {
	reports := make([]GateReport, 0, len(r.Gates))
	var firstErr error
	for _, g := range r.Gates {
		rep := evalGate(ctx, clock, g)
		reports = append(reports, rep)
		if !rep.Passed && g.required() && firstErr == nil {
			firstErr = fmt.Errorf("gate %q failed: %s", g.Name, rep.Err)
			// still continue to collect reports
		}
		if ctx.Err() != nil && firstErr == nil {
			firstErr = ctx.Err()
		}
	}
	return reports, firstErr
}

func (r Readiness) runParallel(ctx context.Context, clock Clock) ([]GateReport, error) {
	reports := make([]GateReport, len(r.Gates))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i, g := range r.Gates {
		wg.Add(1)
		go func(i int, g Gate) {
			defer wg.Done()
			rep := evalGate(ctx, clock, g)
			reports[i] = rep
			if !rep.Passed && g.required() {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("gate %q failed: %s", g.Name, rep.Err)
				}
				mu.Unlock()
			}
		}(i, g)
	}
	wg.Wait()
	return reports, firstErr
}

func evalGate(ctx context.Context, clock Clock, g Gate) GateReport {
	need := g.stableNeed()
	stable := 0
	attempts := 0
	start := clock.Now()
	retries := g.Retries
	var lastErr error

	for stable < need {
		if ctx.Err() != nil {
			return GateReport{
				Name: g.Name, Passed: false, Required: g.required(),
				Attempts: attempts, Duration: clock.Since(start), Err: ctx.Err().Error(),
			}
		}
		attempts++
		err := runCheck(ctx, g)
		if err == nil {
			stable++
			lastErr = nil
			continue
		}
		stable = 0
		lastErr = err
		if retries <= 0 {
			break
		}
		retries--
		delay := g.RetryDelay
		if delay <= 0 {
			delay = 50 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return GateReport{
				Name: g.Name, Passed: false, Required: g.required(),
				Attempts: attempts, Duration: clock.Since(start), Err: ctx.Err().Error(),
			}
		case <-clock.After(delay):
		}
	}

	rep := GateReport{
		Name:     g.Name,
		Passed:   lastErr == nil && stable >= need,
		Required: g.required(),
		Attempts: attempts,
		Duration: clock.Since(start),
	}
	if lastErr != nil {
		rep.Err = lastErr.Error()
	}
	// Optional gates never fail the group via Passed=false for required check;
	// they still report Passed accurately.
	return rep
}

func runCheck(ctx context.Context, g Gate) error {
	if g.Check == nil {
		return nil
	}
	cctx := ctx
	var cancel context.CancelFunc
	if g.Timeout > 0 {
		cctx, cancel = context.WithTimeout(ctx, g.Timeout)
		defer cancel()
	}
	return g.Check(cctx)
}
