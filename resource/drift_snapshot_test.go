package resource_test

import (
	"context"
	"testing"

	"github.com/theworker02/shiftlock/resource"
	"github.com/theworker02/shiftlock/resource/memory"
)

func TestDriftAndSanitizedSnapshot(t *testing.T) {
	id := resource.MustParseResourceID("database/prod/pay/orders")
	d := resource.DriftDetector{DefaultAction: resource.DriftActionReport}
	reports := d.Compare(id,
		map[string]string{"schema_version": "8", "role": "primary"},
		map[string]string{"schema_version": "7", "role": "primary", "password": "nope"},
	)
	if len(reports) < 1 {
		t.Fatal("expected drift")
	}
	found := false
	for _, r := range reports {
		if r.Field == "schema_version" && r.Severity == resource.SeverityCritical {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", reports)
	}

	reg := resource.NewRegistry(resource.RegistryConfig{})
	res := memory.Worker("dev", "svc", "w")
	ent, err := reg.Register(res, resource.Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := resource.CaptureSnapshot(context.Background(), ent)
	if err != nil {
		t.Fatal(err)
	}
	dirty := resource.SanitizeSnapshotFields(map[string]string{
		"schema_version": "8",
		"password":       "secret",
		"api_token":      "tok",
		"role":           "primary",
	})
	if _, ok := dirty["password"]; ok {
		t.Fatal("password leaked")
	}
	if _, ok := dirty["api_token"]; ok {
		t.Fatal("token leaked")
	}
	if dirty["role"] != "primary" {
		t.Fatalf("%v", dirty)
	}
	_ = snap
}
