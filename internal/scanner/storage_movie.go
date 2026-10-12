package scanner

import (
	"context"
	"fmt"
	"path"
	"strconv"
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

// StorageMovieMatch is a published movie item still awaiting a provider match:
// its path identity and the provider IDs its folder or file name declares.
type StorageMovieMatch struct {
	ContentID      string
	Title          string
	Year           int
	TmdbID, ImdbID string
	Alternates     []StorageMovieTitle
}

// StorageMoviePublishResult counts one movie page and lists the items its
// commit leaves for provider matching.
type StorageMoviePublishResult struct {
	StoragePublishResult
	Matches []StorageMovieMatch
}

// PublishStorageMoviesTx publishes an already probed page inside the journal's
// authorized transaction. Catalog keys identify entries, never local paths or
// stream URLs. Revisions update the same file, retaining matched item metadata.
// Titles, years, provider IDs, editions and content groups come from Silo's
// naming rules applied to each entry's logical path. A new file joins the item
// of its content group (editions and copies of one title), otherwise it gets a
// pending item. The calling StorageScanner and resource authority both hold
// movie admission by default until the host's startup readiness is enabled.
func (s *Scanner) PublishStorageMoviesTx(ctx context.Context, tx pgx.Tx, folder *models.MediaFolder, location storagesource.Location, configurationRevision int64, movies []StorageMovie) (StorageMoviePublishResult, error) {
	var result StorageMoviePublishResult
	if s == nil || s.itemRepo == nil || s.fileRepo == nil || tx == nil || folder == nil ||
		folder.ID != location.FolderID || !folder.Enabled || !librarykind.IsMovie(folder.Type) || len(folder.Paths) != 0 || configurationRevision <= 0 {
		return result, fmt.Errorf("storage movie publication is not configured")
	}
	if len(movies) == 0 {
		return result, nil
	}
	paths := make([]string, len(movies))
	entries := make([]*storagev1.Entry, len(movies))
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
		seen[key], paths[i], entries[i] = true, key, movie.Entry
	}
	identities := storageMovieIdentities(entries)
	existing := make(map[string]storageExistingMovie, len(movies))
	rows, err := tx.Query(ctx, `SELECT f.file_path,COALESCE(f.content_id,''),COALESCE(r.revision,''),COALESCE(i.type,''),COALESCE(i.status,'')
		FROM media_files f LEFT JOIN bloem_storage_file_refs r ON r.media_file_id=f.id LEFT JOIN media_items i ON i.content_id=f.content_id
		WHERE f.media_folder_id=$1 AND f.file_path=ANY($2)`, folder.ID, paths)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var key string
		var movie storageExistingMovie
		if err := rows.Scan(&key, &movie.contentID, &movie.revision, &movie.kind, &movie.status); err != nil {
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
	groups, err := storageMovieGroupItemsTx(ctx, tx, folder.ID, identities, paths, existing)
	if err != nil {
		return result, err
	}
	files := make([]models.MediaFile, len(movies))
	var items []*models.MediaItem
	contentIDs := make([]string, len(movies))
	statuses := make([]string, len(movies))
	for i, movie := range movies {
		old, ok := existing[paths[i]]
		id, status := old.contentID, old.status
		identity := identities[i]
		groupKey := storageGroupLookupKey(identity)
		if id == "" {
			if group, found := groups[groupKey]; found && groupKey != "" {
				id, status = group.contentID, group.status
			} else {
				id, status = contentid.ForLocal(paths[i]), "pending"
				if identity.ambiguous {
					status = "ambiguous"
				}
				items = append(items, &models.MediaItem{ContentID: id, Type: "movie", Title: identity.title, SortTitle: titleutil.DeriveDefaultSortTitle(identity.title), Year: identity.year, Runtime: (movie.Probe.Duration + 59) / 60, Status: status})
				if groupKey != "" {
					groups[groupKey] = storageMovieGroup{contentID: id, status: status}
				}
			}
		}
		switch {
		case !ok:
			result.New++
		case old.revision != movie.Entry.GetRevision():
			result.Updated++
		default:
			result.Unchanged++
		}
		contentIDs[i], statuses[i] = id, status
		modified := normalizeFileModifiedAt(time.Unix(0, movie.Entry.GetModifiedUnixNano()))
		files[i] = models.MediaFile{ContentID: id, MediaFolderID: folder.ID, FilePath: paths[i], FileSize: movie.Entry.GetSize(), FileModifiedAt: &modified, BaseType: "movie",
			BaseTitle: identity.title, BaseYear: identity.year, ContentGroupKey: identity.groupKey, GroupKeyVersion: identity.groupKeyVersion, IdentityConfidence: identity.confidence}
		applyStorageMovieVariant(&files[i], identity.variant)
		applyProbeData(&files[i], movie.Probe, "storage")
	}
	applyStorageMoviePartTotals(files)
	result.Matches = storageMovieMatches(identities, contentIDs, statuses)
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

type storageExistingMovie struct{ contentID, revision, kind, status string }

type storageMovieGroup struct{ contentID, status string }

func storageGroupLookupKey(identity storageMovieIdentity) string {
	if identity.groupKey == "" {
		return ""
	}
	return strconv.Itoa(identity.groupKeyVersion) + "|" + identity.groupKey
}

// storageMovieGroupItemsTx finds the movie item each new file's content group
// already has in this library: another edition or copy of the same title,
// published from another folder or an earlier page. A matched item wins.
func storageMovieGroupItemsTx(ctx context.Context, tx pgx.Tx, folderID int, identities []storageMovieIdentity, paths []string, existing map[string]storageExistingMovie) (map[string]storageMovieGroup, error) {
	groups := make(map[string]storageMovieGroup)
	var versions []int
	var keys []string
	for i, identity := range identities {
		if existing[paths[i]].contentID != "" || identity.groupKey == "" {
			continue
		}
		versions, keys = append(versions, identity.groupKeyVersion), append(keys, identity.groupKey)
	}
	if len(keys) == 0 {
		return groups, nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (f.group_key_version,f.content_group_key) f.group_key_version,f.content_group_key,f.content_id,COALESCE(i.status,'')
		FROM unnest($2::int[],$3::text[]) AS g(version,key)
		JOIN media_files f ON f.media_folder_id=$1 AND f.group_key_version=g.version AND f.content_group_key=g.key
		JOIN media_items i ON i.content_id=f.content_id
		WHERE f.missing_since IS NULL AND f.extra_id IS NULL AND i.type='movie'
		ORDER BY f.group_key_version,f.content_group_key,(i.status='matched') DESC,f.id`, folderID, versions, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var version int
		var key string
		var group storageMovieGroup
		if err := rows.Scan(&version, &key, &group.contentID, &group.status); err != nil {
			return nil, err
		}
		groups[strconv.Itoa(version)+"|"+key] = group
	}
	return groups, rows.Err()
}

// applyStorageMovieVariant records Silo's edition and presentation hints.
func applyStorageMovieVariant(file *models.MediaFile, hints *naming.VariantHints) {
	if hints == nil {
		return
	}
	file.EditionRaw, file.EditionKey, file.EditionConfidence, file.EditionSource = hints.EditionRaw, hints.EditionKey, hints.EditionConfidence, hints.EditionSource
	file.PresentationKind, file.PresentationGroupKey, file.PresentationPartIndex = hints.PresentationKind, hints.PresentationGroupKey, hints.PresentationPartIndex
}

// applyStorageMoviePartTotals mirrors the scanner's variant finalization for
// one page: parts of a split movie count only when at least two part numbers
// of one item, edition and presentation group are present.
func applyStorageMoviePartTotals(files []models.MediaFile) {
	parts := make(map[string]map[int]bool)
	partKey := func(file models.MediaFile) (string, bool) {
		if file.PresentationKind != "multipart_movie" || file.PresentationGroupKey == "" || file.PresentationPartIndex <= 0 {
			return "", false
		}
		return file.ContentID + "|" + file.EditionKey + "|" + file.PresentationGroupKey, true
	}
	for _, file := range files {
		if key, ok := partKey(file); ok {
			if parts[key] == nil {
				parts[key] = make(map[int]bool)
			}
			parts[key][file.PresentationPartIndex] = true
		}
	}
	for i := range files {
		key, ok := partKey(files[i])
		if !ok || len(parts[key]) < 2 {
			continue
		}
		for index := range parts[key] {
			files[i].PresentationPartTotal = max(files[i].PresentationPartTotal, index)
		}
	}
}

// storageMovieMatches lists each published item that still needs a provider
// match: a pending item, or an unmatched one whose names declare provider IDs
// (an earlier attempt can have failed transiently). Ambiguous and matched
// items are left to an administrator and to refresh.
func storageMovieMatches(identities []storageMovieIdentity, contentIDs, statuses []string) []StorageMovieMatch {
	var matches []StorageMovieMatch
	seen := make(map[string]bool, len(contentIDs))
	for i, id := range contentIDs {
		identity := identities[i]
		tagged := identity.tmdbID != "" || identity.imdbID != ""
		retry := statuses[i] == "pending" || (statuses[i] == "unmatched" && tagged)
		if seen[id] || !retry {
			continue
		}
		seen[id] = true
		matches = append(matches, StorageMovieMatch{ContentID: id, Title: identity.title, Year: identity.year,
			TmdbID: identity.tmdbID, ImdbID: identity.imdbID, Alternates: identity.alternates})
	}
	return matches
}

// MarkStorageMovieUnmatched records that a native movie's automatic match
// found nothing, as the queued matcher does for path-based files. A matched
// item is never demoted.
func (s *Scanner) MarkStorageMovieUnmatched(ctx context.Context, contentID string) error {
	if s == nil || s.itemRepo == nil {
		return fmt.Errorf("storage movie item repository is not configured")
	}
	_, err := s.itemRepo.SetStatusUnlessMatched(ctx, contentID, "unmatched")
	return err
}
