package storagesource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// An old partial journal and old token are not proof that any directory was
// fully traversed. Upgrade retains publication and forces a fresh full run.
func TestLocationListingEvidenceUpgradeFencesUnknownResumeDB(t *testing.T) {
	r, s, l := scopedFixture(t, "root")
	paths, err := filepath.Glob("../../migrations/sql/*_bloem_storage_directory_listing_evidence.sql")
	if err != nil || len(paths) != 1 {
		t.Fatal("evidence migration missing", err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("invalid migration")
	}
	execSQL(t, r.pool, parts[1])
	oldRun := uuid.New()
	execSQL(t, r.pool, `INSERT INTO bloem_storage_scan_runs(id,source_key,configuration_revision,state,lease_epoch,owner,lease_until,location_id,selection_revision) VALUES($1,$2,1,'running',1,'legacy',clock_timestamp()+interval '1 hour',$3,1)`, oldRun, s.Key, l.ID)
	execSQL(t, r.pool, `INSERT INTO bloem_storage_scan_directories(run_id,directory_id,current_cursor,processed_count) VALUES($1,'root','partial',1)`, oldRun)
	execSQL(t, r.pool, `INSERT INTO bloem_storage_location_directories(location_id,directory_id,configuration_revision,selection_revision,last_complete_run) VALUES($1,'root',1,1,$2)`, l.ID, oldRun)
	execSQL(t, r.pool, `INSERT INTO bloem_storage_change_tokens(source_key,location_id,directory_id,configuration_revision,selection_revision,token) VALUES($1,$2,'root',1,1,'legacy-token')`, s.Key, l.ID)
	catalogPath, err := CatalogLocation(l.ID, "retained-book")
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, r.pool, `INSERT INTO media_files(id,media_folder_id,file_path) VALUES(91003,$1,$2)`, l.FolderID, catalogPath)
	execSQL(t, r.pool, `INSERT INTO bloem_storage_file_refs(media_file_id,location_id,entry_id,revision,logical_path,configuration_revision) VALUES(91003,$1,'retained-book','v1','Book.epub',1)`, l.ID)
	execSQL(t, r.pool, `INSERT INTO bloem_storage_file_roots(location_id,media_file_id,root_id) SELECT $1,91003,id FROM bloem_storage_selected_roots WHERE location_id=$1`, l.ID)
	execSQL(t, r.pool, parts[0])
	old := Lease{RunID: oldRun, SourceKey: s.Key, LocationID: l.ID, SelectionRevision: 1, ConfigurationRevision: 1, Epoch: 1, Owner: "legacy"}
	if _, _, err = r.NextDirectory(t.Context(), old); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("unknown legacy resume admitted: %v", err)
	}
	var state, mode, token string
	var missing bool
	if err = r.pool.QueryRow(t.Context(), `SELECT r.state,d.listing_mode,t.token,f.missing_since IS NOT NULL FROM bloem_storage_scan_runs r JOIN bloem_storage_scan_directories d ON d.run_id=r.id JOIN bloem_storage_change_tokens t ON t.location_id=r.location_id JOIN media_files f ON f.id=91003 WHERE r.id=$1`, oldRun).Scan(&state, &mode, &token, &missing); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || mode != "pending" || token != "legacy-token" || missing {
		t.Fatalf("upgrade invented evidence or lost data: %s/%s/%s/%v", state, mode, token, missing)
	}
	lease := scopedBegin(t, r, l)
	if lease.RunID == oldRun {
		t.Fatal("legacy journal resumed")
	}
	cp, more, err := r.NextDirectory(t.Context(), lease)
	if err != nil || !more || cp.Cursor != "" {
		t.Fatal("fresh traversal absent", err)
	}
	client := providerClient(t, func(_ context.Context, q *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		if q.ChangesSince != "" || q.Cursor != "" {
			return nil, status.Error(codes.InvalidArgument, "unknown evidence resumed")
		}
		return &storagev1.ListResponse{Complete: true}, nil
	})
	page, err := r.FetchPage(t.Context(), lease, cp, client)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ApplyPage(t.Context(), lease, cp, page, nil); err != nil {
		t.Fatal(err)
	}
	if err = r.pool.QueryRow(t.Context(), `SELECT missing_since IS NOT NULL FROM media_files WHERE id=91003`).Scan(&missing); err != nil || missing {
		t.Fatalf("page invented final absence: %v", err)
	}
	if err = r.Complete(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if err = r.pool.QueryRow(t.Context(), `SELECT missing_since IS NOT NULL FROM media_files WHERE id=91003`).Scan(&missing); err != nil || !missing {
		t.Fatalf("fresh full completion did not establish absence: %v", err)
	}
}
func TestLocationFallbackAfterIncrementalPageDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	var calls int
	round := 0
	refused := false
	client := providerClient(t, func(_ context.Context, q *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		if round == 0 || q.ChangesSince == "" {
			return &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book")}, Complete: true, ChangeToken: "T"}, nil
		}
		if q.Cursor == "" {
			return &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("provisional")}, NextCursor: "second"}, nil
		}
		refused = true
		return nil, status.Error(codes.FailedPrecondition, "incremental cursor expired")
	})
	scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	round = 1
	lease := scopedBegin(t, r, l)
	for step := 0; step < 5; step++ {
		cp, more, err := r.NextDirectory(t.Context(), lease)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			if err = r.Complete(t.Context(), lease); err != nil {
				t.Fatal(err)
			}
			break
		}
		page, err := r.FetchPage(t.Context(), lease, cp, client)
		if errors.Is(err, ErrCheckpointRestart) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = r.ApplyPage(t.Context(), lease, cp, page, scopedPublisher(t, l, &calls)); err != nil {
			t.Fatal(err)
		}
	}
	var mode, state string
	var pages, seen, missing int
	if err := r.pool.QueryRow(t.Context(), `SELECT d.listing_mode,r.state,(SELECT count(*) FROM bloem_storage_scan_cursors WHERE run_id=$1),(SELECT count(*) FROM bloem_storage_scan_directory_entries WHERE run_id=$1),(SELECT count(*) FROM media_files WHERE media_folder_id=$2 AND missing_since IS NOT NULL) FROM bloem_storage_scan_directories d JOIN bloem_storage_scan_runs r ON r.id=d.run_id WHERE d.run_id=$1`, lease.RunID, l.FolderID).Scan(&mode, &state, &pages, &seen, &missing); err != nil {
		t.Fatal(err)
	}
	if !refused || mode != "full" || state != "complete" || pages != 1 || seen != 1 || missing != 1 {
		t.Fatalf("fallback failed fresh full traversal: refused=%v mode=%s state=%s pages=%d seen=%d missing=%d", refused, mode, state, pages, seen, missing)
	}
}
func TestLocationIncompleteRemovalCannotProveAbsenceDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	var calls int
	client := providerClient(t, func(context.Context, *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		return &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book")}, Complete: true, ChangeToken: "T"}, nil
	})
	scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	lease := scopedBegin(t, r, l)
	cp, _, err := r.NextDirectory(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	// Fetch establishes incremental mode; the interrupted delta has an explicit
	// removal but no terminal directory evidence.
	if _, err = r.FetchPage(t.Context(), lease, cp, client); err != nil {
		t.Fatal(err)
	}
	if err = r.ApplyPage(t.Context(), lease, cp, &storagev1.ListResponse{RemovedEntryIds: []string{"book"}, NextCursor: "unavailable-next"}, nil); err != nil {
		t.Fatal(err)
	}
	if err = r.Complete(t.Context(), lease); !errors.Is(err, ErrCheckpointConflict) {
		t.Fatalf("incomplete removal completed: %v", err)
	}
	var missing, active int
	if err = r.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM media_files WHERE missing_since IS NOT NULL),(SELECT count(*) FROM bloem_storage_file_roots WHERE enabled)`).Scan(&missing, &active); err != nil || missing != 0 || active != 1 {
		t.Fatalf("incomplete run revoked publication: missing=%d active=%d err=%v", missing, active, err)
	}
	// Failed finalization must also roll back absence/membership changes.
	cp.Cursor = "unavailable-next"
	if err = r.ApplyPage(t.Context(), lease, cp, &storagev1.ListResponse{Complete: true}, nil); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("finalization unavailable")
	if err = r.CompleteAuthorized(t.Context(), lease, func(context.Context, pgx.Tx) error { return nil }, func(context.Context, pgx.Tx) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err = r.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM media_files WHERE missing_since IS NOT NULL),(SELECT count(*) FROM bloem_storage_file_roots WHERE enabled)`).Scan(&missing, &active); err != nil || missing != 0 || active != 1 {
		t.Fatalf("failed finalization revoked publication: %d/%d %v", missing, active, err)
	}
}
