// Command developer-tool demonstrates local-first ShiftLock usage with a
// filesystem resource and in-memory coordination (no cloud required).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/theworker02/shiftlock/resource"
	"github.com/theworker02/shiftlock/resource/cache/memory"
	"github.com/theworker02/shiftlock/resource/storage/filesystem"
)

func main() {
	root := filepath.Join(".", ".shiftlock-demo")
	_ = os.MkdirAll(root, 0o750)

	reg := resource.NewRegistry(resource.RegistryConfig{MaxResources: 32})
	defer reg.Close()

	fsRes, err := filesystem.New(filesystem.Config{
		ID: resource.ResourceID{
			Kind: resource.KindFilesystem, Environment: "local",
			Service: "devtools", Name: "workspace",
		},
		Path:     root,
		Hardened: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := fsRes.EnsureDir(); err != nil {
		log.Fatal(err)
	}
	if err := fsRes.AcquireExclusive("devtools-1"); err != nil {
		log.Fatal(err)
	}
	defer func() { _ = fsRes.ReleaseExclusive("devtools-1") }()

	cacheRes, err := memory.New(memory.Config{
		ID: resource.ResourceID{
			Kind: resource.KindCache, Environment: "local",
			Service: "devtools", Name: "index",
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	if _, err := reg.Register(fsRes, resource.Metadata{Source: "local-first"}); err != nil {
		log.Fatal(err)
	}
	if _, err := reg.Register(cacheRes, resource.Metadata{Source: "local-first"}); err != nil {
		log.Fatal(err)
	}

	if err := fsRes.AtomicReplace("manifest.json", []byte(`{"version":1}`)); err != nil {
		log.Fatal(err)
	}
	sum, err := fsRes.ChecksumSHA256("manifest.json")
	if err != nil {
		log.Fatal(err)
	}
	cacheRes.Set("manifest_sha256", sum)

	ctx := context.Background()
	fmt.Println("resources:", reg.Count())
	fmt.Println("filesystem health:", fsRes.Health(ctx).Overall)
	fmt.Println("cache generation:", cacheRes.Generation())
	fmt.Println("manifest checksum:", sum)
	fmt.Println("local-first demo OK (state under", root+")")
}
