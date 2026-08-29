package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// musicReconcileQueryTracer pins the missing-file reconciliation to a fixed
// statement budget. Recording SQL rather than wall-clock time makes the test
// deterministic on both local machines and loaded CI workers.
type musicReconcileQueryTracer struct {
	mu      sync.Mutex
	queries []string
}

func (t *musicReconcileQueryTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.queries = append(t.queries, data.SQL)
	return ctx
}

func (*musicReconcileQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
}

func (t *musicReconcileQueryTracer) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.queries = nil
}

func (t *musicReconcileQueryTracer) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.queries)
}

func newMusicScannerWithTracer(
	t *testing.T,
	fixture *musicScanTestFixture,
	tracer *musicReconcileQueryTracer,
) *Scanner {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse traced music database config: %v", err)
	}
	config.ConnConfig.Tracer = tracer
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(fixture.ctx, config)
	if err != nil {
		t.Fatalf("create traced music pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewScanner(NewFileRepository(pool), fixture.scanner.ffprobePath, nil, 1, false, 0)
}

// TestMusicMissingReconcileQueryCountIsBounded pins the 16-statement full
// reconcile transaction for both one and 100 absent files. Any reviewed query
// shape change must update the constant while keeping both sizes identical.
func TestMusicMissingReconcileQueryCountIsBounded(t *testing.T) {
	const wantStatements = 16
	queryCounts := make([]int, 0, 2)
	for _, candidateCount := range []int{1, 100} {
		trackNames := make([]string, candidateCount)
		for i := range trackNames {
			trackNames[i] = fmt.Sprintf("query-count-%03d.flac", i)
		}
		fixture := newMusicScanTestFixture(t, trackNames...)
		if err := fixture.scan(); err != nil {
			t.Fatalf("baseline %d-candidate music scan: %v", candidateCount, err)
		}
		for _, name := range trackNames {
			if err := os.Remove(filepath.Join(fixture.album, name)); err != nil {
				t.Fatalf("remove %d-candidate track %q: %v", candidateCount, name, err)
			}
		}
		candidates, err := fixture.scanner.snapshotMusicReconcileCandidates(fixture.ctx, fixture.folder.ID)
		if err != nil {
			t.Fatalf("snapshot %d music candidates: %v", candidateCount, err)
		}
		if len(candidates) != candidateCount {
			t.Fatalf("snapshot candidates = %d, want %d", len(candidates), candidateCount)
		}

		tracer := &musicReconcileQueryTracer{}
		reconcileScanner := newMusicScannerWithTracer(t, fixture, tracer)
		tracer.reset()
		if err := reconcileScanner.reconcileMissingMusic(
			fixture.ctx,
			fixture.folder.ID,
			candidates,
			map[string]struct{}{},
			nil,
		); err != nil {
			t.Fatalf("reconcile %d missing music candidates: %v", candidateCount, err)
		}
		queryCounts = append(queryCounts, tracer.count())
	}

	if queryCounts[0] != wantStatements || queryCounts[1] != wantStatements {
		t.Fatalf("missing reconcile statements = 1 candidate:%d, 100 candidates:%d; want %d for both",
			queryCounts[0], queryCounts[1], wantStatements)
	}
}

func TestMusicScopedRepairsForUnrelatedAlbumsDoNotSerialize(t *testing.T) {
	fixture := newMusicScanTestFixture(t)
	firstAlbum := filepath.Join(fixture.root, "Artist", "First Album")
	secondAlbum := filepath.Join(fixture.root, "Artist", "Second Album")
	if err := os.MkdirAll(firstAlbum, 0o755); err != nil {
		t.Fatalf("create first music album: %v", err)
	}
	if err := os.MkdirAll(secondAlbum, 0o755); err != nil {
		t.Fatalf("create second music album: %v", err)
	}
	firstPath := filepath.Join(firstAlbum, "first.flac")
	secondPath := filepath.Join(secondAlbum, "second.flac")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("scoped repair audio payload"), 0o644); err != nil {
			t.Fatalf("write scoped music track %q: %v", path, err)
		}
	}

	applicationName := fmt.Sprintf("task5-music-scoped-repair-%d", fixture.folder.ID)
	firstScanner := newMusicScannerWithApplicationName(t, fixture, applicationName)
	barrierKey := int64(570_000_000_000) + int64(fixture.folder.ID)
	barrier := newMusicPostgresBarrier(t, fixture.pool, barrierKey)
	functionName := fmt.Sprintf("task5_music_scoped_repair_barrier_%d", fixture.folder.ID)
	triggerName := fmt.Sprintf("task5_music_scoped_repair_barrier_%d", fixture.folder.ID)
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $fn$
		BEGIN
			IF current_setting('application_name') = %s
			   AND NEW.media_folder_id = %d
			   AND NEW.canonical_root_path = %s THEN
				PERFORM pg_advisory_xact_lock(%d::bigint);
			END IF;
			RETURN NEW;
		END
		$fn$`, functionName, musicPostgresLiteral(applicationName), fixture.folder.ID,
		musicPostgresLiteral(firstAlbum), barrierKey)); err != nil {
		t.Fatalf("create scoped repair barrier function: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON media_item_roots
		FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create scoped repair barrier trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON media_item_roots`, triggerName))
	})

	first := make(chan error, 1)
	go func() {
		first <- firstScanner.ScanMusicFolder(
			fixture.ctx,
			scopedFolderPaths(fixture.folder, []string{firstAlbum}),
			false,
		)
	}()
	barrier.waitForWaiter(t, first)

	second := make(chan error, 1)
	go func() {
		second <- fixture.scanner.ScanMusicFolder(
			fixture.ctx,
			scopedFolderPaths(fixture.folder, []string{secondAlbum}),
			false,
		)
	}()
	secondCompleted, secondErr := barrier.waitForDownstreamCompletionOrLockChain(t, first, second)

	barrier.release(t)
	if err := waitForMusicOperation(t, first); err != nil {
		t.Fatalf("first scoped music repair: %v", err)
	}
	if !secondCompleted {
		secondErr = waitForMusicOperation(t, second)
	}
	if secondErr != nil {
		t.Fatalf("second scoped music repair: %v", secondErr)
	}
	if !secondCompleted {
		t.Fatal("unrelated album scan serialized behind a folder-wide scoped repair")
	}

	var files, tracks, roots int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT
			(SELECT count(*) FROM media_files WHERE media_folder_id = $1 AND missing_since IS NULL),
			(SELECT count(*) FROM music_tracks mt JOIN media_files mf ON mf.id = mt.media_file_id
				WHERE mf.media_folder_id = $1),
			(SELECT count(*) FROM media_item_roots WHERE media_folder_id = $1)
	`, fixture.folder.ID).Scan(&files, &tracks, &roots); err != nil {
		t.Fatalf("read unrelated scoped music state: %v", err)
	}
	if files != 2 || tracks != 2 || roots != 2 {
		t.Fatalf("unrelated scoped music state = files:%d tracks:%d roots:%d, want 2/2/2", files, tracks, roots)
	}
}

func TestMusicVanishedNoRowRechecksPathAfterFolderLock(t *testing.T) {
	fixture := newMusicScanTestFixture(t)
	trackPath := filepath.Join(fixture.album, "reappeared.flac")
	barrier := newMusicPostgresBarrier(t, fixture.pool, musicFolderMutationLockKey(fixture.folder.ID))

	scan := make(chan error, 1)
	go func() { scan <- fixture.scanner.ScanFile(fixture.ctx, trackPath, fixture.folder) }()
	barrier.waitForWaiter(t, scan)
	if err := os.WriteFile(trackPath, []byte("reappeared audio payload"), 0o644); err != nil {
		barrier.release(t)
		_ = waitForMusicOperation(t, scan)
		t.Fatalf("restore music path while vanished scan waits: %v", err)
	}
	barrier.release(t)
	if err := waitForMusicOperation(t, scan); err != nil {
		t.Fatalf("vanished no-row scan after path reappeared: %v", err)
	}

	var activeFile, track bool
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT
			EXISTS (SELECT 1 FROM media_files
				WHERE media_folder_id = $1 AND file_path = $2 AND missing_since IS NULL),
			EXISTS (SELECT 1 FROM music_tracks mt JOIN media_files mf ON mf.id = mt.media_file_id
				WHERE mf.media_folder_id = $1 AND mf.file_path = $2)
	`, fixture.folder.ID, trackPath).Scan(&activeFile, &track); err != nil {
		t.Fatalf("read reappeared no-row music state: %v", err)
	}
	if !activeFile || !track {
		t.Fatalf("reappeared no-row music state = active-file:%t track:%t, want true/true", activeFile, track)
	}
}

func TestMusicCrossFolderRefreshWinsAgainstOrphanDelete(t *testing.T) {
	firstFolder := newMusicScanTestFixture(t, "shared.flac")
	if err := firstFolder.scan(); err != nil {
		t.Fatalf("baseline first-folder music scan: %v", err)
	}
	firstPath := filepath.Join(firstFolder.album, "shared.flac")
	var sharedContentID string
	if err := firstFolder.pool.QueryRow(firstFolder.ctx, `
		SELECT content_id FROM media_files
		WHERE media_folder_id = $1 AND file_path = $2
	`, firstFolder.folder.ID, firstPath).Scan(&sharedContentID); err != nil {
		t.Fatalf("read shared album identity: %v", err)
	}

	secondFolder := newMusicScanTestFixture(t)
	secondPath := filepath.Join(secondFolder.album, "shared.flac")
	if err := os.WriteFile(secondPath, []byte("second-folder shared audio"), 0o644); err != nil {
		t.Fatalf("write second-folder shared track: %v", err)
	}
	if _, err := secondFolder.pool.Exec(secondFolder.ctx, `
		INSERT INTO media_files (
			content_id, media_folder_id, canonical_root_path, observed_root_path,
			content_group_key, group_key_version, base_title, base_type,
			identity_confidence, file_path, file_size, missing_since
		) VALUES ($1, $2, $3, $3, $1, 1, 'shared', 'music', 'high', $4, 1, NOW())
	`, sharedContentID, secondFolder.folder.ID, secondFolder.album, secondPath); err != nil {
		t.Fatalf("seed second-folder missing shared file: %v", err)
	}

	if err := os.Remove(firstPath); err != nil {
		t.Fatalf("remove first-folder shared track: %v", err)
	}
	candidates, err := firstFolder.scanner.snapshotMusicReconcileCandidates(firstFolder.ctx, firstFolder.folder.ID)
	if err != nil {
		t.Fatalf("snapshot first-folder shared candidate: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("first-folder candidates = %d, want 1", len(candidates))
	}

	var oldSearchProvider string
	if err := firstFolder.pool.QueryRow(firstFolder.ctx, `
		SELECT value FROM server_settings WHERE key = 'catalog.search.provider'
	`).Scan(&oldSearchProvider); err != nil {
		t.Fatalf("read catalog search provider: %v", err)
	}
	if _, err := firstFolder.pool.Exec(firstFolder.ctx, `
		UPDATE server_settings SET value = 'meilisearch' WHERE key = 'catalog.search.provider'
	`); err != nil {
		t.Fatalf("enable search delete event capture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = firstFolder.pool.Exec(context.Background(), `
			UPDATE server_settings SET value = $1 WHERE key = 'catalog.search.provider'
		`, oldSearchProvider)
	})
	var baselineDeleteEvents int
	if err := firstFolder.pool.QueryRow(firstFolder.ctx, `
		SELECT count(*) FROM catalog_search_index_events
		WHERE action = 'delete' AND content_id = $1
	`, sharedContentID).Scan(&baselineDeleteEvents); err != nil {
		t.Fatalf("count baseline shared-item delete events: %v", err)
	}

	applicationName := fmt.Sprintf("task5-music-cross-folder-refresh-%d", secondFolder.folder.ID)
	refreshScanner := newMusicScannerWithApplicationName(t, secondFolder, applicationName)
	barrierKey := int64(580_000_000_000) + int64(secondFolder.folder.ID)
	barrier := newMusicPostgresBarrier(t, secondFolder.pool, barrierKey)
	functionName := fmt.Sprintf("task5_music_cross_folder_barrier_%d", secondFolder.folder.ID)
	triggerName := fmt.Sprintf("task5_music_cross_folder_barrier_%d", secondFolder.folder.ID)
	if _, err := secondFolder.pool.Exec(secondFolder.ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $fn$
		BEGIN
			IF current_setting('application_name') = %s
			   AND NEW.album_id = %s THEN
				PERFORM pg_advisory_xact_lock(%d::bigint);
			END IF;
			RETURN NEW;
		END
		$fn$`, functionName, musicPostgresLiteral(applicationName),
		musicPostgresLiteral(sharedContentID), barrierKey)); err != nil {
		t.Fatalf("create cross-folder refresh barrier function: %v", err)
	}
	t.Cleanup(func() {
		_, _ = secondFolder.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})
	if _, err := secondFolder.pool.Exec(secondFolder.ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON music_tracks
		FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create cross-folder refresh barrier trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = secondFolder.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON music_tracks`, triggerName))
	})

	refresh := make(chan error, 1)
	go func() {
		refresh <- refreshScanner.ScanMusicFolder(
			secondFolder.ctx,
			scopedFolderPaths(secondFolder.folder, []string{secondFolder.album}),
			false,
		)
	}()
	barrier.waitForWaiter(t, refresh)

	reconcile := make(chan error, 1)
	go func() {
		reconcile <- firstFolder.scanner.reconcileMissingMusic(
			firstFolder.ctx,
			firstFolder.folder.ID,
			candidates,
			map[string]struct{}{},
			nil,
		)
	}()
	reconcileCompleted, reconcileErr := barrier.waitForDownstreamCompletionOrLockChain(t, refresh, reconcile)

	barrier.release(t)
	if err := waitForMusicOperation(t, refresh); err != nil {
		t.Fatalf("second-folder shared album refresh: %v", err)
	}
	if !reconcileCompleted {
		reconcileErr = waitForMusicOperation(t, reconcile)
	}
	if reconcileErr != nil {
		t.Fatalf("first-folder orphan reconcile: %v", reconcileErr)
	}
	if reconcileCompleted {
		t.Error("first-folder orphan reconcile did not wait for the shared-item refresh")
	}

	var activeFile, item, membership, track bool
	if err := secondFolder.pool.QueryRow(secondFolder.ctx, `
		SELECT
			EXISTS (SELECT 1 FROM media_files
				WHERE media_folder_id = $1 AND file_path = $2 AND missing_since IS NULL),
			EXISTS (SELECT 1 FROM media_items WHERE content_id = $3),
			EXISTS (SELECT 1 FROM media_item_libraries
				WHERE media_folder_id = $1 AND content_id = $3),
			EXISTS (SELECT 1 FROM music_tracks mt JOIN media_files mf ON mf.id = mt.media_file_id
				WHERE mf.media_folder_id = $1 AND mf.file_path = $2 AND mt.album_id = $3)
	`, secondFolder.folder.ID, secondPath, sharedContentID).Scan(&activeFile, &item, &membership, &track); err != nil {
		t.Fatalf("read cross-folder shared album state: %v", err)
	}
	if !activeFile || !item || !membership || !track {
		t.Fatalf("cross-folder shared album state = active-file:%t item:%t membership:%t track:%t, want all true",
			activeFile, item, membership, track)
	}

	var deleteEvents int
	if err := secondFolder.pool.QueryRow(secondFolder.ctx, `
		SELECT count(*) FROM catalog_search_index_events
		WHERE action = 'delete' AND content_id = $1
	`, sharedContentID).Scan(&deleteEvents); err != nil {
		t.Fatalf("count final shared-item delete events: %v", err)
	}
	if deleteEvents != baselineDeleteEvents {
		t.Fatalf("shared surviving item delete events = %d, want unchanged %d", deleteEvents, baselineDeleteEvents)
	}
}

func TestMusicOrphanDeletePreservesGloballyActiveUnlinkedFile(t *testing.T) {
	firstFolder := newMusicScanTestFixture(t, "first.flac")
	if err := firstFolder.scan(); err != nil {
		t.Fatalf("baseline first-folder music scan: %v", err)
	}
	firstPath := filepath.Join(firstFolder.album, "first.flac")
	var sharedContentID string
	if err := firstFolder.pool.QueryRow(firstFolder.ctx, `
		SELECT content_id FROM media_files
		WHERE media_folder_id = $1 AND file_path = $2
	`, firstFolder.folder.ID, firstPath).Scan(&sharedContentID); err != nil {
		t.Fatalf("read globally active guard identity: %v", err)
	}
	t.Cleanup(func() {
		_, _ = firstFolder.pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id = $1`, sharedContentID)
	})

	secondFolder := newMusicScanTestFixture(t)
	secondPath := filepath.Join(secondFolder.album, "active-unlinked.flac")
	if _, err := secondFolder.pool.Exec(secondFolder.ctx, `
		INSERT INTO media_files (
			content_id, media_folder_id, canonical_root_path, observed_root_path,
			content_group_key, group_key_version, base_title, base_type,
			identity_confidence, file_path, file_size
		) VALUES ($1, $2, $3, $3, $1, 1, 'active unlinked', 'music', 'high', $4, 1)
	`, sharedContentID, secondFolder.folder.ID, secondFolder.album, secondPath); err != nil {
		t.Fatalf("seed globally active unlinked file: %v", err)
	}
	if err := os.Remove(firstPath); err != nil {
		t.Fatalf("remove first-folder music path: %v", err)
	}
	candidates, err := firstFolder.scanner.snapshotMusicReconcileCandidates(firstFolder.ctx, firstFolder.folder.ID)
	if err != nil {
		t.Fatalf("snapshot first-folder active-guard candidate: %v", err)
	}
	if err := firstFolder.scanner.reconcileMissingMusic(
		firstFolder.ctx,
		firstFolder.folder.ID,
		candidates,
		map[string]struct{}{},
		nil,
	); err != nil {
		t.Fatalf("reconcile with globally active unlinked file: %v", err)
	}

	var item, activeFile, firstMembership bool
	if err := firstFolder.pool.QueryRow(firstFolder.ctx, `
		SELECT
			EXISTS (SELECT 1 FROM media_items WHERE content_id = $1),
			EXISTS (SELECT 1 FROM media_files
				WHERE content_id = $1 AND media_folder_id = $2 AND missing_since IS NULL),
			EXISTS (SELECT 1 FROM media_item_libraries
				WHERE content_id = $1 AND media_folder_id = $3)
	`, sharedContentID, secondFolder.folder.ID, firstFolder.folder.ID).Scan(&item, &activeFile, &firstMembership); err != nil {
		t.Fatalf("read globally active orphan guard state: %v", err)
	}
	if !item || !activeFile || firstMembership {
		t.Fatalf("globally active orphan guard = item:%t active-file:%t stale-first-membership:%t, want true/true/false",
			item, activeFile, firstMembership)
	}
}
