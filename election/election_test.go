package election_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/theworker02/shiftlock/election"
)

type memLock struct {
	mu    sync.Mutex
	owner string
	tok   uint64
}

func (m *memLock) Acquire(ctx context.Context, claim string) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner != "" {
		return 0, election.ErrDenied
	}
	m.tok++
	m.owner = claim
	return m.tok, nil
}

func (m *memLock) Release(ctx context.Context, claim string, token uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner == claim && m.tok == token {
		m.owner = ""
	}
	return nil
}

func (m *memLock) Renew(ctx context.Context, claim string, token uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner != claim || m.tok != token {
		return election.ErrNotLeader
	}
	return nil
}

func TestJoinResign(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e, err := election.Join(ctx, election.Config{
		Name: "lead", ParticipantID: "p1", Lock: &memLock{}, RenewEvery: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for !e.IsLeader() {
		select {
		case <-deadline:
			t.Fatal("timeout becoming leader")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := e.Resign(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = e.Close(context.Background())
}
