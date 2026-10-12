package storagesource

import (
	"context"
	"errors"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func scopedFixture(t *testing.T, roots ...string) (*Repository, SourceConfig, Location) {
	t.Helper()
	pool := storageTestPool(t)
	s, r := fixtureSource(t, pool)
	fixtureFolder(t, pool, 91000)
	l := fixtureLocation(t, r, s.Key, 91000)
	execSQL(t, pool, `DELETE FROM bloem_storage_selected_roots WHERE location_id=$1`, l.ID)
	for _, root := range roots {
		execSQL(t, pool, `INSERT INTO bloem_storage_selected_roots(id,location_id,directory_id,name,logical_path) VALUES($1,$2,$3,$3,'')`, uuid.New(), l.ID, root)
	}
	return r, s, l
}
func scopedBegin(t *testing.T, r *Repository, l Location) Lease {
	t.Helper()
	lease, err := r.BeginLocationAuthorized(t.Context(), l, "worker", time.Minute, func(context.Context, pgx.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return lease
}
func scopedRun(t *testing.T, r *Repository, l Location, client storagev1.StorageProviderClient, publish PagePublisher) Lease {
	t.Helper()
	lease := scopedBegin(t, r, l)
	for steps := 0; steps < 30; steps++ {
		cp, more, err := r.NextDirectory(t.Context(), lease)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			if err = r.Complete(t.Context(), lease); err != nil {
				t.Fatal(err)
			}
			return lease
		}
		page, err := r.FetchPage(t.Context(), lease, cp, client)
		if err != nil {
			t.Fatal(err)
		}
		if err = r.ApplyPage(t.Context(), lease, cp, page, publish); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("directory graph did not terminate")
	return lease
}
func scopedPublisher(t *testing.T, l Location, calls *int) PagePublisher {
	t.Helper()
	return func(ctx context.Context, tx pgx.Tx, files []*storagev1.Entry, _ []string) error {
		for _, e := range files {
			*calls++
			catalogPath, err := CatalogLocation(l.ID, e.Id)
			if err != nil {
				return err
			}
			var id int
			if err = tx.QueryRow(ctx, `INSERT INTO media_files(media_folder_id,file_path) VALUES($1,$2) ON CONFLICT(file_path) DO UPDATE SET missing_since=NULL RETURNING id`, l.FolderID, catalogPath).Scan(&id); err != nil {
				return err
			}
			if err = AttachFilesTx(ctx, tx, 1, []FileRef{{MediaFileID: id, PersistedRef: PersistedRef{LocationID: l.ID, EntryID: e.Id, Revision: e.Revision, LogicalPath: e.LogicalPath}}}); err != nil {
				return err
			}
		}
		return nil
	}
}
func dirEntry(id string) *storagev1.Entry {
	return &storagev1.Entry{Id: id, Name: id, Kind: storagev1.EntryKind_ENTRY_KIND_DIRECTORY, Revision: "d1"}
}

// Cross-library source staging must never substitute for independent listing evidence.
func TestLocationDiscoveryTokensAndInventoriesDB(t *testing.T) {
	r, s, a := scopedFixture(t, "root")
	fixtureFolder(t, r.pool, 91002)
	b := fixtureLocation(t, r, s.Key, 91002)
	var calls int
	client := providerClient(t, func(_ context.Context, q *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		if q.ChangesSince != "" {
			return nil, status.Error(codes.InvalidArgument, "another location's token")
		}
		return &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book")}, Complete: true, ChangeToken: "T"}, nil
	})
	scopedRun(t, r, a, client, scopedPublisher(t, a, &calls))
	scopedRun(t, r, b, client, scopedPublisher(t, b, &calls))
	var inventories, tokens, members int
	if err := r.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM bloem_storage_location_entries WHERE entry_id='book'),(SELECT count(*) FROM bloem_storage_change_tokens),(SELECT count(*) FROM bloem_storage_file_roots WHERE enabled)`).Scan(&inventories, &tokens, &members); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || inventories != 2 || tokens != 2 || members != 2 {
		t.Fatalf("independent publication: calls=%d inventories=%d tokens=%d memberships=%d", calls, inventories, tokens, members)
	}
}

// Repeated opaque identities across directories are aliases; repeats within one directory are pagination defects.
func TestSelectedGraphAliasesCyclesAndMembershipsDB(t *testing.T) {
	r, _, l := scopedFixture(t, "a", "b", "z")
	var calls int
	visits := map[string]int{}
	client := providerClient(t, func(_ context.Context, q *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		visits[q.DirectoryId]++
		page := &storagev1.ListResponse{Complete: true, Entries: []*storagev1.Entry{bookEntry("book")}}
		switch q.DirectoryId {
		case "a":
			page.Entries = append(page.Entries, dirEntry("b"))
		case "b", "z":
			page.Entries = append(page.Entries, dirEntry("a"))
		default:
			return nil, status.Error(codes.InvalidArgument, "outside selected roots")
		}
		return page, nil
	})
	lease := scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	var memberships, reaches int
	if err := r.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM bloem_storage_file_roots WHERE location_id=$1 AND enabled),(SELECT count(*) FROM bloem_storage_scan_directory_roots WHERE run_id=$2)`, l.ID, lease.RunID).Scan(&memberships, &reaches); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(visits) != 3 || visits["a"] != 1 || visits["b"] != 1 || visits["z"] != 1 || memberships != 3 || reaches != 7 {
		t.Fatalf("graph publication calls=%d visits=%v roots=%d reaches=%d", calls, visits, memberships, reaches)
	}
}
func TestSelectedGraphContradictoryIdentityDB(t *testing.T) {
	for _, field := range []string{"revision", "size", "kind"} {
		t.Run(field, func(t *testing.T) {
			r, _, l := scopedFixture(t, "a", "b")
			lease := scopedBegin(t, r, l)
			cp, _, err := r.NextDirectory(t.Context(), lease)
			if err != nil {
				t.Fatal(err)
			}
			if err = r.ApplyPage(t.Context(), lease, cp, &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book")}, Complete: true}, nil); err != nil {
				t.Fatal(err)
			}
			cp, _, err = r.NextDirectory(t.Context(), lease)
			if err != nil {
				t.Fatal(err)
			}
			e := bookEntry("book")
			switch field {
			case "revision":
				e.Revision = "v2"
			case "size":
				e.Size++
			case "kind":
				e.Kind = storagev1.EntryKind_ENTRY_KIND_DIRECTORY
			}
			if err = r.ApplyPage(t.Context(), lease, cp, &storagev1.ListResponse{Entries: []*storagev1.Entry{e}, Complete: true}, nil); !errors.Is(err, ErrReferenceConflict) {
				t.Fatalf("contradictory %s accepted: %v", field, err)
			}
			var pending bool
			if err = r.pool.QueryRow(t.Context(), `SELECT NOT complete FROM bloem_storage_scan_directories WHERE run_id=$1 AND directory_id='b'`, lease.RunID).Scan(&pending); err != nil || !pending {
				t.Fatalf("conflict committed: %v", err)
			}
		})
	}
}
func TestLocationDirectoryPaginationDuplicatesDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	lease := scopedBegin(t, r, l)
	cp, _, err := r.NextDirectory(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ApplyPage(t.Context(), lease, cp, &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book")}, NextCursor: "second"}, nil); err != nil {
		t.Fatal(err)
	}
	cp.Cursor = "second"
	if err = r.ApplyPage(t.Context(), lease, cp, &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book")}, Complete: true}, nil); err == nil {
		t.Fatal("duplicate pagination accepted")
	}
}
func TestLocationRemovalDuringProviderIODB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	lease := scopedBegin(t, r, l)
	cp, _, err := r.NextDirectory(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	client := providerClient(t, func(ctx context.Context, _ *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book")}, Complete: true}, nil
	})
	result := make(chan error, 1)
	var calls int
	go func() {
		page, err := r.FetchPage(t.Context(), lease, cp, client)
		if err == nil {
			err = r.ApplyPage(t.Context(), lease, cp, page, scopedPublisher(t, l, &calls))
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider gate missing")
	}
	execSQL(t, r.pool, `DELETE FROM bloem_storage_selected_roots WHERE location_id=$1`, l.ID)
	execSQL(t, r.pool, `UPDATE library_storage_locations SET selection_revision=selection_revision+1 WHERE id=$1`, l.ID)
	close(release)
	if err = <-result; !errors.Is(err, ErrStaleLease) {
		t.Fatalf("removed root recreated: %v", err)
	}
	var refs int
	if err = r.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_file_refs`).Scan(&refs); err != nil || refs != 0 || calls != 0 {
		t.Fatalf("stale publication refs=%d calls=%d err=%v", refs, calls, err)
	}
}
func TestLocationIncrementalUnchangedChildrenDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	var calls int
	requests := map[string]string{}
	client := providerClient(t, func(_ context.Context, q *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		requests[q.DirectoryId] = q.ChangesSince
		page := &storagev1.ListResponse{Complete: true, ChangeToken: q.DirectoryId + "-T"}
		if q.ChangesSince == "" {
			if q.DirectoryId == "root" {
				page.Entries = []*storagev1.Entry{dirEntry("child")}
			} else {
				page.Entries = []*storagev1.Entry{bookEntry("book")}
			}
		}
		return page, nil
	})
	scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	clear(requests)
	lease := scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	var roots int
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_file_roots WHERE location_id=$1 AND enabled`, l.ID).Scan(&roots); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests["root"] != "root-T" || requests["child"] != "child-T" || calls != 1 || roots != 1 {
		t.Fatalf("unchanged child lost: requests=%v calls=%d roots=%d", requests, calls, roots)
	}
	var modes int
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_scan_directories WHERE run_id=$1 AND complete AND listing_mode='incremental'`, lease.RunID).Scan(&modes); err != nil || modes != 2 {
		t.Fatalf("incremental evidence=%d err=%v", modes, err)
	}
}
func TestLocationIncrementalRemovalKeepsOtherRootDB(t *testing.T) {
	r, _, l := scopedFixture(t, "a", "b")
	var calls int
	round := 0
	client := providerClient(t, func(_ context.Context, q *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		page := &storagev1.ListResponse{Complete: true, ChangeToken: "T"}
		if round == 0 {
			page.Entries = []*storagev1.Entry{bookEntry("book")}
		} else if q.DirectoryId == "a" || round == 2 {
			page.RemovedEntryIds = []string{"book"}
		}
		return page, nil
	})
	scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	round = 1
	scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	var memberships, missing int
	check := func(wantRoots, wantMissing int) {
		t.Helper()
		if err := r.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM bloem_storage_file_roots WHERE location_id=$1 AND enabled),(SELECT count(*) FROM media_files WHERE media_folder_id=$2 AND missing_since IS NOT NULL)`, l.ID, l.FolderID).Scan(&memberships, &missing); err != nil {
			t.Fatal(err)
		}
		if memberships != wantRoots || missing != wantMissing {
			t.Fatalf("removal roots=%d missing=%d want=%d/%d", memberships, missing, wantRoots, wantMissing)
		}
	}
	check(1, 0)
	round = 2
	scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	check(0, 1)
}
func TestLocationEmptyFullFallbackPreservesScopedInventoryDB(t *testing.T) {
	r, s, a := scopedFixture(t, "root")
	fixtureFolder(t, r.pool, 91002)
	b := fixtureLocation(t, r, s.Key, 91002)
	var calls int
	empty := false
	client := providerClient(t, func(_ context.Context, q *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		if q.ChangesSince != "" {
			return nil, status.Error(codes.FailedPrecondition, "expired token")
		}
		page := &storagev1.ListResponse{Complete: true, ChangeToken: "T"}
		if !empty {
			page.Entries = []*storagev1.Entry{bookEntry("book")}
		}
		return page, nil
	})
	scopedRun(t, r, a, client, scopedPublisher(t, a, &calls))
	scopedRun(t, r, b, client, scopedPublisher(t, b, &calls))
	empty = true
	lease := scopedRun(t, r, a, client, scopedPublisher(t, a, &calls))
	var aMissing, bMissing int
	var mode string
	if err := r.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM media_files WHERE media_folder_id=$1 AND missing_since IS NOT NULL),(SELECT count(*) FROM media_files WHERE media_folder_id=$2 AND missing_since IS NOT NULL),(SELECT listing_mode FROM bloem_storage_scan_directories WHERE run_id=$3)`, a.FolderID, b.FolderID, lease.RunID).Scan(&aMissing, &bMissing, &mode); err != nil {
		t.Fatal(err)
	}
	if aMissing != 0 || bMissing != 0 || mode != "full" {
		t.Fatalf("fallback scoped absence=%d/%d mode=%s", aMissing, bMissing, mode)
	}
}
func TestLocationTakeoverCannotResumeAnotherSelectionDB(t *testing.T) {
	r, s, a := scopedFixture(t, "root")
	old := scopedBegin(t, r, a)
	fixtureFolder(t, r.pool, 91002)
	b := fixtureLocation(t, r, s.Key, 91002)
	if _, err := r.BeginLocationAuthorized(t.Context(), b, "other", time.Minute, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(err, ErrSourceBusy) {
		t.Fatalf("busy source: %v", err)
	}
	execSQL(t, r.pool, `UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.RunID)
	next := scopedBegin(t, r, b)
	if next.RunID == old.RunID || next.LocationID != b.ID {
		t.Fatal("another location resumed old run")
	}
	execSQL(t, r.pool, `UPDATE library_storage_locations SET selection_revision=2 WHERE id=$1`, b.ID)
	b.SelectionRevision = 2
	newer := scopedBegin(t, r, b)
	if newer.RunID == next.RunID || newer.SelectionRevision != 2 {
		t.Fatal("new selection resumed old graph")
	}
}

func TestLocationSourceLockContentionIsBusyDB(t *testing.T) {
	r, s, l := scopedFixture(t, "root")
	blocker, err := r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(t.Context(), `SELECT key FROM bloem_storage_sources WHERE key=$1 FOR SHARE`, s.Key); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err = r.BeginLocationAuthorized(ctx, l, "contending", time.Minute, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT key FROM bloem_storage_sources WHERE key=$1 FOR SHARE`, s.Key)
		return err
	})
	if !errors.Is(err, ErrSourceBusy) {
		t.Fatalf("shared source lock became deadline/deadlock: %v", err)
	}
	var runs int
	if err = r.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_scan_runs WHERE source_key=$1`, s.Key).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("contention created evidence: %d %v", runs, err)
	}
}

// Omitted subtree edges retain access after one full observation, are not
// traversed while suspect, and cannot retire another selected root's alias.
func TestLocationOmittedSubtreeNeedsSecondScopedObservationDB(t *testing.T) {
	r, _, l := scopedFixture(t, "a", "b")
	phase, childVisits, calls := 0, 0, 0
	client := providerClient(t, func(_ context.Context, q *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		page := &storagev1.ListResponse{Complete: true}
		switch q.DirectoryId {
		case "a", "b":
			page.Entries = []*storagev1.Entry{bookEntry("sentinel-" + q.DirectoryId)}
			if (q.DirectoryId == "a" && phase == 0) || (q.DirectoryId == "b" && phase < 3) {
				page.Entries = append(page.Entries, dirEntry("child"))
			}
		case "child":
			childVisits++
			page.Entries = []*storagev1.Entry{bookEntry("book")}
		default:
			t.Fatalf("unselected directory %s", q.DirectoryId)
		}
		return page, nil
	})
	check := func(roots, missing int) {
		t.Helper()
		var gotRoots, gotMissing int
		if err := r.pool.QueryRow(t.Context(), `SELECT
   (SELECT count(*) FROM bloem_storage_file_roots m JOIN bloem_storage_file_refs f ON f.location_id=m.location_id AND f.media_file_id=m.media_file_id WHERE m.location_id=$1 AND m.enabled AND f.entry_id='book'),
   (SELECT count(*) FROM media_files m JOIN bloem_storage_file_refs f ON f.media_file_id=m.id WHERE f.location_id=$1 AND f.entry_id='book' AND m.missing_since IS NOT NULL)`, l.ID).Scan(&gotRoots, &gotMissing); err != nil {
			t.Fatal(err)
		}
		if gotRoots != roots || gotMissing != missing {
			t.Fatalf("phase %d roots/missing=%d/%d want=%d/%d", phase, gotRoots, gotMissing, roots, missing)
		}
	}
	for phase = 0; phase < 5; phase++ {
		beforeVisits := childVisits
		scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
		switch phase {
		case 0, 1:
			check(2, 0)
		case 2, 3:
			check(1, 0)
		case 4:
			check(0, 1)
		}
		if phase == 4 && childVisits != beforeVisits {
			t.Fatal("confirmed-absent subtree was traversed")
		}
	}
	// Restoring the omitted subtree restores both roots and its published file.
	phase = 0
	scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	check(2, 0)
}

func TestLocationOmissionCannotUseStaleSelectionEvidenceDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	omit, calls := false, 0
	client := providerClient(t, func(_ context.Context, _ *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		entries := []*storagev1.Entry{bookEntry("sentinel")}
		if !omit {
			entries = append(entries, bookEntry("book"))
		}
		return &storagev1.ListResponse{Complete: true, Entries: entries}, nil
	})
	scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	omit = true
	scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	// The complete graph's retained selection changed between observations.
	execSQL(t, r.pool, `UPDATE library_storage_locations SET selection_revision=selection_revision+1 WHERE id=$1`, l.ID)
	l.SelectionRevision++
	scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	var missing int
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.location_id=$1 AND r.entry_id='book' AND f.missing_since IS NOT NULL`, l.ID).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != 0 {
		t.Fatal("prior selection's omission retired a retained file")
	}
	scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.location_id=$1 AND r.entry_id='book' AND f.missing_since IS NOT NULL`, l.ID).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != 1 {
		t.Fatal("two current-selection omissions did not retire the file")
	}
}
