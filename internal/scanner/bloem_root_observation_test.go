package scanner

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/rootcheck"
)

func TestClassifyRootProbeResultsSeparatesUnreachableEmptyAndHealthy(t *testing.T) {
	roots := []string{"/missing", "/unreadable", "/empty", "/healthy"}
	probes := []rootcheck.Result{
		{ErrorCode: rootcheck.ErrCodeNotFound, ErrorMessage: "Path does not exist"},
		{ErrorCode: rootcheck.ErrCodePermissionDenied, ErrorMessage: "Permission denied"},
		{Reachable: true, Empty: true},
		{Reachable: true},
	}
	unreachable, empty := classifyRootProbeResults(roots, probes)
	if want := []string{"/missing", "/unreadable"}; !reflect.DeepEqual(unreachable, want) {
		t.Fatalf("unreachable roots = %v, want %v", unreachable, want)
	}
	if want := []string{"/empty"}; !reflect.DeepEqual(empty, want) {
		t.Fatalf("empty roots = %v, want %v", empty, want)
	}
}
