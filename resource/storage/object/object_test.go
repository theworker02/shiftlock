package object_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/theworker02/shiftlock/resource"
	"github.com/theworker02/shiftlock/resource/storage/object"
	objmem "github.com/theworker02/shiftlock/resource/storage/object/memory"
)

func TestMemoryPutGetListDelete(t *testing.T) {
	st := objmem.NewStore(objmem.Config{})
	ctx := context.Background()
	meta, err := st.Put(ctx, "b", "a/x", []byte("hello"), object.PutOptions{
		ContentType: "text/plain", IdempotencyKey: "op-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Checksum != object.ChecksumSHA256([]byte("hello")) {
		t.Fatalf("checksum=%s", meta.Checksum)
	}
	// Idempotent replay
	meta2, err := st.Put(ctx, "b", "a/x", []byte("hello"), object.PutOptions{IdempotencyKey: "op-1"})
	if err != nil || meta2.Generation != meta.Generation {
		t.Fatalf("idempotency: %+v %v", meta2, err)
	}
	got, body, err := st.Get(ctx, "b", "a/x", object.GetOptions{})
	if err != nil || string(body) != "hello" {
		t.Fatalf("get: %s %v", body, err)
	}
	if got.Checksum != meta.Checksum {
		t.Fatal("meta mismatch")
	}
	list, err := st.List(ctx, "b", object.ListOptions{Prefix: "a/"})
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	if err := st.Delete(ctx, "b", "a/x"); err != nil {
		t.Fatal(err)
	}
	_, _, err = st.Get(ctx, "b", "a/x", object.GetOptions{})
	if !errors.Is(err, object.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestIfNotExistsAndChecksum(t *testing.T) {
	st := objmem.NewStore(objmem.Config{})
	ctx := context.Background()
	_, err := st.Put(ctx, "", "k", []byte("v1"), object.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Put(ctx, "", "k", []byte("v2"), object.PutOptions{IfNotExists: true})
	if !errors.Is(err, object.ErrConflict) {
		t.Fatalf("got %v", err)
	}
	_, err = st.Put(ctx, "", "k", []byte("v2"), object.PutOptions{Checksum: "deadbeef"})
	if !errors.Is(err, object.ErrChecksum) {
		t.Fatalf("got %v", err)
	}
}

func TestClientBoundedConcurrency(t *testing.T) {
	st := objmem.NewStore(objmem.Config{})
	c := object.NewClient(st, 2)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "k" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			_, _ = c.Put(ctx, "b", key, []byte("x"), object.PutOptions{})
		}(i)
	}
	wg.Wait()
}

func TestResourceCapabilitiesHonest(t *testing.T) {
	id := resource.ResourceID{Kind: resource.KindObjectStore, Environment: "dev", Service: "media", Name: "blob"}
	res, _, err := objmem.NewResource(id, "blob")
	if err != nil {
		t.Fatal(err)
	}
	caps := res.Capabilities()
	if !caps.SupportsHealth || !caps.SupportsSnapshots {
		t.Fatalf("caps=%+v", caps)
	}
	if caps.SupportsFencing {
		t.Fatal("memory object store must not claim fencing without activate-manifest epoch")
	}
	h := res.Health(context.Background())
	if h.Overall != resource.HealthHealthy {
		t.Fatalf("health=%s", h.Overall)
	}
	snap, err := res.Snapshot(context.Background())
	if err != nil || snap["adapter"] != "object" {
		t.Fatalf("snap=%v err=%v", snap, err)
	}
}
