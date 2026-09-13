package jellycompat

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"os"
	"testing"
)

func TestCurrentProcessTokenIdentifiesRunningProcess(t *testing.T) {
	first := currentProcessToken()
	second := processToken(os.Getpid())

	if first == "" {
		t.Fatal("currentProcessToken() = empty, want a portable process identity")
	}
	if second != first {
		t.Fatalf("processToken(current pid) = %q, want %q", second, first)
	}
}
