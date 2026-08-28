package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type musicScanTestFixture struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	scanner *Scanner
	folder  *models.MediaFolder
	root    string
	album   string
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
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM media_files WHERE media_folder_id = $1`, folderID)
		_, _ = pool.Exec(ctx, `
			DELETE FROM media_items mi
			USING media_item_libraries mil
			WHERE mil.content_id = mi.content_id AND mil.media_folder_id = $1`, folderID)
		_, _ = pool.Exec(ctx, `
			DELETE FROM music_artists ar
			WHERE ar.id = $1 AND NOT EXISTS (
				SELECT 1 FROM music_albums ma WHERE ma.artist_id = ar.id
			)`, stableMusicSemanticID("artist", "Artist"))
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

func TestStableMusicSemanticIDsRemainCaseInsensitive(t *testing.T) {
	if upper, lower := stableMusicSemanticID("artist", "Bloem Artist"), stableMusicSemanticID("artist", "bloem artist"); upper != lower {
		t.Fatalf("case-equivalent artist IDs differ: %q != %q", upper, lower)
	}
	if upper, lower := stableMusicSemanticID("album", "Bloem Album"), stableMusicSemanticID("album", "bloem album"); upper != lower {
		t.Fatalf("case-equivalent album IDs differ: %q != %q", upper, lower)
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
