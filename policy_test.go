package shiftlock_test

import (
	"errors"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
)

func TestPolicyTable(t *testing.T) {
	be := memory.New()
	defer be.Close()

	cases := []struct {
		name    string
		cfg     shiftlock.Config
		wantErr error
	}{
		{
			name: "ok",
			cfg: shiftlock.Config{
				Service: "s", InstanceID: "i", Backend: be,
				LeaseTTL: time.Second, RenewInterval: 200 * time.Millisecond,
			},
		},
		{
			name: "missing service",
			cfg: shiftlock.Config{
				InstanceID: "i", Backend: be, LeaseTTL: time.Second, RenewInterval: 100 * time.Millisecond,
			},
			wantErr: shiftlock.ErrPolicy,
		},
		{
			name: "renew too slow",
			cfg: shiftlock.Config{
				Service: "s", InstanceID: "i", Backend: be,
				LeaseTTL: time.Second, RenewInterval: time.Second,
				Policy: shiftlock.Policy{RequireRenewBelowTTL: true, MinLeaseTTL: time.Millisecond},
			},
			wantErr: shiftlock.ErrPolicy,
		},
		{
			name: "ttl too small",
			cfg: shiftlock.Config{
				Service: "s", InstanceID: "i", Backend: be,
				LeaseTTL: time.Millisecond, RenewInterval: time.Microsecond,
				Policy: shiftlock.Policy{MinLeaseTTL: 10 * time.Millisecond},
			},
			wantErr: shiftlock.ErrPolicy,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := shiftlock.New(tc.cfg)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatal(err)
				}
				_ = c.Close()
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v want %v", err, tc.wantErr)
			}
		})
	}
}
