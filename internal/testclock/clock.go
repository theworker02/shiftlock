package testclock

import (
	"sync"
	"time"

	"github.com/theworker02/shiftlock"
)

// Clock is a deterministic clock for tests.
type Clock struct {
	mu      sync.Mutex
	now     time.Time
	timers  []*timer
	tickers []*ticker
}

// New returns a clock starting at t (or Unix 0 if zero).
func New(start time.Time) *Clock {
	if start.IsZero() {
		start = time.Unix(0, 0).UTC()
	}
	return &Clock{now: start}
}

// Now returns the current fake time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Since returns Now().Sub(t).
func (c *Clock) Since(t time.Time) time.Duration {
	return c.Now().Sub(t)
}

// Advance moves time forward and fires due timers/tickers.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.fireLocked()
}

// Set sets absolute time and fires due timers.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
	c.fireLocked()
}

func (c *Clock) fireLocked() {
	remaining := c.timers[:0]
	for _, t := range c.timers {
		if t.stopped {
			continue
		}
		if !t.when.After(c.now) {
			select {
			case t.ch <- t.when:
			default:
			}
			continue
		}
		remaining = append(remaining, t)
	}
	c.timers = remaining

	for _, tk := range c.tickers {
		if tk.stopped {
			continue
		}
		for !tk.next.After(c.now) {
			select {
			case tk.ch <- tk.next:
			default:
			}
			tk.next = tk.next.Add(tk.d)
		}
	}
}

// After returns a channel that receives at now+d.
func (c *Clock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &timer{
		when: c.now.Add(d),
		ch:   make(chan time.Time, 1),
	}
	if d <= 0 {
		t.ch <- c.now
		return t.ch
	}
	c.timers = append(c.timers, t)
	return t.ch
}

type timer struct {
	when    time.Time
	ch      chan time.Time
	stopped bool
}

type ticker struct {
	d       time.Duration
	next    time.Time
	ch      chan time.Time
	stopped bool
}

// tickerHandle implements shiftlock.Ticker.
type tickerHandle struct{ t *ticker }

func (h *tickerHandle) C() <-chan time.Time { return h.t.ch }
func (h *tickerHandle) Stop()               { h.t.stopped = true }

// NewTicker creates a fake ticker.
func (c *Clock) NewTicker(d time.Duration) shiftlock.Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	tk := &ticker{
		d:    d,
		next: c.now.Add(d),
		ch:   make(chan time.Time, 1),
	}
	c.tickers = append(c.tickers, tk)
	return &tickerHandle{t: tk}
}

var _ shiftlock.Clock = (*Clock)(nil)
