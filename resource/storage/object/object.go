// Package object defines an object-storage resource abstraction.
//
// The API is intentionally S3-shaped (bucket/key, Put/Get/List/Delete,
// content checksums, conditional/idempotent puts) so out-of-tree adapters can
// map to S3, GCS, or Azure Blob without pulling cloud SDKs into ShiftLock
// core. This package is stdlib-only.
package object

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

var (
	ErrInvalidArg    = errors.New("object: invalid argument")
	ErrNotFound      = errors.New("object: not found")
	ErrConflict      = errors.New("object: conflict")
	ErrChecksum      = errors.New("object: checksum mismatch")
	ErrBoundExceeded = errors.New("object: bound exceeded")
	ErrClosed        = errors.New("object: closed")
)

// DefaultMaxObjects bounds in-memory stores.
const DefaultMaxObjects = 4096

// DefaultMaxParallel bounds concurrent Put/Get/List/Delete ops on a Client.
const DefaultMaxParallel = 8

// DefaultMaxBytes is the per-object body size limit for memory adapters.
const DefaultMaxBytes = 4 << 20 // 4 MiB

// ObjectMeta is sanitized object metadata (no secret values).
type ObjectMeta struct {
	Bucket       string            `json:"bucket"`
	Key          string            `json:"key"`
	Size         int64             `json:"size"`
	Checksum     string            `json:"checksum"` // sha256 hex
	ContentType  string            `json:"content_type,omitempty"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Attrs        map[string]string `json:"attrs,omitempty"`
	Generation   uint64            `json:"generation,omitempty"`
	Idempotency  string            `json:"idempotency_key,omitempty"`
}

// PutOptions controls a Put.
//
// S3-shaped notes (no AWS SDK):
//   - IdempotencyKey ≈ client token / dedupe key for retries
//   - IfChecksum / IfNotExists ≈ conditional put / If-None-Match
//   - Checksum is content integrity (sha256), analogous to checksum algorithms
type PutOptions struct {
	ContentType    string
	Attrs          map[string]string
	IdempotencyKey string
	// IfChecksum, when non-empty, requires the existing object to match (CAS).
	IfChecksum string
	// IfNotExists refuses overwrite when the key already exists.
	IfNotExists bool
	// Checksum, when set, must match sha256(body); empty computes from body.
	Checksum string
}

// GetOptions controls a Get.
type GetOptions struct {
	// IfChecksum, when non-empty, requires the stored checksum to match.
	IfChecksum string
}

// ListOptions filters a List.
type ListOptions struct {
	Prefix string
	// StartAfter is an exclusive key cursor (S3-shaped ContinuationToken lite).
	StartAfter string
	Limit      int
}

// Store is the object CRUD contract (bucket-scoped or flat namespace).
type Store interface {
	Put(ctx context.Context, bucket, key string, body []byte, opts PutOptions) (ObjectMeta, error)
	Get(ctx context.Context, bucket, key string, opts GetOptions) (ObjectMeta, []byte, error)
	List(ctx context.Context, bucket string, opts ListOptions) ([]ObjectMeta, error)
	Delete(ctx context.Context, bucket, key string) error
}

// Client wraps a Store with bounded concurrency.
type Client struct {
	store Store
	sem   chan struct{}
}

// NewClient returns a concurrency-bounded Store façade.
func NewClient(store Store, maxParallel int) *Client {
	if maxParallel <= 0 {
		maxParallel = DefaultMaxParallel
	}
	return &Client{store: store, sem: make(chan struct{}, maxParallel)}
}

func (c *Client) acquire(ctx context.Context) error {
	select {
	case c.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) release() { <-c.sem }

func (c *Client) Put(ctx context.Context, bucket, key string, body []byte, opts PutOptions) (ObjectMeta, error) {
	if err := c.acquire(ctx); err != nil {
		return ObjectMeta{}, err
	}
	defer c.release()
	return c.store.Put(ctx, bucket, key, body, opts)
}

func (c *Client) Get(ctx context.Context, bucket, key string, opts GetOptions) (ObjectMeta, []byte, error) {
	if err := c.acquire(ctx); err != nil {
		return ObjectMeta{}, nil, err
	}
	defer c.release()
	return c.store.Get(ctx, bucket, key, opts)
}

func (c *Client) List(ctx context.Context, bucket string, opts ListOptions) ([]ObjectMeta, error) {
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	return c.store.List(ctx, bucket, opts)
}

func (c *Client) Delete(ctx context.Context, bucket, key string) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	return c.store.Delete(ctx, bucket, key)
}

// ChecksumSHA256 returns hex-encoded sha256 of body.
func ChecksumSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// ValidateKey rejects empty or path-escaping keys.
func ValidateKey(key string) error {
	if key == "" || strings.Contains(key, "..") || strings.HasPrefix(key, "/") {
		return ErrInvalidArg
	}
	return nil
}

// Config configures an object-store resource adapter.
type Config struct {
	ID          resource.ResourceID
	DisplayName string
	Store       Store
	// MaxParallel bounds Client concurrency when wrapping Store.
	MaxParallel int
}

// Resource is a fabric Resource over an object Store.
//
// Capabilities: SupportsHealth + SupportsSnapshots only. SupportsFencing is
// false unless an adapter implements epoch advances on activate-manifest
// (this base adapter does not).
type Resource struct {
	cfg    Config
	client *Client
	mu     sync.Mutex
	closed bool
}

// New builds a Resource. Store is required.
func New(cfg Config) (*Resource, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: Store required", ErrInvalidArg)
	}
	if cfg.ID.Kind == "" {
		cfg.ID.Kind = resource.KindObjectStore
	}
	if cfg.ID.Kind != resource.KindObjectStore {
		return nil, fmt.Errorf("%w: id kind must be object-store", ErrInvalidArg)
	}
	if err := cfg.ID.Validate(); err != nil {
		return nil, err
	}
	return &Resource{cfg: cfg, client: NewClient(cfg.Store, cfg.MaxParallel)}, nil
}

func (r *Resource) ID() resource.ResourceID { return r.cfg.ID }
func (r *Resource) Kind() resource.Kind     { return resource.KindObjectStore }

func (r *Resource) Describe() resource.Description {
	name := r.cfg.DisplayName
	if name == "" {
		name = r.cfg.ID.Name
	}
	return resource.Description{
		DisplayName: name,
		Summary:     "Object storage resource (S3-shaped, no cloud SDK)",
		Labels:      map[string]string{"adapter": "object"},
	}
}

func (r *Resource) Capabilities() resource.ResourceCapabilities {
	return resource.ResourceCapabilities{
		SupportsHealth:    true,
		SupportsSnapshots: true,
		// No fencing: activate-manifest epoch not implemented here.
		SupportsFencing: false,
	}
}

func (r *Resource) Health(ctx context.Context) resource.ResourceHealth {
	_ = ctx
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	h := resource.ResourceHealth{
		CheckedAt: time.Now().UTC(),
		Dimensions: map[resource.HealthDimension]resource.DimensionHealth{
			resource.DimAvailability: {Status: resource.HealthHealthy},
			resource.DimDurability:   {Status: resource.HealthHealthy, Message: "adapter-local"},
		},
	}
	if closed {
		h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{
			Status: resource.HealthUnhealthy, Message: "closed",
		}
	}
	h.ComputeOverall()
	return h
}

// Snapshot lists a sanitized object count (no bodies/secrets).
func (r *Resource) Snapshot(ctx context.Context) (map[string]string, error) {
	metas, err := r.client.List(ctx, "", ListOptions{Limit: DefaultMaxObjects})
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"object_count": fmt.Sprintf("%d", len(metas)),
		"adapter":      "object",
	}, nil
}

// Client returns the bounded concurrency client.
func (r *Resource) Client() *Client { return r.client }

// Store returns the underlying store.
func (r *Resource) Store() Store { return r.cfg.Store }

// Close marks the resource closed for health probes.
func (r *Resource) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

// CopyReader drains r into a bounded buffer (helpers for adapters).
func CopyReader(r io.Reader, max int) ([]byte, error) {
	if max <= 0 {
		max = DefaultMaxBytes
	}
	lr := io.LimitReader(r, int64(max)+1)
	b, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if len(b) > max {
		return nil, ErrBoundExceeded
	}
	return b, nil
}

// SortMetas sorts by bucket then key.
func SortMetas(metas []ObjectMeta) {
	sort.Slice(metas, func(i, j int) bool {
		if metas[i].Bucket != metas[j].Bucket {
			return metas[i].Bucket < metas[j].Bucket
		}
		return metas[i].Key < metas[j].Key
	})
}
