package shiftlock

import (
	"encoding/json"
	"net/http"
)

// DiagnosticsHandler returns an http.Handler that serves sanitized coordinator
// diagnostics as JSON. It does not start a server; mount it on your own mux.
// No secrets or backend credentials are included.
func DiagnosticsHandler(c *Coordinator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		d := c.Diagnostics()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(d)
	})
}
