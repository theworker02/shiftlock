package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/theworker02/shiftlock"
)

// Backend stores ownership in PostgreSQL using transactions and row locks.
type Backend struct {
	db     *sql.DB
	schema string
	closed bool
}

// Option configures the postgres backend.
type Option func(*Backend)

// WithSchema sets the schema name (default public).
func WithSchema(schema string) Option {
	return func(b *Backend) { b.schema = schema }
}

// New wraps an existing *sql.DB. Call Migrate to create tables (opt-in).
func New(db *sql.DB, opts ...Option) *Backend {
	b := &Backend{db: db, schema: "public"}
	for _, o := range opts {
		o(b)
	}
	return b
}

func (b *Backend) t(name string) string {
	return fmt.Sprintf("%s.shiftlock_%s", b.schema, name)
}

// Migrate creates required tables. Opt-in; not run automatically.
func (b *Backend) Migrate(ctx context.Context) error {
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id TEXT PRIMARY KEY,
			service TEXT NOT NULL,
			instance_id TEXT NOT NULL,
			state TEXT NOT NULL,
			started_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			reason TEXT
		)`, b.t("generations")),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			name TEXT PRIMARY KEY,
			owner_generation TEXT NOT NULL DEFAULT '',
			fencing_token BIGINT NOT NULL DEFAULT 0,
			phase TEXT NOT NULL DEFAULT 'unowned',
			acquired_at TIMESTAMPTZ,
			expires_at TIMESTAMPTZ,
			previous_owner TEXT NOT NULL DEFAULT '',
			pending_successor TEXT NOT NULL DEFAULT '',
			drain_status TEXT NOT NULL DEFAULT '',
			transfer_status TEXT NOT NULL DEFAULT '',
			last_heartbeat TIMESTAMPTZ,
			reason TEXT,
			version BIGINT NOT NULL DEFAULT 0
		)`, b.t("claims")),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			op_id TEXT PRIMARY KEY,
			result_json TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`, b.t("ops")),
	}
	for _, s := range stmts {
		if _, err := b.db.ExecContext(ctx, s); err != nil {
			return &shiftlock.Error{Op: "postgres.Migrate", Err: shiftlock.ErrBackend, Message: err.Error()}
		}
	}
	return nil
}

func (b *Backend) RegisterGeneration(ctx context.Context, gen shiftlock.Generation) error {
	q := fmt.Sprintf(`INSERT INTO %s (id, service, instance_id, state, started_at, updated_at, reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (id) DO UPDATE SET state=$4, updated_at=$6, reason=$7`, b.t("generations"))
	_, err := b.db.ExecContext(ctx, q, gen.ID, gen.Service, gen.InstanceID, string(gen.State),
		gen.StartedAt, gen.UpdatedAt, string(gen.Reason))
	return mapErr("RegisterGeneration", err)
}

func (b *Backend) UpdateGeneration(ctx context.Context, gen shiftlock.Generation) error {
	q := fmt.Sprintf(`UPDATE %s SET state=$2, updated_at=$3, reason=$4 WHERE id=$1`, b.t("generations"))
	_, err := b.db.ExecContext(ctx, q, gen.ID, string(gen.State), gen.UpdatedAt, string(gen.Reason))
	return mapErr("UpdateGeneration", err)
}

func (b *Backend) GetGeneration(ctx context.Context, generationID string) (*shiftlock.Generation, error) {
	q := fmt.Sprintf(`SELECT id, service, instance_id, state, started_at, updated_at, reason FROM %s WHERE id=$1`, b.t("generations"))
	row := b.db.QueryRowContext(ctx, q, generationID)
	var g shiftlock.Generation
	var state, reason string
	err := row.Scan(&g.ID, &g.Service, &g.InstanceID, &state, &g.StartedAt, &g.UpdatedAt, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, shiftlock.ErrGenerationNotFound
	}
	if err != nil {
		return nil, mapErr("GetGeneration", err)
	}
	g.State = shiftlock.GenerationState(state)
	g.Reason = shiftlock.TransitionReason(reason)
	return &g, nil
}

func (b *Backend) GetClaim(ctx context.Context, claimName string) (*shiftlock.ClaimRecord, error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, mapErr("GetClaim", err)
	}
	defer tx.Rollback()
	rec, err := b.getClaimForUpdate(ctx, tx, claimName, false)
	if err != nil {
		return nil, err
	}
	if err := b.expireIfNeeded(ctx, tx, rec); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, mapErr("GetClaim", err)
	}
	return rec, nil
}

func (b *Backend) getClaimForUpdate(ctx context.Context, tx *sql.Tx, name string, create bool) (*shiftlock.ClaimRecord, error) {
	q := fmt.Sprintf(`SELECT name, owner_generation, fencing_token, phase, acquired_at, expires_at,
		previous_owner, pending_successor, drain_status, transfer_status, last_heartbeat, reason, version
		FROM %s WHERE name=$1 FOR UPDATE`, b.t("claims"))
	row := tx.QueryRowContext(ctx, q, name)
	rec, err := scanClaim(row)
	if errors.Is(err, sql.ErrNoRows) {
		if !create {
			return nil, shiftlock.ErrClaimNotFound
		}
		ins := fmt.Sprintf(`INSERT INTO %s (name) VALUES ($1) RETURNING name, owner_generation, fencing_token, phase,
			acquired_at, expires_at, previous_owner, pending_successor, drain_status, transfer_status,
			last_heartbeat, reason, version`, b.t("claims"))
		row = tx.QueryRowContext(ctx, ins, name)
		return scanClaim(row)
	}
	return rec, err
}

type scannable interface {
	Scan(dest ...any) error
}

func scanClaim(row scannable) (*shiftlock.ClaimRecord, error) {
	var r shiftlock.ClaimRecord
	var phase, reason string
	var acquired, expires, hb sql.NullTime
	err := row.Scan(&r.Name, &r.OwnerGeneration, &r.FencingToken, &phase, &acquired, &expires,
		&r.PreviousOwner, &r.PendingSuccessor, &r.DrainStatus, &r.TransferStatus, &hb, &reason, &r.Version)
	if err != nil {
		return nil, err
	}
	r.Phase = shiftlock.ClaimPhase(phase)
	r.Reason = shiftlock.TransitionReason(reason)
	if acquired.Valid {
		r.AcquiredAt = acquired.Time
	}
	if expires.Valid {
		r.ExpiresAt = expires.Time
	}
	if hb.Valid {
		r.LastHeartbeat = hb.Time
	}
	return &r, nil
}

func (b *Backend) expireIfNeeded(ctx context.Context, tx *sql.Tx, rec *shiftlock.ClaimRecord) error {
	if rec.ExpiresAt.IsZero() || time.Now().Before(rec.ExpiresAt) {
		return nil
	}
	if rec.Phase != shiftlock.ClaimOwned && rec.Phase != shiftlock.ClaimDraining && rec.Phase != shiftlock.ClaimReserved {
		return nil
	}
	q := fmt.Sprintf(`UPDATE %s SET previous_owner=owner_generation, owner_generation='', pending_successor='',
		phase='unowned', drain_status='', transfer_status='', reason=$2, version=version+1
		WHERE name=$1`, b.t("claims"))
	_, err := tx.ExecContext(ctx, q, rec.Name, string(shiftlock.ReasonExpired))
	if err != nil {
		return mapErr("expire", err)
	}
	rec.PreviousOwner = rec.OwnerGeneration
	rec.OwnerGeneration = ""
	rec.PendingSuccessor = ""
	rec.Phase = shiftlock.ClaimUnowned
	rec.DrainStatus = ""
	rec.TransferStatus = ""
	rec.Reason = shiftlock.ReasonExpired
	rec.Version++
	return nil
}

func (b *Backend) AcquireClaim(ctx context.Context, req shiftlock.AcquireRequest) (*shiftlock.ClaimRecord, error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, mapErr("AcquireClaim", err)
	}
	defer tx.Rollback()

	if rec, err, ok := b.recallOp(ctx, tx, req.OperationID); ok {
		_ = tx.Commit()
		return rec, err
	}

	rec, err := b.getClaimForUpdate(ctx, tx, req.ClaimName, true)
	if err != nil {
		return nil, mapErr("AcquireClaim", err)
	}
	if err := b.expireIfNeeded(ctx, tx, rec); err != nil {
		return nil, err
	}

	now := time.Now()
	if rec.Phase == shiftlock.ClaimOwned || rec.Phase == shiftlock.ClaimReserved || rec.Phase == shiftlock.ClaimDraining {
		if rec.OwnerGeneration == req.GenerationID {
			q := fmt.Sprintf(`UPDATE %s SET expires_at=$2, last_heartbeat=$3, reason=$4, version=version+1
				WHERE name=$1 RETURNING name, owner_generation, fencing_token, phase, acquired_at, expires_at,
				previous_owner, pending_successor, drain_status, transfer_status, last_heartbeat, reason, version`, b.t("claims"))
			row := tx.QueryRowContext(ctx, q, req.ClaimName, now.Add(req.TTL), now, string(shiftlock.ReasonRenewed))
			out, err := scanClaim(row)
			if err != nil {
				return nil, mapErr("AcquireClaim", err)
			}
			_ = b.storeOp(ctx, tx, req.OperationID, out, nil)
			if err := tx.Commit(); err != nil {
				return nil, mapErr("AcquireClaim", err)
			}
			return out, nil
		}
		_ = b.storeOp(ctx, tx, req.OperationID, rec, shiftlock.ErrClaimHeld)
		_ = tx.Commit()
		return rec, shiftlock.ErrClaimHeld
	}

	if rec.FencingToken >= shiftlock.MaxSafeFencingToken {
		_ = b.storeOp(ctx, tx, req.OperationID, nil, shiftlock.ErrTokenOverflow)
		_ = tx.Commit()
		return nil, shiftlock.ErrTokenOverflow
	}

	q := fmt.Sprintf(`UPDATE %s SET owner_generation=$2, fencing_token=fencing_token+1, phase='owned',
		acquired_at=$3, expires_at=$4, last_heartbeat=$3, pending_successor='', transfer_status='',
		drain_status='', reason=$5, version=version+1
		WHERE name=$1 AND fencing_token < $6
		RETURNING name, owner_generation, fencing_token, phase, acquired_at, expires_at,
		previous_owner, pending_successor, drain_status, transfer_status, last_heartbeat, reason, version`, b.t("claims"))
	row := tx.QueryRowContext(ctx, q, req.ClaimName, req.GenerationID, now, now.Add(req.TTL),
		string(shiftlock.ReasonAcquired), uint64(shiftlock.MaxSafeFencingToken))
	out, err := scanClaim(row)
	if errors.Is(err, sql.ErrNoRows) {
		_ = b.storeOp(ctx, tx, req.OperationID, nil, shiftlock.ErrTokenOverflow)
		_ = tx.Commit()
		return nil, shiftlock.ErrTokenOverflow
	}
	if err != nil {
		return nil, mapErr("AcquireClaim", err)
	}
	_ = b.storeOp(ctx, tx, req.OperationID, out, nil)
	if err := tx.Commit(); err != nil {
		return nil, mapErr("AcquireClaim", err)
	}
	_, _ = b.db.ExecContext(ctx, `SELECT pg_notify('shiftlock_claims', $1)`, req.ClaimName)
	return out, nil
}

func (b *Backend) RenewClaim(ctx context.Context, req shiftlock.RenewRequest) (*shiftlock.ClaimRecord, error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, mapErr("RenewClaim", err)
	}
	defer tx.Rollback()
	if rec, err, ok := b.recallOp(ctx, tx, req.OperationID); ok {
		_ = tx.Commit()
		return rec, err
	}
	rec, err := b.getClaimForUpdate(ctx, tx, req.ClaimName, false)
	if err != nil {
		return nil, err
	}
	if err := b.expireIfNeeded(ctx, tx, rec); err != nil {
		return nil, err
	}
	if rec.OwnerGeneration != req.GenerationID {
		return nil, shiftlock.ErrNotOwner
	}
	if rec.FencingToken != req.Token {
		return nil, shiftlock.ErrStaleToken
	}
	if rec.Phase != shiftlock.ClaimOwned && rec.Phase != shiftlock.ClaimDraining && rec.Phase != shiftlock.ClaimReserved {
		return nil, shiftlock.ErrNotOwner
	}
	now := time.Now()
	q := fmt.Sprintf(`UPDATE %s SET expires_at=$2, last_heartbeat=$3, reason=$4, version=version+1
		WHERE name=$1 AND fencing_token=$5
		RETURNING name, owner_generation, fencing_token, phase, acquired_at, expires_at,
		previous_owner, pending_successor, drain_status, transfer_status, last_heartbeat, reason, version`, b.t("claims"))
	row := tx.QueryRowContext(ctx, q, req.ClaimName, now.Add(req.TTL), now, string(shiftlock.ReasonRenewed), req.Token)
	out, err := scanClaim(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, shiftlock.ErrStaleToken
	}
	if err != nil {
		return nil, mapErr("RenewClaim", err)
	}
	_ = b.storeOp(ctx, tx, req.OperationID, out, nil)
	if err := tx.Commit(); err != nil {
		return nil, mapErr("RenewClaim", err)
	}
	return out, nil
}

func (b *Backend) PrepareTransfer(ctx context.Context, req shiftlock.TransferRequest) (*shiftlock.ClaimRecord, error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, mapErr("PrepareTransfer", err)
	}
	defer tx.Rollback()
	if rec, err, ok := b.recallOp(ctx, tx, req.OperationID); ok {
		_ = tx.Commit()
		return rec, err
	}
	rec, err := b.getClaimForUpdate(ctx, tx, req.ClaimName, false)
	if err != nil {
		return nil, err
	}
	if err := b.expireIfNeeded(ctx, tx, rec); err != nil {
		return nil, err
	}
	if rec.OwnerGeneration != req.FromGeneration {
		return nil, shiftlock.ErrNotOwner
	}
	if rec.FencingToken != req.Token {
		return nil, shiftlock.ErrStaleToken
	}
	if rec.Phase == shiftlock.ClaimReserved && rec.PendingSuccessor != "" && rec.PendingSuccessor != req.ToGeneration {
		return nil, shiftlock.ErrConcurrentTransfer
	}
	if rec.Phase == shiftlock.ClaimReserved && rec.PendingSuccessor == req.ToGeneration {
		_ = b.storeOp(ctx, tx, req.OperationID, rec, nil)
		_ = tx.Commit()
		return rec, nil
	}
	now := time.Now()
	q := fmt.Sprintf(`UPDATE %s SET phase='reserved', pending_successor=$2, transfer_status='prepared',
		drain_status='complete', expires_at=$3, last_heartbeat=$4, reason=$5, version=version+1
		WHERE name=$1 AND fencing_token=$6
		RETURNING name, owner_generation, fencing_token, phase, acquired_at, expires_at,
		previous_owner, pending_successor, drain_status, transfer_status, last_heartbeat, reason, version`, b.t("claims"))
	row := tx.QueryRowContext(ctx, q, req.ClaimName, req.ToGeneration, now.Add(req.TTL), now,
		string(shiftlock.ReasonTransferPrepared), req.Token)
	out, err := scanClaim(row)
	if err != nil {
		return nil, mapErr("PrepareTransfer", err)
	}
	_ = b.storeOp(ctx, tx, req.OperationID, out, nil)
	if err := tx.Commit(); err != nil {
		return nil, mapErr("PrepareTransfer", err)
	}
	return out, nil
}

func (b *Backend) CommitTransfer(ctx context.Context, req shiftlock.CommitRequest) (*shiftlock.ClaimRecord, error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, mapErr("CommitTransfer", err)
	}
	defer tx.Rollback()
	if rec, err, ok := b.recallOp(ctx, tx, req.OperationID); ok {
		_ = tx.Commit()
		return rec, err
	}
	rec, err := b.getClaimForUpdate(ctx, tx, req.ClaimName, false)
	if err != nil {
		return nil, err
	}
	if err := b.expireIfNeeded(ctx, tx, rec); err != nil {
		return nil, err
	}
	if rec.Phase == shiftlock.ClaimOwned && rec.OwnerGeneration == req.ToGeneration &&
		rec.FencingToken == req.ExpectedToken+1 && rec.TransferStatus == "committed" {
		_ = b.storeOp(ctx, tx, req.OperationID, rec, nil)
		_ = tx.Commit()
		return rec, nil
	}
	if rec.Phase != shiftlock.ClaimReserved {
		return nil, shiftlock.ErrNoTransfer
	}
	if rec.OwnerGeneration != req.FromGeneration || rec.PendingSuccessor != req.ToGeneration {
		return nil, shiftlock.ErrConcurrentTransfer
	}
	if rec.FencingToken != req.ExpectedToken {
		return nil, shiftlock.ErrStaleToken
	}
	if rec.FencingToken >= shiftlock.MaxSafeFencingToken {
		_ = b.storeOp(ctx, tx, req.OperationID, nil, shiftlock.ErrTokenOverflow)
		_ = tx.Commit()
		return nil, shiftlock.ErrTokenOverflow
	}
	now := time.Now()
	q := fmt.Sprintf(`UPDATE %s SET previous_owner=owner_generation, owner_generation=$2,
		fencing_token=fencing_token+1, phase='owned', pending_successor='', transfer_status='committed',
		drain_status='', acquired_at=$3, expires_at=$4, last_heartbeat=$3, reason=$5, version=version+1
		WHERE name=$1 AND fencing_token=$6 AND phase='reserved'
		RETURNING name, owner_generation, fencing_token, phase, acquired_at, expires_at,
		previous_owner, pending_successor, drain_status, transfer_status, last_heartbeat, reason, version`, b.t("claims"))
	row := tx.QueryRowContext(ctx, q, req.ClaimName, req.ToGeneration, now, now.Add(req.TTL),
		string(shiftlock.ReasonTransferCommitted), req.ExpectedToken)
	out, err := scanClaim(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, shiftlock.ErrStaleToken
	}
	if err != nil {
		return nil, mapErr("CommitTransfer", err)
	}
	_ = b.storeOp(ctx, tx, req.OperationID, out, nil)
	if err := tx.Commit(); err != nil {
		return nil, mapErr("CommitTransfer", err)
	}
	_, _ = b.db.ExecContext(ctx, `SELECT pg_notify('shiftlock_claims', $1)`, req.ClaimName)
	return out, nil
}

func (b *Backend) AbortTransfer(ctx context.Context, req shiftlock.AbortRequest) (*shiftlock.ClaimRecord, error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, mapErr("AbortTransfer", err)
	}
	defer tx.Rollback()
	if rec, err, ok := b.recallOp(ctx, tx, req.OperationID); ok {
		_ = tx.Commit()
		return rec, err
	}
	rec, err := b.getClaimForUpdate(ctx, tx, req.ClaimName, false)
	if err != nil {
		return nil, err
	}
	if err := b.expireIfNeeded(ctx, tx, rec); err != nil {
		return nil, err
	}
	if rec.Phase != shiftlock.ClaimReserved {
		if rec.OwnerGeneration == req.FromGeneration && rec.Phase == shiftlock.ClaimOwned {
			_ = b.storeOp(ctx, tx, req.OperationID, rec, nil)
			_ = tx.Commit()
			return rec, nil
		}
		return nil, shiftlock.ErrNoTransfer
	}
	if rec.FencingToken != req.ExpectedToken {
		return nil, shiftlock.ErrStaleToken
	}
	if rec.OwnerGeneration != req.FromGeneration {
		return nil, shiftlock.ErrNotOwner
	}
	q := fmt.Sprintf(`UPDATE %s SET pending_successor='', phase='owned', transfer_status='aborted',
		reason=$2, version=version+1 WHERE name=$1 AND fencing_token=$3
		RETURNING name, owner_generation, fencing_token, phase, acquired_at, expires_at,
		previous_owner, pending_successor, drain_status, transfer_status, last_heartbeat, reason, version`, b.t("claims"))
	row := tx.QueryRowContext(ctx, q, req.ClaimName, string(shiftlock.ReasonTransferAborted), req.ExpectedToken)
	out, err := scanClaim(row)
	if err != nil {
		return nil, mapErr("AbortTransfer", err)
	}
	_ = b.storeOp(ctx, tx, req.OperationID, out, nil)
	if err := tx.Commit(); err != nil {
		return nil, mapErr("AbortTransfer", err)
	}
	return out, nil
}

func (b *Backend) ReleaseClaim(ctx context.Context, req shiftlock.ReleaseRequest) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return mapErr("ReleaseClaim", err)
	}
	defer tx.Rollback()
	if _, err, ok := b.recallOp(ctx, tx, req.OperationID); ok {
		_ = tx.Commit()
		return err
	}
	rec, err := b.getClaimForUpdate(ctx, tx, req.ClaimName, false)
	if err != nil {
		return err
	}
	if err := b.expireIfNeeded(ctx, tx, rec); err != nil {
		return err
	}
	if rec.FencingToken != req.Token {
		return shiftlock.ErrStaleToken
	}
	if rec.OwnerGeneration != req.GenerationID {
		if rec.Phase == shiftlock.ClaimUnowned {
			_ = b.storeOp(ctx, tx, req.OperationID, nil, nil)
			_ = tx.Commit()
			return nil
		}
		return shiftlock.ErrNotOwner
	}
	q := fmt.Sprintf(`UPDATE %s SET previous_owner=owner_generation, owner_generation='', pending_successor='',
		phase='unowned', transfer_status='', drain_status='', reason=$2, version=version+1
		WHERE name=$1 AND fencing_token=$3`, b.t("claims"))
	res, err := tx.ExecContext(ctx, q, req.ClaimName, string(shiftlock.ReasonReleased), req.Token)
	if err != nil {
		return mapErr("ReleaseClaim", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return shiftlock.ErrStaleToken
	}
	_ = b.storeOp(ctx, tx, req.OperationID, nil, nil)
	if err := tx.Commit(); err != nil {
		return mapErr("ReleaseClaim", err)
	}
	_, _ = b.db.ExecContext(ctx, `SELECT pg_notify('shiftlock_claims', $1)`, req.ClaimName)
	return nil
}

func (b *Backend) WatchClaim(ctx context.Context, claimName string) (<-chan shiftlock.ClaimEvent, error) {
	ch := make(chan shiftlock.ClaimEvent, 16)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		var lastVersion uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rec, err := b.GetClaim(ctx, claimName)
				if err != nil {
					continue
				}
				if rec.Version != lastVersion {
					lastVersion = rec.Version
					select {
					case ch <- shiftlock.ClaimEvent{Claim: *rec, Time: time.Now(), Reason: rec.Reason}:
					default:
					}
				}
			}
		}
	}()
	return ch, nil
}

func (b *Backend) Close() error {
	b.closed = true
	return nil // caller owns *sql.DB
}

func mapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return &shiftlock.Error{Op: "postgres." + op, Err: shiftlock.ErrBackend, Message: err.Error()}
}

var _ shiftlock.Backend = (*Backend)(nil)
var _ shiftlock.Capabler = (*Backend)(nil)
