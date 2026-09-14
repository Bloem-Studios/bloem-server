package catalog

import (
	"fmt"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

// A metadata refresh carries the provider's own image URL, because the provider
// has no idea we cached anything. The episode upsert used to write that URL
// straight over still_path, discarding a cached object key that was serving
// correctly -- and leaving the object itself unreferenced, so artwork GC then
// deleted it. On one deployment that silently unpicked 804,490 episode stills
// whose cache jobs were all recorded as succeeded.
//
// metadata.preserveCachedArtwork applies this rule in Go, but only when the
// caller managed to load the existing row first; that read comes from a
// prefetch map, and a miss produces a provider-only write that reaches the
// upsert looking like a fresh row. These tests pin the rule at the SQL, where
// nothing can route around it.
func TestEpisodeUpsertNeverReplacesACachedStillWithAProviderURL(t *testing.T) {
	repo, seriesID, seasonID := seedCompletionEpisodes(t, 1)
	ctx := t.Context()

	const (
		cachedPath    = "artwork/series/still/original.cached-revision.webp"
		cachedHash    = "cached-thumbhash"
		providerURL   = "tvdb://episodes/12345/still.jpg"
		refreshedURL  = "tvdb://episodes/12345/still-v2.jpg"
		newCachedPath = "artwork/series/still/original.newer-revision.webp"
	)

	// Upsert is the single-episode path; BulkUpsert is the one a series refresh
	// actually uses. They carry the same ON CONFLICT clause, so both are pinned.
	for _, mode := range []struct {
		name   string
		upsert func(ep *models.Episode) error
	}{
		{"Upsert", func(ep *models.Episode) error { return repo.Upsert(ctx, ep) }},
		{"BulkUpsert", func(ep *models.Episode) error {
			return repo.BulkUpsert(ctx, seriesID, []*models.Episode{ep})
		}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			episodeNumber := 1
			if mode.name == "BulkUpsert" {
				episodeNumber = 2
			}
			base := func() *models.Episode {
				return &models.Episode{
					ContentID:     fmt.Sprintf("still-guard-%s-%d", mode.name, time.Now().UnixNano()),
					SeriesID:      seriesID,
					SeasonID:      seasonID,
					SeasonNumber:  1,
					EpisodeNumber: episodeNumber,
					Title:         "Still guard fixture",
				}
			}

			// The state the image cache pipeline leaves behind.
			seed := base()
			seed.StillPath = cachedPath
			seed.StillSourcePath = providerURL
			seed.StillThumbhash = cachedHash
			if err := mode.upsert(seed); err != nil {
				t.Fatalf("seed cached still: %v", err)
			}

			// A refresh whose provider returns the same image. The cached path
			// must survive: nothing about the artwork has changed.
			refresh := base()
			refresh.StillPath = providerURL
			refresh.StillSourcePath = providerURL
			refresh.StillThumbhash = ""
			if err := mode.upsert(refresh); err != nil {
				t.Fatalf("refresh with provider URL: %v", err)
			}

			gotPath, gotSource, gotHash := stillOf(t, repo, seriesID, episodeNumber)
			if gotPath != cachedPath {
				t.Errorf("still_path = %q, want the cached path %q to survive the refresh", gotPath, cachedPath)
			}
			if gotHash != cachedHash {
				t.Errorf("still_thumbhash = %q, want %q -- the hash belongs to the cached image, not the URL", gotHash, cachedHash)
			}
			if gotSource != providerURL {
				t.Errorf("still_source_path = %q, want %q", gotSource, providerURL)
			}

			// A refresh whose provider changed the image. The source must move
			// so the cache pipeline sees new work; the cached path keeps serving
			// the old image until that job completes, rather than falling back
			// to a raw provider URL the client cannot render consistently.
			moved := base()
			moved.StillPath = refreshedURL
			moved.StillSourcePath = refreshedURL
			if err := mode.upsert(moved); err != nil {
				t.Fatalf("refresh with a changed provider URL: %v", err)
			}
			gotPath, gotSource, _ = stillOf(t, repo, seriesID, episodeNumber)
			if gotPath != cachedPath {
				t.Errorf("still_path = %q, want the cached path to keep serving until re-cached", gotPath)
			}
			if gotSource != refreshedURL {
				t.Errorf("still_source_path = %q, want the new source %q so the image is re-cached", gotSource, refreshedURL)
			}

			// The guard must not freeze artwork: the cache pipeline writing a
			// newer cached path has to win, or a re-cached still could never
			// replace the one it supersedes.
			recached := base()
			recached.StillPath = newCachedPath
			recached.StillSourcePath = refreshedURL
			recached.StillThumbhash = "newer-thumbhash"
			if err := mode.upsert(recached); err != nil {
				t.Fatalf("write a newer cached still: %v", err)
			}
			gotPath, _, gotHash = stillOf(t, repo, seriesID, episodeNumber)
			if gotPath != newCachedPath {
				t.Errorf("still_path = %q, want the newer cached path %q", gotPath, newCachedPath)
			}
			if gotHash != "newer-thumbhash" {
				t.Errorf("still_thumbhash = %q, want it to follow the newer cached path", gotHash)
			}

			// An episode that genuinely has no cached still yet must still
			// accept the provider URL, which is what the pre-cache window shows.
			blank := base()
			blank.EpisodeNumber = episodeNumber + 10
			blank.StillPath = providerURL
			blank.StillSourcePath = providerURL
			if err := mode.upsert(blank); err != nil {
				t.Fatalf("seed uncached episode: %v", err)
			}
			if gotPath, _, _ := stillOf(t, repo, seriesID, blank.EpisodeNumber); gotPath != providerURL {
				t.Errorf("uncached still_path = %q, want the provider URL %q", gotPath, providerURL)
			}
		})
	}
}

func stillOf(t *testing.T, repo *EpisodeRepository, seriesID string, episodeNumber int) (string, string, string) {
	t.Helper()
	var path, source, hash string
	if err := repo.pool.QueryRow(t.Context(), `
		SELECT COALESCE(still_path, ''), COALESCE(still_source_path, ''), COALESCE(still_thumbhash, '')
		FROM episodes WHERE series_id = $1 AND season_number = 1 AND episode_number = $2
	`, seriesID, episodeNumber).Scan(&path, &source, &hash); err != nil {
		t.Fatalf("read still for episode %d: %v", episodeNumber, err)
	}
	return path, source, hash
}
