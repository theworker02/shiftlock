// Package sync provides source/target synchronization with conflict policies.
// The memory engine is suitable for demos and tests; production adapters
// supply cursors over their own stores.
package sync

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidArg   = errors.New("sync: invalid argument")
	ErrConflict     = errors.New("sync: conflict")
	ErrManual       = errors.New("sync: manual resolution required")
	ErrNotFound     = errors.New("sync: not found")
	ErrBoundExceeded = errors.New("sync: bound exceeded")
)

// DefaultMaxKeys bounds in-memory demo stores.
const DefaultMaxKeys = 4096

// ConflictPolicy selects how source/target disagreements are resolved.
type ConflictPolicy string

const (
	PreferSource ConflictPolicy = "prefer-source"
	PreferTarget ConflictPolicy = "prefer-target"
	PreferLatest ConflictPolicy = "prefer-latest"
	Manual       ConflictPolicy = "manual"
	Reject       ConflictPolicy = "reject"
)

// Record is a syncable item (no secret values).
type Record struct {
	Key       string
	Value     string
	UpdatedAt time.Time
	Version   uint64
}

// Cursor tracks progress through a source.
type Cursor struct {
	Position string
	Updated  time.Time
}

// Source reads records after a cursor.
type Source interface {
	Next(ctx context.Context, cur Cursor, limit int) ([]Record, Cursor, error)
}

// Target applies records and reports existing values for conflict checks.
type Target interface {
	Get(ctx context.Context, key string) (Record, bool, error)
	Put(ctx context.Context, rec Record) error
}

// Engine runs sync passes.
type Engine struct {
	mu       sync.Mutex
	policy   ConflictPolicy
	cursor   Cursor
	applied  int
	rejected int
	manual   []string
	maxManual int
}

// Config configures an Engine.
type Config struct {
	Policy    ConflictPolicy
	MaxManual int // bound queued manual keys (0 = 256)
}

// New creates a sync engine.
func New(cfg Config) (*Engine, error) {
	if cfg.Policy == "" {
		cfg.Policy = PreferSource
	}
	switch cfg.Policy {
	case PreferSource, PreferTarget, PreferLatest, Manual, Reject:
	default:
		return nil, ErrInvalidArg
	}
	if cfg.MaxManual <= 0 {
		cfg.MaxManual = 256
	}
	return &Engine{policy: cfg.Policy, maxManual: cfg.MaxManual}, nil
}

// Result summarizes one Sync pass.
type Result struct {
	Applied   int      `json:"applied"`
	Skipped   int      `json:"skipped"`
	Rejected  int      `json:"rejected"`
	Manual    []string `json:"manual,omitempty"`
	Cursor    Cursor   `json:"cursor"`
	Conflicts int      `json:"conflicts"`
}

// Sync pulls from source and applies to target per policy.
func (e *Engine) Sync(ctx context.Context, src Source, dst Target, limit int) (Result, error) {
	if src == nil || dst == nil {
		return Result{}, ErrInvalidArg
	}
	if limit <= 0 {
		limit = 100
	}
	e.mu.Lock()
	cur := e.cursor
	policy := e.policy
	e.mu.Unlock()

	recs, next, err := src.Next(ctx, cur, limit)
	if err != nil {
		return Result{}, err
	}
	var res Result
	for _, rec := range recs {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		existing, ok, err := dst.Get(ctx, rec.Key)
		if err != nil {
			return res, err
		}
		if !ok {
			if err := dst.Put(ctx, rec); err != nil {
				return res, err
			}
			res.Applied++
			continue
		}
		if same(existing, rec) {
			res.Skipped++
			continue
		}
		res.Conflicts++
		switch policy {
		case PreferSource:
			if err := dst.Put(ctx, rec); err != nil {
				return res, err
			}
			res.Applied++
		case PreferTarget:
			res.Skipped++
		case PreferLatest:
			useSource := rec.UpdatedAt.After(existing.UpdatedAt) ||
				(rec.UpdatedAt.Equal(existing.UpdatedAt) && rec.Version > existing.Version)
			if useSource {
				if err := dst.Put(ctx, rec); err != nil {
					return res, err
				}
				res.Applied++
			} else {
				res.Skipped++
			}
		case Manual:
			if err := e.queueManual(rec.Key); err != nil {
				return res, err
			}
			res.Manual = append(res.Manual, rec.Key)
		case Reject:
			res.Rejected++
			e.mu.Lock()
			e.rejected++
			e.mu.Unlock()
			return res, ErrConflict
		}
	}
	e.mu.Lock()
	e.cursor = next
	e.applied += res.Applied
	res.Cursor = e.cursor
	if len(e.manual) > 0 {
		res.Manual = append([]string(nil), e.manual...)
	}
	e.mu.Unlock()
	return res, nil
}

// Cursor returns the current cursor.
func (e *Engine) Cursor() Cursor {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cursor
}

// ManualKeys returns keys awaiting operator resolution.
func (e *Engine) ManualKeys() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.manual...)
}

func (e *Engine) queueManual(key string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, k := range e.manual {
		if k == key {
			return nil
		}
	}
	if len(e.manual) >= e.maxManual {
		return ErrBoundExceeded
	}
	e.manual = append(e.manual, key)
	return nil
}

func same(a, b Record) bool {
	return a.Key == b.Key && a.Value == b.Value && a.Version == b.Version
}

// MemoryStore is an in-process Source and Target for demos/tests.
type MemoryStore struct {
	mu   sync.Mutex
	data map[string]Record
	order []string
	max  int
}

// NewMemoryStore creates a bounded memory store.
func NewMemoryStore(max int) *MemoryStore {
	if max <= 0 {
		max = DefaultMaxKeys
	}
	return &MemoryStore{data: make(map[string]Record), max: max}
}

// Put upserts a record.
func (m *MemoryStore) Put(_ context.Context, rec Record) error {
	if rec.Key == "" {
		return ErrInvalidArg
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[rec.Key]; !ok {
		if len(m.data) >= m.max {
			return ErrBoundExceeded
		}
		m.order = append(m.order, rec.Key)
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}
	m.data[rec.Key] = rec
	return nil
}

// Get returns a record.
func (m *MemoryStore) Get(_ context.Context, key string) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.data[key]
	return r, ok, nil
}

// Next implements Source by scanning keys after cursor position.
func (m *MemoryStore) Next(_ context.Context, cur Cursor, limit int) ([]Record, Cursor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	start := 0
	if cur.Position != "" {
		for i, k := range m.order {
			if k == cur.Position {
				start = i + 1
				break
			}
		}
	}
	var out []Record
	pos := cur.Position
	for i := start; i < len(m.order) && len(out) < limit; i++ {
		k := m.order[i]
		out = append(out, m.data[k])
		pos = k
	}
	return out, Cursor{Position: pos, Updated: time.Now().UTC()}, nil
}

// Len returns entry count.
func (m *MemoryStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.data)
}
