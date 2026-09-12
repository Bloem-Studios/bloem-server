package catalog

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/cache"
)

func newTestGroups(n int) []AudiobookGroup {
	groups := make([]AudiobookGroup, n)
	for i := range groups {
		groups[i] = AudiobookGroup{Name: "author-" + string(rune('a'+i)), ItemCount: i + 1}
	}
	return groups
}

// The grouped list is the expensive aggregation; paging through it (the client
// fetches every page on each load) must compute it once, not once per page.
func TestAudiobookGroupsCache_ComputesFullListOncePerKey(t *testing.T) {
	full := newTestGroups(5)
	var fetches int
	c := &AudiobookGroupsCache{
		cache: cache.NewTTLCache[*groupsCacheEntry](),
		ttl:   time.Minute,
		fetch: func(context.Context, AudiobookGroupsQuery, AccessFilter) ([]AudiobookGroup, int, error) {
			fetches++
			return full, len(full), nil
		},
	}
	defer c.Close()

	q := AudiobookGroupsQuery{LibraryID: 7, GroupBy: AudiobookGroupByAuthor, Sort: "name"}
	filter := AccessFilter{UserID: 1, ProfileID: "p1"}

	page1, total, err := c.Page(context.Background(), AudiobookGroupsQuery{LibraryID: q.LibraryID, GroupBy: q.GroupBy, Sort: q.Sort, Limit: 2, Offset: 0}, filter)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	page2, _, err := c.Page(context.Background(), AudiobookGroupsQuery{LibraryID: q.LibraryID, GroupBy: q.GroupBy, Sort: q.Sort, Limit: 2, Offset: 2}, filter)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	page3, _, err := c.Page(context.Background(), AudiobookGroupsQuery{LibraryID: q.LibraryID, GroupBy: q.GroupBy, Sort: q.Sort, Limit: 2, Offset: 4}, filter)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}

	if fetches != 1 {
		t.Fatalf("full list computed %d times across 3 pages of one key; want 1", fetches)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(page1) != 2 || len(page2) != 2 || len(page3) != 1 {
		t.Fatalf("page sizes = %d,%d,%d; want 2,2,1", len(page1), len(page2), len(page3))
	}
	if page1[0].Name != "author-a" || page2[0].Name != "author-c" || page3[0].Name != "author-e" {
		t.Fatalf("slice boundaries wrong: %q %q %q", page1[0].Name, page2[0].Name, page3[0].Name)
	}
}

// A different viewer must not be served another profile's cached counts.
func TestAudiobookGroupsCache_KeyedByViewer(t *testing.T) {
	var fetches int
	c := &AudiobookGroupsCache{
		cache: cache.NewTTLCache[*groupsCacheEntry](),
		ttl:   time.Minute,
		fetch: func(context.Context, AudiobookGroupsQuery, AccessFilter) ([]AudiobookGroup, int, error) {
			fetches++
			return newTestGroups(3), 3, nil
		},
	}
	defer c.Close()
	q := AudiobookGroupsQuery{LibraryID: 7, GroupBy: AudiobookGroupByAuthor, Sort: "name", Limit: 10}
	if _, _, err := c.Page(context.Background(), q, AccessFilter{UserID: 1, ProfileID: "p1"}); err != nil {
		t.Fatalf("viewer1: %v", err)
	}
	if _, _, err := c.Page(context.Background(), q, AccessFilter{UserID: 2, ProfileID: "p2"}); err != nil {
		t.Fatalf("viewer2: %v", err)
	}
	if fetches != 2 {
		t.Fatalf("distinct viewers shared a cache entry: fetches=%d, want 2", fetches)
	}
}

func TestAudiobookGroupsCache_DeduplicatesConcurrentMisses(t *testing.T) {
	var fetches atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	c := &AudiobookGroupsCache{
		cache: cache.NewTTLCache[*groupsCacheEntry](),
		ttl:   time.Minute,
		fetch: func(context.Context, AudiobookGroupsQuery, AccessFilter) ([]AudiobookGroup, int, error) {
			fetches.Add(1)
			startedOnce.Do(func() { close(started) })
			<-release
			return newTestGroups(4), 4, nil
		},
	}
	defer c.Close()

	q := AudiobookGroupsQuery{LibraryID: 7, GroupBy: AudiobookGroupByAuthor, Sort: "name", Limit: 2}
	filter := AccessFilter{UserID: 1, ProfileID: "p1"}
	const callers = 8
	ready := make(chan struct{}, callers)
	begin := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			<-begin
			groups, total, err := c.Page(context.Background(), q, filter)
			if err != nil {
				errs <- err
				return
			}
			if total != 4 || len(groups) != 2 {
				errs <- fmt.Errorf("page result = len %d total %d, want len 2 total 4", len(groups), total)
			}
		}()
	}
	for range callers {
		<-ready
	}
	close(begin)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cache fetch to start")
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent page call failed: %v", err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("concurrent miss fetched %d times; want 1", got)
	}
}

func TestAudiobookGroupsCache_KeyIncludesExcludedMediaTypes(t *testing.T) {
	var fetches int
	c := &AudiobookGroupsCache{
		cache: cache.NewTTLCache[*groupsCacheEntry](),
		ttl:   time.Minute,
		fetch: func(context.Context, AudiobookGroupsQuery, AccessFilter) ([]AudiobookGroup, int, error) {
			fetches++
			return newTestGroups(3), 3, nil
		},
	}
	defer c.Close()

	q := AudiobookGroupsQuery{LibraryID: 7, GroupBy: AudiobookGroupByAuthor, Sort: "name", Limit: 10}
	if _, _, err := c.Page(context.Background(), q, AccessFilter{UserID: 1, ProfileID: "p1", ExcludedMediaTypes: []string{"podcast"}}); err != nil {
		t.Fatalf("podcast excluded: %v", err)
	}
	if _, _, err := c.Page(context.Background(), q, AccessFilter{UserID: 1, ProfileID: "p1", ExcludedMediaTypes: []string{"audiobook"}}); err != nil {
		t.Fatalf("audiobook excluded: %v", err)
	}
	if _, _, err := c.Page(context.Background(), q, AccessFilter{UserID: 1, ProfileID: "p1", ExcludedMediaTypes: []string{"audiobook"}}); err != nil {
		t.Fatalf("audiobook excluded cached: %v", err)
	}
	if fetches != 2 {
		t.Fatalf("excluded media type variants shared a cache entry: fetches=%d, want 2", fetches)
	}
}

// Two viewers with different library ceilings (e.g. two organizations, each
// entitled to a distinct library) must never share a cached grouped list —
// otherwise the second viewer's page would serve counts computed against the
// first viewer's library.
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

// A library ceiling that shrinks to empty (every entitlement revoked) must
// key separately from both an unrestricted (nil) ceiling and a populated one
// — otherwise a viewer who just lost all access could still be served a
// stale full grouped list from before the revocation.
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
