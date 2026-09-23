package ebooks

// Bloem stale-artwork-protection coverage moved out of Silo's
// enrichment_queue_test.go.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrichmentQueueRecalculatesArtworkProtectionFromCurrentCatalog(t *testing.T) {
	query := strings.Join(strings.Fields(mergeEbookProtectedFieldsSQL), " ")
	artworkFields := "('poster_path', 'backdrop_path', 'logo_path')"
	for _, fragment := range []string{
		"FROM unnest(ebook_enrichment_state.protected_fields) AS existing_field",
		"WHERE existing_field NOT IN " + artworkFields,
		"FROM unnest(EXCLUDED.protected_fields) AS candidate",
		"WHERE candidate IN " + artworkFields,
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("artwork protection merge missing %q:\n%s", fragment, mergeEbookProtectedFieldsSQL)
		}
	}
}

func TestRepairStaleEbookArtworkProtectionMigration(t *testing.T) {
	matches, err := filepath.Glob("../../migrations/sql/*_repair_stale_ebook_artwork_protection.sql")
	if err != nil {
		t.Fatalf("find stale artwork protection migration: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("stale artwork protection migrations = %d, want 1", len(matches))
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read stale artwork protection migration: %v", err)
	}
	migration := strings.Join(strings.Fields(string(body)), " ")
	for _, fragment := range []string{
		"UPDATE ebook_enrichment_state AS state",
		"JOIN media_items AS item",
		"item.content_id = state.content_id",
		"field NOT IN ('poster_path', 'backdrop_path', 'logo_path')",
		"field = 'poster_path' AND trim(COALESCE(item.poster_path, '')) <> ''",
		"field = 'backdrop_path' AND trim(COALESCE(item.backdrop_path, '')) <> ''",
		"field = 'logo_path' AND trim(COALESCE(item.logo_path, '')) <> ''",
		"state.protected_fields IS DISTINCT FROM repaired.protected_fields",
		"UPDATE media_files AS file",
		"SET group_key_version = 1",
		"JOIN media_folders AS folder ON folder.id = file.media_folder_id",
		"folder.type = 'ebooks'",
		"trim(COALESCE(item.poster_path, '')) = ''",
		"file.missing_since IS NULL",
		"file.group_key_version >= 2",
	} {
		if !strings.Contains(migration, fragment) {
			t.Fatalf("stale artwork protection migration missing %q:\n%s", fragment, body)
		}
	}
}
