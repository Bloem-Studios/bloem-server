package scanner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
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

func stableMusicID(kind, value string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + strings.ToLower(strings.TrimSpace(value))))
	return fmt.Sprintf("music-%s-%x", kind, sum[:12])
}

// ScanMusicFolder indexes one album per directory containing supported audio
// files. Tags are authoritative when present; filesystem names are only
// fallbacks, and absent disc/track numbers remain unknown rather than guessed.
func (s *Scanner) ScanMusicFolder(ctx context.Context, folder *models.MediaFolder, fullScan bool) error {
	if s == nil || folder == nil {
		return errors.New("ScanMusicFolder: nil scanner or folder")
	}
	paths := make([]string, 0)
	for _, root := range folder.Paths {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path != root && ignoredDirNames[strings.ToLower(entry.Name())] {
					return filepath.SkipDir
				}
				return nil
			}
			if SupportsAudioFile(path) {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("walk music root %s: %w", root, err)
		}
	}
	sort.Strings(paths)
	if s.fileRepo == nil || s.itemRepo == nil {
		return nil
	}
	if len(paths) == 0 {
		if fullScan {
			return s.reconcileMissingMusic(ctx, folder.ID, map[string]struct{}{})
		}
		return nil
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
		if err := s.reconcileMissingMusic(ctx, folder.ID, seen); err != nil {
			return err
		}
	}
	return s.syncFolderScopedAudioLibraryState(ctx, folder.ID)
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
	artistID := stableMusicID("artist", track.AlbumArtist)
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
	mf, err := s.fileRepo.Upsert(ctx, models.MediaFile{
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
		FileSize:           info.Size(),
		FileModifiedAt:     &modified,
		CodecAudio:         track.Probe.CodecAudio,
		AudioChannels:      track.Probe.AudioChannels,
		Container:          track.Probe.Container,
		Duration:           track.Probe.Duration,
		Bitrate:            track.Probe.Bitrate,
		ProbeSource:        "local",
	})
	if err != nil {
		return fmt.Errorf("upsert music media file: %w", err)
	}
	trackID := stableMusicID("track", track.Path)
	if _, err := pool.Exec(ctx, `
		INSERT INTO music_tracks (id, album_id, artist_id, media_file_id, title, duration_ms, disc_number, track_number)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET album_id = EXCLUDED.album_id, artist_id = EXCLUDED.artist_id,
			media_file_id = EXCLUDED.media_file_id, title = EXCLUDED.title, duration_ms = EXCLUDED.duration_ms,
			disc_number = EXCLUDED.disc_number, track_number = EXCLUDED.track_number, updated_at = NOW()`,
		trackID, albumID, artistID, mf.ID, track.Title, track.DurationMS, track.DiscNumber, track.TrackNumber); err != nil {
		return fmt.Errorf("upsert music track: %w", err)
	}
	return nil
}

func (s *Scanner) reconcileMissingMusic(ctx context.Context, folderID int, seen map[string]struct{}) error {
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
		if err := s.fileRepo.MarkMissing(ctx, file.id, time.Now().UTC()); err != nil {
			return err
		}
	}
	if _, err := s.fileRepo.Pool().Exec(ctx, `
		DELETE FROM music_tracks mt USING media_files mf
		WHERE mt.media_file_id = mf.id AND mf.media_folder_id = $1 AND mf.missing_since IS NOT NULL`, folderID); err != nil {
		return fmt.Errorf("delete missing music tracks: %w", err)
	}
	if _, err := s.fileRepo.Pool().Exec(ctx, `DELETE FROM music_albums ma WHERE NOT EXISTS (SELECT 1 FROM music_tracks mt WHERE mt.album_id = ma.content_id)`); err != nil {
		return fmt.Errorf("delete empty music albums: %w", err)
	}
	if s.libraryRepo != nil {
		if _, _, _, err := s.reconcileLibraryMemberships(ctx, folderID, nil); err != nil {
			return fmt.Errorf("reconcile music library membership: %w", err)
		}
	}
	return nil
}
