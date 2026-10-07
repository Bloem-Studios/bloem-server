//go:build integration

package storagesource

// PRE-MODE repository/protocol/migration controls only. Callback stand-ins and
// trusted file/ref writes here supply no CURRENT native publication authority.
// Legal current publication coverage belongs to the B-backed consumer fixture.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func ingestionMigrationSQL(t *testing.T, up bool) string {
	t.Helper()
	paths, err := filepath.Glob(preModeSourceDir + "/migrations/sql/*_bloem_native_storage_ingestion.sql")
	if err != nil || len(paths) != 1 {
		t.Fatal("ingestion migration absent or ambiguous")
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("ingestion down absent")
	}
	if up {
		return parts[0]
	}
	return parts[1]
}
func ingestionFixture(t *testing.T) (*Repository, SourceConfig, Lease, Binding) {
	t.Helper()
	r, s, lease, checkpoint := scanFixture(t)
	fixtureFolder(t, r.pool, 91001)
	b, err := r.Bind(context.Background(), s.Key, 91001)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ApplyPage(context.Background(), lease, checkpoint, &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("a"), bookEntry("b")}, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err = r.Complete(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	return r, s, lease, b
}
func claimFixture(t *testing.T) (*Repository, SourceConfig, Binding, IngestionLease, IngestionClaim) {
	t.Helper()
	r, s, run, b := ingestionFixture(t)
	l, err := r.BeginIngestion(context.Background(), run.RunID, b.ID, "ingester", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	c, ok, err := r.NextIngestion(context.Background(), l)
	if err != nil || !ok || c.Entry.GetId() != "a" {
		t.Fatalf("first claim: %v", err)
	}
	return r, s, b, l, c
}
func TestNativeIngestionRestartAndFencing(t *testing.T) {
	r, _, _, lease, claim := claimFixture(t)
	restarted := NewRepository(r.pool)
	next, err := restarted.BeginIngestion(context.Background(), lease.RunID, lease.BindingID, "ingester", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if next.Epoch <= lease.Epoch {
		t.Fatal("restart did not fence predecessor")
	}
	if err = r.PublishIngestion(context.Background(), claim, nil); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("stale publish: %v", err)
	}
	c, ok, err := restarted.NextIngestion(context.Background(), next)
	if err != nil || !ok || c.Entry.GetId() != "a" || c.Token == claim.Token {
		t.Fatalf("restart claim: %v", err)
	}
	if err = restarted.PublishIngestion(context.Background(), c, nil); err != nil {
		t.Fatal(err)
	}
	c, ok, err = restarted.NextIngestion(context.Background(), next)
	if err != nil || !ok || c.Entry.GetId() != "b" {
		t.Fatalf("next claim: %v", err)
	}
	if err = restarted.PublishIngestion(context.Background(), c, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = restarted.NextIngestion(context.Background(), next); err != nil || ok {
		t.Fatalf("completed generation: %v", err)
	}
}
func TestNativeIngestionAtomicFileReferenceAndCheckpoint(t *testing.T) {
	r, _, b, lease, claim := claimFixture(t)
	ctx := context.Background()
	location, err := CatalogLocation(b.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, r.pool, "INSERT INTO media_items(content_id,type,title) VALUES('native-book','ebook','Parsed title')")
	publish := func(ctx context.Context, tx pgx.Tx, entry *storagev1.Entry) error {
		if _, err := tx.Exec(ctx, "INSERT INTO media_files(id,content_id,media_folder_id,file_path,container) VALUES(91001,'native-book',91001,$1,'epub')", location); err != nil {
			return err
		}
		return r.AttachFileTx(ctx, tx, 91001, PersistedRef{BindingID: b.ID, EntryID: entry.GetId(), Revision: entry.GetRevision(), LogicalPath: entry.GetLogicalPath()})
	}
	late := errors.New("synthetic late SQL failure")
	if err = r.PublishIngestion(ctx, claim, func(ctx context.Context, tx pgx.Tx, e *storagev1.Entry) error {
		if err := publish(ctx, tx, e); err != nil {
			return err
		}
		return late
	}); !errors.Is(err, late) {
		t.Fatalf("late failure: %v", err)
	}
	var files, refs int
	if err = r.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM media_files WHERE id=91001),(SELECT count(*) FROM bloem_storage_file_refs)").Scan(&files, &refs); err != nil || files != 0 || refs != 0 {
		t.Fatalf("partial publication: %d/%d %v", files, refs, err)
	}
	again, ok, err := r.NextIngestion(ctx, lease)
	if err != nil || !ok || again.Token != claim.Token {
		t.Fatalf("checkpoint moved after rollback: %v", err)
	}
	if err = r.PublishIngestion(ctx, claim, publish); err != nil {
		t.Fatal(err)
	}
	if err = r.PublishIngestion(ctx, claim, nil); !errors.Is(err, ErrCheckpointConflict) {
		t.Fatalf("duplicate publish: %v", err)
	}
	if err = r.pool.QueryRow(ctx, "SELECT count(*) FROM bloem_storage_file_refs WHERE media_file_id=91001 AND revision='v1'").Scan(&refs); err != nil || refs != 1 {
		t.Fatalf("reference absent: %v", err)
	}
}
func TestNativeIngestionRevisionAndConfigurationFencing(t *testing.T) {
	for _, change := range []string{"entry", "config", "disabled", "expired"} {
		t.Run(change, func(t *testing.T) {
			r, s, _, lease, claim := claimFixture(t)
			switch change {
			case "entry":
				execSQL(t, r.pool, "UPDATE bloem_storage_entries SET revision='v2' WHERE source_key=$1 AND entry_id='a'", s.Key)
			case "config":
				execSQL(t, r.pool, "UPDATE bloem_storage_sources SET configuration_revision=2 WHERE key=$1", s.Key)
			case "disabled":
				execSQL(t, r.pool, "UPDATE bloem_storage_sources SET enabled=false WHERE key=$1", s.Key)
			case "expired":
				execSQL(t, r.pool, "UPDATE bloem_storage_ingestion SET lease_until=clock_timestamp()-interval '1 second' WHERE run_id=$1 AND binding_id=$2", lease.RunID, lease.BindingID)
			}
			called := false
			err := r.PublishIngestion(context.Background(), claim, func(context.Context, pgx.Tx, *storagev1.Entry) error { called = true; return nil })
			if err == nil || called {
				t.Fatalf("superseded data reached catalog publisher: %v", err)
			}
		})
	}
}
func TestNativeIngestionRequiresCompletedDiscoveryAndCorrectBinding(t *testing.T) {
	r, s, run, _ := scanFixture(t)
	fixtureFolder(t, r.pool, 91001)
	b, err := r.Bind(context.Background(), s.Key, 91001)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.BeginIngestion(context.Background(), run.RunID, b.ID, "ingester", time.Minute); err == nil {
		t.Fatal("incomplete discovery ingested")
	}
	if _, err = r.BeginIngestion(context.Background(), uuid.New(), b.ID, "ingester", time.Minute); err == nil {
		t.Fatal("unrelated run ingested")
	}
}
func TestNativeIngestionForeignLeaseAndGuardedDown(t *testing.T) {
	r, _, b, l, _ := claimFixture(t)
	if _, err := r.BeginIngestion(context.Background(), l.RunID, b.ID, "foreign", time.Minute); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("active lease stolen: %v", err)
	}
	if _, err := r.pool.Exec(context.Background(), ingestionMigrationSQL(t, false)); err == nil {
		t.Fatal("populated checkpoint dropped")
	}
}

func TestNativeIngestionNewDiscoveryGenerationFencesOldPublication(t *testing.T) {
	r, s, _, _, claim := claimFixture(t)
	if _, err := r.Begin(context.Background(), s.Key, "new-discovery", time.Minute); err != nil {
		t.Fatal(err)
	}
	called := false
	err := r.PublishIngestion(context.Background(), claim, func(context.Context, pgx.Tx, *storagev1.Entry) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("newer discovery did not fence old publication: %v", err)
	}
}

func TestNativeIngestionBindingLookupAndPendingRun(t *testing.T) {
	r, s, run, b := ingestionFixture(t)
	ctx := context.Background()
	got, ok, err := r.FolderBinding(ctx, 91001)
	if err != nil || !ok || got != b {
		t.Fatalf("binding lookup: %v", err)
	}
	pending, ok, err := r.PendingIngestionRun(ctx, b.ID)
	if err != nil || !ok || pending != run.RunID {
		t.Fatalf("unstarted ingestion did not resume completed discovery: %v", err)
	}
	execSQL(t, r.pool, "UPDATE bloem_storage_sources SET enabled=false WHERE key=$1", s.Key)
	got, ok, err = r.FolderBinding(ctx, 91001)
	if err != nil || !ok || got != b {
		t.Fatalf("disabled binding fell back to filesystem: %v", err)
	}
	fixtureFolder(t, r.pool, 91002)
	if _, ok, err = r.FolderBinding(ctx, 91002); err != nil || ok {
		t.Fatalf("local folder falsely bound: %v", err)
	}
	other, _ := fixtureSource(t, r.pool)
	if _, err = r.Bind(ctx, other.Key, 91001); err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.FolderBinding(ctx, 91001); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("mixed source binding silently selected: %v", err)
	}
}
