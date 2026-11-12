package libraryingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/remotesource"
	"github.com/Silo-Server/silo-server/internal/scanner"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// A rescan of a page of 5,000 unchanged `.strm` objects reads their rows in
// one query and sends one one-byte request each, a few at a time, and opens
// none of them. One changed object is left for probing.
func TestStrmReuseScalesToThousandsOfEntriesDB(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	const entries = 5000
	var requests, inFlight, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		now := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		if r.Header.Get("Range") != "bytes=0-0" {
			t.Errorf("an unchanged entry asked for %q", r.Header.Get("Range"))
		}
		w.Header().Set("ETag", `"`+r.URL.Path+`"`)
		http.ServeContent(w, r, "x.mkv", time.Unix(1_700_000_000, 0), bytes.NewReader(make([]byte, 4096)))
	}))
	t.Cleanup(server.Close)

	location := storagesource.Location{ID: uuid.New()}
	probe, _ := json.Marshal(scanner.ProbeData{CodecVideo: "h264", Container: "mkv", Duration: 60, VideoTracks: []scanner.VideoTrackInfo{{Codec: "h264", Width: 640, Height: 360}}})
	page := &storagev1.ListResponse{}
	paths := make([]string, 0, entries)
	urls := make([]string, 0, entries)
	etags := make([]string, 0, entries)
	strms := make([]int, 0, entries)
	for i := range entries {
		id := fmt.Sprintf("movies/%05d.strm", i)
		page.Entries = append(page.Entries, &storagev1.Entry{Id: id, Name: fmt.Sprintf("%05d.strm", i), Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Revision: "object-v1"})
		key, err := storagesource.CatalogLocation(location.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, key)
		path := fmt.Sprintf("/films/%05d.mkv", i)
		urls = append(urls, server.URL+path)
		etags = append(etags, `"`+path+`"`)
		strms = append(strms, i)
	}
	// The last object has a new revision since its row was written.
	page.Entries[entries-1].Revision = "object-v2"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM bloem_remote_sources WHERE file_path = ANY($1)`, paths)
	})
	if _, err := pool.Exec(t.Context(), `INSERT INTO bloem_remote_sources(file_path,url,revision,source_revision,remote_size,etag,last_modified,probe)
		SELECT p, u, 'url-sha256:' || encode(sha256(convert_to(u,'UTF8')),'hex'), 'object-v1', 4096, e, NULL, $4::jsonb
		FROM unnest($1::text[], $2::text[], $3::text[]) AS t(p,u,e)`, paths, urls, etags, string(probe)); err != nil {
		t.Fatal(err)
	}
	c := &StorageScanner{remote: &remotesource.Runtime{Store: remotesource.NewStore(pool), Guard: remotesource.NewGuard(remotesource.Options{AllowPrivate: func(context.Context) bool { return true }})}}

	began := time.Now()
	movies, probeLeft, err := c.reuseStrmMovies(t.Context(), location, page, strms)
	elapsed := time.Since(began)
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != entries-1 || len(probeLeft) != 1 || probeLeft[0] != entries-1 {
		t.Fatalf("reused %d, left %v; want %d reused and the changed object left", len(movies), probeLeft, entries-1)
	}
	if movies[0].Probe == nil || movies[0].Probe.CodecVideo != "h264" {
		t.Fatalf("reused probe = %+v", movies[0].Probe)
	}
	if got := requests.Load(); got != entries-1 {
		t.Fatalf("validator requests = %d, want %d", got, entries-1)
	}
	if p := peak.Load(); p > strmReuseWorkers || p < 2 {
		t.Fatalf("peak concurrent requests = %d, want 2..%d", p, strmReuseWorkers)
	}
	t.Logf("checked %d unchanged .strm entries in %v", entries-1, elapsed)
}
