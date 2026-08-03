package memory_test

import (
	"testing"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/shiftlockcert"
)

func TestCertification(t *testing.T) {
	shiftlockcert.RunBackendSuite(t, func(t *testing.T) shiftlock.Backend {
		return memory.New()
	})
}
