// Package antireplay provides a bounded request-ID / nonce cache with expiry.
// Advancing a SecurityEpoch invalidates prior authorization material.
package antireplay

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrReplay       = errors.New("antireplay: replay detected")
	ErrExpired      = errors.New("antireplay: entry expired")
	ErrEmptyID      = errors.New("antireplay: empty request id or nonce")
	ErrEpochRollback = errors.New("antireplay: security epoch rollback")
	ErrCapacity     = errors.New("antireplay: cache at capacity")
)

// SecurityEpoch is a monotonically increasing authorization generation.
// Advancing the epoch invalidates old capabilities and nonces bound to prior epochs.
type SecurityEpoch uint64

// Cache is a bounded anti-replay store keyed by (epoch, id).
type Cache struct {
	mu       sync.Mutex
	max      int
	entries  map[string]entry
	epoch    SecurityEpoch
	now      func() time.Time
}

type entry struct {
	expires time.Time
	epoch   SecurityEpoch
}

// Option configures Cache.
type Option func(*Cache)

// WithClock overrides the time source (tests).
func WithClock(now func() time.Time) Option {
	return func(c *Cache) { c.now = now }
}

// New creates a cache that stores at most maxEntries live items.
// maxEntries must be > 0; if <= 0, defaults to 4096.
func New(maxEntries int, opts ...Option) *Cache {
	if maxEntries <= 0 {
		maxEntries = 4096
	}
	c := &Cache{
		max:     maxEntries,
		entries: make(map[string]entry, maxEntries),
		now:     func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Epoch returns the current security epoch.
func (c *Cache) Epoch() SecurityEpoch {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch
}

// AdvanceEpoch increments the security epoch and clears all cached nonces.
// Epoch never decreases or wraps silently.
func (c *Cache) AdvanceEpoch() (SecurityEpoch, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.epoch == ^SecurityEpoch(0) {
		return c.epoch, errors.New("antireplay: security epoch overflow")
	}
	c.epoch++
	c.entries = make(map[string]entry, c.max)
	return c.epoch, nil
}

// SetEpoch sets the epoch only if newEpoch >= current.
func (c *Cache) SetEpoch(newEpoch SecurityEpoch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if newEpoch < c.epoch {
		return ErrEpochRollback
	}
	if newEpoch > c.epoch {
		c.epoch = newEpoch
		c.entries = make(map[string]entry, c.max)
	}
	return nil
}

func cacheKey(epoch SecurityEpoch, id string) string {
	return formatEpoch(epoch) + "\x00" + id
}

func formatEpoch(e SecurityEpoch) string {
	const hexdigits = "0123456789abcdef"
	var b [16]byte
	v := uint64(e)
	for i := 15; i >= 0; i-- {
		b[i] = hexdigits[v&0xf]
		v >>= 4
	}
	return string(b[:])
}

// CheckAndStore records id if unseen for the current epoch.
// Returns ErrReplay if already seen, ErrExpired if ttl already elapsed,
// ErrCapacity if the cache is full after purge.
func (c *Cache) CheckAndStore(id string, ttl time.Duration) error {
	if id == "" {
		return ErrEmptyID
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	now := c.now()
	expires := now.Add(ttl)
	if !expires.After(now) {
		return ErrExpired
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeLocked(now)

	k := cacheKey(c.epoch, id)
	if e, ok := c.entries[k]; ok {
		if now.After(e.expires) {
			delete(c.entries, k)
		} else {
			return ErrReplay
		}
	}
	if len(c.entries) >= c.max {
		return ErrCapacity
	}
	c.entries[k] = entry{expires: expires, epoch: c.epoch}
	return nil
}

// Seen reports whether id is present and unexpired for the current epoch.
func (c *Cache) Seen(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.purgeLocked(now)
	e, ok := c.entries[cacheKey(c.epoch, id)]
	return ok && !now.After(e.expires)
}

// Len returns live entry count after purge.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeLocked(c.now())
	return len(c.entries)
}

func (c *Cache) purgeLocked(now time.Time) {
	for k, e := range c.entries {
		if now.After(e.expires) || e.epoch != c.epoch {
			delete(c.entries, k)
		}
	}
}
