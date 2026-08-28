package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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

// stableMusicTrackID deliberately preserves path case: a case-sensitive
// filesystem may hold Track.flac and track.flac as two distinct files.
// Scoping the cleaned, album-relative path by folder and the root-resolved
// album ID keeps those physical identities deterministic without coupling
// them to an absolute library location.
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
	var reconcileCandidates []musicReconcileCandidate
	if fullScan {
		var err error
		rootObservation, err = s.ObserveRoots(ctx, folder.ID, folder.Paths)
		if err != nil {
			return err
		}
		reconcileCandidates, err = s.snapshotMusicReconcileCandidates(ctx, folder.ID)
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
		albumID, err = s.upsertMusicTrack(ctx, folder, albumID, albumRoot, track)
		if err != nil {
			return err
		}
		albumIDs[albumRoot] = albumID
		seen[path] = struct{}{}
	}
	if fullScan {
		protectedRoots := append([]string(nil), rootObservation.UnreachableRoots...)
		protectedRoots = append(protectedRoots, rootObservation.SuspectEmptyRoots...)
		for _, failedPath := range walkFailures {
			protectedRoots = appendUniquePath(protectedRoots, failedPath)
		}
		if !allConfiguredRootsUnreachable(rootObservation.ConfiguredRoots, rootObservation.UnreachableRoots) {
			if err := s.reconcileMissingMusic(ctx, folder.ID, reconcileCandidates, seen, protectedRoots); err != nil {
				return err
			}
		}
	}
	if err := s.syncMusicFolderLibraryState(ctx, folder.ID); err != nil {
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

func findOrCreateMusicAlbumIDTx(ctx context.Context, tx pgx.Tx, folderID int, albumRoot string) (string, error) {
	var contentID string
	err := tx.QueryRow(ctx, `
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

func musicAlbumRootLockKey(folderID int, albumRoot string) int64 {
	identity := fmt.Sprintf("bloem:music-album-root\x00%d\x00%s", folderID, filepath.Clean(albumRoot))
	sum := sha256.Sum256([]byte(identity))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func musicFolderMutationLockKey(folderID int) int64 {
	identity := fmt.Sprintf("bloem:music-folder-mutation:v1\x00%d", folderID)
	sum := sha256.Sum256([]byte(identity))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// Music mutations use a folder-scoped advisory read/write lock across every
// server process. Ingest takes the shared mode so independent albums can be
// written concurrently; cleanup and state restoration take the exclusive mode
// because their set-oriented statements use a different row-lock order.
// Filesystem walking and probing happen before this lock. Vanished-file cleanup
// performs only its required exact-path stat recheck while holding it. The
// narrower album-root lock is always acquired after the folder lock, giving
// every music mutation one deadlock-safe order.
func lockMusicFolderMutationSharedTx(ctx context.Context, tx pgx.Tx, folderID int) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, musicFolderMutationLockKey(folderID)); err != nil {
		return fmt.Errorf("lock music folder mutation in shared mode: %w", err)
	}
	return nil
}

func lockMusicFolderMutationExclusiveTx(ctx context.Context, tx pgx.Tx, folderID int) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, musicFolderMutationLockKey(folderID)); err != nil {
		return fmt.Errorf("lock music folder mutation exclusively: %w", err)
	}
	return nil
}

func (s *Scanner) upsertMusicTrack(
	ctx context.Context,
	folder *models.MediaFolder,
	albumID string,
	albumRoot string,
	track parsedMusicTrack,
) (string, error) {
	artistID := stableMusicSemanticID("artist", track.AlbumArtist)
	info, err := os.Stat(track.Path)
	if err != nil {
		return "", fmt.Errorf("stat music file: %w", err)
	}
	modified := normalizeFileModifiedAt(info.ModTime())

	pool := s.fileRepo.Pool()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin music track upsert: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := lockMusicFolderMutationSharedTx(ctx, tx, folder.ID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, musicAlbumRootLockKey(folder.ID, albumRoot)); err != nil {
		return "", fmt.Errorf("lock music album root: %w", err)
	}
	if albumID == "" {
		albumID, err = findOrCreateMusicAlbumIDTx(ctx, tx, folder.ID, albumRoot)
		if err != nil {
			return "", err
		}
	}

	item := &models.MediaItem{
		ContentID: albumID,
		Type:      "music_album",
		Title:     track.Album,
		SortTitle: track.Album,
		Year:      track.Year,
		Runtime:   int(track.DurationMS / 60000),
		Status:    "matched",
	}
	mediaFile := musicMediaFile(folder, albumID, albumRoot, track, info.Size(), modified)
	trackID := stableMusicTrackID(folder.ID, albumID, albumRoot, track.Path)
	legacyTrackID := legacyMusicTrackID(track.Path)

	if err := s.itemRepo.UpsertTx(ctx, tx, item); err != nil {
		return "", fmt.Errorf("upsert music album item: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO music_artists (id, name, sort_name)
		VALUES ($1, $2, $2)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, sort_name = EXCLUDED.sort_name, updated_at = NOW()`, artistID, track.AlbumArtist); err != nil {
		return "", fmt.Errorf("upsert music artist: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO music_albums (content_id, artist_id, year)
		VALUES ($1, $2, NULLIF($3, 0))
		ON CONFLICT (content_id) DO UPDATE SET artist_id = EXCLUDED.artist_id,
			year = COALESCE(EXCLUDED.year, music_albums.year), updated_at = NOW()`, albumID, artistID, track.Year); err != nil {
		return "", fmt.Errorf("upsert music album: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO media_item_libraries (content_id, media_folder_id, first_seen_at)
		VALUES ($1, $2, NOW()) ON CONFLICT (content_id, media_folder_id) DO NOTHING`, albumID, folder.ID); err != nil {
		return "", fmt.Errorf("upsert music library membership: %w", err)
	}
	mf, err := s.fileRepo.UpsertTx(ctx, tx, mediaFile)
	if err != nil {
		return "", fmt.Errorf("upsert music media file: %w", err)
	}
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
			return "", fmt.Errorf("reconcile legacy music track identity: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO music_tracks (id, album_id, artist_id, media_file_id, title, duration_ms, disc_number, track_number)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET album_id = EXCLUDED.album_id, artist_id = EXCLUDED.artist_id,
			media_file_id = EXCLUDED.media_file_id, title = EXCLUDED.title, duration_ms = EXCLUDED.duration_ms,
			disc_number = EXCLUDED.disc_number, track_number = EXCLUDED.track_number, updated_at = NOW()`,
		trackID, albumID, artistID, mf.ID, track.Title, track.DurationMS, track.DiscNumber, track.TrackNumber); err != nil {
		return "", fmt.Errorf("upsert music track: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit music track upsert: %w", err)
	}
	return albumID, nil
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

type musicReconcileCandidate struct {
	id         int
	path       string
	version    string
	wasMissing bool
}

// snapshotMusicReconcileCandidates captures the only database rows a full
// scan may treat as absent. It runs before the filesystem walk: rows created
// later were never part of that walk's world-view, while xmin lets the
// reconcile pass detect candidates refreshed by a concurrent scoped scan.
func (s *Scanner) snapshotMusicReconcileCandidates(ctx context.Context, folderID int) ([]musicReconcileCandidate, error) {
	rows, err := s.fileRepo.Pool().Query(ctx, `
		SELECT id, file_path, xmin::text, missing_since IS NOT NULL
		FROM media_files
		WHERE media_folder_id = $1 AND base_type = 'music'
	`, folderID)
	if err != nil {
		return nil, fmt.Errorf("snapshot existing music files: %w", err)
	}
	defer rows.Close()
	candidates := make([]musicReconcileCandidate, 0)
	for rows.Next() {
		var candidate musicReconcileCandidate
		if err := rows.Scan(&candidate.id, &candidate.path, &candidate.version, &candidate.wasMissing); err != nil {
			return nil, fmt.Errorf("scan existing music candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate existing music candidates: %w", err)
	}
	return candidates, nil
}

func (s *Scanner) reconcileMissingMusic(
	ctx context.Context,
	folderID int,
	candidates []musicReconcileCandidate,
	seen map[string]struct{},
	protectedRoots []string,
) error {
	absentCandidates := make([]musicReconcileCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate.path]; ok {
			continue
		}
		if pathWithinAnyRoot(candidate.path, protectedRoots) {
			continue
		}
		absentCandidates = append(absentCandidates, candidate)
	}

	tx, err := s.fileRepo.Pool().Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin music missing reconciliation: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := lockMusicFolderMutationExclusiveTx(ctx, tx, folderID); err != nil {
		return err
	}

	cleanupFileIDs := make([]int, 0, len(absentCandidates))
	missingAt := time.Now().UTC()
	for _, candidate := range absentCandidates {
		var id int
		if candidate.wasMissing {
			err = tx.QueryRow(ctx, `
				SELECT id
				FROM media_files
				WHERE id = $1
				  AND media_folder_id = $2
				  AND file_path = $3
				  AND base_type = 'music'
				  AND xmin = ($4::text)::xid
				  AND missing_since IS NOT NULL
				FOR UPDATE
			`, candidate.id, folderID, candidate.path, candidate.version).Scan(&id)
		} else {
			err = tx.QueryRow(ctx, `
				UPDATE media_files
				SET missing_since = $1, updated_at = NOW()
				WHERE id = $2
				  AND media_folder_id = $3
				  AND file_path = $4
				  AND base_type = 'music'
				  AND xmin = ($5::text)::xid
				  AND missing_since IS NULL
				RETURNING id
			`, missingAt, candidate.id, folderID, candidate.path, candidate.version).Scan(&id)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("verify missing music candidate %d: %w", candidate.id, err)
		}
		cleanupFileIDs = append(cleanupFileIDs, id)
	}
	if len(cleanupFileIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			DELETE FROM music_tracks mt USING media_files mf
			WHERE mt.media_file_id = mf.id
			  AND mf.id = ANY($1::int[])
			  AND mf.missing_since IS NOT NULL
		`, cleanupFileIDs); err != nil {
			return fmt.Errorf("delete missing music tracks: %w", err)
		}
	}
	if s.libraryRepo != nil {
		if _, _, _, err := s.libraryRepo.ReconcileFolderMembershipTx(ctx, tx, folderID, protectedRoots); err != nil {
			return fmt.Errorf("reconcile music library membership: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit music missing reconciliation: %w", err)
	}
	return nil
}

func (s *Scanner) syncMusicFolderLibraryState(ctx context.Context, folderID int) error {
	tx, err := s.fileRepo.Pool().Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin music folder state repair: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := lockMusicFolderMutationExclusiveTx(ctx, tx, folderID); err != nil {
		return err
	}
	if err := s.syncFolderScopedAudioLibraryStateTx(ctx, tx, folderID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit music folder state repair: %w", err)
	}
	return nil
}
