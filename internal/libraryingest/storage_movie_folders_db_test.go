//go:build integration

package libraryingest

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-server/internal/metadata"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeNativeMovieMetadata stands in for the metadata pipeline: an identified
// item becomes matched, a title search finds nothing, and a listed TMDB ID
// fails once as a provider outage would.
type fakeNativeMovieMetadata struct {
	pool     *pgxpool.Pool
	mu       sync.Mutex
	requests []metadata.ProcessRequest
	failOnce map[string]bool
}

func (f *fakeNativeMovieMetadata) Process(ctx context.Context, req metadata.ProcessRequest) (*metadata.ProcessResult, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	fail := f.failOnce[req.ProviderIDs["tmdb"]]
	delete(f.failOnce, req.ProviderIDs["tmdb"])
	f.mu.Unlock()
	if fail {
		return nil, context.DeadlineExceeded
	}
	if req.Mode != metadata.ModeIdentify {
		return &metadata.ProcessResult{ContentID: req.ContentID}, nil
	}
	if _, err := f.pool.Exec(ctx, `UPDATE media_items SET status='matched' WHERE content_id=$1`, req.ContentID); err != nil {
		return nil, err
	}
	return &metadata.ProcessResult{ContentID: req.ContentID, Updated: true}, nil
}

func (f *fakeNativeMovieMetadata) take() []metadata.ProcessRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	requests := f.requests
	f.requests = nil
	slices.SortFunc(requests, func(a, b metadata.ProcessRequest) int {
		return strings.Compare(a.ProviderIDs["tmdb"]+a.ProviderIDs["imdb"]+"|"+hintTitle(a), b.ProviderIDs["tmdb"]+b.ProviderIDs["imdb"]+"|"+hintTitle(b))
	})
	return requests
}

func hintTitle(req metadata.ProcessRequest) string {
	if req.Hints == nil {
		return ""
	}
	return req.Hints.Title
}

type nativeMovieItem struct {
	title, status string
	year, files   int
	editions      string
}

func nativeMovieItems(t *testing.T, pool *pgxpool.Pool, folderID int) map[string]nativeMovieItem {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT i.content_id,i.title,i.year,i.status,count(f.id),string_agg(COALESCE(f.edition_key,''),',' ORDER BY f.edition_key)
		FROM media_items i JOIN media_files f ON f.content_id=i.content_id
		WHERE f.media_folder_id=$1 GROUP BY i.content_id,i.title,i.year,i.status`, folderID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	items := make(map[string]nativeMovieItem)
	for rows.Next() {
		var id string
		var item nativeMovieItem
		if err := rows.Scan(&id, &item.title, &item.year, &item.status, &item.files, &item.editions); err != nil {
			t.Fatal(err)
		}
		items[item.title] = item
		items[item.title+"#id"] = nativeMovieItem{title: id}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return items
}

// Folder names from the live movies bucket. Titles must come from the title
// folder, editions must share one item, sidecars, extras and samples must not
// be read or published, ignored folders must never be listed, and folder
// provider tags must match through the metadata pipeline, not the path queue.
func TestNativeMovieStorageTitleFoldersPublishAndMatch(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeMovieScanFixture(t, binary, manifest, true, "movies-folders")
	meta := &fakeNativeMovieMetadata{pool: f.pool, failOnce: map[string]bool{"949": true}}
	f.scan.ConfigureNativeMovieMatching(meta)

	result, err := f.scan.ScanStorageFolder(t.Context(), f.folder, f.location)
	if err != nil {
		t.Fatal(err)
	}
	if result.ScanResult.New != 4 || result.ScanResult.Unchanged != 4 {
		t.Fatalf("scan result %+v", result.ScanResult)
	}
	items := nativeMovieItems(t, f.pool, f.folder.ID)
	threeAM, eerie, heat := items["3 A.M."], items["13 Eerie"], items["Heat"]
	if len(items) != 6 || threeAM.year != 2001 || threeAM.files != 1 || threeAM.status != "matched" ||
		eerie.year != 2013 || eerie.files != 1 || eerie.status != "unmatched" ||
		heat.year != 1995 || heat.files != 2 || heat.editions != ",director_cut" || heat.status != "pending" {
		t.Fatalf("published items %+v", items)
	}
	audit, err := os.ReadFile(f.notify + ".io")
	if err != nil {
		t.Fatal(err)
	}
	if !bytesContainAll(audit, "read 02-three-am", "read 05-eerie", "read 08-heat-cut", "read 09-heat") {
		t.Fatalf("movies were not probed:\n%s", audit)
	}
	for _, id := range []string{"03-three-am-srt", "04-three-am-poster", "07-heat-trailer", "10-heat-sample"} {
		if strings.Contains(string(audit), " "+id) {
			t.Fatalf("skipped entry %s reached the provider:\n%s", id, audit)
		}
	}
	requests := meta.take()
	if len(requests) != 3 ||
		requests[0].Mode != metadata.ModeIdentify || requests[0].ProviderIDs["tmdb"] != "77084" || requests[0].ContentID != items["3 A.M.#id"].title ||
		requests[1].Mode != metadata.ModeIdentify || requests[1].ProviderIDs["tmdb"] != "949" || requests[1].ContentID != items["Heat#id"].title ||
		requests[2].Mode != metadata.ModeInitialMatch || requests[2].Hints == nil || requests[2].Hints.Title != "13 Eerie" || requests[2].Hints.Year != 2013 || requests[2].Hints.Type != "movie" {
		t.Fatalf("match requests %+v", requests)
	}
	var queued int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM movie_match_queue WHERE media_folder_id=$1`, f.folder.ID).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("native movies entered the path queue: %d %v", queued, err)
	}

	// The failed match stays pending and is retried; matched and unmatched
	// untagged items are not searched again.
	if _, err = f.scan.ScanStorageFolder(t.Context(), f.folder, f.location); err != nil {
		t.Fatal(err)
	}
	if requests = meta.take(); len(requests) != 1 || requests[0].ProviderIDs["tmdb"] != "949" {
		t.Fatalf("retry requests %+v", requests)
	}
	if items = nativeMovieItems(t, f.pool, f.folder.ID); items["Heat"].status != "matched" || items["Heat"].files != 2 {
		t.Fatalf("retried item %+v", items["Heat"])
	}

	// A path-queue pass can mark a native item unmatched; its folder tags
	// still identify it on the next scan.
	if _, err = f.pool.Exec(t.Context(), `UPDATE media_items SET status='unmatched' WHERE content_id=$1`, items["3 A.M.#id"].title); err != nil {
		t.Fatal(err)
	}
	if _, err = f.scan.ScanStorageFolder(t.Context(), f.folder, f.location); err != nil {
		t.Fatal(err)
	}
	if requests = meta.take(); len(requests) != 1 || requests[0].ProviderIDs["tmdb"] != "77084" {
		t.Fatalf("tagged unmatched requests %+v", requests)
	}
	if _, err = f.scan.ScanStorageFolder(t.Context(), f.folder, f.location); err != nil {
		t.Fatal(err)
	}
	if requests = meta.take(); len(requests) != 0 {
		t.Fatalf("settled library searched again %+v", requests)
	}
}
