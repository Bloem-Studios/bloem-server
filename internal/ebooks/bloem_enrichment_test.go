package ebooks

// Bloem stale-artwork-protection coverage moved out of Silo's enrichment_test.go.

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/metadata"
)

func TestPreserveEbookMetadataIgnoresStaleArtworkProtection(t *testing.T) {
	result := &metadata.MetadataResult{
		HasMetadata:       true,
		PosterPath:        "https://example.test/poster.jpg",
		PosterThumbhash:   "poster-thumb",
		BackdropPath:      "https://example.test/backdrop.jpg",
		BackdropThumbhash: "backdrop-thumb",
		LogoPath:          "https://example.test/logo.png",
	}

	preserveEbookLocalMetadata(enrichmentItemRow{
		Status:          "matched",
		ProtectedFields: []string{"poster_path", "backdrop_path", "logo_path"},
	}, result)

	if result.PosterPath == "" || result.PosterThumbhash == "" ||
		result.BackdropPath == "" || result.BackdropThumbhash == "" || result.LogoPath == "" {
		t.Fatalf("stale artwork protection suppressed provider images: %+v", result)
	}
}
