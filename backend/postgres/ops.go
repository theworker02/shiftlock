package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/theworker02/shiftlock"
)

// Capabilities implements shiftlock.Capabler.
func (b *Backend) Capabilities() shiftlock.Capabilities {
	return shiftlock.Capabilities{
		AtomicCAS:           true,
		IdempotentMutations: true,
		WatchSupported:      true,
		DurableStorage:      true,
		ExpireBeforeMutate:  true,
		RenewDuringReserved: true,
		GlobalExclusive:     true,
		MaxFencingToken:     shiftlock.MaxSafeFencingToken,
	}
}

type storedOp struct {
	OK      bool                   `json:"ok"`
	ErrCode string                 `json:"err,omitempty"`
	Claim   *shiftlock.ClaimRecord `json:"claim,omitempty"`
}

func (b *Backend) recallOp(ctx context.Context, tx *sql.Tx, id shiftlock.OperationID) (*shiftlock.ClaimRecord, error, bool) {
	if id.Empty() {
		return nil, nil, false
	}
	q := fmt.Sprintf(`SELECT result_json FROM %s WHERE op_id=$1`, b.t("ops"))
	var raw string
	err := tx.QueryRowContext(ctx, q, string(id)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false
	}
	if err != nil {
		return nil, mapErr("recallOp", err), true
	}
	var s storedOp
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, mapErr("recallOp", err), true
	}
	if !s.OK {
		return s.Claim, decodeOpErr(s.ErrCode), true
	}
	if s.Claim != nil {
		cp := *s.Claim
		return &cp, nil, true
	}
	return nil, nil, true
}

func (b *Backend) storeOp(ctx context.Context, tx *sql.Tx, id shiftlock.OperationID, rec *shiftlock.ClaimRecord, opErr error) error {
	if id.Empty() {
		return nil
	}
	s := storedOp{OK: opErr == nil, Claim: rec}
	if opErr != nil {
		s.ErrCode = encodeOpErr(opErr)
		if rec != nil {
			cp := *rec
			s.Claim = &cp
		}
	} else if rec != nil {
		cp := *rec
		s.Claim = &cp
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`INSERT INTO %s (op_id, result_json) VALUES ($1,$2)
		ON CONFLICT (op_id) DO NOTHING`, b.t("ops"))
	_, err = tx.ExecContext(ctx, q, string(id), string(raw))
	return err
}

func encodeOpErr(err error) string {
	switch {
	case errors.Is(err, shiftlock.ErrClaimHeld):
		return "held"
	case errors.Is(err, shiftlock.ErrStaleToken):
		return "stale"
	case errors.Is(err, shiftlock.ErrNotOwner):
		return "not_owner"
	case errors.Is(err, shiftlock.ErrNoTransfer):
		return "no_transfer"
	case errors.Is(err, shiftlock.ErrConcurrentTransfer):
		return "concurrent"
	case errors.Is(err, shiftlock.ErrTokenOverflow):
		return "overflow"
	case errors.Is(err, shiftlock.ErrClaimNotFound):
		return "not_found"
	default:
		return "backend"
	}
}

func decodeOpErr(code string) error {
	switch code {
	case "held":
		return shiftlock.ErrClaimHeld
	case "stale":
		return shiftlock.ErrStaleToken
	case "not_owner":
		return shiftlock.ErrNotOwner
	case "no_transfer":
		return shiftlock.ErrNoTransfer
	case "concurrent":
		return shiftlock.ErrConcurrentTransfer
	case "overflow":
		return shiftlock.ErrTokenOverflow
	case "not_found":
		return shiftlock.ErrClaimNotFound
	case "":
		return nil
	default:
		return shiftlock.ErrBackend
	}
}
