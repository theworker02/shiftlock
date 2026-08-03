// Command media-processing-pipeline demos semaphore + budget + memory queue
// coordination for a bounded media job pipeline.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/theworker02/shiftlock/budget"
	"github.com/theworker02/shiftlock/resource"
	"github.com/theworker02/shiftlock/resource/queue"
	"github.com/theworker02/shiftlock/syncprim"
	"github.com/theworker02/shiftlock/workflow"
)

// fencedSlots is a tiny in-process claim adapter for syncprim.Semaphore demos.
type fencedSlots struct {
	tokens map[string]uint64
	next   uint64
}

func (f *fencedSlots) Acquire(_ context.Context, claim string) (uint64, error) {
	if f.tokens == nil {
		f.tokens = make(map[string]uint64)
	}
	if _, ok := f.tokens[claim]; ok {
		return 0, fmt.Errorf("held")
	}
	f.next++
	f.tokens[claim] = f.next
	return f.next, nil
}

func (f *fencedSlots) Release(_ context.Context, claim string, _ uint64) error {
	delete(f.tokens, claim)
	return nil
}

func (f *fencedSlots) Renew(context.Context, string, uint64) error { return nil }

func main() {
	reg := resource.NewRegistry(resource.RegistryConfig{MaxResources: 32})
	defer reg.Close()

	qMem := queue.NewMemory(64)
	jobs, err := queue.New(queue.Config{
		ID:      resource.MustParseResourceID("queue/demo/media/transcode"),
		Backend: qMem,
	})
	if err != nil {
		log.Fatal(err)
	}
	if _, err := reg.Register(jobs, resource.Metadata{Source: "media-demo"}); err != nil {
		log.Fatal(err)
	}

	bud := budget.New(budget.Config{
		Name: "transcode-batch", MaxBytes: 10 << 20, MaxRetries: 5,
		MaxDuration: 30 * time.Second, OnExhausted: budget.BehaviorPause,
	})
	sem, err := syncprim.NewSemaphore(&fencedSlots{}, "media-workers", 2)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	for _, msg := range []string{"job-1", "job-2", "job-3"} {
		if err := qMem.Publish(msg); err != nil {
			log.Fatal(err)
		}
	}

	processed := 0
	def, err := workflow.Define("media-drain").
		Step("drain", func(ctx context.Context, _ *workflow.ExecContext) (workflow.Result, error) {
			for {
				if err := bud.Allow(); err != nil {
					return workflow.Result{Evidence: workflow.Evidence{Event: "budget", Summary: err.Error()}}, nil
				}
				release, err := sem.Acquire(ctx)
				if err != nil {
					return workflow.Result{}, err
				}
				msg, ok := qMem.Consume()
				if !ok {
					_ = release(ctx)
					break
				}
				_ = bud.AddBytes(int64(len(msg)))
				processed++
				_ = release(ctx)
			}
			return workflow.Result{Evidence: workflow.Evidence{Event: "drained"}}, nil
		}).
		Build()
	if err != nil {
		log.Fatal(err)
	}
	eng := workflow.NewEngine(workflow.EngineConfig{MaxRuns: 8})
	_ = eng.Register(def)
	inst, err := eng.Run(ctx, "media-drain", workflow.RunOptions{})
	if err != nil {
		log.Fatal(err)
	}

	depth, _ := jobs.Depth(ctx)
	fmt.Println("resources:", reg.Count())
	fmt.Println("processed:", processed)
	fmt.Println("queue depth:", depth)
	fmt.Println("budget:", bud.Snapshot().Bytes)
	fmt.Println("workflow:", inst.State)
	fmt.Println("media-processing-pipeline OK")
}
