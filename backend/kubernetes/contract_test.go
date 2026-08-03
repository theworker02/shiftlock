package kubernetes_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/backendtest"
	"github.com/theworker02/shiftlock/backend/kubernetes"
	"github.com/theworker02/shiftlock/shiftlockcert"
)

func TestContract(t *testing.T) {
	backendtest.RunContract(t, func(t *testing.T) shiftlock.Backend {
		return kubernetes.New(kubernetes.NewMemoryLeaseClient(), "default")
	})
}

func TestCertification(t *testing.T) {
	shiftlockcert.RunBackendSuite(t, func(t *testing.T) shiftlock.Backend {
		return kubernetes.New(kubernetes.NewMemoryLeaseClient(), "default")
	})
}

func TestCommitIdempotentLostResponse(t *testing.T) {
	be := kubernetes.New(kubernetes.NewMemoryLeaseClient(), "default")
	defer be.Close()
	ctx := context.Background()
	rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
		ClaimName: "c", GenerationID: "a", TTL: time.Minute, OperationID: "acq",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = be.PrepareTransfer(ctx, shiftlock.TransferRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b",
		Token: rec.FencingToken, TTL: time.Minute, OperationID: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := shiftlock.CommitRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b",
		ExpectedToken: rec.FencingToken, TTL: time.Minute, OperationID: "commit-1",
	}
	out1, err := be.CommitTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := be.CommitTransfer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if out1.FencingToken != out2.FencingToken {
		t.Fatalf("retry advanced token: %d vs %d", out1.FencingToken, out2.FencingToken)
	}
}

func TestOverflow(t *testing.T) {
	be := kubernetes.New(kubernetes.NewMemoryLeaseClient(), "default")
	defer be.Close()
	be.ForceSetToken("ov", shiftlock.MaxSafeFencingToken)
	_, err := be.AcquireClaim(context.Background(), shiftlock.AcquireRequest{
		ClaimName: "ov", GenerationID: "a", TTL: time.Minute,
	})
	if !errors.Is(err, shiftlock.ErrTokenOverflow) {
		t.Fatalf("got %v", err)
	}
}
