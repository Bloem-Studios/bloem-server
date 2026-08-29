package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
)

type LifecyclePhaseReader func(context.Context) (lifecycleidempotency.Phase, error)
type LifecycleRouteMatcher func(method, path string) bool

type LifecycleIdempotencyPreflight struct {
	phase LifecyclePhaseReader
	match LifecycleRouteMatcher
}

func NewLifecycleIdempotencyPreflight(phase LifecyclePhaseReader, match LifecycleRouteMatcher) *LifecycleIdempotencyPreflight {
	return &LifecycleIdempotencyPreflight{phase: phase, match: match}
}

// Handler is the outer, pre-authentication lifecycle gate. The coordinator
// repeats the phase check while holding the shared handoff lock; this early
// check exists so missing/malformed keys never trigger authentication or
// mutable actor/target lookups.
func (m *LifecycleIdempotencyPreflight) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if m == nil || m.phase == nil || m.match == nil || !m.match(request.Method, request.URL.Path) {
			next.ServeHTTP(w, request)
			return
		}
		phase, err := m.phase(request.Context())
		if err != nil {
			writeLifecyclePreflightError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
			return
		}
		key := request.Header.Get("Idempotency-Key")
		if key == "" {
			if phase == lifecycleidempotency.PhaseRequired {
				writeLifecyclePreflightError(w, http.StatusPreconditionRequired, "idempotency_key_required", "Idempotency-Key is required for this lifecycle mutation")
				return
			}
			next.ServeHTTP(w, request)
			return
		}
		if !lifecycleidempotency.ValidKey(key) {
			writeLifecyclePreflightError(w, http.StatusBadRequest, "idempotency_key_invalid", "Idempotency-Key must be a bounded opaque ASCII value")
			return
		}
		next.ServeHTTP(w, request)
	})
}

func writeLifecyclePreflightError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
