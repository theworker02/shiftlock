// Command infrastructure-orchestrator runs a deployment-style workflow across
// fake resources (config, workers, object store) with parallel fan-out and
// compensation on failure.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
	"github.com/theworker02/shiftlock/resource"
	resmem "github.com/theworker02/shiftlock/resource/memory"
	"github.com/theworker02/shiftlock/resource/storage/object"
	objmem "github.com/theworker02/shiftlock/resource/storage/object/memory"
	"github.com/theworker02/shiftlock/workflow"
)

func main() {
	dir := filepath.Join(os.TempDir(), "shiftlock-infra-demo")
	_ = os.RemoveAll(dir)
	_ = os.MkdirAll(dir, 0o700)

	be := memory.New()
	cfg := shiftlock.RuntimeConfig{
		Config: shiftlock.Config{
			Service: "infra", InstanceID: "orchestrator-1", Backend: be, LeaseTTL: time.Minute,
		},
	}
	shiftlock.WithLocalStateDir(dir)(&cfg)
	rt, err := shiftlock.NewRuntime(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	reg := rt.Resources()
	cfgRes := resmem.New(
		resource.ResourceID{Kind: resource.KindConfiguration, Environment: "prod", Service: "payments", Name: "app-config"},
		resource.Description{DisplayName: "app-config"},
		resource.ResourceCapabilities{SupportsHealth: true, SupportsSnapshots: true},
	)
	workerA := resmem.Worker("prod", "payments", "api-a")
	workerB := resmem.Worker("prod", "payments", "api-b")
	objID := resource.ResourceID{Kind: resource.KindObjectStore, Environment: "prod", Service: "payments", Name: "artifacts"}
	objRes, objStore, err := objmem.NewResource(objID, "artifacts")
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range []resource.Resource{cfgRes, workerA, workerB, objRes} {
		if _, err := reg.Register(r, resource.Metadata{Source: "infra-demo"}); err != nil {
			log.Fatal(err)
		}
	}

	def, err := workflow.Define("deploy-payments").
		Step("validate-config", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			fmt.Println("validate-config")
			return workflow.Result{Evidence: workflow.Evidence{Event: "validate"}}, nil
		}).
		Step("push-artifact", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			_, err := objStore.Put(ctx, "artifacts", "payments/v1.tar", []byte("payload-v1"), object.PutOptions{
				ContentType: "application/octet-stream", IdempotencyKey: exec.OperationID + ":artifact",
			})
			return workflow.Result{Evidence: workflow.Evidence{Event: "artifact"}}, err
		}).
		Step("drain-a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			fmt.Println("drain-a")
			return workflow.Result{}, nil
		}).
		Compensate("drain-a", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			fmt.Println("undrain-a")
			return workflow.Result{}, nil
		}).
		Step("drain-b", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			fmt.Println("drain-b")
			return workflow.Result{}, nil
		}).
		Compensate("drain-b", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			fmt.Println("undrain-b")
			return workflow.Result{}, nil
		}).
		Step("activate", func(ctx context.Context, exec *workflow.ExecContext) (workflow.Result, error) {
			fmt.Println("activate")
			return workflow.Result{}, nil
		}).
		Depend("push-artifact", "validate-config").
		Depend("drain-a", "push-artifact").
		Depend("drain-b", "push-artifact").
		Depend("activate", "drain-a", "drain-b").
		ParallelGroup("drain", "drain-a", "drain-b").
		Mutating("activate", true).
		Build()
	if err != nil {
		log.Fatal(err)
	}
	if err := rt.Workflows().Register(def); err != nil {
		log.Fatal(err)
	}
	inst, err := rt.Workflows().Run(context.Background(), "deploy-payments", workflow.RunOptions{
		InstanceID: "deploy-1", OperationID: "op-deploy-1",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("state:", inst.State)
	fmt.Println("local state dir:", rt.LocalStateDir())
	fmt.Println("objects:", objStore.Len())
	fmt.Println("infrastructure-orchestrator OK")
}
