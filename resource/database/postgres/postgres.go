// Package postgres is a thin database resource adapter.
//
// It uses database/sql (or any Pinger) via injection — the ShiftLock module
// does not require pgx. Fencing is advertised only when a Fencer is supplied.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

// Pinger is the minimal health dependency (sql.DB satisfies it).
type Pinger interface {
	PingContext(ctx context.Context) error
}

// SchemaVersioner optionally reports an application schema version.
type SchemaVersioner interface {
	SchemaVersion(ctx context.Context) (string, error)
}

// Fencer optionally enforces fencing tokens on mutations.
// When nil, Capabilities.SupportsFencing is false.
type Fencer interface {
	Check(ctx context.Context, token uint64) error
}

// Drainer optionally supports connection/drain readiness.
type Drainer interface {
	Drain(ctx context.Context) error
	Ready(ctx context.Context) error
}

// Config configures the adapter.
type Config struct {
	ID          resource.ResourceID
	DB          Pinger // typically *sql.DB
	DisplayName string
	Schema      SchemaVersioner
	Fencer      Fencer
	Drainer     Drainer
	// PingTimeout bounds Health ping; zero uses 3s.
	PingTimeout time.Duration
}

// Resource implements resource.Resource for PostgreSQL (or any SQL DB).
type Resource struct {
	cfg     Config
	drained atomic.Bool
}

// New validates config and returns a Resource.
func New(cfg Config) (*Resource, error) {
	if cfg.DB == nil {
		return nil, errors.New("postgres: DB/Pinger required")
	}
	if cfg.ID.Kind == "" {
		cfg.ID.Kind = resource.KindDatabase
	}
	if cfg.ID.Kind != resource.KindDatabase {
		return nil, fmt.Errorf("postgres: id kind must be %q", resource.KindDatabase)
	}
	if err := cfg.ID.Validate(); err != nil {
		return nil, err
	}
	if cfg.PingTimeout <= 0 {
		cfg.PingTimeout = 3 * time.Second
	}
	return &Resource{cfg: cfg}, nil
}

// ID implements resource.Resource.
func (r *Resource) ID() resource.ResourceID { return r.cfg.ID }

// Kind implements resource.Resource.
func (r *Resource) Kind() resource.Kind { return resource.KindDatabase }

// Describe implements resource.Resource.
func (r *Resource) Describe() resource.Description {
	name := r.cfg.DisplayName
	if name == "" {
		name = r.cfg.ID.Name
	}
	return resource.Description{
		DisplayName: name,
		Summary:     "PostgreSQL database resource (stdlib sql / injected pinger)",
		Labels:      map[string]string{"adapter": "postgres"},
	}
}

// Capabilities never advertises fencing unless a Fencer is configured.
func (r *Resource) Capabilities() resource.ResourceCapabilities {
	return resource.ResourceCapabilities{
		SupportsOwnership:    false, // ownership is Coordinator-level, not DB-native here
		SupportsFencing:      r.cfg.Fencer != nil,
		SupportsDrain:        true, // local drained flag; optional Drainer hook
		SupportsHealth:       true,
		SupportsFailover:     false,
		SupportsTransactions: true, // callers may use sql.Tx; adapter does not claim XA
		SupportsSnapshots:    true,
		SupportsRecovery:     false,
		SupportsRateLimit:    false,
	}
}

// Health pings the database and reports connectivity.
func (r *Resource) Health(ctx context.Context) resource.ResourceHealth {
	h := resource.ResourceHealth{
		CheckedAt:  time.Now().UTC(),
		Dimensions: map[resource.HealthDimension]resource.DimensionHealth{},
	}
	if r.drained.Load() {
		h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{
			Status: resource.HealthBlocked, Message: "draining",
		}
		h.Dimensions[resource.DimConnectivity] = resource.DimensionHealth{Status: resource.HealthHealthy}
		h.ComputeOverall()
		h.Message = "draining"
		return h
	}
	pingCtx, cancel := context.WithTimeout(ctx, r.cfg.PingTimeout)
	defer cancel()
	start := time.Now()
	err := r.cfg.DB.PingContext(pingCtx)
	lat := time.Since(start)
	if err != nil {
		h.Dimensions[resource.DimConnectivity] = resource.DimensionHealth{
			Status: resource.HealthUnhealthy, Message: err.Error(),
		}
		h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{Status: resource.HealthUnhealthy}
		h.ComputeOverall()
		h.Message = "ping failed"
		return h
	}
	h.Dimensions[resource.DimConnectivity] = resource.DimensionHealth{Status: resource.HealthHealthy}
	h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{Status: resource.HealthHealthy}
	h.Dimensions[resource.DimLatency] = resource.DimensionHealth{
		Status:  resource.HealthHealthy,
		Message: lat.String(),
	}
	if r.cfg.Schema != nil {
		ver, err := r.cfg.Schema.SchemaVersion(ctx)
		if err != nil {
			h.Dimensions[resource.DimConfiguration] = resource.DimensionHealth{
				Status: resource.HealthDegraded, Message: err.Error(),
			}
		} else {
			h.Dimensions[resource.DimConfiguration] = resource.DimensionHealth{
				Status: resource.HealthHealthy, Message: "schema=" + ver,
			}
		}
	}
	h.ComputeOverall()
	h.Message = "ok"
	return h
}

// SchemaVersion returns the hooked schema version, if configured.
func (r *Resource) SchemaVersion(ctx context.Context) (string, error) {
	if r.cfg.Schema == nil {
		return "", errors.New("postgres: no schema versioner")
	}
	return r.cfg.Schema.SchemaVersion(ctx)
}

// Drain marks the resource as draining. If a Drainer is set, it is invoked.
func (r *Resource) Drain(ctx context.Context) error {
	r.drained.Store(true)
	if r.cfg.Drainer != nil {
		return r.cfg.Drainer.Drain(ctx)
	}
	return nil
}

// Ready reports whether the resource may accept work.
func (r *Resource) Ready(ctx context.Context) error {
	if r.drained.Load() {
		return errors.New("postgres: draining")
	}
	if r.cfg.Drainer != nil {
		return r.cfg.Drainer.Ready(ctx)
	}
	pingCtx, cancel := context.WithTimeout(ctx, r.cfg.PingTimeout)
	defer cancel()
	return r.cfg.DB.PingContext(pingCtx)
}

// CheckFence validates a fencing token when a Fencer is configured.
func (r *Resource) CheckFence(ctx context.Context, token uint64) error {
	if r.cfg.Fencer == nil {
		return &resource.Error{Op: "CheckFence", ID: r.cfg.ID, Err: resource.ErrCapabilityClaimed, Message: "fencing not configured"}
	}
	return r.cfg.Fencer.Check(ctx, token)
}

// Snapshot returns a sanitized operational snapshot (no credentials).
func (r *Resource) Snapshot(ctx context.Context) (map[string]string, error) {
	out := map[string]string{
		"adapter": "postgres",
		"drained": fmt.Sprintf("%v", r.drained.Load()),
	}
	h := r.Health(ctx)
	out["health"] = string(h.Overall)
	if r.cfg.Schema != nil {
		if ver, err := r.cfg.Schema.SchemaVersion(ctx); err == nil {
			out["schema_version"] = ver
		}
	}
	// Never include DSN, passwords, or connection strings.
	return out, nil
}

// OpenSQL is a helper documenting that callers open *sql.DB themselves.
// ShiftLock does not open connections or parse DSNs containing secrets.
func OpenSQL(db *sql.DB) Pinger { return db }

// Ensure *sql.DB remains a valid Pinger at compile time when database/sql is used.
var _ Pinger = (*sql.DB)(nil)
