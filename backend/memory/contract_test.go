package memory_test

import (
	"testing"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/backendtest"
	"github.com/theworker02/shiftlock/backend/memory"
)

func TestContract(t *testing.T) {
	backendtest.RunContract(t, func(t *testing.T) shiftlock.Backend {
		return memory.New()
	})
}
