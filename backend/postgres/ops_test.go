package postgres

import (
	"errors"
	"testing"

	"github.com/theworker02/shiftlock"
)

func TestEncodeDecodeOpErr(t *testing.T) {
	cases := []error{
		shiftlock.ErrClaimHeld,
		shiftlock.ErrStaleToken,
		shiftlock.ErrNotOwner,
		shiftlock.ErrNoTransfer,
		shiftlock.ErrConcurrentTransfer,
		shiftlock.ErrTokenOverflow,
		shiftlock.ErrClaimNotFound,
	}
	for _, want := range cases {
		code := encodeOpErr(want)
		got := decodeOpErr(code)
		if !errors.Is(got, want) {
			t.Fatalf("%v: code=%q got=%v", want, code, got)
		}
	}
}
