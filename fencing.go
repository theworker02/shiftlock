package shiftlock

import (
	"sync/atomic"
)

// FencingToken is a monotonically increasing ownership epoch.
// Callers MUST reject work associated with a token older than the
// newest accepted token for a given claim.
type FencingToken uint64

// Zero reports whether the token is unset.
func (t FencingToken) Zero() bool { return t == 0 }

// Less reports whether t is strictly older than other.
func (t FencingToken) Less(other FencingToken) bool { return t < other }

// TokenValidator rejects stale fencing tokens.
// Accept is safe for concurrent use.
type TokenValidator struct {
	current atomic.Uint64
}

// NewTokenValidator returns a validator that initially accepts any non-zero token.
func NewTokenValidator() *TokenValidator {
	return &TokenValidator{}
}

// Current returns the newest accepted token.
func (v *TokenValidator) Current() FencingToken {
	return FencingToken(v.current.Load())
}

// Accept returns true if token is newer than or equal to the current epoch,
// and atomically advances the stored epoch when token is newer.
// A zero token is always rejected.
//
// Invariant: once Accept returns true for token T, any later call with
// token < T returns false. Tokens never decrease.
func (v *TokenValidator) Accept(token FencingToken) bool {
	if token.Zero() {
		return false
	}
	for {
		cur := v.current.Load()
		if uint64(token) < cur {
			return false
		}
		if uint64(token) == cur {
			return true
		}
		if v.current.CompareAndSwap(cur, uint64(token)) {
			return true
		}
	}
}

/*
CAS SQL pattern for atomic fencing-token advance (PostgreSQL):

	UPDATE claims
	SET fencing_token = fencing_token + 1,
	    owner_generation = $1,
	    updated_at = now()
	WHERE name = $2
	  AND fencing_token = $3 -- expected current token
	RETURNING fencing_token;

If zero rows are updated, another writer advanced the token; the caller
must abort and re-read. Lease timeout alone is insufficient because a
partitioned owner can still believe it holds the lease and mutate shared
resources after a successor has already taken over. Fencing tokens give
resource managers a linearizable epoch so stale writers can be rejected
even when their local lease clock has not yet expired.
*/
