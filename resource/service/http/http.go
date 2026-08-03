// Package httpresource provides HTTP service resource guardrails.
//
// It is not a full HTTP client framework: health probes, circuit/rate-limit
// hooks, and Execute with IdempotencyID are the scope.
package httpresource

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

// Config configures an HTTP service resource.
type Config struct {
	ID          resource.ResourceID
	BaseURL     string
	DisplayName string
	Client      *http.Client
	HealthPath  string // default /healthz
	// MaxConcurrent bounds in-flight Execute calls (0 = 64).
	MaxConcurrent int
	// CircuitThreshold consecutive failures before opening (0 = 5).
	CircuitThreshold int
	// CircuitOpenFor is how long the circuit stays open (0 = 30s).
	CircuitOpenFor time.Duration
}

// Request is a guarded outbound operation.
type Request struct {
	Method        string
	Path          string
	Body          io.Reader
	Header        http.Header
	IdempotencyID string
	Operation     string
}

// Result is a sanitized execute outcome.
type Result struct {
	StatusCode int
	// BodyLen is recorded instead of raw body content.
	BodyLen int
}

// Resource implements resource.Resource for an HTTP dependency.
type Resource struct {
	cfg Config

	sem chan struct{}

	mu            sync.Mutex
	failures      int
	circuitOpenUntil time.Time
	seenIDs       map[string]struct{}
	execCount     atomic.Uint64
}

// New constructs an HTTP resource.
func New(cfg Config) (*Resource, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("http: BaseURL required")
	}
	if cfg.ID.Kind == "" {
		cfg.ID.Kind = resource.KindHTTPService
	}
	if cfg.ID.Kind != resource.KindHTTPService {
		return nil, errors.New("http: id kind must be http-service")
	}
	if err := cfg.ID.Validate(); err != nil {
		return nil, err
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.HealthPath == "" {
		cfg.HealthPath = "/healthz"
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 64
	}
	if cfg.CircuitThreshold <= 0 {
		cfg.CircuitThreshold = 5
	}
	if cfg.CircuitOpenFor <= 0 {
		cfg.CircuitOpenFor = 30 * time.Second
	}
	return &Resource{
		cfg:     cfg,
		sem:     make(chan struct{}, cfg.MaxConcurrent),
		seenIDs: make(map[string]struct{}),
	}, nil
}

func (r *Resource) ID() resource.ResourceID { return r.cfg.ID }
func (r *Resource) Kind() resource.Kind     { return resource.KindHTTPService }

func (r *Resource) Describe() resource.Description {
	name := r.cfg.DisplayName
	if name == "" {
		name = r.cfg.ID.Name
	}
	return resource.Description{
		DisplayName: name,
		Summary:     "HTTP service resource guardrails",
		Labels:      map[string]string{"adapter": "http"},
	}
}

func (r *Resource) Capabilities() resource.ResourceCapabilities {
	return resource.ResourceCapabilities{
		SupportsHealth:    true,
		SupportsFailover:  true,
		SupportsRateLimit: true, // concurrency bound / circuit hooks
		SupportsSnapshots: true,
		SupportsFencing:   false,
	}
}

func (r *Resource) Health(ctx context.Context) resource.ResourceHealth {
	h := resource.ResourceHealth{
		CheckedAt:  time.Now().UTC(),
		Dimensions: map[resource.HealthDimension]resource.DimensionHealth{},
	}
	r.mu.Lock()
	open := time.Now().Before(r.circuitOpenUntil)
	r.mu.Unlock()
	if open {
		h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{
			Status: resource.HealthBlocked, Message: "circuit open",
		}
		h.ComputeOverall()
		h.Message = "circuit open"
		return h
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.cfg.BaseURL, "/")+r.cfg.HealthPath, nil)
	if err != nil {
		h.Dimensions[resource.DimConnectivity] = resource.DimensionHealth{
			Status: resource.HealthUnhealthy, Message: err.Error(),
		}
		h.ComputeOverall()
		return h
	}
	resp, err := r.cfg.Client.Do(req)
	if err != nil {
		h.Dimensions[resource.DimConnectivity] = resource.DimensionHealth{
			Status: resource.HealthUnhealthy, Message: err.Error(),
		}
		h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{Status: resource.HealthUnhealthy}
		h.ComputeOverall()
		h.Message = "probe failed"
		return h
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 500 {
		h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{
			Status: resource.HealthUnhealthy, Message: resp.Status,
		}
	} else if resp.StatusCode >= 400 {
		h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{
			Status: resource.HealthDegraded, Message: resp.Status,
		}
	} else {
		h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{Status: resource.HealthHealthy}
		h.Dimensions[resource.DimConnectivity] = resource.DimensionHealth{Status: resource.HealthHealthy}
	}
	h.ComputeOverall()
	h.Message = "ok"
	return h
}

// Execute performs a guarded request. IdempotencyID prevents duplicate in-process retries.
func (r *Resource) Execute(ctx context.Context, req Request) (Result, error) {
	if req.Method == "" {
		req.Method = http.MethodPost
	}
	if req.Path == "" {
		return Result{}, errors.New("http: Path required")
	}
	if req.IdempotencyID != "" {
		r.mu.Lock()
		if _, ok := r.seenIDs[req.IdempotencyID]; ok {
			r.mu.Unlock()
			return Result{}, errors.New("http: duplicate IdempotencyID")
		}
		// Cap seen IDs.
		if len(r.seenIDs) >= 4096 {
			r.seenIDs = make(map[string]struct{})
		}
		r.seenIDs[req.IdempotencyID] = struct{}{}
		r.mu.Unlock()
	}

	r.mu.Lock()
	if time.Now().Before(r.circuitOpenUntil) {
		r.mu.Unlock()
		return Result{}, errors.New("http: circuit open")
	}
	r.mu.Unlock()

	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}

	url := strings.TrimRight(r.cfg.BaseURL, "/") + "/" + strings.TrimLeft(req.Path, "/")
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, url, req.Body)
	if err != nil {
		return Result{}, err
	}
	if req.Header != nil {
		httpReq.Header = req.Header.Clone()
	}
	if req.IdempotencyID != "" {
		httpReq.Header.Set("Idempotency-Key", req.IdempotencyID)
	}
	if req.Operation != "" {
		httpReq.Header.Set("X-ShiftLock-Operation", req.Operation)
	}

	resp, err := r.cfg.Client.Do(httpReq)
	if err != nil {
		r.recordFailure()
		return Result{}, err
	}
	defer resp.Body.Close()
	n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 500 {
		r.recordFailure()
	} else {
		r.recordSuccess()
	}
	r.execCount.Add(1)
	return Result{StatusCode: resp.StatusCode, BodyLen: int(n)}, nil
}

// CircuitOpen reports whether the circuit breaker is open.
func (r *Resource) CircuitOpen() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Now().Before(r.circuitOpenUntil)
}

// OpenCircuit forces the circuit open (failover/tests).
func (r *Resource) OpenCircuit(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d <= 0 {
		d = r.cfg.CircuitOpenFor
	}
	r.circuitOpenUntil = time.Now().Add(d)
}

func (r *Resource) recordFailure() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures++
	if r.failures >= r.cfg.CircuitThreshold {
		r.circuitOpenUntil = time.Now().Add(r.cfg.CircuitOpenFor)
		r.failures = 0
	}
}

func (r *Resource) recordSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = 0
}

// Snapshot is sanitized (no auth headers/tokens/bodies).
func (r *Resource) Snapshot(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	open := time.Now().Before(r.circuitOpenUntil)
	r.mu.Unlock()
	return map[string]string{
		"adapter":      "http",
		"circuit_open": boolStr(open),
		"exec_count":   uitoa(r.execCount.Load()),
		// BaseURL host only — avoid leaking query secrets if misconfigured.
		"base": stripQuery(r.cfg.BaseURL),
	}, nil
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func stripQuery(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}

func uitoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
