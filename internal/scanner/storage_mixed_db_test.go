package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Mixed cleanup deliberately retains membership-less items for another lane.
// Remove fixture-owned items while their surviving files still identify them,
// before the base fixture deletes the folder and loses that association.
func mixedStorageFixture(t *testing.T) (*pgxpool.Pool, *Scanner, *models.MediaFolder, storagesource.Location) {
	t.Helper()
	pool, s, folder, location := storageEbookFixture(t)
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id IN (
   SELECT content_id FROM media_files WHERE media_folder_id=$1
   UNION SELECT content_id FROM media_item_libraries WHERE media_folder_id=$1
  )`, folder.ID)
		if err != nil {
			t.Error(err)
		}
	})
	return pool, s, folder, location
}

func TestLocalCleanupPreservesNativeFilesIncludingExpiredMissingDB(t *testing.T) {
	pool, s, folder, location := mixedStorageFixture(t)
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("native", "epub", &storagev1.EbookMetadata{Title: "Native"}, "r1"))
	var id int
	var filePath string
	if err := pool.QueryRow(t.Context(), `SELECT id, file_path FROM media_files WHERE media_folder_id=$1`, folder.ID).Scan(&id, &filePath); err != nil {
		t.Fatal(err)
	}
	if err := s.fileRepo.MarkMissing(t.Context(), id, filePath, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	deleted, err := s.fileRepo.DeleteMissingByFolder(t.Context(), folder.ID, 0, nil)
	if err != nil || deleted != 0 {
		t.Fatalf("native trash deleted=%d err=%v", deleted, err)
	}
	if deleted, err = s.fileRepo.DeleteByIDs(t.Context(), []int{id}); err != nil || deleted != 0 {
		t.Fatalf("native explicit delete=%d err=%v", deleted, err)
	}
}

func TestLocalReconciliationRetainsExpiredNativeVersionDB(t *testing.T) {
	pool, s, folder, location := mixedStorageFixture(t)
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("native", "epub", &storagev1.EbookMetadata{Title: "Native"}, "r1"))
	var contentID string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1`, folder.ID).Scan(&contentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_files(content_id,media_folder_id,file_path,missing_since) VALUES($1,$2,'/gone/book.epub',clock_timestamp()-interval '1 hour')`, contentID, folder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET missing_since=clock_timestamp()-interval '1 hour' WHERE media_folder_id=$1`, folder.ID); err != nil {
		t.Fatal(err)
	}
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	if _, _, _, err := s.reconcileLibraryMemberships(t.Context(), folder.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	var files int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files WHERE media_folder_id=$1 AND file_path LIKE 'bloem-storage:%'`, folder.ID).Scan(&files); err != nil {
		t.Fatal(err)
	}
	// The exact native path scheme may change; the reference is authoritative.
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_file_refs WHERE location_id=$1`, location.ID).Scan(&files); err != nil || files != 1 {
		t.Fatalf("native retained files=%d err=%v", files, err)
	}
	if deleted, err := s.fileRepo.DeleteMissingByFolder(t.Context(), folder.ID, 0, nil); err != nil || deleted != 1 {
		t.Fatalf("local expired version could not leave retained native item: deleted=%d err=%v", deleted, err)
	}
}

func TestStorageEbookJoinsCuratedLocalMetadataDB(t *testing.T) {
	pool, s, folder, location := mixedStorageFixture(t)
	meta := &storagev1.EbookMetadata{Title: "Original", Authors: []string{"Curated Author"}, Isbn: "9780441013593"}
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("local", "epub", meta, "r1"))
	var id string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1`, folder.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM bloem_storage_file_refs WHERE location_id=$1`, location.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET file_path='/books/curated.epub' WHERE media_folder_id=$1`, folder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_items SET title='Curated title',sort_title='Curated sort',poster_path='curated/poster.webp',overview='Curated description' WHERE content_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(p))::text,'') FROM item_people p WHERE content_id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("native", "pdf", &storagev1.EbookMetadata{Title: "Provider replacement", Authors: []string{"Replacement"}, Isbn: meta.Isbn, CoverEntryId: "cover", CoverRevision: "r2"}, "r2"))
	var title, sort, poster, overview, after string
	var files int
	if err := pool.QueryRow(t.Context(), `SELECT title,sort_title,poster_path,overview,(SELECT count(*) FROM media_files WHERE content_id=$1),(SELECT COALESCE(jsonb_agg(to_jsonb(p))::text,'') FROM item_people p WHERE content_id=$1) FROM media_items WHERE content_id=$1`, id).Scan(&title, &sort, &poster, &overview, &files, &after); err != nil {
		t.Fatal(err)
	}
	if title != "Curated title" || sort != "Curated sort" || poster != "curated/poster.webp" || overview != "Curated description" || files != 2 || before != after {
		t.Fatalf("curation lost: %s/%s/%s/%s files=%d credits equal=%v", title, sort, poster, overview, files, before == after)
	}
}

func TestLocalAudiobookScanPreservesNativeAudioInMixedLibraryDB(t *testing.T) {
	pool, s, folder, location := mixedStorageFixture(t)
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("native", "epub", &storagev1.EbookMetadata{Title: "Native"}, "r1"))
	// Seed audio catalog facts without implementing the later audio publisher.
	if _, err := pool.Exec(t.Context(), `UPDATE media_items SET type='audiobook' WHERE content_id IN(SELECT content_id FROM media_files WHERE media_folder_id=$1)`, folder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET container='mp3',codec_audio='mp3',duration=90,presentation_kind='multipart',presentation_group_key='bloem-storage:audiobook:fixture',presentation_part_index=1,presentation_part_total=1 WHERE media_folder_id=$1`, folder.ID); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	folder.Type = "audiobooks"
	folder.Paths = []string{root}
	if _, err := pool.Exec(t.Context(), `UPDATE media_folders SET type='audiobooks' WHERE id=$1`, folder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,$2)`, folder.ID, root); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET missing_since=clock_timestamp()-interval '1 day' WHERE media_folder_id=$1`, folder.ID); err != nil {
		t.Fatal(err)
	}
	s.emptyTrashAfterScan = true
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	if _, err := s.ScanFolder(t.Context(), folder); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_file_refs WHERE location_id=$1`, location.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("native rows=%d err=%v", count, err)
	}
}

func TestStorageCompletionSweepsOnlyCompletingLocationDB(t *testing.T) {
	pool, s, folder, location := mixedStorageFixture(t)
	sourceRepo := storagesource.NewRepository(pool)
	source, err := sourceRepo.CreateSource(t.Context(), storagesource.SourceConfig{PluginID: "fixture", ProviderSourceID: "other-" + fmt.Sprint(time.Now().UnixNano()), RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	other, err := sourceRepo.AddLocationTx(t.Context(), tx, source.Key, folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM bloem_storage_sources WHERE key=$1`, source.Key)
	})
	meta := &storagev1.EbookMetadata{Title: "Shared", Isbn: "9780441013593"}
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("first", "epub", meta, "r1"))
	publishStorageEbooks(t, pool, s, folder, other, storageEbookEntry("other", "pdf", meta, "r1"))
	var id string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1 LIMIT 1`, folder.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_files(content_id,media_folder_id,file_path,missing_since) VALUES($1,$2,'/books/local.epub',clock_timestamp()-interval '1 day')`, id, folder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET missing_since=clock_timestamp()-interval '1 day' WHERE id IN(SELECT media_file_id FROM bloem_storage_file_refs WHERE location_id=ANY($1))`, []uuid.UUID{location.ID, other.ID}); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(r))::text,'') FROM bloem_storage_file_roots r WHERE location_id=$1`, other.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	authorize := func(context.Context, pgx.Tx) error { return nil }
	lease, err := sourceRepo.BeginLocationAuthorized(t.Context(), location, "test", time.Minute, authorize)
	if err != nil {
		t.Fatal(err)
	}
	cp, more, err := sourceRepo.NextDirectory(t.Context(), lease)
	if err != nil || !more {
		t.Fatal(err)
	}
	if err := sourceRepo.ApplyPageAuthorized(t.Context(), lease, cp, &storagev1.ListResponse{Complete: true}, authorize, nil); err != nil {
		t.Fatal(err)
	}
	s.emptyTrashAfterScan = true
	deleted := 0
	if _, err := sourceRepo.CompleteLocationAuthorized(t.Context(), lease, authorize, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		deleted, _, err = s.SweepStorageLocationTx(ctx, tx, folder, location)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var ownFiles, otherFiles, localFiles int
	var after string
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM bloem_storage_file_refs WHERE location_id=$1),(SELECT count(*) FROM bloem_storage_file_refs WHERE location_id=$2),(SELECT count(*) FROM media_files WHERE media_folder_id=$3 AND file_path='/books/local.epub'),(SELECT COALESCE(jsonb_agg(to_jsonb(r))::text,'') FROM bloem_storage_file_roots r WHERE location_id=$2)`, location.ID, other.ID, folder.ID).Scan(&ownFiles, &otherFiles, &localFiles, &after); err != nil {
		t.Fatal(err)
	}
	if deleted != 1 || ownFiles != 0 || otherFiles != 1 || localFiles != 1 || after != before {
		t.Fatalf("deleted=%d own=%d other=%d local=%d roots equal=%v", deleted, ownFiles, otherFiles, localFiles, after == before)
	}
}

func TestNilMaintenanceScopeRetainsOriginalNativeTrashBehaviorDB(t *testing.T) {
	pool, s, folder, location := mixedStorageFixture(t)
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("native", "epub", &storagev1.EbookMetadata{Title: "Native"}, "r1"))
	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET missing_since=clock_timestamp()-interval '1 day' WHERE media_folder_id=$1`, folder.ID); err != nil {
		t.Fatal(err)
	}
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	s.SetMaintenanceFileScope(nil)
	if deleted, err := s.fileRepo.DeleteMissingByFolder(t.Context(), folder.ID, 0, nil); err != nil || deleted != 1 {
		t.Fatalf("nil scope changed upstream trash: deleted=%d err=%v", deleted, err)
	}
}

func TestMaintenanceCandidatesExcludeNativeMissingUnionDB(t *testing.T) {
	pool, s, folder, location := mixedStorageFixture(t)
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("native", "epub", &storagev1.EbookMetadata{Title: "Native"}, "r1"), storageEbookEntry("local", "pdf", &storagev1.EbookMetadata{Title: "Local"}, "r1"))
	var nativeID, localID int
	var nativePath, localContent string
	if err := pool.QueryRow(t.Context(), `SELECT f.id,f.file_path FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.location_id=$1 AND r.entry_id='epub/native'`, location.ID).Scan(&nativeID, &nativePath); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT f.id,f.content_id FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.location_id=$1 AND r.entry_id='pdf/local'`, location.ID).Scan(&localID, &localContent); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM bloem_storage_file_refs WHERE media_file_id=$1`, localID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET file_path='/old/local.pdf' WHERE id=$1`, localID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET missing_since=clock_timestamp()-interval '1 day' WHERE media_folder_id=$1`, folder.ID); err != nil {
		t.Fatal(err)
	}
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	states, err := s.fileRepo.maintenanceScanStateByPrefix(t.Context(), folder.ID, nativePath)
	if err != nil || len(states) != 0 {
		t.Fatalf("native states=%d err=%v", len(states), err)
	}
	items, _, err := s.fileRepo.maintenanceMembershipTargets(t.Context(), folder.ID, "/new", true)
	if err != nil || len(items) != 1 || items[0] != localContent {
		t.Fatalf("missing union=%v err=%v", items, err)
	}
	ids, err := s.fileRepo.ListIDsOutsideRoots(t.Context(), folder.ID, nil)
	if err != nil || len(ids) != 1 || ids[0] != localID {
		t.Fatalf("outside candidates=%v err=%v", ids, err)
	}
	if deleted, err := s.fileRepo.DeleteByIDs(t.Context(), []int{localID, nativeID}); err != nil || deleted != 1 {
		t.Fatalf("explicit cleanup=%d err=%v", deleted, err)
	}
}

func TestLocalMaintenanceStillReconcilesFilelessItemsDB(t *testing.T) {
	pool, s, folder, location := mixedStorageFixture(t)
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("local", "epub", &storagev1.EbookMetadata{Title: "Local"}, "r1"))
	var id string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1`, folder.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM media_files WHERE media_folder_id=$1`, folder.ID); err != nil {
		t.Fatal(err)
	}
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	if _, _, _, err := s.reconcileLibraryMemberships(t.Context(), folder.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_items WHERE content_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("fileless item retained count=%d err=%v", count, err)
	}
}

func TestLocalVideoScansPreserveNativePresentAndExpiredFilesDB(t *testing.T) {
	pool, s, folder, location := mixedStorageFixture(t)
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("present", "epub", &storagev1.EbookMetadata{Title: "Present"}, "r1"), storageEbookEntry("missing", "pdf", &storagev1.EbookMetadata{Title: "Missing"}, "r1"))
	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET missing_since=clock_timestamp()-interval '1 day' WHERE id IN(SELECT media_file_id FROM bloem_storage_file_refs WHERE location_id=$1 AND entry_id='pdf/missing')`, location.ID); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	folder.Type = "movies"
	folder.Paths = []string{root}
	if err := os.WriteFile(filepath.Join(root, "Local (2026).mp4"), []byte("scanner fixture without probing"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_folders SET type='movies' WHERE id=$1`, folder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,$2)`, folder.ID, root); err != nil {
		t.Fatal(err)
	}
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	s.emptyTrashAfterScan = true
	if result, err := s.ScanFolder(t.Context(), folder); err != nil || result.New != 1 {
		t.Fatalf("local full scan result=%+v err=%v", result, err)
	}
	if _, err := s.ScanSubtree(t.Context(), folder, root); err != nil {
		t.Fatal(err)
	}
	var present, missing int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE f.missing_since IS NULL),count(*) FILTER(WHERE f.missing_since IS NOT NULL) FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.location_id=$1`, location.ID).Scan(&present, &missing); err != nil || present != 1 || missing != 1 {
		t.Fatalf("native present=%d missing=%d err=%v", present, missing, err)
	}
}

func TestStorageEbookLockedNativeMetadataAndFileIdentityDB(t *testing.T) {
	pool, s, folder, location := mixedStorageFixture(t)
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("native", "epub", &storagev1.EbookMetadata{Title: "Original", Authors: []string{"Original Author"}}, "r1"))
	var contentID string
	var fileID int
	if err := pool.QueryRow(t.Context(), `SELECT content_id,id FROM media_files WHERE media_folder_id=$1`, folder.ID).Scan(&contentID, &fileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_items SET title='Locked title',locked_fields=ARRAY[1] WHERE content_id=$1`, contentID); err != nil {
		t.Fatal(err)
	}
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("native", "epub", &storagev1.EbookMetadata{Title: "Replacement", Authors: []string{"Replacement Author"}}, "r2"))
	var title string
	var retainedFileID int
	if err := pool.QueryRow(t.Context(), `SELECT mi.title,f.id FROM media_items mi JOIN media_files f ON f.content_id=mi.content_id WHERE mi.content_id=$1`, contentID).Scan(&title, &retainedFileID); err != nil {
		t.Fatal(err)
	}
	if title != "Locked title" || retainedFileID != fileID {
		t.Fatalf("locked title=%s file=%d want=%d", title, retainedFileID, fileID)
	}
}

func TestStorageMetadataOwnershipRechecksLocalVersionAfterItemWaitDB(t *testing.T) {
	pool, s, folder, location := mixedStorageFixture(t)
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("native", "epub", &storagev1.EbookMetadata{Title: "Native"}, "r1"))
	var id string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1`, folder.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	curator, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = curator.Rollback(context.Background()) }()
	if _, err := curator.Exec(ctx, `UPDATE media_items SET title='Curated after wait' WHERE content_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	publisher, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = publisher.Rollback(context.Background()) }()
	var pid int
	if err := publisher.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		owned map[string]bool
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		owned, err := storageMetadataOwnedTx(ctx, publisher, []string{id})
		done <- outcome{owned, err}
	}()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock')`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("publisher did not wait for item lock")
		case <-ticker.C:
		}
	}
	if _, err := curator.Exec(ctx, `INSERT INTO media_files(content_id,media_folder_id,file_path) VALUES($1,$2,'/books/concurrent-local.epub')`, id, folder.ID); err != nil {
		t.Fatal(err)
	}
	if err := curator.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || result.owned[id] {
			t.Fatalf("ownership used pre-wait presence: owned=%v err=%v", result.owned, result.err)
		}
	case <-ctx.Done():
		t.Fatal("publisher did not resume after curator commit")
	}
}

func TestMaintenanceScopeFilelessCandidatesStayFolderWideDB(t *testing.T) {
	pool, s, folder, location := storageEbookFixture(t)
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("orphan", "epub", &storagev1.EbookMetadata{Title: uuid.NewString()}, "r1"))
	var contentID string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1`, folder.ID).Scan(&contentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM media_files WHERE content_id=$1`, contentID); err != nil {
		t.Fatal(err)
	}
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	items, episodes, err := s.fileRepo.maintenanceMembershipTargets(t.Context(), folder.ID, "/unrelated", true)
	if err != nil || len(items) != 0 || len(episodes) != 0 {
		t.Fatalf("scoped candidates=%v/%v err=%v", items, episodes, err)
	}
	items, _, err = s.fileRepo.maintenanceMembershipTargets(t.Context(), folder.ID, "", true)
	if err != nil || len(items) != 1 || items[0] != contentID {
		t.Fatalf("whole-folder candidates=%v err=%v", items, err)
	}
}

func TestMaintenanceScopePreservesCrossFolderMissingNativeReferenceDB(t *testing.T) {
	pool, s, folder, location := storageEbookFixture(t)
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("native", "epub", &storagev1.EbookMetadata{Title: uuid.NewString()}, "r1"))
	var contentID string
	var otherFolder int
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1`, folder.ID).Scan(&contentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name) VALUES('ebooks','Other maintenance folder') RETURNING id`).Scan(&otherFolder); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id=$1`, contentID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_folders WHERE id=$1`, otherFolder)
	})
	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET media_folder_id=$1,missing_since=clock_timestamp()-interval '1 day' WHERE content_id=$2`, otherFolder, contentID); err != nil {
		t.Fatal(err)
	}
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	removed, deleted, _, err := s.reconcileLibraryMemberships(t.Context(), folder.ID, true, nil)
	if err != nil || removed != 1 || deleted != 0 {
		t.Fatalf("removed=%d deleted=%d err=%v", removed, deleted, err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_file_refs WHERE location_id=$1`, location.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("native references=%d err=%v", count, err)
	}
}

func TestMaintenanceScopeRestoresOnlyLocalEpisodeMembershipsDB(t *testing.T) {
	pool, s, folder, location := storageEbookFixture(t)
	publishStorageEbooks(t, pool, s, folder, location, storageEbookEntry("native", "epub", &storagev1.EbookMetadata{Title: uuid.NewString()}, "r1"))
	var seriesID string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE media_folder_id=$1`, folder.ID).Scan(&seriesID); err != nil {
		t.Fatal(err)
	}
	localEpisode, nativeEpisode, orphanEpisode := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(t.Context(), `UPDATE media_items SET type='series' WHERE content_id=$1`, seriesID); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{localEpisode, nativeEpisode, orphanEpisode} {
		if _, err := pool.Exec(t.Context(), `INSERT INTO episodes(content_id,series_id,season_number,episode_number,title) VALUES($1,$2,1,$3,'Maintenance episode')`, id, seriesID, i+1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_files SET episode_id=$1 WHERE media_folder_id=$2`, nativeEpisode, folder.ID); err != nil {
		t.Fatal(err)
	}
	firstSeen := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_files(content_id,episode_id,media_folder_id,file_path,created_at) VALUES($1,$2,$3,'/local/episode.mkv',$4)`, seriesID, localEpisode, folder.ID, firstSeen); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO episode_libraries(episode_id,media_folder_id) VALUES($1,$2)`, orphanEpisode, folder.ID); err != nil {
		t.Fatal(err)
	}
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	if _, _, _, err := s.reconcileLibraryMemberships(t.Context(), folder.ID, false, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM episode_libraries WHERE episode_id=$1`, localEpisode).Scan(&count); err != nil || count != 0 {
		t.Fatalf("restore=false local count=%d err=%v", count, err)
	}
	if _, _, _, err := s.reconcileLibraryMemberships(t.Context(), folder.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(array_agg(episode_id),ARRAY[]::text[]) FROM episode_libraries WHERE media_folder_id=$1`, folder.ID).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != localEpisode {
		t.Fatalf("restored episodes=%v want only %s", ids, localEpisode)
	}
	var episodeFirstSeen, latestAdded time.Time
	if err := pool.QueryRow(t.Context(), `SELECT el.first_seen_at,mi.latest_episode_added_at FROM episode_libraries el JOIN episodes e ON e.content_id=el.episode_id JOIN media_items mi ON mi.content_id=e.series_id WHERE el.episode_id=$1 AND el.media_folder_id=$2`, localEpisode, folder.ID).Scan(&episodeFirstSeen, &latestAdded); err != nil {
		t.Fatal(err)
	}
	if !episodeFirstSeen.Equal(firstSeen) || !latestAdded.Equal(firstSeen) {
		t.Fatalf("first seen=%s latest added=%s want %s", episodeFirstSeen, latestAdded, firstSeen)
	}
	// Removing the extension retains upstream's folder-wide restoration.
	s.SetMaintenanceFileScope(nil)
	if _, _, _, err := s.reconcileLibraryMemberships(t.Context(), folder.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM episode_libraries WHERE episode_id=$1 AND media_folder_id=$2`, nativeEpisode, folder.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("nil scope native count=%d err=%v", count, err)
	}
}
