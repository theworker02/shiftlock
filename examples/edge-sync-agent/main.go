// Command edge-sync-agent stubs offline buffering and reconnect sync using
// the sync package memory stores and conflict policies.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/theworker02/shiftlock/sync"
)

func main() {
	ctx := context.Background()
	local := sync.NewMemoryStore(256)
	remote := sync.NewMemoryStore(256)

	// Offline writes accumulate locally.
	offline := true
	_ = local.Put(ctx, sync.Record{Key: "device/config", Value: "v1", Version: 1, UpdatedAt: time.Unix(1, 0)})
	_ = local.Put(ctx, sync.Record{Key: "device/cache", Value: "warm", Version: 1, UpdatedAt: time.Unix(2, 0)})
	fmt.Println("offline buffered:", local.Len())

	// Remote has a conflicting older/newer value for one key.
	_ = remote.Put(ctx, sync.Record{Key: "device/config", Value: "v0", Version: 1, UpdatedAt: time.Unix(0, 0)})

	offline = false
	fmt.Println("reconnect:", !offline)

	eng, err := sync.New(sync.Config{Policy: sync.PreferLatest})
	if err != nil {
		log.Fatal(err)
	}
	res, err := eng.Sync(ctx, local, remote, 100)
	if err != nil {
		log.Fatal(err)
	}
	cfg, ok, _ := remote.Get(ctx, "device/config")
	if !ok || cfg.Value != "v1" {
		log.Fatalf("expected prefer-latest v1, got %+v", cfg)
	}
	fmt.Println("applied:", res.Applied, "conflicts:", res.Conflicts, "cursor:", res.Cursor.Position)
	fmt.Println("remote size:", remote.Len())
	fmt.Println("edge-sync-agent OK")
}
