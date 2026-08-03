package dualwrite_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/theworker02/shiftlock/migration/dualwrite"
)

func TestPrimaryFirst(t *testing.T) {
	var order []string
	h, err := dualwrite.New(dualwrite.Config{
		Primary: func(ctx context.Context, key, value, op string) error {
			order = append(order, "p")
			return nil
		},
		Secondary: func(ctx context.Context, key, value, op string) error {
			order = append(order, "s")
			return nil
		},
		Mode: dualwrite.ModePrimaryFirst,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Write(context.Background(), "k", "v", "op1")
	if err != nil || !res.PrimaryOK || !res.SecondaryOK {
		t.Fatalf("%+v %v", res, err)
	}
	if len(order) != 2 || order[0] != "p" || order[1] != "s" {
		t.Fatalf("%v", order)
	}
}

func TestFailOpenSecondary(t *testing.T) {
	h, err := dualwrite.New(dualwrite.Config{
		Primary: func(ctx context.Context, key, value, op string) error { return nil },
		Secondary: func(ctx context.Context, key, value, op string) error {
			return errors.New("down")
		},
		FailOpenSecondary: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.Write(context.Background(), "k", "v", "")
	if err != nil {
		t.Fatal(err)
	}
	if h.SecondaryErrors() != 1 {
		t.Fatal("expected secondary error count")
	}
}

func TestModeFlip(t *testing.T) {
	var sec atomic.Int32
	h, err := dualwrite.New(dualwrite.Config{
		Primary:   func(ctx context.Context, key, value, op string) error { return nil },
		Secondary: func(ctx context.Context, key, value, op string) error { sec.Add(1); return nil },
		Mode:      dualwrite.ModePrimaryFirst,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = h.SetMode(dualwrite.ModePrimaryOnly)
	_, _ = h.Write(context.Background(), "k", "v", "")
	if sec.Load() != 0 {
		t.Fatal("secondary should be skipped")
	}
}
