package scanner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/idgen"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

type parsedMusicTrack struct {
	Path        string
	Title       string
	Album       string
	AlbumArtist string
	Artist      string
	Year        int
	DiscNumber  int
	TrackNumber int
	DurationMS  int64
	Probe       ProbeData
}

func musicTrackFromProbe(path string, probe ProbeData) parsedMusicTrack {
	tags := probe.FormatTags
	albumDir := filepath.Dir(path)
	artistDir := filepath.Dir(albumDir)
	return parsedMusicTrack{
		Path:        path,
		Title:       firstNonEmpty(tags["title"], strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))),
		Album:       firstNonEmpty(tags["album"], filepath.Base(albumDir)),
		AlbumArtist: firstNonEmpty(tags["album_artist"], tags["artist"], filepath.Base(artistDir), "Unknown Artist"),
		Artist:      firstNonEmpty(tags["artist"], tags["album_artist"], filepath.Base(artistDir), "Unknown Artist"),
		Year:        parseTagYear(firstNonEmpty(tags["date"], tags["year"])),
		DiscNumber:  musicOrdinal(firstNonEmpty(tags["disc"], tags["discnumber"])),
		TrackNumber: musicOrdinal(firstNonEmpty(tags["track"], tags["tracknumber"])),
		DurationMS:  int64(probe.Duration) * 1000,
		Probe:       probe,
	}
}

func musicOrdinal(value string) int {
	value = strings.TrimSpace(strings.SplitN(value, "/", 2)[0])
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func stableMusicSemanticID(kind, value string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + strings.ToLower(strings.TrimSpace(value))))
	return fmt.Sprintf("music-%s-%x", kind, sum[:12])
}

func legacyMusicTrackID(trackPath string) string {
	return stableMusicSemanticID("track", trackPath)
}

// stableMusicTrackID deliberately preserves path case. Artist/album identity
// is semantic and case-insensitive, but a case-sensitive filesystem may hold
// Track.flac and track.flac as two distinct files. Scoping the cleaned,
// album-relative path by folder and album keeps those physical identities
// deterministic without coupling them to an absolute library location.
func stableMusicTrackID(folderID int, albumID, albumRoot, trackPath string) string {
	relativePath := filepath.Clean(trackPath)
	if rel, err := filepath.Rel(filepath.Clean(albumRoot), relativePath); err == nil &&
		rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		relativePath = filepath.Clean(rel)
	}
	identity := fmt.Sprintf("%d\x00%s\x00%s", folderID, strings.TrimSpace(albumID), filepath.ToSlash(relativePath))
	sum := sha256.Sum256([]byte("track\x00" + identity))
	return fmt.Sprintf("music-track-%x", sum[:12])
}

// ScanMusicFolder indexes one album per directory containing supported audio
// files. Tags are authoritative when present; filesystem names are only
// fallbacks, and absent disc/track numbers remain unknown rather than guessed.
func (s *Scanner) ScanMusicFolder(ctx context.Context, folder *models.MediaFolder, fullScan bool) error {
	if s == nil || folder == nil {
		return errors.New("ScanMusicFolder: nil scanner or folder")
	}
	if s.fileRepo == nil || s.itemRepo == nil {
		return nil
	}

	var rootObservation RootSetObservation
	if fullScan {
		var err error
		rootObservation, err = s.ObserveRoots(ctx, folder.ID, folder.Paths)
		if err != nil {
			return err
		}
	}
	paths, walkFailures, err := collectLogicalFilePaths(ctx, folder.Paths, folder.Type)
	if err != nil {
		return fmt.Errorf("walk music roots: %w", err)
	}
	if fullScan {
		// A mount can drop while it is being walked. Conservatively retain every
		// root seen unsafe by either sample so a partial inventory never becomes
		// evidence of deletion.
		postWalk, observeErr := s.ObserveRoots(ctx, folder.ID, folder.Paths)
		if observeErr != nil {
			return observeErr
		}
		rootObservation = mergeRootSetObservations(rootObservation, postWalk)
	}
	sort.Strings(paths)

	confirmedCleanup := false
	if fullScan {
		hasCatalogedFiles, err := s.hasCatalogedMusicFiles(ctx, folder.ID)
		if err != nil {
			return err
		}
		allRootsUnreachable := allConfiguredRootsUnreachable(
			rootObservation.ConfiguredRoots,
			rootObservation.UnreachableRoots,
		)
		if len(paths) == 0 && hasCatalogedFiles && !allRootsUnreachable {
			confirmedCleanup, err = s.folderRepo.ConsumeEmptyCleanupAllowance(ctx, folder.ID)
			if err != nil {
				return fmt.Errorf("checking empty cleanup confirmation for folder %d: %w", folder.ID, err)
			}
			if !confirmedCleanup {
				if err := s.folderRepo.SetScanWarning(ctx, folder.ID,
					"empty_root",
					"Scan found 0 media files; cleanup was skipped until deletion is confirmed.",
					time.Now().UTC(),
				); err != nil {
					return fmt.Errorf("recording empty-root warning for folder %d: %w", folder.ID, err)
				}
				return nil
			}
		} else if len(paths) > 0 && len(rootObservation.SuspectEmptyRoots) > 0 {
			confirmedCleanup, err = s.folderRepo.ConsumeEmptyCleanupAllowance(ctx, folder.ID)
			if err != nil {
				return fmt.Errorf("checking empty cleanup confirmation for folder %d: %w", folder.ID, err)
			}
		}
		if confirmedCleanup {
			rootObservation.SuspectEmptyRoots = nil
		}
	}

	seen := make(map[string]struct{}, len(paths))
	albumIDs := make(map[string]string)
	albumTrackIndexes := make(map[string]int)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		probe, err := ProbeFile(ctx, s.ffprobePath, path)
		if err != nil {
			return fmt.Errorf("probe music file %s: %w", path, err)
		}
		track := musicTrackFromProbe(path, *probe)
		albumRoot := filepath.Dir(path)
		albumTrackIndexes[albumRoot]++
		if track.DiscNumber == 0 {
			track.DiscNumber = 1
		}
		if track.TrackNumber == 0 {
			track.TrackNumber = albumTrackIndexes[albumRoot]
		}
		albumID := albumIDs[albumRoot]
		if albumID == "" {
			albumID, err = s.findOrCreateMusicAlbumID(ctx, folder.ID, albumRoot)
			if err != nil {
				return err
			}
			albumIDs[albumRoot] = albumID
		}
		if err := s.upsertMusicTrack(ctx, folder, albumID, albumRoot, track); err != nil {
			return err
		}
		seen[path] = struct{}{}
	}
	if fullScan {
		protectedRoots := append([]string(nil), rootObservation.UnreachableRoots...)
		protectedRoots = append(protectedRoots, rootObservation.SuspectEmptyRoots...)
		for _, failedPath := range walkFailures {
			protectedRoots = appendUniquePath(protectedRoots, failedPath)
		}
		if !allConfiguredRootsUnreachable(rootObservation.ConfiguredRoots, rootObservation.UnreachableRoots) {
			if err := s.reconcileMissingMusic(ctx, folder.ID, seen, protectedRoots); err != nil {
				return err
			}
		}
	}
	if err := s.syncFolderScopedAudioLibraryState(ctx, folder.ID); err != nil {
		return err
	}
	if !fullScan {
		return nil
	}

	switch {
	case len(rootObservation.UnreachableRoots) > 0 || len(rootObservation.SuspectEmptyRoots) > 0:
		if err := s.folderRepo.SetScanWarning(ctx, folder.ID,
			"dead_root",
			deadRootWarningMessage(
				len(rootObservation.ConfiguredRoots),
				rootObservation.UnreachableRoots,
				rootObservation.SuspectEmptyRoots,
			),
			time.Now().UTC(),
		); err != nil {
			return fmt.Errorf("recording dead-root warning for folder %d: %w", folder.ID, err)
		}
	case len(paths) > 0 || confirmedCleanup:
		if err := s.folderRepo.ClearScanWarning(ctx, folder.ID); err != nil {
			return fmt.Errorf("clearing scan warning for folder %d: %w", folder.ID, err)
		}
	}
	return nil
}

func mergeRootSetObservations(first, second RootSetObservation) RootSetObservation {
	merged := RootSetObservation{ConfiguredRoots: append([]string(nil), first.ConfiguredRoots...)}
	if len(merged.ConfiguredRoots) == 0 {
		merged.ConfiguredRoots = append(merged.ConfiguredRoots, second.ConfiguredRoots...)
	}
	for _, root := range append(first.UnreachableRoots, second.UnreachableRoots...) {
		merged.UnreachableRoots = appendUniquePath(merged.UnreachableRoots, root)
	}
	for _, root := range append(first.EmptyRoots, second.EmptyRoots...) {
		merged.EmptyRoots = appendUniquePath(merged.EmptyRoots, root)
	}
	for _, root := range append(first.SuspectEmptyRoots, second.SuspectEmptyRoots...) {
		merged.SuspectEmptyRoots = appendUniquePath(merged.SuspectEmptyRoots, root)
	}
	return merged
}

func allConfiguredRootsUnreachable(configuredRoots, unreachableRoots []string) bool {
	if len(configuredRoots) == 0 {
		return false
	}
	for _, root := range configuredRoots {
		found := false
		for _, unreachable := range unreachableRoots {
			if root == unreachable {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (s *Scanner) hasCatalogedMusicFiles(ctx context.Context, folderID int) (bool, error) {
	var exists bool
	if err := s.fileRepo.Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM media_files
			WHERE media_folder_id = $1 AND base_type = 'music'
		)`, folderID).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking existing music files for folder %d: %w", folderID, err)
	}
	return exists, nil
}

func (s *Scanner) findOrCreateMusicAlbumID(ctx context.Context, folderID int, albumRoot string) (string, error) {
	var contentID string
	err := s.fileRepo.Pool().QueryRow(ctx, `
		SELECT ma.content_id
		FROM music_albums ma
		JOIN media_files mf ON mf.content_id = ma.content_id
		WHERE mf.media_folder_id = $1 AND mf.canonical_root_path = $2
		LIMIT 1`, folderID, albumRoot).Scan(&contentID)
	if err == nil {
		return contentID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("find music album by root: %w", err)
	}
	contentID, err = idgen.NextID()
	if err != nil {
		return "", err
	}
	return contentID, nil
}

func (s *Scanner) upsertMusicTrack(ctx context.Context, folder *models.MediaFolder, albumID, albumRoot string, track parsedMusicTrack) error {
	artistID := stableMusicSemanticID("artist", track.AlbumArtist)
	item := &models.MediaItem{
		ContentID: albumID,
		Type:      "music_album",
		Title:     track.Album,
		SortTitle: track.Album,
		Year:      track.Year,
		Runtime:   int(track.DurationMS / 60000),
		Status:    "matched",
	}
	if err := s.itemRepo.Upsert(ctx, item); err != nil {
		return fmt.Errorf("upsert music album item: %w", err)
	}
	pool := s.fileRepo.Pool()
	if _, err := pool.Exec(ctx, `
		INSERT INTO music_artists (id, name, sort_name)
		VALUES ($1, $2, $2)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, sort_name = EXCLUDED.sort_name, updated_at = NOW()`, artistID, track.AlbumArtist); err != nil {
		return fmt.Errorf("upsert music artist: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO music_albums (content_id, artist_id, year)
		VALUES ($1, $2, NULLIF($3, 0))
		ON CONFLICT (content_id) DO UPDATE SET artist_id = EXCLUDED.artist_id,
			year = COALESCE(EXCLUDED.year, music_albums.year), updated_at = NOW()`, albumID, artistID, track.Year); err != nil {
		return fmt.Errorf("upsert music album: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_item_libraries (content_id, media_folder_id, first_seen_at)
		VALUES ($1, $2, NOW()) ON CONFLICT (content_id, media_folder_id) DO NOTHING`, albumID, folder.ID); err != nil {
		return fmt.Errorf("upsert music library membership: %w", err)
	}
	info, err := os.Stat(track.Path)
	if err != nil {
		return fmt.Errorf("stat music file: %w", err)
	}
	modified := normalizeFileModifiedAt(info.ModTime())
	mediaFile := musicMediaFile(folder, albumID, albumRoot, track, info.Size(), modified)
	mf, err := s.fileRepo.Upsert(ctx, mediaFile)
	if err != nil {
		return fmt.Errorf("upsert music media file: %w", err)
	}
	trackID := stableMusicTrackID(folder.ID, albumID, albumRoot, track.Path)
	legacyTrackID := legacyMusicTrackID(track.Path)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin music track upsert: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if legacyTrackID != trackID {
		// The legacy scheme lowercased full paths, so two case-distinct files
		// could share one ID. Move that row only when it already belongs to this
		// media file: the existing owner deterministically keeps its history,
		// while any collided sibling receives its own new ID below.
		if _, err := tx.Exec(ctx, `
			UPDATE music_tracks legacy
			SET id = $1
			WHERE legacy.id = $2
			  AND legacy.media_file_id = $3
			  AND NOT EXISTS (
				SELECT 1 FROM music_tracks current WHERE current.id = $1
			  )`, trackID, legacyTrackID, mf.ID); err != nil {
			return fmt.Errorf("reconcile legacy music track identity: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO music_tracks (id, album_id, artist_id, media_file_id, title, duration_ms, disc_number, track_number)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET album_id = EXCLUDED.album_id, artist_id = EXCLUDED.artist_id,
			media_file_id = EXCLUDED.media_file_id, title = EXCLUDED.title, duration_ms = EXCLUDED.duration_ms,
			disc_number = EXCLUDED.disc_number, track_number = EXCLUDED.track_number, updated_at = NOW()`,
		trackID, albumID, artistID, mf.ID, track.Title, track.DurationMS, track.DiscNumber, track.TrackNumber); err != nil {
		return fmt.Errorf("upsert music track: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit music track upsert: %w", err)
	}
	return nil
}

// musicMediaFile is the one boundary between the music catalogue scanner and
// playback. Keep the complete normalized probe here: a partial copy can render
// album metadata successfully while every playback planner correctly rejects
// the same file as source_metadata_incomplete.
func musicMediaFile(
	folder *models.MediaFolder,
	albumID string,
	albumRoot string,
	track parsedMusicTrack,
	fileSize int64,
	modified time.Time,
) models.MediaFile {
	mediaFile := models.MediaFile{
		ContentID:          albumID,
		MediaFolderID:      folder.ID,
		CanonicalRootPath:  albumRoot,
		ObservedRootPath:   albumRoot,
		ContentGroupKey:    albumID,
		GroupKeyVersion:    1,
		BaseTitle:          track.Title,
		BaseYear:           track.Year,
		BaseType:           "music",
		IdentityConfidence: "high",
		FilePath:           track.Path,
		FileSize:           fileSize,
		FileModifiedAt:     &modified,
	}
	applyProbeData(&mediaFile, &track.Probe, "local")
	return mediaFile
}

func (s *Scanner) reconcileMissingMusic(ctx context.Context, folderID int, seen map[string]struct{}, protectedRoots []string) error {
	rows, err := s.fileRepo.Pool().Query(ctx, `SELECT id, file_path FROM media_files WHERE media_folder_id = $1 AND base_type = 'music' AND missing_since IS NULL`, folderID)
	if err != nil {
		return fmt.Errorf("list existing music files: %w", err)
	}
	type existingFile struct {
		id   int
		path string
	}
	existing := make([]existingFile, 0)
	for rows.Next() {
		var file existingFile
		if err := rows.Scan(&file.id, &file.path); err != nil {
			rows.Close()
			return fmt.Errorf("scan existing music file: %w", err)
		}
		existing = append(existing, file)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("iterate existing music files: %w", err)
	}
	for _, file := range existing {
		if _, ok := seen[file.path]; ok {
			continue
		}
		if pathWithinAnyRoot(file.path, protectedRoots) {
			continue
		}
		if err := s.fileRepo.MarkMissing(ctx, file.id, time.Now().UTC()); err != nil {
			return err
		}
	}
	deleteTracksQuery := `
		DELETE FROM music_tracks mt USING media_files mf
		WHERE mt.media_file_id = mf.id AND mf.media_folder_id = $1 AND mf.missing_since IS NOT NULL`
	deleteTracksArgs := []any{folderID}
	if clauses, clauseArgs := rootCoverageClauses(protectedRoots, len(deleteTracksArgs)+1); len(clauses) > 0 {
		deleteTracksQuery += " AND NOT (" + strings.Join(clauses, " OR ") + ")"
		deleteTracksArgs = append(deleteTracksArgs, clauseArgs...)
	}
	if _, err := s.fileRepo.Pool().Exec(ctx, deleteTracksQuery, deleteTracksArgs...); err != nil {
		return fmt.Errorf("delete missing music tracks: %w", err)
	}
	deleteAlbumsQuery := `
		DELETE FROM music_albums ma
		WHERE NOT EXISTS (
			SELECT 1 FROM music_tracks mt WHERE mt.album_id = ma.content_id
		)
		  AND EXISTS (
			SELECT 1 FROM media_files mf
			WHERE mf.media_folder_id = $1 AND mf.content_id = ma.content_id
		  )`
	deleteAlbumsArgs := []any{folderID}
	if clauses, clauseArgs := rootCoverageClauses(protectedRoots, len(deleteAlbumsArgs)+1); len(clauses) > 0 {
		deleteAlbumsQuery += `
		  AND NOT EXISTS (
			SELECT 1 FROM media_files mf
			WHERE mf.media_folder_id = $1 AND mf.content_id = ma.content_id
			  AND (` + strings.Join(clauses, " OR ") + `)
		  )`
		deleteAlbumsArgs = append(deleteAlbumsArgs, clauseArgs...)
	}
	if _, err := s.fileRepo.Pool().Exec(ctx, deleteAlbumsQuery, deleteAlbumsArgs...); err != nil {
		return fmt.Errorf("delete empty music albums: %w", err)
	}
	if s.libraryRepo != nil {
		if _, _, _, err := s.reconcileLibraryMemberships(ctx, folderID, protectedRoots); err != nil {
			return fmt.Errorf("reconcile music library membership: %w", err)
		}
	}
	return nil
}
