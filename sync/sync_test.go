package sync_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theworker02/shiftlock/sync"
)

func TestPreferSourceAndReject(t *testing.T) {
	src := sync.NewMemoryStore(0)
	dst := sync.NewMemoryStore(0)
	ctx := context.Background()
	_ = src.Put(ctx, sync.Record{Key: "a", Value: "1", Version: 2, UpdatedAt: time.Unix(2, 0)})
	_ = dst.Put(ctx, sync.Record{Key: "a", Value: "0", Version: 1, UpdatedAt: time.Unix(1, 0)})

	eng, err := sync.New(sync.Config{Policy: sync.PreferSource})
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.Sync(ctx, src, dst, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 {
		t.Fatalf("%+v", res)
	}
	got, ok, _ := dst.Get(ctx, "a")
	if !ok || got.Value != "1" {
		t.Fatalf("%+v", got)
	}

	rej, err := sync.New(sync.Config{Policy: sync.Reject})
	if err != nil {
		t.Fatal(err)
	}
	_ = src.Put(ctx, sync.Record{Key: "b", Value: "x", Version: 1})
	_ = dst.Put(ctx, sync.Record{Key: "b", Value: "y", Version: 1})
	// Reset cursor by using fresh engine; seed conflict only on b via new stores
	src2 := sync.NewMemoryStore(0)
	dst2 := sync.NewMemoryStore(0)
	_ = src2.Put(ctx, sync.Record{Key: "b", Value: "x", Version: 1})
	_ = dst2.Put(ctx, sync.Record{Key: "b", Value: "y", Version: 1})
	_, err = rej.Sync(ctx, src2, dst2, 10)
	if !errors.Is(err, sync.ErrConflict) {
		t.Fatalf("got %v", err)
	}
}

func TestPreferLatestAndManual(t *testing.T) {
	ctx := context.Background()
	src := sync.NewMemoryStore(0)
	dst := sync.NewMemoryStore(0)
	_ = src.Put(ctx, sync.Record{Key: "k", Value: "new", Version: 2, UpdatedAt: time.Unix(10, 0)})
	_ = dst.Put(ctx, sync.Record{Key: "k", Value: "old", Version: 1, UpdatedAt: time.Unix(1, 0)})
	eng, _ := sync.New(sync.Config{Policy: sync.PreferLatest})
	res, err := eng.Sync(ctx, src, dst, 10)
	if err != nil || res.Applied != 1 {
		t.Fatalf("%+v %v", res, err)
	}

	src = sync.NewMemoryStore(0)
	dst = sync.NewMemoryStore(0)
	_ = src.Put(ctx, sync.Record{Key: "m", Value: "1", Version: 1})
	_ = dst.Put(ctx, sync.Record{Key: "m", Value: "2", Version: 1})
	man, _ := sync.New(sync.Config{Policy: sync.Manual})
	res, err = man.Sync(ctx, src, dst, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Manual) != 1 || res.Manual[0] != "m" {
		t.Fatalf("%+v", res)
	}
}
