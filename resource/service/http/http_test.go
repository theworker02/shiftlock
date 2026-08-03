package httpresource

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

func TestHTTPHealthExecuteIdempotencyCircuit(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/pay" {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	id := resource.MustParseResourceID("http-service/test/demo/payment")
	r, err := New(Config{
		ID:               id,
		BaseURL:          srv.URL,
		CircuitThreshold: 2,
		CircuitOpenFor:   time.Minute,
		MaxConcurrent:    4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Capabilities().SupportsFencing {
		t.Fatal("must not claim fencing")
	}
	h := r.Health(context.Background())
	if h.Overall != resource.HealthHealthy {
		t.Fatalf("%v", h)
	}
	res, err := r.Execute(context.Background(), Request{
		Method:        http.MethodPost,
		Path:          "/pay",
		Body:          strings.NewReader(`{}`),
		IdempotencyID: "pay-1",
		Operation:     "payment.capture",
	})
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := r.Execute(context.Background(), Request{
		Method: http.MethodPost, Path: "/pay", IdempotencyID: "pay-1",
	}); err == nil {
		t.Fatal("expected duplicate idempotency reject")
	}

	// Trip circuit via forced open.
	r.OpenCircuit(time.Minute)
	if !r.CircuitOpen() {
		t.Fatal("expected open")
	}
	h = r.Health(context.Background())
	if h.Overall != resource.HealthBlocked {
		t.Fatalf("got %s", h.Overall)
	}
	snap, _ := r.Snapshot(context.Background())
	if strings.Contains(snap["base"], "?") {
		t.Fatal("query must be stripped")
	}
	_ = hits
}
