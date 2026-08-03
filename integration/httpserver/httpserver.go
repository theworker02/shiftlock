// Package httpserver integrates ShiftLock ownership with HTTP leadership gates.
package httpserver

import (
	"net/http"

	"github.com/theworker02/shiftlock"
)

// RequireOwnership returns middleware that rejects requests unless the claim
// is owned by this generation with a live lease.
func RequireOwnership(coord *shiftlock.Coordinator, claim string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cl, err := coord.Claim(r.Context(), claim)
			if err != nil || !cl.Ownership().OwnedBy(coord.Generation().ID) {
				http.Error(w, "not owner", http.StatusServiceUnavailable)
				return
			}
			o := cl.Ownership()
			if !o.OwnedBy(coord.Generation().ID) || o.FencingToken.Zero() {
				http.Error(w, "lease invalid", http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
