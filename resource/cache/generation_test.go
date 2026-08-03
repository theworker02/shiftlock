package cache_test

import (
	"context"
	"testing"

	"github.com/theworker02/shiftlock/resource"
	"github.com/theworker02/shiftlock/resource/cache"
	cachemem "github.com/theworker02/shiftlock/resource/cache/memory"
)

func TestGenerationFlow(t *testing.T) {
	id := resource.MustParseResourceID("cache/dev/app/index")
	mem, err := cachemem.New(cachemem.Config{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	mem.Set("old", "1")
	reg := resource.NewRegistry(resource.RegistryConfig{})
	defer reg.Close()
	if _, err := reg.Register(mem, resource.Metadata{}); err != nil {
		t.Fatal(err)
	}

	res, err := cache.RunGenerationFlow(context.Background(), mem, cache.FlowOptions{
		ResourceID: id,
		Build: func(ctx context.Context, gen uint64) (map[string]string, error) {
			return map[string]string{"new": "2"}, nil
		},
		Verify: func(ctx context.Context, gen uint64, seed map[string]string) error {
			if seed["new"] != "2" {
				t.Fatal("bad seed")
			}
			return nil
		},
		Epochs: reg,
		Retire: func(ctx context.Context, previous uint64) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Activated != 1 || mem.Generation() != 1 {
		t.Fatalf("%+v gen=%d", res, mem.Generation())
	}
	if _, ok := mem.Get("old"); ok {
		t.Fatal("old key should be retired with activate clear")
	}
	if v, ok := mem.Get("new"); !ok || v != "2" {
		t.Fatal("missing new")
	}

	dry, err := cache.RunGenerationFlow(context.Background(), mem, cache.FlowOptions{
		DryRun: true,
		Build:  func(ctx context.Context, gen uint64) (map[string]string, error) { return map[string]string{"x": "1"}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Activated != 0 || mem.Generation() != 1 {
		t.Fatalf("dry-run mutated: %+v", dry)
	}
}
