package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type musicPostgresBarrier struct {
	ctx       context.Context
	conn      *pgxpool.Conn
	holderPID int
	released  bool
}

func newMusicPostgresBarrier(t *testing.T, pool *pgxpool.Pool, key int64) *musicPostgresBarrier {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire music barrier connection: %v", err)
	}
	barrier := &musicPostgresBarrier{ctx: ctx, conn: conn}
	if err := conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&barrier.holderPID); err != nil {
		conn.Release()
		t.Fatalf("read music barrier backend: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		conn.Release()
		t.Fatalf("acquire music advisory barrier: %v", err)
	}
	t.Cleanup(func() {
		if barrier.released {
			return
		}
		_, _ = barrier.conn.Exec(context.Background(), `SELECT pg_advisory_unlock_all()`)
		barrier.conn.Release()
		barrier.released = true
	})
	return barrier
}

func (b *musicPostgresBarrier) waitForWaiter(t *testing.T, operation <-chan error) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		select {
		case err := <-operation:
			t.Fatalf("music operation finished before reaching database barrier: %v", err)
		default:
		}
		var waiting bool
		err := b.conn.QueryRow(waitCtx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_locks waiting
				JOIN pg_locks held
				  ON held.locktype = waiting.locktype
				 AND held.database IS NOT DISTINCT FROM waiting.database
				 AND held.classid IS NOT DISTINCT FROM waiting.classid
				 AND held.objid IS NOT DISTINCT FROM waiting.objid
				 AND held.objsubid IS NOT DISTINCT FROM waiting.objsubid
				WHERE held.pid = $1
				  AND held.granted
				  AND NOT waiting.granted
				  AND waiting.pid <> held.pid
			)
		`, b.holderPID).Scan(&waiting)
		if err != nil {
			t.Fatalf("observe music database barrier: %v", err)
		}
		if waiting {
			return
		}
		if err := waitCtx.Err(); err != nil {
			t.Fatalf("music operation did not reach database barrier: %v", err)
		}
		runtime.Gosched()
	}
}

// waitForDownstreamCompletionOrLockChain waits until downstream either
// finishes or is observably blocked by the operation already waiting on this
// barrier. The lock-chain branch proves database-backed serialization without
// timing guesses: holder <- upstream <- downstream.
func (b *musicPostgresBarrier) waitForDownstreamCompletionOrLockChain(
	t *testing.T,
	upstream <-chan error,
	downstream <-chan error,
) (bool, error) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		select {
		case err := <-upstream:
			t.Fatalf("upstream music operation finished before barrier release: %v", err)
		case err := <-downstream:
			return true, err
		default:
		}
		var chained bool
		err := b.conn.QueryRow(waitCtx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity blocked_upstream
				JOIN pg_stat_activity blocked_downstream
				  ON blocked_upstream.pid = ANY(pg_blocking_pids(blocked_downstream.pid))
				WHERE $1 = ANY(pg_blocking_pids(blocked_upstream.pid))
			)
		`, b.holderPID).Scan(&chained)
		if err != nil {
			t.Fatalf("observe music database lock chain: %v", err)
		}
		if chained {
			return false, nil
		}
		if err := waitCtx.Err(); err != nil {
			t.Fatalf("downstream music operation neither finished nor reached lock chain: %v", err)
		}
		runtime.Gosched()
	}
}

func (b *musicPostgresBarrier) release(t *testing.T) {
	t.Helper()
	if b.released {
		return
	}
	if _, err := b.conn.Exec(b.ctx, `SELECT pg_advisory_unlock_all()`); err != nil {
		t.Fatalf("release music database barrier: %v", err)
	}
	b.conn.Release()
	b.released = true
}

func waitForMusicOperation(t *testing.T, operation <-chan error) error {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case err := <-operation:
		return err
	case <-waitCtx.Done():
		t.Fatalf("music operation did not finish after barrier release: %v", waitCtx.Err())
		return nil
	}
}

func musicPostgresLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func newMusicScannerWithApplicationName(t *testing.T, fixture *musicScanTestFixture, applicationName string) *Scanner {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse music operation database config: %v", err)
	}
	config.ConnConfig.RuntimeParams["application_name"] = applicationName
	pool, err := pgxpool.NewWithConfig(fixture.ctx, config)
	if err != nil {
		t.Fatalf("create music operation pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewScanner(NewFileRepository(pool), fixture.scanner.ffprobePath, nil, 1, false, 0)
}

type musicScanTestFixture struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	scanner *Scanner
	folder  *models.MediaFolder
	root    string
	album   string
	artists []string
}

type musicCatalogCounts struct {
	Files       int
	ActiveFiles int
	Tracks      int
	Albums      int
	Artists     int
	Memberships int
}

func newMusicScanTestFixture(t *testing.T, trackNames ...string) *musicScanTestFixture {
	t.Helper()
	pool := newDeadRootTestPool(t)
	ctx := context.Background()
	folderID := seedDeadRootTestFolder(t, pool, "music", fmt.Sprintf("Music scan %d", time.Now().UnixNano()))
	root := t.TempDir()
	artist := filepath.Join(root, "Artist")
	album := filepath.Join(artist, "Album")
	if err := os.MkdirAll(album, 0o755); err != nil {
		t.Fatalf("create music album: %v", err)
	}
	for _, name := range trackNames {
		if err := os.WriteFile(filepath.Join(album, name), []byte("fake audio payload"), 0o644); err != nil {
			t.Fatalf("write music track %q: %v", name, err)
		}
	}

	toolDir := t.TempDir()
	ffprobe := filepath.Join(toolDir, "ffprobe")
	writeFakeTool(t, ffprobe, `#!/bin/sh
printf '%s\n' '{"format":{"format_name":"flac","duration":"60","bit_rate":"1000000","tags":{}},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"sample_rate":"48000"}]}'
`)
	folder := &models.MediaFolder{
		ID:      folderID,
		Paths:   []string{root},
		Type:    "music",
		Name:    "Music scan test",
		Enabled: true,
	}
	fixture := &musicScanTestFixture{
		t:       t,
		ctx:     ctx,
		pool:    pool,
		scanner: NewScanner(NewFileRepository(pool), ffprobe, nil, 1, false, 0),
		folder:  folder,
		root:    root,
		album:   album,
		artists: []string{stableMusicSemanticID("artist", "Artist")},
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM media_files WHERE media_folder_id = $1`, folderID)
		_, _ = pool.Exec(ctx, `
			DELETE FROM media_items mi
			USING media_item_libraries mil
			WHERE mil.content_id = mi.content_id AND mil.media_folder_id = $1`, folderID)
		_, _ = pool.Exec(ctx, `
			DELETE FROM music_artists ar
			WHERE ar.id = ANY($1) AND NOT EXISTS (
				SELECT 1 FROM music_albums ma WHERE ma.artist_id = ar.id
			)`, fixture.artists)
	})
	return fixture
}

func (f *musicScanTestFixture) scan() error {
	f.t.Helper()
	return f.scanner.ScanMusicFolder(f.ctx, f.folder, true)
}

func (f *musicScanTestFixture) counts() musicCatalogCounts {
	f.t.Helper()
	var got musicCatalogCounts
	if err := f.pool.QueryRow(f.ctx, `
		SELECT
			(SELECT count(*) FROM media_files WHERE media_folder_id = $1 AND base_type = 'music'),
			(SELECT count(*) FROM media_files WHERE media_folder_id = $1 AND base_type = 'music' AND missing_since IS NULL),
			(SELECT count(*) FROM music_tracks mt JOIN media_files mf ON mf.id = mt.media_file_id WHERE mf.media_folder_id = $1),
			(SELECT count(DISTINCT ma.content_id) FROM music_albums ma JOIN media_item_libraries mil ON mil.content_id = ma.content_id WHERE mil.media_folder_id = $1),
			(SELECT count(*) FROM music_artists WHERE id = $2),
			(SELECT count(*) FROM media_item_libraries WHERE media_folder_id = $1)
	`, f.folder.ID, stableMusicSemanticID("artist", "Artist")).Scan(
		&got.Files,
		&got.ActiveFiles,
		&got.Tracks,
		&got.Albums,
		&got.Artists,
		&got.Memberships,
	); err != nil {
		f.t.Fatalf("read music catalog counts: %v", err)
	}
	return got
}

func (f *musicScanTestFixture) folderState() (warning *string, allowance bool) {
	f.t.Helper()
	if err := f.pool.QueryRow(f.ctx, `
		SELECT scan_warning_code, allow_empty_cleanup_once
		FROM media_folders WHERE id = $1
	`, f.folder.ID).Scan(&warning, &allowance); err != nil {
		f.t.Fatalf("read music folder state: %v", err)
	}
	return warning, allowance
}

func (f *musicScanTestFixture) armEmptyCleanup() {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE media_folders SET allow_empty_cleanup_once = true WHERE id = $1
	`, f.folder.ID); err != nil {
		f.t.Fatalf("arm empty cleanup: %v", err)
	}
}

func TestMusicTrackFromProbeUsesTagsAndPreservesUnknownOrder(t *testing.T) {
	got := musicTrackFromProbe("/Music/Artist/Album/07 - Song.flac", ProbeData{
		Duration: 241,
		FormatTags: map[string]string{
			"title":        "Song",
			"album":        "Album",
			"album_artist": "Artist",
			"artist":       "Guest",
			"date":         "2024-04-01",
			"disc":         "2/3",
			"track":        "7/12",
		},
	})
	if got.Title != "Song" || got.Album != "Album" || got.AlbumArtist != "Artist" || got.Artist != "Guest" {
		t.Fatalf("music tags = %+v", got)
	}
	if got.Year != 2024 || got.DiscNumber != 2 || got.TrackNumber != 7 || got.DurationMS != 241000 {
		t.Fatalf("music numeric tags = %+v", got)
	}

	fallback := musicTrackFromProbe("/Music/Artist/Album/03 - Untagged.mp3", ProbeData{})
	if fallback.Title != "03 - Untagged" || fallback.Album != "Album" || fallback.AlbumArtist != "Artist" {
		t.Fatalf("filesystem fallback = %+v", fallback)
	}
	if fallback.DiscNumber != 0 || fallback.TrackNumber != 0 {
		t.Fatalf("unknown ordering must stay unknown: %+v", fallback)
	}
}

func TestStableMusicArtistIDRemainsCaseInsensitive(t *testing.T) {
	if upper, lower := stableMusicSemanticID("artist", "Bloem Artist"), stableMusicSemanticID("artist", "bloem artist"); upper != lower {
		t.Fatalf("case-equivalent artist IDs differ: %q != %q", upper, lower)
	}
}

func TestStableMusicTrackIDPreservesCleanRelativePathCase(t *testing.T) {
	const (
		folderID = 7
		albumID  = "album-identity"
		root     = "/Music/Artist/Album"
	)
	upper := stableMusicTrackID(folderID, albumID, root, root+"/Track.flac")
	lower := stableMusicTrackID(folderID, albumID, root, root+"/track.flac")
	if upper == lower {
		t.Fatalf("case-distinct track paths collided at %q", upper)
	}
	cleaned := stableMusicTrackID(folderID, albumID, root, root+"/Disc/../Track.flac")
	if cleaned != upper {
		t.Fatalf("equivalent cleaned track paths differ: %q != %q", cleaned, upper)
	}
	if repeated := stableMusicTrackID(folderID, albumID, root, root+"/Track.flac"); repeated != upper {
		t.Fatalf("track identity is unstable: %q != %q", repeated, upper)
	}
	if otherLibrary := stableMusicTrackID(folderID+1, albumID, root, root+"/Track.flac"); otherLibrary == upper {
		t.Fatalf("same relative path in distinct libraries collided at %q", upper)
	}
}

func TestMusicMediaFilePreservesCompletePlaybackProbe(t *testing.T) {
	modified := time.Date(2026, 8, 26, 7, 0, 0, 0, time.UTC)
	track := parsedMusicTrack{
		Path:  "/Music/Bloem Artist/Bloem Album/01 - First Bloom.m4a",
		Title: "First Bloom",
		Probe: ProbeData{
			CodecAudio:    "aac",
			AudioChannels: 2,
			Container:     "mp4",
			Duration:      12,
			Bitrate:       130,
			AudioTracks:   []AudioTrackInfo{{Codec: "aac", Channels: 2, Default: true}},
			Chapters:      []ChapterInfo{},
		},
	}
	got := musicMediaFile(
		&models.MediaFolder{ID: 3}, "album-1", "/Music/Bloem Artist/Bloem Album",
		track, 195735, modified,
	)
	if got.ProbeUpdatedAt == nil || got.ProbeSource != "local" {
		t.Fatalf("probe provenance = source %q updated %v", got.ProbeSource, got.ProbeUpdatedAt)
	}
	if got.CodecAudio != "aac" || got.Duration != 12 || len(got.AudioTracks) != 1 {
		t.Fatalf("playback probe = codec %q duration %d tracks %+v", got.CodecAudio, got.Duration, got.AudioTracks)
	}
	if got.Chapters == nil {
		t.Fatal("chapters must preserve a known-empty probe inventory")
	}
	if NeedsCriticalProbeRepair(&got) {
		t.Fatalf("fresh music scan produced playback-incomplete file: %+v", got)
	}
}

func TestMusicScanMissingRootRetainsCatalog(t *testing.T) {
	fixture := newMusicScanTestFixture(t, "one.flac", "two.flac")
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline music scan: %v", err)
	}
	want := fixture.counts()
	fixture.armEmptyCleanup()
	if err := os.RemoveAll(fixture.root); err != nil {
		t.Fatalf("remove music root: %v", err)
	}

	if err := fixture.scan(); err != nil {
		t.Fatalf("scan missing music root: %v", err)
	}
	if got := fixture.counts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog after missing root = %+v, want retained %+v", got, want)
	}
	warning, allowance := fixture.folderState()
	if warning == nil || *warning != "dead_root" {
		t.Fatalf("scan warning = %v, want dead_root", warning)
	}
	if !allowance {
		t.Fatal("unreachable root consumed the one-time empty-cleanup allowance")
	}
}

func TestMusicScanUnreadableRootRetainsCatalog(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not deny access")
	}
	fixture := newMusicScanTestFixture(t, "one.flac", "two.flac")
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline music scan: %v", err)
	}
	want := fixture.counts()
	if err := os.Chmod(fixture.root, 0); err != nil {
		t.Fatalf("make music root unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(fixture.root, 0o755) })
	if _, err := os.ReadDir(fixture.root); err == nil {
		t.Skip("filesystem still permits reads after chmod 000")
	}

	if err := fixture.scan(); err != nil {
		t.Fatalf("scan unreadable music root: %v", err)
	}
	if got := fixture.counts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog after unreadable root = %+v, want retained %+v", got, want)
	}
}

func TestMusicScanHealthyRootReconcilesWithoutTouchingUnavailableSibling(t *testing.T) {
	fixture := newMusicScanTestFixture(t, "available.flac")
	offlineRoot := t.TempDir()
	offlineAlbum := filepath.Join(offlineRoot, "Artist", "Offline Album")
	if err := os.MkdirAll(offlineAlbum, 0o755); err != nil {
		t.Fatalf("create sibling music album: %v", err)
	}
	if err := os.WriteFile(filepath.Join(offlineAlbum, "offline.flac"), []byte("fake audio payload"), 0o644); err != nil {
		t.Fatalf("write sibling music track: %v", err)
	}
	fixture.folder.Paths = append(fixture.folder.Paths, offlineRoot)
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline multi-root music scan: %v", err)
	}
	want := fixture.counts()
	if err := os.RemoveAll(offlineRoot); err != nil {
		t.Fatalf("remove sibling music root: %v", err)
	}

	if err := fixture.scan(); err != nil {
		t.Fatalf("scan with unavailable sibling music root: %v", err)
	}
	if got := fixture.counts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog with unavailable sibling = %+v, want retained %+v", got, want)
	}
	warning, _ := fixture.folderState()
	if warning == nil || *warning != "dead_root" {
		t.Fatalf("scan warning = %v, want dead_root", warning)
	}
}

func TestMusicScanTransientlyEmptyRootRequiresConfirmation(t *testing.T) {
	fixture := newMusicScanTestFixture(t, "one.flac", "two.flac")
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline music scan: %v", err)
	}
	want := fixture.counts()
	if err := os.RemoveAll(fixture.album); err != nil {
		t.Fatalf("empty music root: %v", err)
	}
	if err := os.Remove(filepath.Dir(fixture.album)); err != nil {
		t.Fatalf("remove empty artist directory: %v", err)
	}

	if err := fixture.scan(); err != nil {
		t.Fatalf("scan transiently empty music root: %v", err)
	}
	if got := fixture.counts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog after transient empty root = %+v, want retained %+v", got, want)
	}
	warning, allowance := fixture.folderState()
	if warning == nil || *warning != "empty_root" {
		t.Fatalf("scan warning = %v, want empty_root", warning)
	}
	if allowance {
		t.Fatal("unconfirmed empty scan armed cleanup")
	}
}

func TestMusicScanConfirmedEmptyRootConsumesAllowance(t *testing.T) {
	fixture := newMusicScanTestFixture(t, "one.flac", "two.flac")
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline music scan: %v", err)
	}
	if err := os.RemoveAll(fixture.album); err != nil {
		t.Fatalf("empty music root: %v", err)
	}
	if err := os.Remove(filepath.Dir(fixture.album)); err != nil {
		t.Fatalf("remove empty artist directory: %v", err)
	}
	fixture.armEmptyCleanup()

	if err := fixture.scan(); err != nil {
		t.Fatalf("scan confirmed-empty music root: %v", err)
	}
	got := fixture.counts()
	if got.ActiveFiles != 0 || got.Tracks != 0 || got.Albums != 0 || got.Memberships != 0 {
		t.Fatalf("catalog after confirmed empty root = %+v, want no active music catalog rows", got)
	}
	warning, allowance := fixture.folderState()
	if allowance {
		t.Fatal("confirmed empty scan did not consume one-time allowance")
	}
	if warning != nil {
		t.Fatalf("scan warning after confirmed cleanup = %q, want cleared", *warning)
	}
}

func TestMusicScanHealthyRootReconcilesOnlyMissingTrack(t *testing.T) {
	fixture := newMusicScanTestFixture(t, "one.flac", "two.flac")
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline music scan: %v", err)
	}
	if err := os.Remove(filepath.Join(fixture.album, "one.flac")); err != nil {
		t.Fatalf("remove one music track: %v", err)
	}

	if err := fixture.scan(); err != nil {
		t.Fatalf("scan healthy music root: %v", err)
	}
	got := fixture.counts()
	if got.Files != 2 || got.ActiveFiles != 1 || got.Tracks != 1 || got.Albums != 1 || got.Memberships != 1 {
		t.Fatalf("catalog after one deleted track = %+v", got)
	}
}

func TestMusicFullScanDoesNotReconcileFilesCreatedOrRefreshedAfterItsSnapshot(t *testing.T) {
	// This catches a full scan consulting the live media_files table after its
	// filesystem walk. A row inserted after that walk was never evidence of
	// absence, and a pre-existing row refreshed by the scoped scan no longer
	// represents the generation the full scan observed.
	fixture := newMusicScanTestFixture(t, "keep.flac")
	// A distinct artist avoids an unrelated row-lock dependency: the full
	// ingestion transaction intentionally holds its own artist row while the
	// media-file barrier is active.
	secondAlbum := filepath.Join(fixture.root, "Concurrent Artist", "Second Album")
	fixture.artists = append(fixture.artists, stableMusicSemanticID("artist", "Concurrent Artist"))
	if err := os.MkdirAll(secondAlbum, 0o755); err != nil {
		t.Fatalf("create second music album: %v", err)
	}
	returningPath := filepath.Join(secondAlbum, "returning.flac")
	gonePath := filepath.Join(secondAlbum, "gone.flac")
	for _, path := range []string{returningPath, gonePath} {
		if err := os.WriteFile(path, []byte("baseline audio payload"), 0o644); err != nil {
			t.Fatalf("write baseline music track %q: %v", path, err)
		}
	}
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline music scan: %v", err)
	}
	for _, path := range []string{returningPath, gonePath} {
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove music track %q before full scan: %v", path, err)
		}
	}

	barrierKey := int64(510_000_000_000) + int64(fixture.folder.ID)
	barrier := newMusicPostgresBarrier(t, fixture.pool, barrierKey)
	functionName := fmt.Sprintf("task5_music_file_barrier_%d", fixture.folder.ID)
	triggerName := fmt.Sprintf("task5_music_file_barrier_%d", fixture.folder.ID)
	keepPath := filepath.Join(fixture.album, "keep.flac")
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $fn$
		BEGIN
			IF NEW.media_folder_id = %d AND NEW.file_path = %s THEN
				PERFORM pg_advisory_xact_lock(%d::bigint);
			END IF;
			RETURN NEW;
		END
		$fn$`, functionName, fixture.folder.ID, musicPostgresLiteral(keepPath), barrierKey)); err != nil {
		t.Fatalf("create music file barrier function: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON media_files
		FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create music file barrier trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON media_files`, triggerName))
	})

	fullScan := make(chan error, 1)
	go func() { fullScan <- fixture.scan() }()
	barrier.waitForWaiter(t, fullScan)

	if err := os.WriteFile(returningPath, []byte("returned audio payload"), 0o644); err != nil {
		t.Fatalf("restore music track during full scan: %v", err)
	}
	newPath := filepath.Join(secondAlbum, "new.flac")
	if err := os.WriteFile(newPath, []byte("new audio payload"), 0o644); err != nil {
		t.Fatalf("create music track during full scan: %v", err)
	}
	scopedScan := make(chan error, 1)
	go func() { scopedScan <- fixture.scanner.ScanFile(fixture.ctx, newPath, fixture.folder) }()
	scopedCompleted, scopedErr := barrier.waitForDownstreamCompletionOrLockChain(t, fullScan, scopedScan)

	barrier.release(t)
	if err := waitForMusicOperation(t, fullScan); err != nil {
		t.Fatalf("concurrent full music scan: %v", err)
	}
	if !scopedCompleted {
		scopedErr = waitForMusicOperation(t, scopedScan)
	}
	if scopedErr != nil {
		t.Fatalf("scoped music scan during full scan: %v", scopedErr)
	}
	if scopedCompleted {
		t.Error("scoped music state restoration bypassed the in-flight full scan transaction")
	}

	want := map[string]struct {
		active   bool
		hasTrack bool
	}{
		keepPath:      {active: true, hasTrack: true},
		returningPath: {active: true, hasTrack: true},
		newPath:       {active: true, hasTrack: true},
		gonePath:      {active: false, hasTrack: false},
	}
	for path, expected := range want {
		var active, hasTrack bool
		if err := fixture.pool.QueryRow(fixture.ctx, `
			SELECT mf.missing_since IS NULL,
			       EXISTS (SELECT 1 FROM music_tracks mt WHERE mt.media_file_id = mf.id)
			FROM media_files mf
			WHERE mf.media_folder_id = $1 AND mf.file_path = $2
		`, fixture.folder.ID, path).Scan(&active, &hasTrack); err != nil {
			t.Fatalf("read reconciled music track %q: %v", path, err)
		}
		if active != expected.active || hasTrack != expected.hasTrack {
			t.Errorf("music track %q state = active:%t track:%t, want active:%t track:%t",
				path, active, hasTrack, expected.active, expected.hasTrack)
		}
	}
}

func TestMusicAlbumIngestionIsAtomicWithOrphanReconciliation(t *testing.T) {
	// This catches album/item creation committing before the corresponding
	// track. The trigger pauses the insert at that exact boundary while a
	// second connection executes the real orphan reconciliation path.
	fixture := newMusicScanTestFixture(t)
	trackPath := filepath.Join(fixture.album, "new.flac")
	if err := os.WriteFile(trackPath, []byte("new audio payload"), 0o644); err != nil {
		t.Fatalf("write music track: %v", err)
	}

	barrierKey := int64(520_000_000_000) + int64(fixture.folder.ID)
	barrier := newMusicPostgresBarrier(t, fixture.pool, barrierKey)
	functionName := fmt.Sprintf("task5_music_track_barrier_%d", fixture.folder.ID)
	triggerName := fmt.Sprintf("task5_music_track_barrier_%d", fixture.folder.ID)
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $fn$
		BEGIN
			IF EXISTS (
				SELECT 1 FROM media_files mf
				WHERE mf.id = NEW.media_file_id AND mf.media_folder_id = %d
			) THEN
				PERFORM pg_advisory_xact_lock(%d::bigint);
			END IF;
			RETURN NEW;
		END
		$fn$`, functionName, fixture.folder.ID, barrierKey)); err != nil {
		t.Fatalf("create music track barrier function: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE INSERT ON music_tracks
		FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create music track barrier trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON music_tracks`, triggerName))
	})

	ingestion := make(chan error, 1)
	go func() {
		ingestion <- fixture.scanner.ScanMusicFolder(
			fixture.ctx,
			scopedFolderPaths(fixture.folder, []string{fixture.album}),
			false,
		)
	}()
	barrier.waitForWaiter(t, ingestion)

	candidates, err := fixture.scanner.snapshotMusicReconcileCandidates(fixture.ctx, fixture.folder.ID)
	if err != nil {
		barrier.release(t)
		_ = waitForMusicOperation(t, ingestion)
		t.Fatalf("snapshot music candidates while track insert is pending: %v", err)
	}
	reconcile := make(chan error, 1)
	go func() {
		reconcile <- fixture.scanner.reconcileMissingMusic(
			fixture.ctx,
			fixture.folder.ID,
			candidates,
			map[string]struct{}{trackPath: {}},
			nil,
		)
	}()
	reconcileCompleted, reconcileErr := barrier.waitForDownstreamCompletionOrLockChain(t, ingestion, reconcile)

	barrier.release(t)
	if err := waitForMusicOperation(t, ingestion); err != nil {
		t.Fatalf("music ingestion after concurrent orphan reconcile: %v", err)
	}
	if !reconcileCompleted {
		reconcileErr = waitForMusicOperation(t, reconcile)
	}
	if reconcileErr != nil {
		t.Fatalf("reconcile music while track insert is pending: %v", reconcileErr)
	}
	if reconcileCompleted {
		t.Error("orphan reconciliation bypassed the in-flight music ingest transaction")
	}

	var files, items, albums, memberships, tracks, orphanItems, orphanAlbums int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT
			(SELECT count(*) FROM media_files mf WHERE mf.media_folder_id = $1 AND mf.missing_since IS NULL),
			(SELECT count(*) FROM media_items mi WHERE mi.type = 'music_album' AND EXISTS (
				SELECT 1 FROM media_files mf WHERE mf.media_folder_id = $1 AND mf.content_id = mi.content_id
			)),
			(SELECT count(*) FROM music_albums ma WHERE EXISTS (
				SELECT 1 FROM media_files mf WHERE mf.media_folder_id = $1 AND mf.content_id = ma.content_id
			)),
			(SELECT count(*) FROM media_item_libraries mil WHERE mil.media_folder_id = $1),
			(SELECT count(*) FROM music_tracks mt JOIN media_files mf ON mf.id = mt.media_file_id WHERE mf.media_folder_id = $1),
			(SELECT count(*) FROM media_items mi WHERE mi.type = 'music_album'
				AND EXISTS (SELECT 1 FROM media_files mf WHERE mf.media_folder_id = $1 AND mf.content_id = mi.content_id)
				AND NOT EXISTS (SELECT 1 FROM music_albums ma WHERE ma.content_id = mi.content_id)),
			(SELECT count(*) FROM music_albums ma
				WHERE EXISTS (SELECT 1 FROM media_files mf WHERE mf.media_folder_id = $1 AND mf.content_id = ma.content_id)
				  AND NOT EXISTS (SELECT 1 FROM music_tracks mt WHERE mt.album_id = ma.content_id))
	`, fixture.folder.ID).Scan(&files, &items, &albums, &memberships, &tracks, &orphanItems, &orphanAlbums); err != nil {
		t.Fatalf("read music ingestion state: %v", err)
	}
	if files != 1 || items != 1 || albums != 1 || memberships != 1 || tracks != 1 || orphanItems != 0 || orphanAlbums != 0 {
		t.Fatalf("music ingestion state = files:%d items:%d albums:%d memberships:%d tracks:%d orphan-items:%d orphan-albums:%d",
			files, items, albums, memberships, tracks, orphanItems, orphanAlbums)
	}
}

func TestMusicScanFileVanishedUsesMembershipOrphanOwner(t *testing.T) {
	// This catches the file-scoped path deleting music_albums directly while
	// leaving its parent media item and folder membership behind. The shared
	// membership reconciler owns the entire item/album orphan lifecycle.
	fixture := newMusicScanTestFixture(t, "vanished.flac")
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline music scan: %v", err)
	}
	trackPath := filepath.Join(fixture.album, "vanished.flac")
	if err := os.Remove(trackPath); err != nil {
		t.Fatalf("remove music track: %v", err)
	}

	if err := fixture.scanner.ScanFile(fixture.ctx, trackPath, fixture.folder); err != nil {
		t.Fatalf("scan vanished music file: %v", err)
	}

	got := fixture.counts()
	if got.Files != 1 || got.ActiveFiles != 0 || got.Tracks != 0 || got.Albums != 0 || got.Memberships != 0 {
		t.Fatalf("catalog after file-scoped removal = %+v, want retained missing file and no item-owned rows", got)
	}
	var orphanMusicItems int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*)
		FROM media_items mi
		WHERE mi.type = 'music_album'
		  AND EXISTS (
			SELECT 1 FROM media_files mf
			WHERE mf.media_folder_id = $1 AND mf.content_id = mi.content_id
		  )
		  AND NOT EXISTS (SELECT 1 FROM music_albums ma WHERE ma.content_id = mi.content_id)
	`, fixture.folder.ID).Scan(&orphanMusicItems); err != nil {
		t.Fatalf("count orphan music items: %v", err)
	}
	if orphanMusicItems != 0 {
		t.Fatalf("orphan music items after file-scoped removal = %d, want 0", orphanMusicItems)
	}
}

func TestMusicVanishedScanSerializesWithConcurrentRefresh(t *testing.T) {
	// The missing scan pauses after marking the file missing but before deleting
	// its track. A real concurrent ingest then refreshes the same media_file.
	// The two catalog mutations must serialize so whichever commits last owns a
	// complete media_file+track state, never an active file without a track.
	fixture := newMusicScanTestFixture(t, "race.flac")
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline music scan: %v", err)
	}
	trackPath := filepath.Join(fixture.album, "race.flac")
	if err := os.Remove(trackPath); err != nil {
		t.Fatalf("remove music track: %v", err)
	}

	barrierKey := int64(530_000_000_000) + int64(fixture.folder.ID)
	barrier := newMusicPostgresBarrier(t, fixture.pool, barrierKey)
	applicationName := fmt.Sprintf("task5-music-vanished-%d", fixture.folder.ID)
	operationConfig, err := pgxpool.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse vanished music operation database config: %v", err)
	}
	operationConfig.ConnConfig.RuntimeParams["application_name"] = applicationName
	operationPool, err := pgxpool.NewWithConfig(fixture.ctx, operationConfig)
	if err != nil {
		t.Fatalf("create vanished music operation pool: %v", err)
	}
	t.Cleanup(operationPool.Close)
	missingScanner := NewScanner(NewFileRepository(operationPool), fixture.scanner.ffprobePath, nil, 1, false, 0)
	functionName := fmt.Sprintf("task5_music_delete_barrier_%d", fixture.folder.ID)
	triggerName := fmt.Sprintf("task5_music_delete_barrier_%d", fixture.folder.ID)
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $fn$
		BEGIN
			IF current_setting('application_name') = %s THEN
				PERFORM pg_advisory_xact_lock(%d::bigint);
			END IF;
			RETURN NULL;
		END
		$fn$`, functionName, musicPostgresLiteral(applicationName), barrierKey)); err != nil {
		t.Fatalf("create music delete barrier function: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE DELETE ON music_tracks
		FOR EACH STATEMENT EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create music delete barrier trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON music_tracks`, triggerName))
	})

	missing := make(chan error, 1)
	go func() { missing <- missingScanner.ScanFile(fixture.ctx, trackPath, fixture.folder) }()
	barrier.waitForWaiter(t, missing)

	if err := os.WriteFile(trackPath, []byte("restored audio payload"), 0o644); err != nil {
		barrier.release(t)
		_ = waitForMusicOperation(t, missing)
		t.Fatalf("restore music track during missing scan: %v", err)
	}
	refresh := make(chan error, 1)
	go func() {
		refresh <- fixture.scanner.ScanMusicFolder(
			fixture.ctx,
			scopedFolderPaths(fixture.folder, []string{trackPath}),
			false,
		)
	}()
	refreshCompleted, refreshErr := barrier.waitForDownstreamCompletionOrLockChain(t, missing, refresh)

	barrier.release(t)
	if err := waitForMusicOperation(t, missing); err != nil {
		t.Fatalf("vanished music scan after concurrent refresh: %v", err)
	}
	if !refreshCompleted {
		refreshErr = waitForMusicOperation(t, refresh)
	}
	if refreshErr != nil {
		t.Fatalf("concurrent music refresh: %v", refreshErr)
	}
	if refreshCompleted {
		t.Error("concurrent refresh bypassed the vanished scan's database transaction")
	}

	var active, hasTrack bool
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT mf.missing_since IS NULL,
		       EXISTS (SELECT 1 FROM music_tracks mt WHERE mt.media_file_id = mf.id)
		FROM media_files mf
		WHERE mf.media_folder_id = $1 AND mf.file_path = $2
	`, fixture.folder.ID, trackPath).Scan(&active, &hasTrack); err != nil {
		t.Fatalf("read refreshed music track: %v", err)
	}
	if !active || !hasTrack {
		t.Fatalf("refreshed music track state = active:%t track:%t, want active:true track:true", active, hasTrack)
	}
}

func TestConcurrentMusicIngestsShareOneNewAlbumIdentity(t *testing.T) {
	// Both scans enter with a previously unseen album root. The first pauses
	// after choosing its ID; the second must wait on a database-visible root
	// lock, then reuse the album established by the first transaction.
	fixture := newMusicScanTestFixture(t)
	firstPath := filepath.Join(fixture.album, "first.flac")
	secondPath := filepath.Join(fixture.album, "second.flac")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("new audio payload"), 0o644); err != nil {
			t.Fatalf("write concurrent music track %q: %v", path, err)
		}
	}

	barrierKey := int64(540_000_000_000) + int64(fixture.folder.ID)
	barrier := newMusicPostgresBarrier(t, fixture.pool, barrierKey)
	functionName := fmt.Sprintf("task5_music_album_root_barrier_%d", fixture.folder.ID)
	triggerName := fmt.Sprintf("task5_music_album_root_barrier_%d", fixture.folder.ID)
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $fn$
		BEGIN
			IF NEW.media_folder_id = %d AND NEW.file_path = %s THEN
				PERFORM pg_advisory_xact_lock(%d::bigint);
			END IF;
			RETURN NEW;
		END
		$fn$`, functionName, fixture.folder.ID, musicPostgresLiteral(firstPath), barrierKey)); err != nil {
		t.Fatalf("create music album-root barrier function: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON media_files
		FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create music album-root barrier trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON media_files`, triggerName))
	})

	first := make(chan error, 1)
	go func() {
		first <- fixture.scanner.ScanMusicFolder(
			fixture.ctx,
			scopedFolderPaths(fixture.folder, []string{firstPath}),
			false,
		)
	}()
	barrier.waitForWaiter(t, first)

	second := make(chan error, 1)
	go func() {
		second <- fixture.scanner.ScanMusicFolder(
			fixture.ctx,
			scopedFolderPaths(fixture.folder, []string{secondPath}),
			false,
		)
	}()
	secondCompleted, secondErr := barrier.waitForDownstreamCompletionOrLockChain(t, first, second)

	barrier.release(t)
	if err := waitForMusicOperation(t, first); err != nil {
		t.Fatalf("first concurrent music ingest: %v", err)
	}
	if !secondCompleted {
		secondErr = waitForMusicOperation(t, second)
	}
	if secondErr != nil {
		t.Fatalf("second concurrent music ingest: %v", secondErr)
	}
	if secondCompleted {
		t.Error("second ingest bypassed serialization for an unseen album root")
	}

	var albums, files, tracks, memberships int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT
			(SELECT count(DISTINCT mf.content_id) FROM media_files mf WHERE mf.media_folder_id = $1),
			(SELECT count(*) FROM media_files mf WHERE mf.media_folder_id = $1 AND mf.missing_since IS NULL),
			(SELECT count(*) FROM music_tracks mt JOIN media_files mf ON mf.id = mt.media_file_id WHERE mf.media_folder_id = $1),
			(SELECT count(*) FROM media_item_libraries mil WHERE mil.media_folder_id = $1)
	`, fixture.folder.ID).Scan(&albums, &files, &tracks, &memberships); err != nil {
		t.Fatalf("read concurrent album state: %v", err)
	}
	if albums != 1 || files != 2 || tracks != 2 || memberships != 1 {
		t.Fatalf("concurrent album state = albums:%d files:%d tracks:%d memberships:%d, want 1/2/2/1",
			albums, files, tracks, memberships)
	}
}

func TestMusicFullReconcileMarkAndDeleteAreAtomicWithRefresh(t *testing.T) {
	// The full reconciler pauses after its active candidate has been CAS-marked
	// missing and before track deletion. A second candidate was already missing
	// at snapshot time. A concurrent scoped ingest must not fit between either
	// candidate's version check and deletion.
	fixture := newMusicScanTestFixture(t, "active.flac", "already-missing.flac")
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline music scan: %v", err)
	}
	activePath := filepath.Join(fixture.album, "active.flac")
	alreadyMissingPath := filepath.Join(fixture.album, "already-missing.flac")
	for _, path := range []string{activePath, alreadyMissingPath} {
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove music track %q: %v", path, err)
		}
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE media_files
		SET missing_since = NOW(), updated_at = NOW()
		WHERE media_folder_id = $1 AND file_path = $2
	`, fixture.folder.ID, alreadyMissingPath); err != nil {
		t.Fatalf("seed already-missing music candidate: %v", err)
	}

	applicationName := fmt.Sprintf("task5-music-full-reconcile-%d", fixture.folder.ID)
	reconcileScanner := newMusicScannerWithApplicationName(t, fixture, applicationName)
	candidates, err := reconcileScanner.snapshotMusicReconcileCandidates(fixture.ctx, fixture.folder.ID)
	if err != nil {
		t.Fatalf("snapshot music reconcile candidates: %v", err)
	}
	barrierKey := int64(550_000_000_000) + int64(fixture.folder.ID)
	barrier := newMusicPostgresBarrier(t, fixture.pool, barrierKey)
	functionName := fmt.Sprintf("task5_music_full_delete_barrier_%d", fixture.folder.ID)
	triggerName := fmt.Sprintf("task5_music_full_delete_barrier_%d", fixture.folder.ID)
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $fn$
		BEGIN
			IF current_setting('application_name') = %s THEN
				PERFORM pg_advisory_xact_lock(%d::bigint);
			END IF;
			RETURN NULL;
		END
		$fn$`, functionName, musicPostgresLiteral(applicationName), barrierKey)); err != nil {
		t.Fatalf("create full reconcile delete barrier function: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE DELETE ON music_tracks
		FOR EACH STATEMENT EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create full reconcile delete barrier trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON music_tracks`, triggerName))
	})

	reconcile := make(chan error, 1)
	go func() {
		reconcile <- reconcileScanner.reconcileMissingMusic(
			fixture.ctx,
			fixture.folder.ID,
			candidates,
			map[string]struct{}{},
			nil,
		)
	}()
	barrier.waitForWaiter(t, reconcile)

	for _, path := range []string{activePath, alreadyMissingPath} {
		if err := os.WriteFile(path, []byte("restored audio payload"), 0o644); err != nil {
			barrier.release(t)
			_ = waitForMusicOperation(t, reconcile)
			t.Fatalf("restore music track %q during reconcile: %v", path, err)
		}
	}
	refresh := make(chan error, 1)
	go func() {
		refresh <- fixture.scanner.ScanMusicFolder(
			fixture.ctx,
			scopedFolderPaths(fixture.folder, []string{fixture.album}),
			false,
		)
	}()
	refreshCompleted, refreshErr := barrier.waitForDownstreamCompletionOrLockChain(t, reconcile, refresh)

	barrier.release(t)
	if err := waitForMusicOperation(t, reconcile); err != nil {
		t.Fatalf("full music reconcile after refresh: %v", err)
	}
	if !refreshCompleted {
		refreshErr = waitForMusicOperation(t, refresh)
	}
	if refreshErr != nil {
		t.Fatalf("music refresh during full reconcile: %v", refreshErr)
	}
	if refreshCompleted {
		t.Error("music refresh bypassed full reconcile mutation serialization")
	}

	for _, path := range []string{activePath, alreadyMissingPath} {
		var active, hasTrack bool
		if err := fixture.pool.QueryRow(fixture.ctx, `
			SELECT mf.missing_since IS NULL,
			       EXISTS (SELECT 1 FROM music_tracks mt WHERE mt.media_file_id = mf.id)
			FROM media_files mf
			WHERE mf.media_folder_id = $1 AND mf.file_path = $2
		`, fixture.folder.ID, path).Scan(&active, &hasTrack); err != nil {
			t.Fatalf("read refreshed full-reconcile candidate %q: %v", path, err)
		}
		if !active || !hasTrack {
			t.Errorf("refreshed candidate %q state = active:%t track:%t, want active:true track:true", path, active, hasTrack)
		}
	}
}

func TestMusicExistingMissingAlbumRefreshSerializesWithOrphanReconcile(t *testing.T) {
	// The ingest pauses after updating the item and membership but before
	// refreshing the existing missing media file. Orphan reconciliation must
	// wait before deleting membership/item state, then re-evaluate the album
	// after the ingest commits.
	fixture := newMusicScanTestFixture(t, "returning.flac")
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline music scan: %v", err)
	}
	trackPath := filepath.Join(fixture.album, "returning.flac")
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE media_files
		SET missing_since = NOW(), updated_at = NOW()
		WHERE media_folder_id = $1 AND file_path = $2
	`, fixture.folder.ID, trackPath); err != nil {
		t.Fatalf("mark existing music file missing: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		DELETE FROM music_tracks mt USING media_files mf
		WHERE mt.media_file_id = mf.id
		  AND mf.media_folder_id = $1
		  AND mf.file_path = $2
	`, fixture.folder.ID, trackPath); err != nil {
		t.Fatalf("remove missing music track: %v", err)
	}
	candidates, err := fixture.scanner.snapshotMusicReconcileCandidates(fixture.ctx, fixture.folder.ID)
	if err != nil {
		t.Fatalf("snapshot existing missing music album: %v", err)
	}

	applicationName := fmt.Sprintf("task5-music-existing-refresh-%d", fixture.folder.ID)
	ingestScanner := newMusicScannerWithApplicationName(t, fixture, applicationName)
	barrierKey := int64(560_000_000_000) + int64(fixture.folder.ID)
	barrier := newMusicPostgresBarrier(t, fixture.pool, barrierKey)
	functionName := fmt.Sprintf("task5_music_existing_refresh_barrier_%d", fixture.folder.ID)
	triggerName := fmt.Sprintf("task5_music_existing_refresh_barrier_%d", fixture.folder.ID)
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $fn$
		BEGIN
			IF current_setting('application_name') = %s
			   AND NEW.media_folder_id = %d
			   AND NEW.file_path = %s THEN
				PERFORM pg_advisory_xact_lock(%d::bigint);
			END IF;
			RETURN NEW;
		END
		$fn$`, functionName, musicPostgresLiteral(applicationName), fixture.folder.ID,
		musicPostgresLiteral(trackPath), barrierKey)); err != nil {
		t.Fatalf("create existing refresh barrier function: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})
	if _, err := fixture.pool.Exec(fixture.ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON media_files
		FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create existing refresh barrier trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON media_files`, triggerName))
	})

	ingest := make(chan error, 1)
	go func() {
		ingest <- ingestScanner.ScanMusicFolder(
			fixture.ctx,
			scopedFolderPaths(fixture.folder, []string{trackPath}),
			false,
		)
	}()
	barrier.waitForWaiter(t, ingest)

	reconcile := make(chan error, 1)
	go func() {
		reconcile <- fixture.scanner.reconcileMissingMusic(
			fixture.ctx,
			fixture.folder.ID,
			candidates,
			map[string]struct{}{},
			nil,
		)
	}()
	reconcileCompleted, reconcileErr := barrier.waitForDownstreamCompletionOrLockChain(t, ingest, reconcile)

	barrier.release(t)
	if err := waitForMusicOperation(t, ingest); err != nil {
		t.Fatalf("existing missing music ingest: %v", err)
	}
	if !reconcileCompleted {
		reconcileErr = waitForMusicOperation(t, reconcile)
	}
	if reconcileErr != nil {
		t.Fatalf("orphan reconcile during existing music ingest: %v", reconcileErr)
	}

	var item, album, membership, activeFile, track bool
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT
			EXISTS (SELECT 1 FROM media_items mi JOIN media_files mf ON mf.content_id = mi.content_id
				WHERE mf.media_folder_id = $1 AND mf.file_path = $2),
			EXISTS (SELECT 1 FROM music_albums ma JOIN media_files mf ON mf.content_id = ma.content_id
				WHERE mf.media_folder_id = $1 AND mf.file_path = $2),
			EXISTS (SELECT 1 FROM media_item_libraries mil JOIN media_files mf ON mf.content_id = mil.content_id
				WHERE mil.media_folder_id = $1 AND mf.media_folder_id = $1 AND mf.file_path = $2),
			EXISTS (SELECT 1 FROM media_files mf
				WHERE mf.media_folder_id = $1 AND mf.file_path = $2 AND mf.missing_since IS NULL),
			EXISTS (SELECT 1 FROM music_tracks mt JOIN media_files mf ON mf.id = mt.media_file_id
				WHERE mf.media_folder_id = $1 AND mf.file_path = $2)
	`, fixture.folder.ID, trackPath).Scan(&item, &album, &membership, &activeFile, &track); err != nil {
		t.Fatalf("read existing album state after concurrent refresh/reconcile: %v", err)
	}
	if !item || !album || !membership || !activeFile || !track {
		t.Fatalf("existing album state = item:%t album:%t membership:%t active-file:%t track:%t, want all true",
			item, album, membership, activeFile, track)
	}
}

func TestMusicVanishedScanCannotMutateAnotherLibraryPathOwner(t *testing.T) {
	// media_files.file_path is global, but a ScanFile request is owned by its
	// supplied library. A stale/cross-library path lookup must not mutate the
	// row merely because its global path matches.
	requested := newMusicScanTestFixture(t)
	owner := newMusicScanTestFixture(t, "owned.flac")
	if err := owner.scan(); err != nil {
		t.Fatalf("baseline owner music scan: %v", err)
	}
	ownedPath := filepath.Join(owner.album, "owned.flac")
	if err := os.Remove(ownedPath); err != nil {
		t.Fatalf("remove owner music file: %v", err)
	}

	if err := requested.scanner.ScanFile(requested.ctx, ownedPath, requested.folder); err != nil {
		t.Fatalf("cross-library vanished music scan: %v", err)
	}

	var ownerFolderID int
	var active, hasTrack bool
	if err := owner.pool.QueryRow(owner.ctx, `
		SELECT mf.media_folder_id,
		       mf.missing_since IS NULL,
		       EXISTS (SELECT 1 FROM music_tracks mt WHERE mt.media_file_id = mf.id)
		FROM media_files mf
		WHERE mf.file_path = $1
	`, ownedPath).Scan(&ownerFolderID, &active, &hasTrack); err != nil {
		t.Fatalf("read cross-library music owner: %v", err)
	}
	if ownerFolderID != owner.folder.ID || !active || !hasTrack {
		t.Fatalf("cross-library owner state = folder:%d active:%t track:%t, want folder:%d active:true track:true",
			ownerFolderID, active, hasTrack, owner.folder.ID)
	}
}

func TestMusicVanishedScanDoesNotPurgeProtectedSiblingRootOrphan(t *testing.T) {
	// A prior protected-root reconciliation may leave an intentionally hidden,
	// membership-less item so its metadata/history can return when that root is
	// reachable again. A file event under a healthy sibling must reconcile only
	// its own album and leave that protected orphan untouched.
	fixture := newMusicScanTestFixture(t, "healthy.flac")
	protectedRoot := t.TempDir()
	protectedAlbum := filepath.Join(protectedRoot, "Artist", "Protected Album")
	if err := os.MkdirAll(protectedAlbum, 0o755); err != nil {
		t.Fatalf("create protected music album: %v", err)
	}
	protectedPath := filepath.Join(protectedAlbum, "protected.flac")
	if err := os.WriteFile(protectedPath, []byte("protected audio payload"), 0o644); err != nil {
		t.Fatalf("write protected music track: %v", err)
	}
	fixture.folder.Paths = append(fixture.folder.Paths, protectedRoot)
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline mixed-root music scan: %v", err)
	}

	healthyPath := filepath.Join(fixture.album, "healthy.flac")
	var healthyContentID, protectedContentID string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT content_id FROM media_files
		WHERE media_folder_id = $1 AND file_path = $2
	`, fixture.folder.ID, healthyPath).Scan(&healthyContentID); err != nil {
		t.Fatalf("read healthy music content id: %v", err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT content_id FROM media_files
		WHERE media_folder_id = $1 AND file_path = $2
	`, fixture.folder.ID, protectedPath).Scan(&protectedContentID); err != nil {
		t.Fatalf("read protected music content id: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE media_files
		SET missing_since = NOW(), updated_at = NOW()
		WHERE media_folder_id = $1 AND file_path = $2
	`, fixture.folder.ID, protectedPath); err != nil {
		t.Fatalf("mark protected-root music file missing: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		DELETE FROM music_tracks mt USING media_files mf
		WHERE mt.media_file_id = mf.id
		  AND mf.media_folder_id = $1
		  AND mf.file_path = $2
	`, fixture.folder.ID, protectedPath); err != nil {
		t.Fatalf("remove protected-root music track: %v", err)
	}
	removed, deleted, _, err := fixture.scanner.libraryRepo.ReconcileFolderMembership(
		fixture.ctx,
		fixture.folder.ID,
		[]string{protectedRoot},
	)
	if err != nil {
		t.Fatalf("seed protected-root orphan state: %v", err)
	}
	if removed != 1 || deleted != 0 {
		t.Fatalf("protected-root seed reconciliation = removed:%d deleted:%d, want 1/0", removed, deleted)
	}
	if err := os.RemoveAll(protectedRoot); err != nil {
		t.Fatalf("make protected music root unreachable: %v", err)
	}
	if err := os.Remove(healthyPath); err != nil {
		t.Fatalf("remove healthy sibling music track: %v", err)
	}

	if err := fixture.scanner.ScanFile(fixture.ctx, healthyPath, fixture.folder); err != nil {
		t.Fatalf("scan vanished healthy-root music track: %v", err)
	}

	var healthyItem, protectedItem, protectedAlbumRow, protectedMembership bool
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT
			EXISTS (SELECT 1 FROM media_items WHERE content_id = $1),
			EXISTS (SELECT 1 FROM media_items WHERE content_id = $2),
			EXISTS (SELECT 1 FROM music_albums WHERE content_id = $2),
			EXISTS (SELECT 1 FROM media_item_libraries WHERE content_id = $2 AND media_folder_id = $3)
	`, healthyContentID, protectedContentID, fixture.folder.ID).Scan(
		&healthyItem,
		&protectedItem,
		&protectedAlbumRow,
		&protectedMembership,
	); err != nil {
		t.Fatalf("read mixed-root orphan state: %v", err)
	}
	if healthyItem {
		t.Error("vanished healthy-root album item was not reconciled")
	}
	if !protectedItem || !protectedAlbumRow || protectedMembership {
		t.Fatalf("protected sibling state = item:%t album:%t membership:%t, want true/true/false",
			protectedItem, protectedAlbumRow, protectedMembership)
	}
}

func TestMusicScanCaseDistinctTracksReconcilesLegacyIDAndStaysStable(t *testing.T) {
	fixture := newMusicScanTestFixture(t, "Track.flac", "track.flac")
	entries, err := os.ReadDir(fixture.album)
	if err != nil {
		t.Fatalf("read case-sensitive fixture: %v", err)
	}
	if len(entries) != 2 {
		t.Skip("test filesystem is case-insensitive")
	}
	if err := fixture.scan(); err != nil {
		t.Fatalf("baseline music scan: %v", err)
	}

	lowerPath := filepath.Join(fixture.album, "track.flac")
	var albumID, artistID string
	var lowerFileID int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT mf.content_id, mt.artist_id, mf.id
		FROM media_files mf
		JOIN music_tracks mt ON mt.media_file_id = mf.id
		WHERE mf.media_folder_id = $1 AND mf.file_path = $2
	`, fixture.folder.ID, lowerPath).Scan(&albumID, &artistID, &lowerFileID); err != nil {
		t.Fatalf("read legacy collision owner: %v", err)
	}
	legacyID := legacyMusicTrackID(lowerPath)
	legacyCreatedAt := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := fixture.pool.Exec(fixture.ctx, `DELETE FROM music_tracks WHERE album_id = $1`, albumID); err != nil {
		t.Fatalf("clear current track identities: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO music_tracks (
			id, album_id, artist_id, media_file_id, title, duration_ms,
			disc_number, track_number, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'legacy collision owner', 60000, 1, 2, $5, $5)
	`, legacyID, albumID, artistID, lowerFileID, legacyCreatedAt); err != nil {
		t.Fatalf("seed legacy lowercased track identity: %v", err)
	}

	if err := fixture.scan(); err != nil {
		t.Fatalf("reconcile legacy music identities: %v", err)
	}
	readIDs := func() ([]string, map[int]time.Time) {
		t.Helper()
		rows, err := fixture.pool.Query(fixture.ctx, `
			SELECT mt.id, mt.media_file_id, mt.created_at
			FROM music_tracks mt
			JOIN media_files mf ON mf.id = mt.media_file_id
			WHERE mf.media_folder_id = $1
			ORDER BY mt.id
		`, fixture.folder.ID)
		if err != nil {
			t.Fatalf("read music track identities: %v", err)
		}
		defer rows.Close()
		ids := make([]string, 0, 2)
		created := make(map[int]time.Time)
		for rows.Next() {
			var id string
			var fileID int
			var createdAt time.Time
			if err := rows.Scan(&id, &fileID, &createdAt); err != nil {
				t.Fatalf("scan music track identity: %v", err)
			}
			ids = append(ids, id)
			created[fileID] = createdAt
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate music track identities: %v", err)
		}
		return ids, created
	}
	firstIDs, created := readIDs()
	if len(firstIDs) != 2 || firstIDs[0] == firstIDs[1] {
		t.Fatalf("case-distinct track IDs = %v, want two distinct IDs", firstIDs)
	}
	if sort.SearchStrings(firstIDs, legacyID) < len(firstIDs) && firstIDs[sort.SearchStrings(firstIDs, legacyID)] == legacyID {
		t.Fatalf("legacy lowercased track ID %q survived reconciliation", legacyID)
	}
	if got := created[lowerFileID]; !got.Equal(legacyCreatedAt) {
		t.Fatalf("legacy collision owner created_at = %v, want preserved %v", got, legacyCreatedAt)
	}

	if err := fixture.scan(); err != nil {
		t.Fatalf("repeat music scan: %v", err)
	}
	secondIDs, _ := readIDs()
	if !reflect.DeepEqual(secondIDs, firstIDs) {
		t.Fatalf("track IDs changed across repeat scan: first=%v second=%v", firstIDs, secondIDs)
	}
}
