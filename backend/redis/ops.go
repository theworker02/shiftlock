package redis

import (
	"context"
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

func (b *Backend) opKey(id shiftlock.OperationID) string {
	return fmt.Sprintf("%s:op:%s", b.prefix, string(id))
}

type storedOp struct {
	OK      bool                   `json:"ok"`
	ErrCode string                 `json:"err,omitempty"`
	Claim   *shiftlock.ClaimRecord `json:"claim,omitempty"`
}

func (b *Backend) recallOp(ctx context.Context, id shiftlock.OperationID) (*shiftlock.ClaimRecord, error, bool) {
	if id.Empty() {
		return nil, nil, false
	}
	s, err := b.client.Get(ctx, b.opKey(id))
	if err != nil || s == "" {
		return nil, nil, false
	}
	var stored storedOp
	if err := json.Unmarshal([]byte(s), &stored); err != nil {
		return nil, &shiftlock.Error{Op: "redis.recallOp", Err: shiftlock.ErrBackend, Message: err.Error()}, true
	}
	if !stored.OK {
		return stored.Claim, decodeRedisOpErr(stored.ErrCode), true
	}
	if stored.Claim != nil {
		cp := *stored.Claim
		return &cp, nil, true
	}
	return nil, nil, true
}

func (b *Backend) storeOp(ctx context.Context, id shiftlock.OperationID, rec *shiftlock.ClaimRecord, opErr error) {
	if id.Empty() {
		return
	}
	s := storedOp{OK: opErr == nil}
	if opErr != nil {
		s.ErrCode = encodeRedisOpErr(opErr)
	}
	if rec != nil {
		cp := *rec
		s.Claim = &cp
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return
	}
	// Best-effort durable recall; empty TTL = persistent.
	_ = b.client.Set(ctx, b.opKey(id), string(raw), 0)
}

func encodeRedisOpErr(err error) string {
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

func decodeRedisOpErr(code string) error {
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

var _ shiftlock.Capabler = (*Backend)(nil)
