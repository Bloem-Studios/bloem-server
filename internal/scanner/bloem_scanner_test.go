package scanner

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

func TestScanFolderMusicLibraryRoutesToDedicatedScanner(t *testing.T) {
	scanner := &Scanner{}
	result, err := scanner.ScanFolder(context.Background(), &models.MediaFolder{
		Type:  " music ",
		Paths: []string{t.TempDir()},
	})
	if err != nil {
		t.Fatalf("ScanFolder music error = %v, want nil", err)
	}
	if result == nil {
		t.Fatal("ScanFolder music result = nil, want empty result")
	}
	if got := walkModeFor("songs"); got != walkModeMusic {
		t.Fatalf("walkModeFor(songs) = %v, want music", got)
	}
}
