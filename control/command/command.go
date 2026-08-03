// Package command registers and invokes audited, rate-limited control commands.
// Shell execution is NOT a default; handlers are in-process only.
package command

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrExists       = errors.New("command: already registered")
	ErrNotFound     = errors.New("command: not found")
	ErrDenied       = errors.New("command: denied")
	ErrTooLarge     = errors.New("command: payload too large")
	ErrRateLimited  = errors.New("command: rate limited")
	ErrClosed       = errors.New("command: closed")
	ErrIdempotency  = errors.New("command: idempotency conflict")
)

// Handler runs in-process. Must not exec shells by default.
type Handler func(ctx context.Context, req Request) (Result, error)

// Request is an invocation.
type Request struct {
	Name       string
	ActorID    string
	Body       []byte
	IdempotencyKey string
	Deadline   time.Duration
}

// Result is a safe response (no secrets).
type Result struct {
	OK      bool
	Message string
	Attrs   map[string]string
}

// Spec registers a command.
type Spec struct {
	Name            string
	Permission      string
	MaxBodyBytes    int
	Deadline        time.Duration
	RateLimitPerMin int
	Handler         Handler
}

// Auditor receives invocation outcomes.
type Auditor interface {
	AuditCommand(actor, name, decision, outcome string)
}

// Authorizer may deny invoke.
type Authorizer interface {
	AuthorizeCommand(actor, name, permission string) error
}

// Registry holds commands.
type Registry struct {
	mu       sync.Mutex
	cmds     map[string]*registered
	auth     Authorizer
	audit    Auditor
	idem     map[string]Result
	idemMax  int
	closed   bool
	defaults Spec
}

type registered struct {
	spec Spec
	hits []time.Time
}

// Config configures a Registry.
type Config struct {
	Authorizer      Authorizer
	Auditor         Auditor
	MaxBodyBytes    int
	DefaultDeadline time.Duration
	IdempotencyMax  int // bounded; default 1024
}

// New creates a Registry.
func New(cfg Config) *Registry {
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 256 << 10
	}
	if cfg.DefaultDeadline <= 0 {
		cfg.DefaultDeadline = 15 * time.Second
	}
	if cfg.IdempotencyMax <= 0 {
		cfg.IdempotencyMax = 1024
	}
	return &Registry{
		cmds:    make(map[string]*registered),
		auth:    cfg.Authorizer,
		audit:   cfg.Auditor,
		idem:    make(map[string]Result),
		idemMax: cfg.IdempotencyMax,
		defaults: Spec{
			MaxBodyBytes: cfg.MaxBodyBytes,
			Deadline:     cfg.DefaultDeadline,
		},
	}
}

// Register adds a command. Permission required.
func (r *Registry) Register(spec Spec) error {
	if spec.Name == "" || spec.Handler == nil {
		return errors.New("command: name and handler required")
	}
	if spec.Permission == "" {
		return errors.New("command: permission required")
	}
	if spec.MaxBodyBytes <= 0 {
		spec.MaxBodyBytes = r.defaults.MaxBodyBytes
	}
	if spec.Deadline <= 0 {
		spec.Deadline = r.defaults.Deadline
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	if _, ok := r.cmds[spec.Name]; ok {
		return ErrExists
	}
	r.cmds[spec.Name] = &registered{spec: spec}
	return nil
}

// Invoke runs a command with auth, size, deadline, rate limit, idempotency, panic containment.
func (r *Registry) Invoke(ctx context.Context, req Request) (res Result, err error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return Result{}, ErrClosed
	}
	reg, ok := r.cmds[req.Name]
	if !ok {
		r.mu.Unlock()
		return Result{}, ErrNotFound
	}
	spec := reg.spec
	if req.IdempotencyKey != "" {
		if prev, ok := r.idem[req.IdempotencyKey]; ok {
			r.mu.Unlock()
			return prev, nil
		}
	}
	if len(req.Body) > spec.MaxBodyBytes {
		r.mu.Unlock()
		r.auditOut(req.ActorID, req.Name, "deny", "too_large")
		return Result{}, ErrTooLarge
	}
	if spec.RateLimitPerMin > 0 {
		now := time.Now()
		cut := now.Add(-time.Minute)
		filtered := reg.hits[:0]
		for _, t := range reg.hits {
			if t.After(cut) {
				filtered = append(filtered, t)
			}
		}
		reg.hits = filtered
		if len(reg.hits) >= spec.RateLimitPerMin {
			r.mu.Unlock()
			r.auditOut(req.ActorID, req.Name, "deny", "rate_limited")
			return Result{}, ErrRateLimited
		}
		reg.hits = append(reg.hits, now)
	}
	r.mu.Unlock()

	if r.auth != nil {
		if err := r.auth.AuthorizeCommand(req.ActorID, req.Name, spec.Permission); err != nil {
			r.auditOut(req.ActorID, req.Name, "deny", "unauthorized")
			return Result{}, ErrDenied
		}
	}

	deadline := spec.Deadline
	if req.Deadline > 0 && req.Deadline < deadline {
		deadline = req.Deadline
	}
	cctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("command: panic contained: %v", rec)
			res = Result{OK: false, Message: "internal error"}
			r.auditOut(req.ActorID, req.Name, "error", "panic")
		}
	}()

	res, err = spec.Handler(cctx, req)
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	r.auditOut(req.ActorID, req.Name, "allow", outcome)

	if req.IdempotencyKey != "" && err == nil {
		r.mu.Lock()
		if len(r.idem) >= r.idemMax {
			// drop arbitrary oldest by clearing half (bounded)
			n := 0
			for k := range r.idem {
				delete(r.idem, k)
				n++
				if n >= r.idemMax/2 {
					break
				}
			}
		}
		r.idem[req.IdempotencyKey] = res
		r.mu.Unlock()
	}
	return res, err
}

// Close disables the registry.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

func (r *Registry) auditOut(actor, name, decision, outcome string) {
	if r.audit != nil {
		r.audit.AuditCommand(actor, name, decision, outcome)
	}
}
