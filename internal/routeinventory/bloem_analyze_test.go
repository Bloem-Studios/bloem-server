package routeinventory

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"strings"
	"testing"
)

func TestOnlyReadOnlyMatchMethodMayEscape(t *testing.T) {
	if _, err := Analyze(fixtureConfig("match_method_value")); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(fixtureConfig("registration_method_value")); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("registration method escaped: %v", err)
	}
}

// TestAnalyzeEnumeratesDerivedRouterBinding covers `alt := r.With(mw)`.
//
// This was refused until the inventory learned the idiom, which was fail-closed
// but not accurate: chi's With returns an inline mux over the SAME tree, so a
// route registered through it really does live at the parent's path, and the
// only thing the middleware changes is what runs before the handler. Refusing
// it forced call sites to repeat r.With(...) once per route -- reconstructing
// the middleware each time -- to keep the inventory buildable.
func TestAnalyzeEnumeratesDerivedRouterBinding(t *testing.T) {
	inv := analyzeFixture(t, "derived_router_bound")
	var found bool
	for _, route := range inv.Routes {
		if route.Method == "GET" && route.Path == "/hidden" {
			found = true
		}
	}
	if !found {
		paths := make([]string, 0, len(inv.Routes))
		for _, route := range inv.Routes {
			paths = append(paths, route.Method+" "+route.Path)
		}
		t.Fatalf("route registered through r.With(mw) is missing; inventory has: %v", paths)
	}
}
