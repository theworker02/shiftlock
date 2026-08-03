package shiftlock_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/kubernetes"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/internal/stategraph"
	"github.com/theworker02/shiftlock/internal/testclock"
)

func newTestCoord(t *testing.T, id string, be shiftlock.Backend, clock shiftlock.Clock) *shiftlock.Coordinator {
	t.Helper()
	c, err := shiftlock.New(shiftlock.Config{
		Service:        "test",
		InstanceID:     id,
		GenerationID:   id,
		Backend:        be,
		Clock:          clock,
		LeaseTTL:       time.Second,
		RenewInterval:  200 * time.Millisecond,
		AcquireInterval: 10 * time.Millisecond,
		TransferTimeout: time.Second,
		DrainTimeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestFencingTokenMonotonic(t *testing.T) {
	v := shiftlock.NewTokenValidator()
	if v.Accept(0) {
		t.Fatal("zero token must be rejected")
	}
	if !v.Accept(5) {
		t.Fatal("expected accept 5")
	}
	if v.Accept(4) {
		t.Fatal("stale token 4 must be rejected")
	}
	if !v.Accept(5) {
		t.Fatal("equal token must be accepted")
	}
	if !v.Accept(6) {
		t.Fatal("expected accept 6")
	}
	if v.Current() != 6 {
		t.Fatalf("current=%d", v.Current())
	}
}

func TestFencingTokenConcurrent(t *testing.T) {
	v := shiftlock.NewTokenValidator()
	var wg sync.WaitGroup
	var accepted atomic.Uint64
	for i := 1; i <= 1000; i++ {
		wg.Add(1)
		go func(tok shiftlock.FencingToken) {
			defer wg.Done()
			if v.Accept(tok) {
				accepted.Add(1)
			}
		}(shiftlock.FencingToken(i))
	}
	wg.Wait()
	if v.Current() != 1000 {
		t.Fatalf("current=%d want 1000", v.Current())
	}
	if accepted.Load() == 0 {
		t.Fatal("expected some accepts")
	}
}

func TestOnlyOneOwner(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()

	a := newTestCoord(t, "gen-a", be, clock)
	b := newTestCoord(t, "gen-b", be, clock)

	ctx := context.Background()
	ca, err := a.Claim(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := ca.WaitForOwnership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	token := lease.FencingToken()
	if token == 0 {
		t.Fatal("expected non-zero token")
	}

	cb, err := b.Claim(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	_, err = be.AcquireClaim(ctx, shiftlock.AcquireRequest{
		ClaimName: "worker", GenerationID: "gen-b", TTL: time.Second,
	})
	if !errors.Is(err, shiftlock.ErrClaimHeld) {
		t.Fatalf("expected ErrClaimHeld, got %v", err)
	}
	own := cb.Ownership()
	_ = own
	if ca.Ownership().OwnerGeneration != "gen-a" {
		t.Fatalf("owner=%s", ca.Ownership().OwnerGeneration)
	}
}

func TestStaleCannotRelease(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()
	ctx := context.Background()

	rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
		ClaimName: "c", GenerationID: "old", TTL: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldToken := rec.FencingToken

	// Simulate handoff: release and new acquire (new token).
	if err := be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{
		ClaimName: "c", GenerationID: "old", Token: oldToken,
	}); err != nil {
		t.Fatal(err)
	}
	rec2, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
		ClaimName: "c", GenerationID: "new", TTL: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rec2.FencingToken.Less(oldToken) && rec2.FencingToken <= oldToken {
		t.Fatalf("token did not advance: old=%d new=%d", oldToken, rec2.FencingToken)
	}

	// Stale release must fail.
	err = be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{
		ClaimName: "c", GenerationID: "old", Token: oldToken,
	})
	if !errors.Is(err, shiftlock.ErrStaleToken) {
		t.Fatalf("expected ErrStaleToken, got %v", err)
	}
	got, _ := be.GetClaim(ctx, "c")
	if got.OwnerGeneration != "new" {
		t.Fatalf("owner corrupted: %s", got.OwnerGeneration)
	}
}

func TestTokensNeverDecrease(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()
	ctx := context.Background()

	var last shiftlock.FencingToken
	for i := 0; i < 20; i++ {
		id := "g" + string(rune('a'+i%5))
		// expire previous
		if i > 0 {
			be.ForceExpire("job")
		}
		rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
			ClaimName: "job", GenerationID: id, TTL: time.Second,
		})
		if err != nil {
			// may be held — force expire
			be.ForceExpire("job")
			rec, err = be.AcquireClaim(ctx, shiftlock.AcquireRequest{
				ClaimName: "job", GenerationID: id, TTL: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		if rec.FencingToken < last {
			t.Fatalf("token decreased: %d -> %d", last, rec.FencingToken)
		}
		last = rec.FencingToken
		_ = be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{
			ClaimName: "job", GenerationID: id, Token: rec.FencingToken,
		})
	}
}

func TestHandoffCommitRetiresOwner(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()

	a := newTestCoord(t, "gen-a", be, clock)
	b := newTestCoord(t, "gen-b", be, clock)
	ctx := context.Background()

	ca, _ := a.Claim(ctx, "billing")
	lease, err := ca.WaitForOwnership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldTok := lease.FencingToken()

	h, err := a.PrepareHandoff(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.Transfer(ctx, "gen-b"); err != nil {
		t.Fatal(err)
	}
	if err := h.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if a.Generation().State != shiftlock.StateRetired {
		t.Fatalf("state=%s", a.Generation().State)
	}
	if lease.Valid() {
		t.Fatal("old lease should be revoked")
	}

	cb, _ := b.Claim(ctx, "billing")
	// After commit, gen-b owns — WaitForOwnership or acquire
	rec, err := be.GetClaim(ctx, "billing")
	if err != nil {
		t.Fatal(err)
	}
	if rec.OwnerGeneration != "gen-b" {
		t.Fatalf("owner=%s", rec.OwnerGeneration)
	}
	if rec.FencingToken <= oldTok {
		t.Fatalf("token not advanced: %d <= %d", rec.FencingToken, oldTok)
	}
	_ = cb
}

func TestHandoffAbortKeepsOwnership(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()

	a := newTestCoord(t, "gen-a", be, clock)
	ctx := context.Background()
	ca, _ := a.Claim(ctx, "billing")
	lease, err := ca.WaitForOwnership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tok := lease.FencingToken()

	h, _ := a.PrepareHandoff(ctx)
	_ = h.Drain(ctx)
	_ = h.Transfer(ctx, "gen-b")
	if err := h.Abort(ctx); err != nil {
		t.Fatal(err)
	}

	rec, _ := be.GetClaim(ctx, "billing")
	if rec.OwnerGeneration != "gen-a" {
		t.Fatalf("owner=%s after abort", rec.OwnerGeneration)
	}
	if rec.FencingToken != tok {
		t.Fatalf("token changed on abort: %d vs %d", rec.FencingToken, tok)
	}
	if rec.Phase != shiftlock.ClaimOwned {
		t.Fatalf("phase=%s", rec.Phase)
	}
	if a.Generation().State != shiftlock.StateActive {
		t.Fatalf("state=%s want active", a.Generation().State)
	}
	if !lease.Valid() {
		t.Fatal("lease must remain Valid after Abort restores ownership")
	}

	// Advance past LeaseTTL in small steps so renewals can extend before expiry.
	for i := 0; i < 20; i++ {
		clock.Advance(100 * time.Millisecond)
		time.Sleep(5 * time.Millisecond)
	}
	got, err := be.GetClaim(ctx, "billing")
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerGeneration != "gen-a" || got.Phase != shiftlock.ClaimOwned {
		t.Fatalf("ownership lost after TTL without renewals: %+v", got)
	}
	if !lease.Valid() {
		t.Fatal("lease should still be Valid after post-Abort renewals")
	}
}

func TestCloseStopsGoroutines(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()

	c := newTestCoord(t, "gen-x", be, clock)
	ctx := context.Background()
	cl, _ := c.Claim(ctx, "w")
	_, err := cl.WaitForOwnership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// Second close idempotent
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = c.Claim(ctx, "other")
	if !errors.Is(err, shiftlock.ErrClosed) {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
}

func TestConcurrentAcquire(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()
	ctx := context.Background()

	const n = 200
	var wg sync.WaitGroup
	var winners atomic.Int64
	tokens := make(chan shiftlock.FencingToken, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "g-" + string(rune('A'+(i%26))) + "-" + itoa(i)
			rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
				ClaimName: "hot", GenerationID: id, TTL: time.Minute,
			})
			if err == nil {
				winners.Add(1)
				tokens <- rec.FencingToken
			}
		}(i)
	}
	wg.Wait()
	close(tokens)
	if winners.Load() != 1 {
		t.Fatalf("winners=%d want 1", winners.Load())
	}
	var tok shiftlock.FencingToken
	for tkn := range tokens {
		tok = tkn
	}
	rec, _ := be.GetClaim(ctx, "hot")
	if rec.FencingToken != tok {
		t.Fatalf("token mismatch")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [16]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}

func TestStateTransitions(t *testing.T) {
	cases := []struct {
		from, to shiftlock.GenerationState
		ok       bool
	}{
		{shiftlock.StateJoining, shiftlock.StateStandby, true},
		{shiftlock.StateStandby, shiftlock.StateActive, true},
		{shiftlock.StateActive, shiftlock.StateDraining, true},
		{shiftlock.StateDraining, shiftlock.StateTransferring, true},
		{shiftlock.StateTransferring, shiftlock.StateRetired, true},
		{shiftlock.StateTransferring, shiftlock.StateActive, true},
		{shiftlock.StateRetired, shiftlock.StateActive, false},
		{shiftlock.StateFailed, shiftlock.StateStandby, false},
		{shiftlock.StateActive, shiftlock.StateJoining, false},
	}
	for _, tc := range cases {
		got := stategraph.CanTransition(stategraph.State(tc.from), stategraph.State(tc.to))
		if got != tc.ok {
			t.Fatalf("%s->%s: got %v want %v", tc.from, tc.to, got, tc.ok)
		}
	}
}

func TestDrainGroup(t *testing.T) {
	g := shiftlock.NewDrainGroup(2)
	done, err := g.BeginNamed("op")
	if err != nil {
		t.Fatal(err)
	}
	g.StartDrain()
	_, err = g.Begin()
	if !errors.Is(err, shiftlock.ErrDraining) {
		t.Fatalf("got %v", err)
	}
	finished := make(chan struct{})
	go func() {
		_ = g.Wait(context.Background())
		close(finished)
	}()
	select {
	case <-finished:
		t.Fatal("wait returned too early")
	case <-time.After(20 * time.Millisecond):
	}
	done()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("wait stuck")
	}
}

func TestReadiness(t *testing.T) {
	clock := testclock.New(time.Unix(0, 0))
	var calls int
	r := shiftlock.Readiness{
		Clock:   clock,
		Timeout: time.Second,
		Gates: []shiftlock.Gate{
			{Name: "ok", Check: func(ctx context.Context) error { calls++; return nil }},
			{Name: "opt", Optional: true, Check: func(ctx context.Context) error { return errors.New("nope") }},
		},
	}
	rep, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed {
		t.Fatal("expected pass")
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestPolicyValidation(t *testing.T) {
	be := memory.New()
	defer be.Close()
	_, err := shiftlock.New(shiftlock.Config{
		Service: "s", InstanceID: "i", Backend: be,
		LeaseTTL: 10 * time.Millisecond, RenewInterval: 20 * time.Millisecond,
		Policy: shiftlock.Policy{MinLeaseTTL: time.Millisecond, RequireRenewBelowTTL: true},
	})
	if !errors.Is(err, shiftlock.ErrPolicy) {
		t.Fatalf("expected policy error, got %v", err)
	}
}

func TestPartitionBlocksMutations(t *testing.T) {
	be := memory.New(memory.WithPartitionSimulation())
	defer be.Close()
	ctx := context.Background()
	_, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
		ClaimName: "c", GenerationID: "g", TTL: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	be.SetPartition(true)
	err = be.ReleaseClaim(ctx, shiftlock.ReleaseRequest{
		ClaimName: "c", GenerationID: "g", Token: 1,
	})
	if !errors.Is(err, shiftlock.ErrBackend) {
		t.Fatalf("got %v", err)
	}
}

func TestFailedCommitLeavesReservationAbortable(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()
	ctx := context.Background()

	rec, _ := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
		ClaimName: "c", GenerationID: "a", TTL: time.Second,
	})
	_, err := be.PrepareTransfer(ctx, shiftlock.TransferRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b",
		Token: rec.FencingToken, TTL: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	be.CrashOnCommit(true)
	_, err = be.CommitTransfer(ctx, shiftlock.CommitRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b",
		ExpectedToken: rec.FencingToken, TTL: time.Second,
	})
	if err == nil {
		t.Fatal("expected crash")
	}
	// Must still be abortable — ownership not silently discarded.
	out, err := be.AbortTransfer(ctx, shiftlock.AbortRequest{
		ClaimName: "c", FromGeneration: "a", ToGeneration: "b",
		ExpectedToken: rec.FencingToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.OwnerGeneration != "a" || out.Phase != shiftlock.ClaimOwned {
		t.Fatalf("bad abort state: %+v", out)
	}
}

func TestWorkerRun(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()
	c := newTestCoord(t, "gen-w", be, clock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var ran atomic.Bool
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Run(ctx, shiftlock.Worker{
			Name: "singleton",
			Run: func(ctx context.Context, ownership *shiftlock.Lease) error {
				ran.Store(true)
				if ownership.FencingToken() == 0 {
					return errors.New("zero token")
				}
				return nil
			},
		})
	}()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if !ran.Load() {
		t.Fatal("worker did not run")
	}
}

func TestConcurrentWaitForOwnershipNoOrphanLease(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()
	c := newTestCoord(t, "gen-conc", be, clock)
	ctx := context.Background()
	cl, err := c.Claim(ctx, "orphan")
	if err != nil {
		t.Fatal(err)
	}

	const n = 16
	leases := make([]*shiftlock.Lease, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lease, err := cl.WaitForOwnership(ctx)
			if err != nil {
				t.Errorf("WaitForOwnership: %v", err)
				return
			}
			leases[i] = lease
		}(i)
	}
	wg.Wait()

	if err := cl.Release(ctx); err != nil {
		t.Fatal(err)
	}
	for i, lease := range leases {
		if lease == nil {
			continue
		}
		if lease.Valid() {
			t.Fatalf("lease %d still Valid after Release", i)
		}
	}
}

func TestPartialTransferAbortsPreparedClaims(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()
	c := newTestCoord(t, "gen-partial", be, clock)
	ctx := context.Background()

	c1, _ := c.Claim(ctx, "alpha")
	c2, _ := c.Claim(ctx, "beta")
	if _, err := c1.WaitForOwnership(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.WaitForOwnership(ctx); err != nil {
		t.Fatal(err)
	}

	be.FailPrepareAt(2)
	h, err := c.PrepareHandoff(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	err = h.Transfer(ctx, "gen-successor")
	if err == nil {
		t.Fatal("expected Transfer failure")
	}

	for _, name := range []string{"alpha", "beta"} {
		rec, err := be.GetClaim(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Phase == shiftlock.ClaimReserved {
			t.Fatalf("claim %s left reserved without compensation: %+v", name, rec)
		}
		if rec.OwnerGeneration != "gen-partial" || rec.Phase != shiftlock.ClaimOwned {
			t.Fatalf("claim %s unexpected state after partial transfer: %+v", name, rec)
		}
	}
}

func TestAcquireThenCloseReleasesBackend(t *testing.T) {
	// Real clock required: SetDelay uses Clock.After and must elapse.
	be := memory.New()
	defer be.Close()
	c, err := shiftlock.New(shiftlock.Config{
		Service: "test", InstanceID: "gen-close-race", GenerationID: "gen-close-race",
		Backend: be, LeaseTTL: time.Second, RenewInterval: 200 * time.Millisecond,
		AcquireInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	cl, err := c.Claim(ctx, "racy")
	if err != nil {
		t.Fatal(err)
	}

	be.SetDelay(80 * time.Millisecond)

	errCh := make(chan error, 1)
	go func() {
		_, err := cl.WaitForOwnership(ctx)
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, shiftlock.ErrClosed) {
			t.Fatalf("expected ErrClosed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForOwnership did not return")
	}

	rec, err := be.GetClaim(ctx, "racy")
	if err != nil && !errors.Is(err, shiftlock.ErrClaimNotFound) {
		t.Fatal(err)
	}
	if rec != nil && rec.Phase != shiftlock.ClaimUnowned {
		t.Fatalf("backend ownership leaked after Close: %+v", rec)
	}
}

func TestPrepareTransferRejectsExpired(t *testing.T) {
	clock := testclock.New(time.Unix(1000, 0))
	be := memory.New(memory.WithClock(clock))
	defer be.Close()
	ctx := context.Background()

	rec, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
		ClaimName: "exp", GenerationID: "owner", TTL: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	be.ForceExpire("exp")
	_, err = be.PrepareTransfer(ctx, shiftlock.TransferRequest{
		ClaimName: "exp", FromGeneration: "owner", ToGeneration: "next",
		Token: rec.FencingToken, TTL: time.Second,
	})
	if !errors.Is(err, shiftlock.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for expired prepare, got %v", err)
	}
}

func TestWatchClaimConcurrentCancelClose(t *testing.T) {
	be := memory.New()
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := be.WatchClaim(ctx, "w")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		cancel()
	}()
	go func() {
		defer wg.Done()
		_ = be.Close()
	}()
	wg.Wait()
	// Drain without panic from double-close.
	for range ch {
	}
}

func TestKubernetesAcquireConflictRetries(t *testing.T) {
	client := kubernetes.NewMemoryLeaseClient()
	be := kubernetes.New(client, "default")
	defer be.Close()
	ctx := context.Background()

	const n = 40
	var wg sync.WaitGroup
	var wins atomic.Int64
	var nonRetryable atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "g-" + itoa(i)
			_, err := be.AcquireClaim(ctx, shiftlock.AcquireRequest{
				ClaimName: "hot-k8s", GenerationID: id, TTL: time.Minute,
			})
			if err == nil {
				wins.Add(1)
				return
			}
			if errors.Is(err, shiftlock.ErrClaimHeld) {
				return // retryable for WaitForOwnership
			}
			nonRetryable.Add(1)
			t.Errorf("non-retryable acquire error: %v", err)
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("wins=%d want 1", wins.Load())
	}
	if nonRetryable.Load() != 0 {
		t.Fatalf("saw %d non-retryable errors (conflicts must map to ErrClaimHeld)", nonRetryable.Load())
	}
}
