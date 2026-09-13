package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/routeinventory"
)

// This file is Bloem-owned: it mirrors route_inventory_test.go's two tests
// (which stay upstream-identical and exercise upstream's newRootHandler/
// newRootMux — the functions the route-inventory generator's static analysis
// keys on by name, see bloem_root_handler.go) but points them at the mux
// Bloem's process actually serves, bloemRootHandler/bloemRootMux. Without
// this file, nothing behavioral guards bloemRootMux itself: a change to its
// /metrics registration, or a reorder that lets "/" shadow it, would pass
// route_inventory_test.go (which never calls Bloem's function) and pass
// TestRouteInventoryMatchesRootListener's pattern reconciliation (which
// compares pattern strings, never handler behavior — see
// internal/routeinventory/reconcile.go's ObservedServeMux).

// TestBloemRouteInventoryMatchesRootListener is
// TestRouteInventoryMatchesRootListener's counterpart for the mux the
// process really serves: same reconciliation against the committed
// artifact, built from bloemRootMux instead of upstream's newRootMux.
func TestBloemRouteInventoryMatchesRootListener(t *testing.T) {
	inventory, err := routeinventory.LoadArtifact(".")
	if err != nil {
		t.Fatal(err)
	}
	if count := inventory.ConditionalCount(routeinventory.ListenerRoot); count != 0 {
		t.Fatalf("the root listener now has %d conditionally registered row(s); "+
			"equality with one wiring no longer holds — switch to Reconcile and say why here", count)
	}

	apiStub := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	// bloemRootHandler seals the mux; the test walks it through the
	// unexported constructor, which is the same mux with the same
	// registrations bloemRootHandler hands out.
	observed, err := routeinventory.ObservedServeMux(bloemRootMux(apiStub, apiStub, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(observed) == 0 {
		t.Fatal("no patterns observed; the root handler no longer registers anything")
	}
	unledgered, unobserved := inventory.ReconcileExact(routeinventory.ListenerRoot, observed)
	if len(unledgered) > 0 {
		t.Errorf("the root listener registers %d route(s) with no inventory row; run `make route-inventory`:\n  %v",
			len(unledgered), unledgered)
	}
	if len(unobserved) > 0 {
		t.Errorf("the inventory claims %d root route(s) the real listener does not register; "+
			"run `make route-inventory`:\n  %v", len(unobserved), unobserved)
	}
}

// TestBloemRootListenerDoesNotServeMetrics is
// TestRootListenerDoesNotServeMetrics's counterpart for
// bloemRootHandler — the handler main() actually wires up — so a change to
// bloemRootMux's /metrics registration (cmd/silo/bloem_root_handler.go) is
// caught here even though it can't be caught via upstream's own,
// byte-identical test.
func TestBloemRootListenerDoesNotServeMetrics(t *testing.T) {
	apiStub := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	bloemRootHandler(apiStub, apiStub, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "# HELP ") {
		t.Fatal("public root listener still serves Prometheus metrics")
	}
}
