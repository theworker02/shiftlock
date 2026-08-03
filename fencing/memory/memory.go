package memory

import (
	"errors"
	"sync"

	"github.com/theworker02/shiftlock"
)

// ErrStaleFencingToken is returned when a resource mutation carries a stale token.
var ErrStaleFencingToken = errors.New("fencing/memory: stale fencing token")

// Resource is an in-process fenced resource for tests and examples.
type Resource struct {
	mu    sync.Mutex
	token shiftlock.FencingToken
	value string
}

// NewResource creates an empty fenced resource.
func NewResource() *Resource { return &Resource{} }

// Write mutates the resource only if token is accepted by the fencing epoch.
func (r *Resource) Write(token shiftlock.FencingToken, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if token.Zero() || token < r.token {
		return ErrStaleFencingToken
	}
	r.token = token
	r.value = value
	return nil
}

// Read returns the current value and fencing token.
func (r *Resource) Read() (string, shiftlock.FencingToken) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.value, r.token
}
