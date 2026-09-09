package main

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Silo-Server/silo-server/internal/compatgateway"
)

// newRootHandler builds the handler the primary port serves.
//
// The API router is not the process's outermost handler: this http.ServeMux is.
// It answers /metrics itself, hands /api/ to the API listener, and serves the
// frontend everywhere else. Those registrations are routes like any other, so
// they are enumerated here — in one small function the route inventory can walk
// — rather than inline in main(), where nothing would notice a fourth
// registration appearing beside them.
//
// The compatibility gateway shares the root fallback with the frontend;
// the optional dedicated ABS listener uses the same compatibility handler.
func newRootHandler(apiRouter, frontend, gateway http.Handler) http.Handler {
	return sealedHandler{h: newRootMux(apiRouter, frontend, gateway)}
}

// sealedHandler is what newRootHandler hands out: the finished mux behind an
// unexported field and a ServeHTTP method, nothing else, so no assertion or
// type switch recovers a registration surface from it, and the route
// inventory refuses the reflect calls that could (MethodByName, Method,
// NumMethod, NewAt, UnsafePointer, UnsafeAddr, Pointer) and any import of
// unsafe in this package: short of unsafe, nothing gets the mux back (see
// docs/architecture/api-contract.md). Do not embed http.Handler here:
// embedding exports the field and promotes its methods.
type sealedHandler struct {
	h http.Handler
}

func (h sealedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.h.ServeHTTP(w, r) }

// newRootMux is the root listener's registration surface. The route inventory
// generator walks this function; every registration must be reachable from it.
func newRootMux(apiRouter, frontend, gateway http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	// Prometheus metrics are not behind auth.
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/api/", apiRouter)
	mux.Handle("/", compatgateway.WithFrontendFallback(gateway, frontend))
	return mux
}
