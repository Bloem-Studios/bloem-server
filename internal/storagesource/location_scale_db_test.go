package storagesource

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// TestLocationScanScaleDB lists a synthetic Bookwarehouse-shaped location
// (a root holding an epub and a pdf directory) of BLOEM_STORAGE_SCALE_ENTRIES
// files in 512-entry pages, with a page authority that holds the library row
// lock and a renewal on the production cadence, then lists it again
// incrementally. It logs page, renewal and completion timings. Opt-in: it
// takes minutes at library scale.
func TestLocationScanScaleDB(t *testing.T) {
	total, _ := strconv.Atoi(os.Getenv("BLOEM_STORAGE_SCALE_ENTRIES"))
	if total <= 0 {
		t.Skip("BLOEM_STORAGE_SCALE_ENTRIES is not set")
	}
	const perPage = 512
	pdfs := max(total/100, 1)
	counts := map[string]int{"epub": total - pdfs, "pdf": pdfs}
	r, _, l := scopedFixture(t, "root")
	ctx := t.Context()
	list := func(_ context.Context, q *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		page := &storagev1.ListResponse{ChangeToken: q.DirectoryId + "-T"}
		if q.ChangesSince != "" {
			page.Complete = true
			return page, nil
		}
		if q.DirectoryId == "root" {
			page.Entries = []*storagev1.Entry{dirEntry("epub"), dirEntry("pdf")}
			page.Complete = true
			return page, nil
		}
		start := 0
		if q.Cursor != "" {
			start, _ = strconv.Atoi(q.Cursor)
		}
		end := min(start+perPage, counts[q.DirectoryId])
		for i := start; i < end; i++ {
			id := fmt.Sprintf("%s-%07d", q.DirectoryId, i)
			page.Entries = append(page.Entries, &storagev1.Entry{Id: id, Name: id + "." + q.DirectoryId, LogicalPath: q.DirectoryId + "/" + id, Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 1000 + int64(i), Revision: "v1"})
		}
		if end < counts[q.DirectoryId] {
			page.NextCursor = strconv.Itoa(end)
			page.ChangeToken = ""
		} else {
			page.Complete = true
		}
		return page, nil
	}
	client := providerClient(t, list)
	lockLibrary := func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT 1 FROM media_folders WHERE id=$1 FOR UPDATE`, l.FolderID)
		return err
	}
	publish := scalePublisher(l)
	for round := 0; round < 2; round++ {
		const ttl = time.Minute
		lease, err := r.BeginLocationAuthorized(ctx, l, "scale", ttl, lockLibrary)
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		var renewals []time.Duration
		var renewalFailures int
		renewCtx, stopRenew := context.WithCancel(ctx)
		renewDone := make(chan struct{})
		go func() {
			defer close(renewDone)
			ticker := time.NewTicker(ttl / 3)
			defer ticker.Stop()
			for {
				select {
				case <-renewCtx.Done():
					return
				case <-ticker.C:
				}
				attempt, cancel := context.WithTimeout(renewCtx, 10*time.Second)
				started := time.Now()
				err := r.RenewAuthorized(attempt, lease, ttl, lockLibrary)
				cancel()
				mu.Lock()
				renewals = append(renewals, time.Since(started))
				if err != nil && renewCtx.Err() == nil {
					renewalFailures++
				}
				mu.Unlock()
			}
		}()
		started := time.Now()
		var pages []time.Duration
		listed, nextReport := 0, total/10
		for {
			cp, more, err := r.NextDirectory(ctx, lease)
			if err != nil {
				t.Fatal(err)
			}
			if !more {
				break
			}
			page, err := r.FetchPage(ctx, lease, cp, client)
			if err != nil {
				t.Fatal(err)
			}
			pageStarted := time.Now()
			if err = r.ApplyPageAuthorized(ctx, lease, cp, page, lockLibrary, publish); err != nil {
				t.Fatalf("round %d page %d after %d entries: %v", round, len(pages), listed, err)
			}
			pages = append(pages, time.Since(pageStarted))
			listed += len(page.GetEntries())
			// A live database's autovacuum keeps statistics roughly proportional
			// to table size; a fresh test database has none. Refresh them as
			// the location doubles, as autovacuum's scale factor would.
			if n := len(pages); n&(n-1) == 0 {
				execSQL(t, r.pool, `ANALYZE`)
			}
			if round == 0 && listed >= nextReport {
				recent := pages[max(len(pages)-20, 0):]
				t.Logf("listed %7d entries in %6.1fs: last-20-page mean %v, max page so far %v", listed, time.Since(started).Seconds(), mean(recent).Round(time.Millisecond), slices.Max(pages).Round(time.Millisecond))
				nextReport += total / 10
			}
		}
		listing := time.Since(started)
		stopRenew()
		<-renewDone
		completeStarted := time.Now()
		if err = r.CompleteAuthorized(ctx, lease, lockLibrary, nil); err != nil {
			t.Fatalf("round %d completion: %v", round, err)
		}
		completion := time.Since(completeStarted)
		var members, missing int
		if err = r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM bloem_storage_file_roots WHERE location_id=$1 AND enabled),(SELECT count(*) FROM media_files WHERE media_folder_id=$2 AND missing_since IS NOT NULL)`, l.ID, l.FolderID).Scan(&members, &missing); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		maxRenewal := time.Duration(0)
		if len(renewals) > 0 {
			maxRenewal = slices.Max(renewals)
		}
		t.Logf("round %d: %d pages, listing %v (%.0f entries/s), mean page %v, max page %v, renewals %d (max wait %v, failed %d), completion %v; memberships %d, missing %d",
			round, len(pages), listing.Round(time.Millisecond), float64(listed)/listing.Seconds(), mean(pages).Round(time.Millisecond), slices.Max(pages).Round(time.Millisecond),
			len(renewals), maxRenewal.Round(time.Millisecond), renewalFailures, completion.Round(time.Millisecond), members, missing)
		mu.Unlock()
		if members != total || missing != 0 {
			t.Fatalf("round %d published %d memberships, %d missing, want %d present", round, members, missing, total)
		}
		if completion >= ttl {
			t.Fatalf("round %d completion %v outlasted the %v lease", round, completion, ttl)
		}
	}
}

// scalePublisher attaches each page's files as the catalog does: one
// media_files upsert and one AttachFilesTx per page.
func scalePublisher(l Location) PagePublisher {
	return func(ctx context.Context, tx pgx.Tx, files []*storagev1.Entry, _ []string) error {
		if len(files) == 0 {
			return nil
		}
		paths := make([]string, len(files))
		for i, e := range files {
			catalogPath, err := CatalogLocation(l.ID, e.Id)
			if err != nil {
				return err
			}
			paths[i] = catalogPath
		}
		rows, err := tx.Query(ctx, `INSERT INTO media_files(media_folder_id,file_path) SELECT $1,p FROM unnest($2::text[]) p ON CONFLICT(file_path) DO UPDATE SET missing_since=NULL RETURNING id,file_path`, l.FolderID, paths)
		if err != nil {
			return err
		}
		ids := map[string]int{}
		for rows.Next() {
			var id int
			var path string
			if err = rows.Scan(&id, &path); err != nil {
				rows.Close()
				return err
			}
			ids[path] = id
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		refs := make([]FileRef, len(files))
		for i, e := range files {
			refs[i] = FileRef{MediaFileID: ids[paths[i]], PersistedRef: PersistedRef{LocationID: l.ID, EntryID: e.Id, Revision: e.Revision, LogicalPath: e.LogicalPath}}
		}
		return AttachFilesTx(ctx, tx, 1, refs)
	}
}

// TestLocationTreeScanScaleDB lists a tree-shaped location, as an S3 movie or
// audiobook library is: a root of BLOEM_STORAGE_SCALE_TREE_DIRS directories of
// BLOEM_STORAGE_SCALE_TREE_FILES files each. It then lists it incrementally
// with a root page that removes BLOEM_STORAGE_SCALE_TREE_REMOVED directories,
// so that page rebuilds the reach. Completion recomputes the whole tree's
// reach, which is the costliest completion shape. Opt-in: it logs page and
// completion timings and fails a completion that outlasts its lease.
func TestLocationTreeScanScaleDB(t *testing.T) {
	dirs, _ := strconv.Atoi(os.Getenv("BLOEM_STORAGE_SCALE_TREE_DIRS"))
	filesPer, _ := strconv.Atoi(os.Getenv("BLOEM_STORAGE_SCALE_TREE_FILES"))
	removedDirs, _ := strconv.Atoi(os.Getenv("BLOEM_STORAGE_SCALE_TREE_REMOVED"))
	if dirs <= 0 || filesPer <= 0 {
		t.Skip("BLOEM_STORAGE_SCALE_TREE_DIRS and BLOEM_STORAGE_SCALE_TREE_FILES are not set")
	}
	const perPage = 512
	r, _, l := scopedFixture(t, "root")
	ctx := t.Context()
	dirID := func(i int) string { return fmt.Sprintf("d%05d", i) }
	removed := make([]string, 0, removedDirs)
	for i := range min(removedDirs, dirs) {
		removed = append(removed, dirID(i*(dirs/max(removedDirs, 1))))
	}
	list := func(_ context.Context, q *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		page := &storagev1.ListResponse{}
		if q.ChangesSince != "" {
			page.Complete = true
			page.ChangeToken = q.DirectoryId + "-T2"
			if q.DirectoryId == "root" {
				page.RemovedEntryIds = removed
			}
			return page, nil
		}
		start := 0
		if q.Cursor != "" {
			start, _ = strconv.Atoi(q.Cursor)
		}
		total := filesPer
		if q.DirectoryId == "root" {
			total = dirs
		}
		end := min(start+perPage, total)
		for i := start; i < end; i++ {
			if q.DirectoryId == "root" {
				page.Entries = append(page.Entries, dirEntry(dirID(i)))
				continue
			}
			id := fmt.Sprintf("%s-f%04d", q.DirectoryId, i)
			page.Entries = append(page.Entries, &storagev1.Entry{Id: id, Name: id + ".epub", LogicalPath: q.DirectoryId + "/" + id, Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 1000 + int64(i), Revision: "v1"})
		}
		if end < total {
			page.NextCursor = strconv.Itoa(end)
		} else {
			page.Complete = true
			page.ChangeToken = q.DirectoryId + "-T"
		}
		return page, nil
	}
	client := providerClient(t, list)
	lockLibrary := func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT 1 FROM media_folders WHERE id=$1 FOR UPDATE`, l.FolderID)
		return err
	}
	publish := scalePublisher(l)
	for round := 0; round < 2; round++ {
		const ttl = time.Minute
		lease, err := r.BeginLocationAuthorized(ctx, l, "tree", ttl, lockLibrary)
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		var pages, rootPages []time.Duration
		listed := 0
		for {
			cp, more, err := r.NextDirectory(ctx, lease)
			if err != nil {
				t.Fatal(err)
			}
			if !more {
				break
			}
			page, err := r.FetchPage(ctx, lease, cp, client)
			if err != nil {
				t.Fatal(err)
			}
			pageStarted := time.Now()
			if err = r.ApplyPageAuthorized(ctx, lease, cp, page, lockLibrary, publish); err != nil {
				t.Fatalf("round %d page %d: %v", round, len(pages), err)
			}
			d := time.Since(pageStarted)
			pages = append(pages, d)
			if cp.DirectoryID == "root" {
				rootPages = append(rootPages, d.Round(time.Millisecond))
			}
			listed += len(page.GetEntries())
			if n := len(pages); n&(n-1) == 0 {
				execSQL(t, r.pool, `ANALYZE`)
			}
		}
		listing := time.Since(started)
		completeStarted := time.Now()
		if err = r.CompleteAuthorized(ctx, lease, lockLibrary, nil); err != nil {
			t.Fatalf("round %d completion: %v", round, err)
		}
		completion := time.Since(completeStarted)
		var members int
		if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM bloem_storage_file_roots WHERE location_id=$1 AND enabled`, l.ID).Scan(&members); err != nil {
			t.Fatal(err)
		}
		t.Logf("round %d: %d pages (root pages %v), listing %v, mean page %v, max page %v, completion %v, memberships %d",
			round, len(pages), rootPages, listing.Round(time.Millisecond), mean(pages).Round(time.Millisecond), slices.Max(pages).Round(time.Millisecond), completion.Round(time.Millisecond), members)
		want := dirs * filesPer
		if round == 1 {
			want -= len(removed) * filesPer
		}
		if members != want {
			t.Fatalf("round %d published %d memberships, want %d", round, members, want)
		}
		if completion >= ttl {
			t.Fatalf("round %d completion %v outlasted the %v lease", round, completion, ttl)
		}
	}
}

func mean(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	var sum time.Duration
	for _, v := range d {
		sum += v
	}
	return sum / time.Duration(len(d))
}
