package shiftlockcert

import (
	"testing"

	"github.com/theworker02/shiftlock/resource"
	cachemem "github.com/theworker02/shiftlock/resource/cache/memory"
	"github.com/theworker02/shiftlock/resource/memory"
	"github.com/theworker02/shiftlock/resource/queue"
	"github.com/theworker02/shiftlock/resource/ratelimit"
	"github.com/theworker02/shiftlock/resource/resourcetest"
)

// RunResourceAdapterSuite is the Phase 7 resource adapter certification entrypoint.
// It delegates to resource/resourcetest and expands coverage via RunMemoryAdapterSuites.
func RunResourceAdapterSuite(t *testing.T, factory func(t *testing.T) resource.Resource) {
	t.Helper()
	resourcetest.RunAdapterSuite(t, factory)
}

// RunMemoryAdapterSuites certifies in-process memory adapters shipped with core.
func RunMemoryAdapterSuites(t *testing.T) {
	t.Helper()
	t.Run("memory-worker", func(t *testing.T) {
		RunResourceAdapterSuite(t, func(t *testing.T) resource.Resource {
			return memory.Worker("cert", "suite", "worker")
		})
	})
	t.Run("memory-cache", func(t *testing.T) {
		RunResourceAdapterSuite(t, func(t *testing.T) resource.Resource {
			r, err := cachemem.New(cachemem.Config{
				ID: resource.MustParseResourceID("cache/cert/suite/index"),
			})
			if err != nil {
				t.Fatal(err)
			}
			return r
		})
	})
	t.Run("memory-queue", func(t *testing.T) {
		RunResourceAdapterSuite(t, func(t *testing.T) resource.Resource {
			r, err := queue.New(queue.Config{
				ID:      resource.MustParseResourceID("queue/cert/suite/events"),
				Backend: queue.NewMemory(16),
			})
			if err != nil {
				t.Fatal(err)
			}
			return r
		})
	})
	t.Run("memory-ratelimit", func(t *testing.T) {
		RunResourceAdapterSuite(t, func(t *testing.T) resource.Resource {
			r, err := ratelimit.New(ratelimit.Config{
				ID:       resource.MustParseResourceID("rate-limit/cert/suite/api"),
				Capacity: 10,
			})
			if err != nil {
				t.Fatal(err)
			}
			return r
		})
	})
}
