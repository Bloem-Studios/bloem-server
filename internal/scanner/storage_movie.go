package scanner

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/contentid"
	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/naming"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/titleutil"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

// StorageMovie carries probe facts obtained from an already authorized pinned
// entry. The host coordinator publishes it only in the authorized page commit.
type StorageMovie struct {
	Entry *storagev1.Entry
	Probe *ProbeData
}

// NativeMovieScanConfigured reports startup composition, not binary or backend
// health. Each entry still requires a successful pinned probe and publication.
func (s *Scanner) NativeMovieScanConfigured() bool {
	return s != nil && s.fileRepo != nil && s.itemRepo != nil && strings.TrimSpace(s.ffprobePath) != ""
}

// NativeMovieDemuxer admits only the standalone container families reviewed by
// ProbeNative. A suffix is a selection hint; actual bytes must pass ffprobe and
// contain video. Remote URL shortcuts and other container families are skipped.
func NativeMovieDemuxer(name string) (NativeProbeDemuxer, bool) {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".mov", ".m4v":
		return NativeProbeMOV, true
	case ".mkv", ".webm":
		return NativeProbeMatroska, true
	default:
		return 0, false
	}
}

// ProbeStorageMovie uses only the scanner's configured trusted binary. Its
// file must come from ScanReader; ProbeNative retains the pinned byte/authority
// failure contract, and never persists its private bridge argument.
func (s *Scanner) ProbeStorageMovie(ctx context.Context, entry *storagev1.Entry, file mediasource.File, cfg NativeProbeConfig) (StorageMovie, error) {
	if s == nil || entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE {
		return StorageMovie{}, ErrNativeProbeInvalid
	}
	demuxer, ok := NativeMovieDemuxer(entry.GetName())
	if !ok {
		return StorageMovie{}, ErrNativeProbeInvalid
	}
	cfg.Demuxer = demuxer
	probe, err := ProbeNative(ctx, s.ffprobePath, file, cfg)
	if err != nil {
		return StorageMovie{}, err
	}
	if len(probe.VideoTracks) == 0 || probe.CodecVideo == "" {
		return StorageMovie{}, ErrNativeProbeInvalid
	}
	return StorageMovie{Entry: proto.CloneOf(entry), Probe: probe}, nil
}

// PublishStorageMoviesTx publishes an already probed page inside the journal's
// authorized transaction. Catalog keys identify entries, never local paths or
// stream URLs. Revisions update the same file, retaining matched item metadata.
// The calling StorageScanner and resource authority both hold movie admission
// by default until the host's startup readiness is explicitly enabled.
func (s *Scanner) PublishStorageMoviesTx(ctx context.Context, tx pgx.Tx, folder *models.MediaFolder, location storagesource.Location, configurationRevision int64, movies []StorageMovie) (StoragePublishResult, error) {
	var result StoragePublishResult
	if s == nil || s.itemRepo == nil || s.fileRepo == nil || tx == nil || folder == nil ||
		folder.ID != location.FolderID || !folder.Enabled || !librarykind.IsMovie(folder.Type) || len(folder.Paths) != 0 || configurationRevision <= 0 {
		return result, fmt.Errorf("storage movie publication is not configured")
	}
	if len(movies) == 0 {
		return result, nil
	}
	paths := make([]string, len(movies))
	seen := make(map[string]bool, len(movies))
	for i, movie := range movies {
		if movie.Entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE || movie.Entry.GetRevision() == "" || movie.Probe == nil || len(movie.Probe.VideoTracks) == 0 || movie.Probe.CodecVideo == "" {
			return result, ErrNativeProbeInvalid
		}
		key, err := storagesource.CatalogLocation(location.ID, movie.Entry.GetId())
		if err != nil {
			return result, err
		}
		if seen[key] {
			return result, storagesource.ErrReferenceConflict
		}
		seen[key], paths[i] = true, key
	}
	type existingMovie struct{ contentID, revision, kind string }
	existing := make(map[string]existingMovie, len(movies))
	rows, err := tx.Query(ctx, `SELECT f.file_path,COALESCE(f.content_id,''),COALESCE(r.revision,''),COALESCE(i.type,'')
		FROM media_files f LEFT JOIN bloem_storage_file_refs r ON r.media_file_id=f.id LEFT JOIN media_items i ON i.content_id=f.content_id
		WHERE f.media_folder_id=$1 AND f.file_path=ANY($2)`, folder.ID, paths)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var key string
		var movie existingMovie
		if err := rows.Scan(&key, &movie.contentID, &movie.revision, &movie.kind); err != nil {
			rows.Close()
			return result, err
		}
		if movie.contentID != "" && movie.kind != "movie" {
			rows.Close()
			return result, storagesource.ErrReferenceConflict
		}
		existing[key] = movie
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}
	files := make([]models.MediaFile, len(movies))
	var items []*models.MediaItem
	contentIDs := make([]string, len(movies))
	for i, movie := range movies {
		old, ok := existing[paths[i]]
		id := old.contentID
		if id == "" {
			id = contentid.ForLocal(paths[i])
			hints := naming.ParseFilename(movie.Entry.GetName(), "movies")
			title := strings.TrimSpace(hints.Title)
			if title == "" {
				title = strings.TrimSuffix(movie.Entry.GetName(), path.Ext(movie.Entry.GetName()))
			}
			items = append(items, &models.MediaItem{ContentID: id, Type: "movie", Title: title, SortTitle: titleutil.DeriveDefaultSortTitle(title), Year: hints.Year, Runtime: (movie.Probe.Duration + 59) / 60, Status: "pending"})
		}
		switch {
		case !ok:
			result.New++
		case old.revision != movie.Entry.GetRevision():
			result.Updated++
		default:
			result.Unchanged++
		}
		contentIDs[i] = id
		modified := normalizeFileModifiedAt(time.Unix(0, movie.Entry.GetModifiedUnixNano()))
		files[i] = models.MediaFile{ContentID: id, MediaFolderID: folder.ID, FilePath: paths[i], FileSize: movie.Entry.GetSize(), FileModifiedAt: &modified, BaseType: "movie"}
		applyProbeData(&files[i], movie.Probe, "storage")
	}
	if err := s.itemRepo.UpsertBatchTx(ctx, tx, items); err != nil {
		return result, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id,first_seen_at)
		SELECT id,$2,NOW() FROM unnest($1::text[]) AS t(id) ON CONFLICT(content_id,media_folder_id) DO NOTHING`, contentIDs, folder.ID); err != nil {
		return result, err
	}
	if err := s.fileRepo.UpsertBatchTx(ctx, tx, files); err != nil {
		return result, err
	}
	fileIDs := make(map[string]int, len(movies))
	rows, err = tx.Query(ctx, `SELECT id,file_path FROM media_files WHERE media_folder_id=$1 AND file_path=ANY($2)`, folder.ID, paths)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id int
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return result, err
		}
		fileIDs[key] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}
	refs := make([]storagesource.FileRef, len(movies))
	for i, movie := range movies {
		refs[i] = storagesource.FileRef{MediaFileID: fileIDs[paths[i]], PersistedRef: storagesource.PersistedRef{LocationID: location.ID, EntryID: movie.Entry.GetId(), Revision: movie.Entry.GetRevision(), LogicalPath: movie.Entry.GetLogicalPath()}}
	}
	if err := storagesource.AttachFilesTx(ctx, tx, configurationRevision, refs); err != nil {
		return result, err
	}
	return result, nil
}
