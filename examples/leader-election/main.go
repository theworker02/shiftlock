// Example leader-election demonstrates election Join using a memory fenced lock adapter.
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/election"
)

type memLock struct {
	mu    sync.Mutex
	owner string
	token uint64
}

func (m *memLock) Acquire(ctx context.Context, claim string) (uint64, error) {
	_ = ctx
	_ = claim
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner != "" {
		return 0, election.ErrDenied
	}
	m.token++
	m.owner = "holder"
	return m.token, nil
}

func (m *memLock) Release(ctx context.Context, claim string, token uint64) error {
	_ = ctx
	_ = claim
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner == "" || m.token != token {
		return election.ErrNotLeader
	}
	m.owner = ""
	return nil
}

func (m *memLock) Renew(ctx context.Context, claim string, token uint64) error {
	_ = ctx
	_ = claim
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner == "" || m.token != token {
		return election.ErrNotLeader
	}
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	el, err := election.Join(ctx, election.Config{
		Name:          "cluster-leader",
		ParticipantID: "node-a",
		Lock:          &memLock{},
		RenewEvery:    200 * time.Millisecond,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "join: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = el.Close(context.Background()) }()

	for ev := range el.Events() {
		fmt.Printf("event=%s leader=%s token=%d\n", ev.Type, ev.Leader, ev.Token)
		if ev.Type == election.EventLeading {
			_ = el.Resign(ctx)
		}
		if ev.Type == election.EventResigned || ev.Type == election.EventLost {
			return
		}
	}
}
