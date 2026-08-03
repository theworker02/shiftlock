package shiftlockcert_test

import (
	"testing"

	"github.com/theworker02/shiftlock/shiftlockcert"
)

func TestMemoryAdapterSuites(t *testing.T) {
	shiftlockcert.RunMemoryAdapterSuites(t)
}
