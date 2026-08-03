// Command object-store-sync demos syncing record metadata through memory
// object stores (S3-shaped Put/Get without cloud SDKs).
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/theworker02/shiftlock/resource/storage/object"
	objmem "github.com/theworker02/shiftlock/resource/storage/object/memory"
	"github.com/theworker02/shiftlock/sync"
)

// objectSource adapts an object.Store as a sync.Source of string records.
type objectSource struct {
	store  object.Store
	bucket string
}

func (s objectSource) Next(ctx context.Context, cur sync.Cursor, limit int) ([]sync.Record, sync.Cursor, error) {
	metas, err := s.store.List(ctx, s.bucket, object.ListOptions{StartAfter: cur.Position, Limit: limit})
	if err != nil {
		return nil, cur, err
	}
	var out []sync.Record
	pos := cur.Position
	for _, m := range metas {
		_, body, err := s.store.Get(ctx, s.bucket, m.Key, object.GetOptions{})
		if err != nil {
			return nil, cur, err
		}
		out = append(out, sync.Record{
			Key: m.Key, Value: string(body), Version: m.Generation, UpdatedAt: m.UpdatedAt,
		})
		pos = m.Key
	}
	return out, sync.Cursor{Position: pos, Updated: time.Now().UTC()}, nil
}

// objectTarget adapts an object.Store as a sync.Target.
type objectTarget struct {
	store  object.Store
	bucket string
}

func (t objectTarget) Get(ctx context.Context, key string) (sync.Record, bool, error) {
	meta, body, err := t.store.Get(ctx, t.bucket, key, object.GetOptions{})
	if err != nil {
		if errors.Is(err, object.ErrNotFound) {
			return sync.Record{}, false, nil
		}
		return sync.Record{}, false, err
	}
	return sync.Record{Key: key, Value: string(body), Version: meta.Generation, UpdatedAt: meta.UpdatedAt}, true, nil
}

func (t objectTarget) Put(ctx context.Context, rec sync.Record) error {
	_, err := t.store.Put(ctx, t.bucket, rec.Key, []byte(rec.Value), object.PutOptions{
		IdempotencyKey: fmt.Sprintf("%s:%d", rec.Key, rec.Version),
	})
	return err
}

func main() {
	ctx := context.Background()
	srcStore := objmem.NewStore(objmem.Config{})
	dstStore := objmem.NewStore(objmem.Config{})

	_, _ = srcStore.Put(ctx, "edge", "cfg/app", []byte(`{"mode":"online"}`), object.PutOptions{})
	_, _ = srcStore.Put(ctx, "edge", "cfg/limits", []byte(`{"rps":100}`), object.PutOptions{})
	_, _ = dstStore.Put(ctx, "edge", "cfg/app", []byte(`{"mode":"offline"}`), object.PutOptions{})

	eng, err := sync.New(sync.Config{Policy: sync.PreferSource})
	if err != nil {
		log.Fatal(err)
	}
	res, err := eng.Sync(ctx, objectSource{store: srcStore, bucket: "edge"}, objectTarget{store: dstStore, bucket: "edge"}, 100)
	if err != nil {
		log.Fatal(err)
	}
	got, ok, _ := objectTarget{store: dstStore, bucket: "edge"}.Get(ctx, "cfg/app")
	if !ok || got.Value != `{"mode":"online"}` {
		log.Fatalf("expected prefer-source online, got %+v", got)
	}
	fmt.Println("applied:", res.Applied, "conflicts:", res.Conflicts, "dst objects:", dstStore.Len())
	fmt.Println("object-store-sync OK")
}
