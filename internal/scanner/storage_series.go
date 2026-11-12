package scanner

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/contentid"
	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/titleutil"
)

// StorageSeriesPage is one listed page of a TV library, already probed.
type StorageSeriesPage struct {
	Folder                *models.MediaFolder
	Location              storagesource.Location
	ConfigurationRevision int64
	// Roots are the library's naming roots (StorageSeriesParseRoots).
	Roots []string
	// Episodes are probed or unchanged (UnprobedStorageVideo) episode files.
	Episodes []StorageMovie
	// Sidecars are the subtitles to attach, keyed by the entry ID of their
	// episode's video. The video may have been published on an earlier page.
	Sidecars map[string][]StorageSeriesSidecar
}

// StorageSeriesMatch is a published series still awaiting a provider match.
type StorageSeriesMatch struct {
	ContentID              string
	Title                  string
	Year                   int
	TvdbID, TmdbID, ImdbID string
}

// StorageSeriesPublishResult counts one page and lists what its commit leaves
// to do: series to match, matched series whose new files need linking to the
// provider's episodes, and unmatched series whose new files need fallback
// episodes.
type StorageSeriesPublishResult struct {
	StoragePublishResult
	Matches  []StorageSeriesMatch
	Link     []string
	Fallback []string
}

// PublishStorageSeriesTx publishes an already probed TV page inside the
// journal's authorized transaction, as PublishStorageMoviesTx does for
// movies. Each show is one series item: a new file joins the item its content
// group (the show folder, anchored by its {tvdb-} tag) already has in this
// library, from an earlier page or season, otherwise it gets a pending item.
// Files keep Silo's persisted season and episode numbers, so Silo's bulk
// episode linking reads them instead of the opaque catalog key; specials keep
// season 0. Native files never enter the path-based series match queue.
//
//nolint:gocyclo,funlen // one page's publication steps, in order
func (s *Scanner) PublishStorageSeriesTx(ctx context.Context, tx pgx.Tx, page StorageSeriesPage) (StorageSeriesPublishResult, error) {
	var result StorageSeriesPublishResult
	folder, location := page.Folder, page.Location
	if s == nil || s.itemRepo == nil || s.fileRepo == nil || tx == nil || folder == nil ||
		folder.ID != location.FolderID || !folder.Enabled || !librarykind.IsTV(folder.Type) || page.ConfigurationRevision <= 0 {
		return result, errors.New("storage series publication is not configured")
	}
	episodes := page.Episodes
	paths := make([]string, len(episodes))
	entries := make([]*storagev1.Entry, len(episodes))
	seen := make(map[string]bool, len(episodes))
	for i, episode := range episodes {
		if episode.Entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE || episode.Entry.GetRevision() == "" || episode.Probe != nil && (len(episode.Probe.VideoTracks) == 0 || episode.Probe.CodecVideo == "") {
			return result, ErrNativeProbeInvalid
		}
		key, err := storagesource.CatalogLocation(location.ID, episode.Entry.GetId())
		if err != nil {
			return result, err
		}
		if seen[key] {
			return result, storagesource.ErrReferenceConflict
		}
		seen[key], paths[i], entries[i] = true, key, episode.Entry
	}
	if err := requireStorageVideosUnchangedTx(ctx, tx, folder.ID, location, page.ConfigurationRevision, episodes); err != nil {
		return result, err
	}
	identities := storageSeriesIdentities(entries, page.Roots)
	for _, identity := range identities {
		if !identity.episodic {
			return result, ErrNativeProbeInvalid
		}
	}
	existing, err := storageExistingFilesTx(ctx, tx, folder.ID, paths, "series")
	if err != nil {
		return result, err
	}
	keys := make([]storageGroupKey, len(identities))
	for i, identity := range identities {
		keys[i] = storageGroupKey{identity.groupKeyVersion, identity.groupKey}
	}
	groups, err := storageGroupItemsTx(ctx, tx, folder.ID, "series", keys, paths, existing)
	if err != nil {
		return result, err
	}
	var items []*models.MediaItem
	var files []models.MediaFile
	var written []int
	var specials []string
	contentIDs := make([]string, len(episodes))
	statuses := make([]string, len(episodes))
	for i, episode := range episodes {
		old, ok := existing[paths[i]]
		id, status := old.contentID, old.status
		identity := identities[i]
		if id == "" {
			if episode.Probe == nil {
				return result, ErrStorageVideoChanged
			}
			var item *models.MediaItem
			id, status, item = storageSeriesItem(groups, identity, paths[i])
			if item != nil {
				items = append(items, item)
			}
		}
		contentIDs[i], statuses[i] = id, status
		switch {
		case !ok:
			result.New++
		case old.revision != episode.Entry.GetRevision():
			result.Updated++
		default:
			result.Unchanged++
		}
		if episode.Probe == nil {
			continue
		}
		modified := normalizeFileModifiedAt(time.Unix(0, episode.Entry.GetModifiedUnixNano()))
		file := models.MediaFile{ContentID: id, MediaFolderID: folder.ID, FilePath: paths[i], FileSize: episode.Entry.GetSize(), FileModifiedAt: &modified, BaseType: "series",
			BaseTitle: identity.title, BaseYear: identity.year, ContentGroupKey: identity.groupKey, GroupKeyVersion: identity.groupKeyVersion, IdentityConfidence: identity.confidence,
			SeasonNumber: identity.season, EpisodeNumber: identity.episode, MultiEpisodeStart: identity.multiStart, MultiEpisodeEnd: identity.multiEnd}
		applyProbeData(&file, episode.Probe, "storage")
		files = append(files, file)
		written = append(written, i)
		if identity.seasonKnown && identity.season == 0 {
			specials = append(specials, paths[i])
		}
		tagged := identity.tvdbID != "" || identity.tmdbID != "" || identity.imdbID != ""
		switch {
		case status == "matched" && !slices.Contains(result.Link, id):
			result.Link = append(result.Link, id)
		case status == "unmatched" && !tagged && !slices.Contains(result.Fallback, id):
			result.Fallback = append(result.Fallback, id)
		}
	}
	result.Matches = storageSeriesMatches(identities, contentIDs, statuses)
	if err := s.itemRepo.UpsertBatchTx(ctx, tx, items); err != nil {
		return result, err
	}
	if err := s.writeStorageSeriesFilesTx(ctx, tx, folder.ID, storagePick(contentIDs, written), files, specials); err != nil {
		return result, err
	}
	fileIDs, err := storageFileIDsTx(ctx, tx, folder.ID, paths)
	if err != nil {
		return result, err
	}
	if len(written) > 0 {
		refs := make([]storagesource.FileRef, 0, len(written))
		for _, i := range written {
			e := episodes[i].Entry
			refs = append(refs, storagesource.FileRef{MediaFileID: fileIDs[paths[i]], PersistedRef: storagesource.PersistedRef{LocationID: location.ID, EntryID: e.GetId(), Revision: e.GetRevision(), LogicalPath: e.GetLogicalPath()}, EntrySHA256: storageVideoDigest(e)})
		}
		if err := storagesource.AttachFilesTx(ctx, tx, page.ConfigurationRevision, refs); err != nil {
			return result, err
		}
	}
	return result, attachStorageSeriesSidecarsTx(ctx, tx, folder.ID, location, page.Sidecars, fileIDs)
}

// storageSeriesItem returns the series item a new episode file joins: its
// content group's item in this library, or a new pending (or ambiguous) item
// it creates, which later files of the group join.
func storageSeriesItem(groups map[string]storageMovieGroup, identity storageSeriesIdentity, path string) (string, string, *models.MediaItem) {
	groupKey := storageGroupLookupKey(storageMovieIdentity{groupKey: identity.groupKey, groupKeyVersion: identity.groupKeyVersion})
	if group, found := groups[groupKey]; found && groupKey != "" {
		return group.contentID, group.status, nil
	}
	id, status := contentid.ForLocal(path), "pending"
	if identity.ambiguous {
		status = "ambiguous"
	}
	if groupKey != "" {
		groups[groupKey] = storageMovieGroup{contentID: id, status: status}
	}
	return id, status, &models.MediaItem{ContentID: id, Type: "series", Title: identity.title, SortTitle: titleutil.DeriveDefaultSortTitle(identity.title), Year: identity.year, Status: status}
}

// writeStorageSeriesFilesTx writes probed episode files and their series
// memberships. The shared upsert stores season 0 as unknown (NULL), and Silo's
// bulk linker skips files without a season, so specials are set back to 0;
// later upserts preserve it.
func (s *Scanner) writeStorageSeriesFilesTx(ctx context.Context, tx pgx.Tx, folderID int, contentIDs []string, files []models.MediaFile, specials []string) error {
	if len(files) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id,first_seen_at)
		SELECT id,$2,NOW() FROM unnest($1::text[]) AS t(id) ON CONFLICT(content_id,media_folder_id) DO NOTHING`, contentIDs, folderID); err != nil {
		return err
	}
	if err := s.fileRepo.UpsertBatchTx(ctx, tx, files); err != nil {
		return err
	}
	if len(specials) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE media_files SET season_number=0 WHERE media_folder_id=$1 AND file_path=ANY($2) AND season_number IS NULL AND extra_id IS NULL`, folderID, specials)
	return err
}

// storageExistingFilesTx reads the published files at paths and refuses one
// that belongs to an item of another type.
func storageExistingFilesTx(ctx context.Context, tx pgx.Tx, folderID int, paths []string, itemType string) (map[string]storageExistingMovie, error) {
	existing := make(map[string]storageExistingMovie, len(paths))
	rows, err := tx.Query(ctx, `SELECT f.file_path,COALESCE(f.content_id,''),COALESCE(r.revision,''),COALESCE(i.type,''),COALESCE(i.status,'')
		FROM media_files f LEFT JOIN bloem_storage_file_refs r ON r.media_file_id=f.id LEFT JOIN media_items i ON i.content_id=f.content_id
		WHERE f.media_folder_id=$1 AND f.file_path=ANY($2)`, folderID, paths)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var file storageExistingMovie
		if err := rows.Scan(&key, &file.contentID, &file.revision, &file.kind, &file.status); err != nil {
			return nil, err
		}
		if file.contentID != "" && file.kind != itemType {
			return nil, storagesource.ErrReferenceConflict
		}
		existing[key] = file
	}
	return existing, rows.Err()
}

func storageFileIDsTx(ctx context.Context, tx pgx.Tx, folderID int, paths []string) (map[string]int, error) {
	ids := make(map[string]int, len(paths))
	if len(paths) == 0 {
		return ids, nil
	}
	rows, err := tx.Query(ctx, `SELECT id,file_path FROM media_files WHERE media_folder_id=$1 AND file_path=ANY($2)`, folderID, paths)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, err
		}
		ids[key] = id
	}
	return ids, rows.Err()
}

// attachStorageSeriesSidecarsTx records each sidecar against its episode's
// file. A sidecar whose video was published on an earlier page is resolved
// through that video's catalog key. A sidecar keeps its entry: listing it
// again beside another video moves it, and a new revision replaces the old.
func attachStorageSeriesSidecarsTx(ctx context.Context, tx pgx.Tx, folderID int, location storagesource.Location, sidecars map[string][]StorageSeriesSidecar, known map[string]int) error {
	if len(sidecars) == 0 {
		return nil
	}
	var lookup []string
	videoPaths := make(map[string]string, len(sidecars))
	for videoID := range sidecars {
		key, err := storagesource.CatalogLocation(location.ID, videoID)
		if err != nil {
			return err
		}
		videoPaths[videoID] = key
		if known[key] == 0 {
			lookup = append(lookup, key)
		}
	}
	if len(lookup) > 0 {
		more, err := storageFileIDsTx(ctx, tx, folderID, lookup)
		if err != nil {
			return err
		}
		for k, v := range more {
			known[k] = v
		}
	}
	var fileIDs []int
	var entries, revisions, names, formats, languages []string
	var forced, hearing []bool
	for videoID, subs := range sidecars {
		fileID := known[videoPaths[videoID]]
		if fileID == 0 {
			continue
		}
		for _, sub := range subs {
			fileIDs = append(fileIDs, fileID)
			entries, revisions, names = append(entries, sub.Entry.GetId()), append(revisions, sub.Entry.GetRevision()), append(names, sub.Entry.GetName())
			formats, languages = append(formats, sub.Format), append(languages, sub.Language)
			forced, hearing = append(forced, sub.Forced), append(hearing, sub.HearingImpaired)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO bloem_storage_episode_sidecars(location_id,entry_id,media_file_id,revision,name,format,language,forced,hearing_impaired)
		SELECT $1,t.e,t.f,t.r,t.n,t.fmt,t.l,t.fo,t.hi FROM unnest($2::text[],$3::bigint[],$4::text[],$5::text[],$6::text[],$7::text[],$8::bool[],$9::bool[]) AS t(e,f,r,n,fmt,l,fo,hi)
		ON CONFLICT(location_id,entry_id) DO UPDATE SET media_file_id=EXCLUDED.media_file_id,revision=EXCLUDED.revision,name=EXCLUDED.name,format=EXCLUDED.format,
		 language=EXCLUDED.language,forced=EXCLUDED.forced,hearing_impaired=EXCLUDED.hearing_impaired
		WHERE (bloem_storage_episode_sidecars.media_file_id,bloem_storage_episode_sidecars.revision,bloem_storage_episode_sidecars.name,bloem_storage_episode_sidecars.language,bloem_storage_episode_sidecars.forced,bloem_storage_episode_sidecars.hearing_impaired)
		 IS DISTINCT FROM (EXCLUDED.media_file_id,EXCLUDED.revision,EXCLUDED.name,EXCLUDED.language,EXCLUDED.forced,EXCLUDED.hearing_impaired)`,
		location.ID, entries, fileIDs, revisions, names, formats, languages, forced, hearing)
	return err
}

// storageSeriesMatches lists each series that still needs a provider match: a
// pending item, or an unmatched one whose show folder declares provider IDs
// (an earlier attempt can have failed transiently).
func storageSeriesMatches(identities []storageSeriesIdentity, contentIDs, statuses []string) []StorageSeriesMatch {
	var matches []StorageSeriesMatch
	seen := make(map[string]bool, len(contentIDs))
	for i, id := range contentIDs {
		identity := identities[i]
		tagged := identity.tvdbID != "" || identity.tmdbID != "" || identity.imdbID != ""
		retry := statuses[i] == "pending" || (statuses[i] == "unmatched" && tagged)
		if seen[id] || !retry {
			continue
		}
		seen[id] = true
		matches = append(matches, StorageSeriesMatch{ContentID: id, Title: identity.title, Year: identity.year,
			TvdbID: identity.tvdbID, TmdbID: identity.tmdbID, ImdbID: identity.imdbID})
	}
	return matches
}

// FinalizeStorageSeriesLocationTx drops the sidecars whose listed entry the
// completing location no longer holds at that revision. It runs in the
// authorized completion transaction, after reconciliation.
func (s *Scanner) FinalizeStorageSeriesLocationTx(ctx context.Context, tx pgx.Tx, location storagesource.Location) error {
	_, err := tx.Exec(ctx, `DELETE FROM bloem_storage_episode_sidecars c WHERE c.location_id=$1 AND NOT EXISTS(
		SELECT 1 FROM bloem_storage_location_entries x WHERE x.location_id=c.location_id AND x.entry_id=c.entry_id AND x.revision=c.revision AND x.absence_confirmed_at IS NULL)`, location.ID)
	return err
}

// LinkStorageSeries links a matched series' newly published files to the
// episodes the provider already lists, with Silo's bulk linker. Episodes the
// provider does not list yet wait for Silo's scheduled refresh.
func (s *Scanner) LinkStorageSeries(ctx context.Context, seriesID string) (int, error) {
	if s == nil || s.fileRepo == nil {
		return 0, errors.New("storage series file repository is not configured")
	}
	return s.fileRepo.BulkLinkEpisodesBySeries(ctx, seriesID)
}

// MarkStorageSeriesUnmatched records a series whose automatic match found
// nothing. A matched item is never demoted.
func (s *Scanner) MarkStorageSeriesUnmatched(ctx context.Context, contentID string) error {
	return s.MarkStorageMovieUnmatched(ctx, contentID)
}

// StorageMissingEpisodesTx lists the episodes of this location's missing
// files. Completion reads them after reconciliation commits the missing flags
// and before the sweep may delete those files, in the same transaction.
func (s *Scanner) StorageMissingEpisodesTx(ctx context.Context, tx pgx.Tx, folder *models.MediaFolder, location storagesource.Location) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT f.episode_id FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id
		WHERE r.location_id=$1 AND f.media_folder_id=$2 AND f.missing_since IS NOT NULL AND f.episode_id IS NOT NULL ORDER BY 1`, location.ID, folder.ID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ReconcileStorageEpisodeMemberships removes the library's memberships of the
// given episodes that no present file serves any more, with Silo's episode
// membership repository, after the completion that listed them committed.
// Every completed scan lists the episodes of the location's missing files
// again, so a failure here only delays the removal.
func (s *Scanner) ReconcileStorageEpisodeMemberships(ctx context.Context, folder *models.MediaFolder, episodes []string) (int, error) {
	if s == nil || s.episodeLibraryRepo == nil || folder == nil || len(episodes) == 0 {
		return 0, nil
	}
	return s.episodeLibraryRepo.RemoveStaleEpisodeMemberships(ctx, folder.ID, episodes)
}
