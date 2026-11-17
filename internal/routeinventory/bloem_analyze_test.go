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

// TestAnalyzeFollowsHelperMountedAtTwoPrefixes covers bloemEnterHelper: Bloem
// mounts one registration helper under more than one prefix, and each mount's
// routes are recorded under its own prefix.
func TestAnalyzeFollowsHelperMountedAtTwoPrefixes(t *testing.T) {
	inv := analyzeFixture(t, "bloem_helper_two_prefixes")
	got := map[string]bool{}
	for _, route := range inv.Routes {
		got[route.Method+" "+route.Path] = true
	}
	for _, want := range []string{"GET /a/items", "GET /b/items"} {
		if !got[want] {
			t.Fatalf("route %s missing; inventory has %v", want, got)
		}
	}
}

// TestAnalyzeRefusesRecursiveHelper keeps the walk finite: a helper already on
// the walk's stack is refused, not followed again.
func TestAnalyzeRefusesRecursiveHelper(t *testing.T) {
	_, err := Analyze(fixtureConfig("bloem_recursive_helper"))
	if err == nil || !strings.Contains(err.Error(), "mountLoop is recursive") {
		t.Fatalf("Analyze() error = %v, want a recursive-helper refusal", err)
	}
}

// TestAnalyzeKeepsRereadBodyAsJSONRequest covers the bytes.NewReader and
// bytes.NewBuffer case in bodyOrigins.expression: bytes re-read from the
// request body are still the request body.
func TestAnalyzeKeepsRereadBodyAsJSONRequest(t *testing.T) {
	inv := analyzeFixture(t, "bloem_reread_body")
	if len(inv.Routes) != 2 {
		t.Fatalf("got %d routes, want 2", len(inv.Routes))
	}
	for _, route := range inv.Routes {
		if route.RequestKind != KindJSON {
			t.Errorf("%s request_kind = %q, want %q", route.Path, route.RequestKind, KindJSON)
		}
	}
}
