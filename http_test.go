package shiftlock_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/theworker02/shiftlock"
	"github.com/theworker02/shiftlock/backend/memory"
)

func TestDiagnosticsHandler(t *testing.T) {
	be := memory.New()
	defer be.Close()
	c, err := shiftlock.New(shiftlock.Config{
		Service: "svc", InstanceID: "i1", Backend: be,
		LeaseTTL: time.Second, RenewInterval: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	req := httptest.NewRequest(http.MethodGet, "/debug/shiftlock", nil)
	rr := httptest.NewRecorder()
	shiftlock.DiagnosticsHandler(c).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("code=%d", rr.Code)
	}
	var d shiftlock.Diagnostics
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Service != "svc" || d.Generation.ID == "" {
		t.Fatalf("%+v", d)
	}
}
