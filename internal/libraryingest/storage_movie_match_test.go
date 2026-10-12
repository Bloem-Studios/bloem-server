package libraryingest

import (
	"github.com/Silo-Server/silo-server/internal/metadata"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"testing"
)

func TestNativeMovieIMDbHintUsesInitialMatch(t *testing.T) {
	request := nativeMovieMatchRequest(7, scanner.StorageMovieMatch{ContentID: "local-test", Title: "3 A.M.", Year: 2001, ImdbID: "tt0248661"})
	if request.Mode != metadata.ModeInitialMatch || request.Hints == nil || request.Hints.ImdbID != "tt0248661" || request.Hints.Title != "3 A.M." || request.Hints.Year != 2001 {
		t.Fatalf("IMDb-only movie bypassed provider ID resolution: %+v", request)
	}
}
