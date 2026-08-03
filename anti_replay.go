package shiftlock

import (
	"sync"
	"time"
)

// ReplayCache is a bounded anti-replay store for request IDs and nonces.
// Expired entries are evicted on access; when full, oldest entries are dropped.
type ReplayCache struct {
	mu      sync.Mutex
	max     int
	entries map[string]time.Time // key -> expiry
	order   []string
	clock   Clock
}

// NewReplayCache creates a bounded cache. maxEntries must be > 0.
func NewReplayCache(maxEntries int, clock Clock) *ReplayCache {
	if maxEntries <= 0 {
		maxEntries = 1024
	}
	if clock == nil {
		clock = realClock{}
	}
	return &ReplayCache{
		max:     maxEntries,
		entries: make(map[string]time.Time, maxEntries),
		order:   make([]string, 0, maxEntries),
		clock:   clock,
	}
}

// CheckAndStore returns ErrReplay if key was seen and not expired; otherwise stores it until expiry.
func (c *ReplayCache) CheckAndStore(key string, ttl time.Duration) error {
	if key == "" {
		return &Error{Op: "ReplayCache", Err: ErrPolicy, Code: CodeInvalidArgument, Category: CategorySecurity, Message: "replay key required"}
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock.Now()
	c.evictExpiredLocked(now)
	if exp, ok := c.entries[key]; ok && exp.After(now) {
		return &Error{Op: "ReplayCache", Err: ErrReplay, Code: CodeReplay, Category: CategorySecurity, Message: "replay detected"}
	}
	c.storeLocked(key, now.Add(ttl))
	return nil
}

// Seen reports whether key is present and unexpired without storing.
func (c *ReplayCache) Seen(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock.Now()
	c.evictExpiredLocked(now)
	exp, ok := c.entries[key]
	return ok && exp.After(now)
}

// Len returns the number of live entries (approximate after lazy eviction).
func (c *ReplayCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpiredLocked(c.clock.Now())
	return len(c.entries)
}

func (c *ReplayCache) storeLocked(key string, exp time.Time) {
	if _, ok := c.entries[key]; !ok {
		if len(c.entries) >= c.max {
			// drop oldest
			old := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, old)
		}
		c.order = append(c.order, key)
	}
	c.entries[key] = exp
}

func (c *ReplayCache) evictExpiredLocked(now time.Time) {
	if len(c.entries) == 0 {
		return
	}
	n := 0
	for _, k := range c.order {
		exp, ok := c.entries[k]
		if !ok {
			continue
		}
		if !exp.After(now) {
			delete(c.entries, k)
			continue
		}
		c.order[n] = k
		n++
	}
	c.order = c.order[:n]
}
