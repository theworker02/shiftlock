package memory

import (
	"context"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/resource"
	"github.com/theworker02/shiftlock/resource/storage/object"
)

// Config configures an in-memory object store.
type Config struct {
	MaxObjects int
	MaxBytes   int
}

// Store is an in-process object.Store for tests and examples.
type Store struct {
	mu         sync.Mutex
	objects    map[string]entry // "bucket/key"
	idempotent map[string]object.ObjectMeta
	maxObjects int
	maxBytes   int
	closed     bool
}

type entry struct {
	meta object.ObjectMeta
	body []byte
}

// NewStore creates a bounded memory object store.
func NewStore(cfg Config) *Store {
	if cfg.MaxObjects <= 0 {
		cfg.MaxObjects = object.DefaultMaxObjects
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = object.DefaultMaxBytes
	}
	return &Store{
		objects:    make(map[string]entry),
		idempotent: make(map[string]object.ObjectMeta),
		maxObjects: cfg.MaxObjects,
		maxBytes:   cfg.MaxBytes,
	}
}

func flatKey(bucket, key string) string {
	if bucket == "" {
		return key
	}
	return bucket + "/" + key
}

// Put implements object.Store.
func (s *Store) Put(_ context.Context, bucket, key string, body []byte, opts object.PutOptions) (object.ObjectMeta, error) {
	if err := object.ValidateKey(key); err != nil {
		return object.ObjectMeta{}, err
	}
	if body == nil {
		body = []byte{}
	}
	if len(body) > s.maxBytes {
		return object.ObjectMeta{}, object.ErrBoundExceeded
	}
	sum := object.ChecksumSHA256(body)
	if opts.Checksum != "" && opts.Checksum != sum {
		return object.ObjectMeta{}, object.ErrChecksum
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return object.ObjectMeta{}, object.ErrClosed
	}

	if opts.IdempotencyKey != "" {
		if prev, ok := s.idempotent[opts.IdempotencyKey]; ok {
			return prev, nil
		}
	}

	fk := flatKey(bucket, key)
	existing, exists := s.objects[fk]
	if opts.IfNotExists && exists {
		return object.ObjectMeta{}, object.ErrConflict
	}
	if opts.IfChecksum != "" {
		if !exists {
			return object.ObjectMeta{}, object.ErrNotFound
		}
		if existing.meta.Checksum != opts.IfChecksum {
			return object.ObjectMeta{}, object.ErrConflict
		}
	}
	if !exists && len(s.objects) >= s.maxObjects {
		return object.ObjectMeta{}, object.ErrBoundExceeded
	}

	gen := uint64(1)
	if exists {
		gen = existing.meta.Generation + 1
	}
	meta := object.ObjectMeta{
		Bucket:      bucket,
		Key:         key,
		Size:        int64(len(body)),
		Checksum:    sum,
		ContentType: opts.ContentType,
		UpdatedAt:   time.Now().UTC(),
		Attrs:       cloneAttrs(opts.Attrs),
		Generation:  gen,
		Idempotency: opts.IdempotencyKey,
	}
	cp := make([]byte, len(body))
	copy(cp, body)
	s.objects[fk] = entry{meta: meta, body: cp}
	if opts.IdempotencyKey != "" {
		s.idempotent[opts.IdempotencyKey] = meta
	}
	return meta, nil
}

// Get implements object.Store.
func (s *Store) Get(_ context.Context, bucket, key string, opts object.GetOptions) (object.ObjectMeta, []byte, error) {
	if err := object.ValidateKey(key); err != nil {
		return object.ObjectMeta{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return object.ObjectMeta{}, nil, object.ErrClosed
	}
	e, ok := s.objects[flatKey(bucket, key)]
	if !ok {
		return object.ObjectMeta{}, nil, object.ErrNotFound
	}
	if opts.IfChecksum != "" && e.meta.Checksum != opts.IfChecksum {
		return object.ObjectMeta{}, nil, object.ErrChecksum
	}
	out := make([]byte, len(e.body))
	copy(out, e.body)
	return e.meta, out, nil
}

// List implements object.Store.
func (s *Store) List(_ context.Context, bucket string, opts object.ListOptions) ([]object.ObjectMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, object.ErrClosed
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = s.maxObjects
	}
	var out []object.ObjectMeta
	for _, e := range s.objects {
		if bucket != "" && e.meta.Bucket != bucket {
			continue
		}
		if opts.Prefix != "" && !hasPrefix(e.meta.Key, opts.Prefix) {
			continue
		}
		if opts.StartAfter != "" && e.meta.Key <= opts.StartAfter {
			continue
		}
		out = append(out, e.meta)
	}
	object.SortMetas(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Delete implements object.Store.
func (s *Store) Delete(_ context.Context, bucket, key string) error {
	if err := object.ValidateKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return object.ErrClosed
	}
	fk := flatKey(bucket, key)
	if _, ok := s.objects[fk]; !ok {
		return object.ErrNotFound
	}
	delete(s.objects, fk)
	return nil
}

// Len returns object count.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

// Close marks the store closed.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// NewResource builds an object.Resource over a memory store.
func NewResource(id resource.ResourceID, display string) (*object.Resource, *Store, error) {
	st := NewStore(Config{})
	res, err := object.New(object.Config{ID: id, DisplayName: display, Store: st})
	return res, st, err
}

func hasPrefix(key, prefix string) bool {
	return len(key) >= len(prefix) && key[:len(prefix)] == prefix
}

func cloneAttrs(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
