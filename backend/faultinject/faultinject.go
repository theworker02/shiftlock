package faultinject

import (
	"context"
	"sync"
	"time"

	"github.com/theworker02/shiftlock"
)

// Fault configures injection behavior.
type Fault struct {
	Latency            time.Duration
	Unavailable        bool
	Timeout            bool
	DuplicateResponse  bool
	LoseResponse       bool // backend succeeds; client sees error (ambiguous)
	CommitThenFail     bool // for CommitTransfer
	StaleRead          bool
	WatchDisconnect    bool
	TxRollback         bool
	SerializationConflict bool
	PoolExhausted      bool
}

// Decorator wraps a Backend with fault injection.
type Decorator struct {
	inner shiftlock.Backend
	mu    sync.Mutex
	fault Fault
}

// Wrap returns a fault-injecting decorator.
func Wrap(inner shiftlock.Backend) *Decorator {
	return &Decorator{inner: inner}
}

// SetFault replaces the active fault configuration.
func (d *Decorator) SetFault(f Fault) {
	d.mu.Lock()
	d.fault = f
	d.mu.Unlock()
}

func (d *Decorator) faultCopy() Fault {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fault
}

func (d *Decorator) before(ctx context.Context) error {
	f := d.faultCopy()
	if f.PoolExhausted {
		return &shiftlock.Error{Op: "faultinject", Err: shiftlock.ErrBackend, Message: "pool exhausted"}
	}
	if f.Unavailable {
		return &shiftlock.Error{Op: "faultinject", Err: shiftlock.ErrBackend, Message: "unavailable"}
	}
	if f.Timeout {
		return context.DeadlineExceeded
	}
	if f.Latency > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(f.Latency):
		}
	}
	if f.SerializationConflict {
		return &shiftlock.Error{Op: "faultinject", Err: shiftlock.ErrConcurrentTransfer, Message: "serialization conflict"}
	}
	if f.TxRollback {
		return &shiftlock.Error{Op: "faultinject", Err: shiftlock.ErrBackend, Message: "tx rolled back"}
	}
	return nil
}

// afterApply handles ambiguous outcomes: op succeeded in backend, client sees error.
func (d *Decorator) afterApply(err error) error {
	f := d.faultCopy()
	if err != nil {
		return err
	}
	if f.LoseResponse {
		return &shiftlock.Error{Op: "faultinject", Err: shiftlock.ErrAmbiguous, Message: "response lost after success"}
	}
	return nil
}

func (d *Decorator) RegisterGeneration(ctx context.Context, gen shiftlock.Generation) error {
	if err := d.before(ctx); err != nil {
		return err
	}
	err := d.inner.RegisterGeneration(ctx, gen)
	return d.afterApply(err)
}

func (d *Decorator) UpdateGeneration(ctx context.Context, gen shiftlock.Generation) error {
	if err := d.before(ctx); err != nil {
		return err
	}
	return d.afterApply(d.inner.UpdateGeneration(ctx, gen))
}

func (d *Decorator) GetGeneration(ctx context.Context, id string) (*shiftlock.Generation, error) {
	if d.faultCopy().StaleRead {
		// Return whatever inner has without refresh semantics
	}
	return d.inner.GetGeneration(ctx, id)
}

func (d *Decorator) GetClaim(ctx context.Context, name string) (*shiftlock.ClaimRecord, error) {
	return d.inner.GetClaim(ctx, name)
}

func (d *Decorator) AcquireClaim(ctx context.Context, req shiftlock.AcquireRequest) (*shiftlock.ClaimRecord, error) {
	if err := d.before(ctx); err != nil {
		return nil, err
	}
	rec, err := d.inner.AcquireClaim(ctx, req)
	if err2 := d.afterApply(err); err2 != nil {
		// Ambiguous: success may have occurred — caller must re-read / use OperationID.
		return nil, err2
	}
	if d.faultCopy().DuplicateResponse && rec != nil {
		_, _ = d.inner.AcquireClaim(ctx, req) // idempotent if OperationID set
	}
	return rec, nil
}

func (d *Decorator) RenewClaim(ctx context.Context, req shiftlock.RenewRequest) (*shiftlock.ClaimRecord, error) {
	if err := d.before(ctx); err != nil {
		return nil, err
	}
	rec, err := d.inner.RenewClaim(ctx, req)
	return rec, d.afterApply(err)
}

func (d *Decorator) PrepareTransfer(ctx context.Context, req shiftlock.TransferRequest) (*shiftlock.ClaimRecord, error) {
	if err := d.before(ctx); err != nil {
		return nil, err
	}
	rec, err := d.inner.PrepareTransfer(ctx, req)
	return rec, d.afterApply(err)
}

func (d *Decorator) CommitTransfer(ctx context.Context, req shiftlock.CommitRequest) (*shiftlock.ClaimRecord, error) {
	if err := d.before(ctx); err != nil {
		return nil, err
	}
	rec, err := d.inner.CommitTransfer(ctx, req)
	f := d.faultCopy()
	if err == nil && f.CommitThenFail {
		return nil, &shiftlock.Error{Op: "faultinject", Err: shiftlock.ErrAmbiguous, Message: "commit succeeded then client failure"}
	}
	return rec, d.afterApply(err)
}

func (d *Decorator) AbortTransfer(ctx context.Context, req shiftlock.AbortRequest) (*shiftlock.ClaimRecord, error) {
	if err := d.before(ctx); err != nil {
		return nil, err
	}
	rec, err := d.inner.AbortTransfer(ctx, req)
	return rec, d.afterApply(err)
}

func (d *Decorator) ReleaseClaim(ctx context.Context, req shiftlock.ReleaseRequest) error {
	if err := d.before(ctx); err != nil {
		return err
	}
	return d.afterApply(d.inner.ReleaseClaim(ctx, req))
}

func (d *Decorator) WatchClaim(ctx context.Context, claimName string) (<-chan shiftlock.ClaimEvent, error) {
	if d.faultCopy().WatchDisconnect {
		ch := make(chan shiftlock.ClaimEvent)
		close(ch)
		return ch, nil
	}
	return d.inner.WatchClaim(ctx, claimName)
}

func (d *Decorator) Close() error { return d.inner.Close() }

// Capabilities forwards if available.
func (d *Decorator) Capabilities() shiftlock.Capabilities {
	if c, ok := d.inner.(shiftlock.Capabler); ok {
		return c.Capabilities()
	}
	return shiftlock.Capabilities{AtomicCAS: true}
}

var _ shiftlock.Backend = (*Decorator)(nil)
var _ shiftlock.Capabler = (*Decorator)(nil)
