package catalog

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/cache"
)

func TestAudiobookGroupsCache_KeyedByLibraryCeiling(t *testing.T) {
	var fetches int
	c := &AudiobookGroupsCache{
		cache: cache.NewTTLCache[*groupsCacheEntry](),
		ttl:   time.Minute,
		fetch: func(_ context.Context, _ AudiobookGroupsQuery, filter AccessFilter) ([]AudiobookGroup, int, error) {
			fetches++
			return newTestGroups(len(filter.AllowedLibraryIDs) + 1), len(filter.AllowedLibraryIDs) + 1, nil
		},
	}
	defer c.Close()

	q := AudiobookGroupsQuery{LibraryID: 7, GroupBy: AudiobookGroupByAuthor, Sort: "name", Limit: 10}
	if _, total, err := c.Page(context.Background(), q, AccessFilter{UserID: 1, ProfileID: "p1", AllowedLibraryIDs: []int{7}}); err != nil || total != 2 {
		t.Fatalf("ceiling {7}: total=%d err=%v", total, err)
	}
	if _, total, err := c.Page(context.Background(), q, AccessFilter{UserID: 1, ProfileID: "p1", AllowedLibraryIDs: []int{8}}); err != nil || total != 2 {
		t.Fatalf("ceiling {8}: total=%d err=%v", total, err)
	}
	if _, total, err := c.Page(context.Background(), q, AccessFilter{UserID: 1, ProfileID: "p1", AllowedLibraryIDs: []int{7}}); err != nil || total != 2 {
		t.Fatalf("ceiling {7} cached: total=%d err=%v", total, err)
	}
	if fetches != 2 {
		t.Fatalf("distinct library ceilings shared a cache entry: fetches=%d, want 2", fetches)
	}
}

func TestAudiobookGroupsCache_EmptyLibraryCeilingAfterRevocation(t *testing.T) {
	var fetches int
	c := &AudiobookGroupsCache{
		cache: cache.NewTTLCache[*groupsCacheEntry](),
		ttl:   time.Minute,
		fetch: func(_ context.Context, _ AudiobookGroupsQuery, filter AccessFilter) ([]AudiobookGroup, int, error) {
			fetches++
			if filter.AllowedLibraryIDs != nil && len(filter.AllowedLibraryIDs) == 0 {
				return nil, 0, nil
			}
			return newTestGroups(3), 3, nil
		},
	}
	defer c.Close()

	q := AudiobookGroupsQuery{LibraryID: 7, GroupBy: AudiobookGroupByAuthor, Sort: "name", Limit: 10}
	filter := AccessFilter{UserID: 1, ProfileID: "p1", AllowedLibraryIDs: []int{7}}
	if _, total, err := c.Page(context.Background(), q, filter); err != nil || total != 3 {
		t.Fatalf("entitled ceiling: total=%d err=%v", total, err)
	}

	revoked := AccessFilter{UserID: 1, ProfileID: "p1", AllowedLibraryIDs: []int{}}
	groups, total, err := c.Page(context.Background(), q, revoked)
	if err != nil {
		t.Fatalf("revoked ceiling: %v", err)
	}
	if total != 0 || len(groups) != 0 {
		t.Fatalf("revoked ceiling served stale groups: total=%d len=%d", total, len(groups))
	}
	if fetches != 2 {
		t.Fatalf("empty ceiling reused the entitled viewer's cache entry: fetches=%d, want 2", fetches)
	}

	if _, total, err := c.Page(context.Background(), q, revoked); err != nil || total != 0 {
		t.Fatalf("revoked ceiling cached: total=%d err=%v", total, err)
	}
	if fetches != 2 {
		t.Fatalf("revoked ceiling was not itself cached: fetches=%d, want 2", fetches)
	}
}
