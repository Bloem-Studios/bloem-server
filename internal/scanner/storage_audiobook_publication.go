package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/idgen"
	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/titleutil"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

const nativeAudioStagingLimit = 64 << 20

type nativeAudioStagedProbe struct {
	Probe           *ProbeData        `json:"probe"`
	DurationSeconds float64           `json:"duration_seconds"`
	Chapters        []ChapterInfo     `json:"chapters"`
	Tags            map[string]string `json:"tags"`
}

func (s *Scanner) PublishStorageAudioPageTx(ctx context.Context, tx pgx.Tx, page StorageAudioPage) (StorageAudioResult, error) {
	var result StorageAudioResult
	if !s.NativeAudioScanConfigured() || tx == nil || page.Folder == nil || !page.Folder.Enabled || !librarykind.IsAudiobook(page.Folder.Type) || page.Folder.ID != page.Location.FolderID || page.Lease.LocationID != page.Location.ID || page.ConfigurationRevision <= 0 || page.ConfigurationRevision != page.Lease.ConfigurationRevision {
		return result, fmt.Errorf("native audio publication is not configured")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "bloem-native-audiobook:"+page.Location.ID.String()+":"+page.DirectoryID); err != nil {
		return result, err
	}
	if _, err := storagesource.DeleteStaleAudioStagingTx(ctx, tx, page.Lease); err != nil {
		return result, err
	}
	for _, track := range page.Tracks {
		if track.Entry == nil || track.Entry.Kind != storagev1.EntryKind_ENTRY_KIND_FILE {
			return result, storagesource.ErrReferenceConflict
		}
		entry, err := proto.MarshalOptions{Deterministic: true}.Marshal(track.Entry)
		if err != nil {
			return result, err
		}
		row := storagesource.StagedAudioTrack{EntryID: track.Entry.Id, Revision: track.Entry.Revision, Status: string(track.Status), Entry: entry}
		if track.Status == StorageAudioTrackOK {
			if track.Probe == nil {
				row.Status = string(StorageAudioTrackInvalid)
			} else {
				p := nativeAudioStagedProbe{Probe: track.Probe.Probe, DurationSeconds: track.Probe.DurationSeconds, Chapters: track.Probe.Chapters, Tags: nativeAudioRetainedTags(track.Probe.Tags)}
				row.Probe, err = json.Marshal(p)
				if err != nil || len(row.Probe) > (1<<20)-4096 {
					row.Status = string(StorageAudioTrackInvalid)
					row.Probe = nil
				}
			}
		}
		var count, size int
		if err := tx.QueryRow(ctx, `SELECT count(*),COALESCE(sum(octet_length(entry)+COALESCE(octet_length(probe::text),0)),0) FROM bloem_storage_audio_staged_tracks WHERE run_id=$1 AND location_id=$2 AND directory_id=$3 AND entry_id<>$4`, page.Lease.RunID, page.Location.ID, page.DirectoryID, row.EntryID).Scan(&count, &size); err != nil {
			return result, err
		}
		if count >= 512 || size+len(row.Entry)+2*len(row.Probe) > nativeAudioStagingLimit {
			// Keep a bounded rejection marker by invalidating an existing staged row.
			if _, err := tx.Exec(ctx, `UPDATE bloem_storage_audio_staged_tracks SET status='invalid',probe=NULL WHERE run_id=$1 AND location_id=$2 AND directory_id=$3 AND entry_id=(SELECT entry_id FROM bloem_storage_audio_staged_tracks WHERE run_id=$1 AND location_id=$2 AND directory_id=$3 ORDER BY entry_id LIMIT 1)`, page.Lease.RunID, page.Location.ID, page.DirectoryID); err != nil {
				return result, err
			}
			continue
		}
		if err := storagesource.StageAudioTracksTx(ctx, tx, page.Lease, page.DirectoryID, []storagesource.StagedAudioTrack{row}); err != nil {
			return result, err
		}
	}
	if !page.Complete {
		return result, nil
	}
	return s.publishStagedAudioDirectoryTx(ctx, tx, page, false)
}

func nativeAudioRetainedTags(tags map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"title", "album", "album_artist", "artist", "composer", "narrator", "performer", "series", "mvnm", "series-part", "mvin", "movement", "asin", "audible_asin", "com.audible.asin", "description", "summary", "comment", "©cmt", "publisher", "label", "©pub", "releasedate", "release_date", "date", "year", "releasetime", "language", "lang", "genre", "tmp_genre1", "tmp_genre2", "tmp_genre3", "tmp_genre4", "tmp_genre5"} {
		if value, ok := tags[key]; ok {
			out[key] = value
		}
	}
	return out
}

func (s *Scanner) publishStagedAudioDirectoryTx(ctx context.Context, tx pgx.Tx, page StorageAudioPage, finalizing bool) (StorageAudioResult, error) {
	var result StorageAudioResult
	rows, err := storagesource.StagedAudioTracksTx(ctx, tx, page.Lease, page.DirectoryID)
	if err != nil {
		return result, err
	}
	if len(rows) == 0 {
		return result, nil
	}
	var tracks []StorageAudioTrack
	for _, row := range rows {
		entry := new(storagev1.Entry)
		if err := proto.Unmarshal(row.Entry, entry); err != nil {
			return result, err
		}
		track := StorageAudioTrack{Entry: entry, Status: StorageAudioTrackStatus(row.Status)}
		if row.Status == "ok" {
			var p nativeAudioStagedProbe
			if err := json.Unmarshal(row.Probe, &p); err != nil {
				return result, err
			}
			track.Probe = &NativeAudioProbe{Probe: p.Probe, DurationSeconds: p.DurationSeconds, Chapters: p.Chapters, Tags: p.Tags}
		}
		tracks = append(tracks, track)
	}
	ordered, err := orderNativeAudioTracks(tracks)
	if err == nil {
		err = validateNativeAudioBook(ordered)
	}
	if err != nil {
		slog.WarnContext(ctx, "native audiobook directory rejected", "location_id", page.Location.ID, "directory_id", page.DirectoryID, "error", err)
		result.BooksRejected = 1
		return result, storagesource.DeleteStagedAudioTracksTx(ctx, tx, page.Lease, page.DirectoryID)
	}
	key, err := storagesource.AudioPresentationKey(page.Location.ID, page.DirectoryID)
	if err != nil {
		return result, err
	}
	paths := make([]string, len(ordered))
	for i, track := range ordered {
		paths[i], err = storagesource.CatalogLocation(page.Location.ID, track.Entry.Id)
		if err != nil {
			return result, err
		}
	}
	var omitted bool
	query := `SELECT EXISTS(SELECT 1 FROM media_files f WHERE f.media_folder_id=$1 AND f.presentation_group_key=$2 AND NOT (f.file_path=ANY($3::text[])) AND f.missing_since IS NULL)`
	if finalizing {
		query = `SELECT EXISTS(SELECT 1 FROM media_files f JOIN bloem_storage_file_refs x ON x.media_file_id=f.id WHERE f.media_folder_id=$1 AND f.presentation_group_key=$2 AND NOT (f.file_path=ANY($3::text[])) AND EXISTS(SELECT 1 FROM bloem_storage_file_roots m JOIN bloem_storage_selected_roots r ON r.location_id=m.location_id AND r.id=m.root_id AND r.enabled WHERE m.location_id=x.location_id AND m.media_file_id=f.id AND m.enabled))`
	}
	if err := tx.QueryRow(ctx, query, page.Folder.ID, key, paths).Scan(&omitted); err != nil {
		return result, err
	}
	if omitted {
		// Before completion the retained graph has not evaluated absence yet.
		// After completion retained omitted tracks preserve the complete prior book.
		if finalizing {
			return result, storagesource.DeleteStagedAudioTracksTx(ctx, tx, page.Lease, page.DirectoryID)
		}
		return result, nil
	}
	result, err = s.publishNativeAudioBookTx(ctx, tx, page, key, paths, ordered)
	if err != nil {
		return result, err
	}
	return result, storagesource.DeleteStagedAudioTracksTx(ctx, tx, page.Lease, page.DirectoryID)
}

func nativeAudioBookMetadata(tracks []StorageAudioTrack) (*parsedAudiobook, []string, []string, string, bool) {
	first := tracks[0]
	book := new(parsedAudiobook)
	book.ASIN = firstNonEmpty(first.Probe.Tags["asin"], first.Probe.Tags["audible_asin"], first.Probe.Tags["com.audible.asin"])
	var authors, narrators []string
	isbn := ""
	provider := first.Entry.GetAudiobook() != nil
	if provider {
		m := first.Entry.GetAudiobook()
		book.Title = m.Title
		book.Author = strings.Join(m.Authors, "; ")
		book.Narrator = strings.Join(m.Narrators, "; ")
		book.Overview = m.Description
		book.Publisher = m.Publisher
		book.ReleaseDate = m.PublishedDate
		book.Year = parseTagYear(m.PublishedDate)
		book.Language = m.Language
		book.Series = m.Series
		book.SeriesPosition = m.SeriesIndex
		book.Genres = m.Genres
		authors = m.Authors
		narrators = m.Narrators
		isbn = m.Isbn
	} else {
		book.populateFromTags(first.Probe.Tags)
		var names []string
		for _, track := range tracks {
			names = append(names, track.Entry.Name)
		}
		book.applyFilesystemFallbacks(path.Dir(first.Entry.LogicalPath), names)
		if book.Author != "" {
			authors = []string{book.Author}
		}
		if book.Narrator != "" {
			narrators = []string{book.Narrator}
		}
	}
	for _, track := range tracks {
		book.Files = append(book.Files, parsedAudiobookFile{Path: track.Entry.LogicalPath, Duration: int(track.Probe.DurationSeconds), CodecAudio: track.Probe.Probe.CodecAudio, Container: track.Probe.Probe.Container})
	}
	return book, authors, narrators, normalizeNativeAudioIdentifier(isbn), provider
}

func normalizeNativeAudioASIN(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func normalizeNativeAudioIdentifier(value string) string {
	return strings.ToUpper(strings.Join(strings.Fields(strings.ReplaceAll(value, "-", "")), ""))
}

func (s *Scanner) nativeAudioItemTx(ctx context.Context, tx pgx.Tx, page StorageAudioPage, key string, paths []string, book *parsedAudiobook, isbn string) (*models.MediaItem, error) {
	identifiers := []string{}
	for _, value := range []string{isbn, normalizeNativeAudioASIN(book.ASIN)} {
		if value != "" {
			identifiers = append(identifiers, value)
		}
	}
	sort.Strings(identifiers)
	for _, value := range identifiers {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "bloem-native-audiobook-identity:"+value); err != nil {
			return nil, err
		}
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT content_id FROM media_files WHERE media_folder_id=$1 AND presentation_group_key=$2 ORDER BY missing_since NULLS FIRST,id LIMIT 1`, page.Folder.ID, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT content_id FROM media_files WHERE file_path=ANY($1::text[]) ORDER BY missing_since NULLS FIRST,id LIMIT 1`, paths).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT content_id FROM media_item_provider_ids WHERE item_type='audiobook' AND ((provider='isbn' AND provider_id=$1 AND $1<>'') OR (provider='asin' AND provider_id=$2 AND $2<>'')) ORDER BY content_id LIMIT 1`, isbn, normalizeNativeAudioASIN(book.ASIN)).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) && book.Author != "" && book.Narrator != "" && book.Year > 0 {
		total := 0
		for _, file := range book.Files {
			total += file.Duration
		}
		rows, e := tx.Query(ctx, audiobookDuplicateCandidateSQL, book.Author, book.Narrator, book.Year, total, max(10, total/200), normalizeAudiobookDedupeTitle(stripNarratorSuffix(book.Title)))
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var candidate, title string
			if e := rows.Scan(&candidate, &title); e != nil {
				rows.Close()
				return nil, e
			}
			if audiobookDedupeTitlesMatch(title, stripNarratorSuffix(book.Title)) {
				id = candidate
				err = nil
				break
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if id == "" {
		id, err = idgen.NextID()
		if err != nil {
			return nil, err
		}
		return &models.MediaItem{ContentID: id, Type: "audiobook", Status: "pending"}, nil
	}
	item, err := s.itemRepo.GetByIDTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if item.Type != "audiobook" {
		return nil, ErrNativeAudioTimeline
	}
	return item, nil
}

func (s *Scanner) publishNativeAudioBookTx(ctx context.Context, tx pgx.Tx, page StorageAudioPage, key string, paths []string, tracks []StorageAudioTrack) (StorageAudioResult, error) {
	var result StorageAudioResult
	book, authors, narrators, isbn, provider := nativeAudioBookMetadata(tracks)
	item, err := s.nativeAudioItemTx(ctx, tx, page, key, paths, book, isbn)
	if errors.Is(err, ErrNativeAudioTimeline) {
		result.BooksRejected = 1
		return result, nil
	}
	if err != nil {
		return result, err
	}
	owned, err := storageMetadataOwnedTx(ctx, tx, []string{item.ContentID})
	if err != nil {
		return result, err
	}
	var foreign bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_files WHERE content_id=$1 AND missing_since IS NULL AND media_folder_id<>$2)`, item.ContentID, page.Folder.ID).Scan(&foreign); err != nil {
		return result, err
	}
	metadata := owned[item.ContentID] && !foreign && (provider || item.Status == "pending")
	if metadata {
		applyBookToMediaItem(item, book)
		item.SortTitle = titleutil.DeriveDefaultSortTitle(item.Title)
		if provider {
			item.Status = storagePublishedStatus
		}
		m := tracks[0].Entry.GetAudiobook()
		if m != nil {
			applyStorageCover(item, &StorageEbook{CoverEntryID: m.CoverEntryId, CoverRevision: m.CoverRevision, CoverThumbhash: m.CoverThumbhash})
		}
		if err := s.itemRepo.UpsertBatchTx(ctx, tx, []*models.MediaItem{item}); err != nil {
			return result, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, item.ContentID, page.Folder.ID); err != nil {
		return result, err
	}
	files := make([]models.MediaFile, len(tracks))
	for i, track := range tracks {
		var prior *string
		err := tx.QueryRow(ctx, `SELECT r.revision FROM media_files f LEFT JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE f.file_path=$1`, paths[i]).Scan(&prior)
		if errors.Is(err, pgx.ErrNoRows) {
			result.New++
		} else if err != nil {
			return result, err
		} else if prior != nil && *prior == track.Entry.Revision {
			result.Unchanged++
		} else {
			result.Updated++
		}
		mf := &files[i]
		modified := normalizeFileModifiedAt(time.Unix(0, track.Entry.ModifiedUnixNano))
		*mf = models.MediaFile{ContentID: item.ContentID, MediaFolderID: page.Folder.ID, FilePath: paths[i], FileSize: track.Entry.Size, FileModifiedAt: &modified, BaseType: "audiobook", BaseTitle: book.Title, BaseYear: book.Year, ContentGroupKey: item.ContentID, GroupKeyVersion: 1, IdentityConfidence: audiobookIdentityConfidence(book, book.Files[i]), PresentationGroupKey: key}
		p := *track.Probe.Probe
		p.Duration = int(track.Probe.DurationSeconds)
		applyProbeData(mf, &p, "storage")
		mf.Chapters = []models.MediaChapter{}
		for _, chapter := range nativeAudioTrackChapters(track, i+1, len(tracks)) {
			mf.Chapters = append(mf.Chapters, models.MediaChapter{Index: chapter.Index, Title: chapter.Title, StartSeconds: chapter.StartSeconds, EndSeconds: chapter.EndSeconds, Source: chapter.Source})
		}
		if len(tracks) > 1 {
			mf.PresentationKind = "multipart"
			mf.PresentationPartIndex = i + 1
			mf.PresentationPartTotal = len(tracks)
		}
	}
	if err := s.fileRepo.UpsertBatchTx(ctx, tx, files); err != nil {
		return result, err
	}
	if _, err := tx.Exec(ctx, `UPDATE media_files SET missing_since=COALESCE(missing_since,clock_timestamp()),presentation_kind='',presentation_group_key='',presentation_part_index=NULL,presentation_part_total=NULL WHERE media_folder_id=$1 AND presentation_group_key=$2 AND NOT(file_path=ANY($3::text[]))`, page.Folder.ID, key, paths); err != nil {
		return result, err
	}
	var refs []storagesource.FileRef
	for i, track := range tracks {
		var id int
		if err := tx.QueryRow(ctx, `SELECT id FROM media_files WHERE file_path=$1`, paths[i]).Scan(&id); err != nil {
			return result, err
		}
		ref := storagesource.FileRef{MediaFileID: id, PersistedRef: storagesource.PersistedRef{LocationID: page.Location.ID, EntryID: track.Entry.Id, Revision: track.Entry.Revision, LogicalPath: track.Entry.LogicalPath}}
		if m := tracks[0].Entry.GetAudiobook(); m != nil {
			ref.CoverEntryID = m.CoverEntryId
			ref.CoverRevision = m.CoverRevision
		}
		refs = append(refs, ref)
	}
	if err := storagesource.AttachFilesTx(ctx, tx, page.ConfigurationRevision, refs); err != nil {
		return result, err
	}
	if metadata {
		if err := publishNativeAudioPeopleTx(ctx, tx, item.ContentID, authors, narrators); err != nil {
			return result, err
		}
		if err := s.upsertAudiobookSeriesTx(ctx, tx, item.ContentID, book); err != nil {
			return result, err
		}
	}
	for provider, value := range map[string]string{"isbn": isbn, "asin": normalizeNativeAudioASIN(book.ASIN)} {
		if value == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO media_item_provider_ids(content_id,provider,provider_id,item_type) SELECT $1,$2,$3,'audiobook' WHERE NOT EXISTS(SELECT 1 FROM media_item_provider_ids WHERE provider=$2 AND provider_id=$3 AND item_type='audiobook' AND content_id<>$1) ON CONFLICT DO NOTHING`, item.ContentID, provider, value); err != nil {
			return result, err
		}
	}
	result.BooksPublished = 1
	return result, nil
}

func publishNativeAudioPeopleTx(ctx context.Context, tx pgx.Tx, contentID string, authors, narrators []string) error {
	names := map[string]string{}
	for _, list := range [][]string{authors, narrators} {
		for _, name := range list {
			if strings.TrimSpace(name) != "" {
				names[strings.ToLower(name)] = name
			}
		}
	}
	people, err := resolveStoragePeopleTx(ctx, tx, names)
	if err != nil {
		return err
	}
	for i, list := range [][]string{authors, narrators} {
		kind := models.PersonKindAuthor
		if i == 1 {
			kind = models.PersonKindNarrator
		}
		if _, err := tx.Exec(ctx, `DELETE FROM item_people WHERE content_id=$1 AND kind=$2`, contentID, kind); err != nil {
			return err
		}
		seen := map[int64]bool{}
		for n, name := range list {
			person := people[strings.ToLower(name)]
			if person == 0 || seen[person] {
				continue
			}
			seen[person] = true
			id, err := nextNumericID()
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO item_people(id,content_id,person_id,kind,character,sort_order) VALUES($1,$2,$3,$4,'',$5) ON CONFLICT DO NOTHING`, id, contentID, person, kind, n); err != nil {
				return err
			}
		}
	}
	return nil
}

// FinalizeStorageAudioLocationTx publishes deferred replacements only after
// the journal reconciles retained authority, then hides every broken group.
func (s *Scanner) FinalizeStorageAudioLocationTx(ctx context.Context, tx pgx.Tx, folder *models.MediaFolder, location storagesource.Location, lease storagesource.Lease) (StorageAudioResult, int, error) {
	var result StorageAudioResult
	if !s.NativeAudioScanConfigured() || tx == nil || folder == nil || folder.ID != location.FolderID || lease.LocationID != location.ID {
		return result, 0, storagesource.ErrReferenceConflict
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT t.directory_id FROM bloem_storage_audio_staged_tracks t JOIN bloem_storage_scan_directories d ON d.run_id=t.run_id AND d.directory_id=t.directory_id AND d.complete WHERE t.run_id=$1 AND t.location_id=$2 ORDER BY t.directory_id`, lease.RunID, location.ID)
	if err != nil {
		return result, 0, err
	}
	directories, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return result, 0, err
	}
	for _, directory := range directories {
		r, e := s.publishStagedAudioDirectoryTx(ctx, tx, StorageAudioPage{Folder: folder, Location: location, Lease: lease, ConfigurationRevision: lease.ConfigurationRevision, DirectoryID: directory, Complete: true}, true)
		if e != nil {
			return result, 0, e
		}
		result.New += r.New
		result.Updated += r.Updated
		result.Unchanged += r.Unchanged
		result.BooksPublished += r.BooksPublished
		result.BooksRejected += r.BooksRejected
	}
	if err := storagesource.RefreshAudioPublicationRootsTx(ctx, tx, lease); err != nil {
		return result, 0, err
	}
	tag, err := tx.Exec(ctx, `WITH broken AS(SELECT DISTINCT f.content_id,f.presentation_group_key FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id AND r.location_id=$2 WHERE f.media_folder_id=$1 AND f.presentation_group_key LIKE 'bloem-storage:audiobook:%' AND f.missing_since IS NOT NULL)
 UPDATE media_files f SET missing_since=clock_timestamp() FROM broken b WHERE f.media_folder_id=$1 AND f.content_id=b.content_id AND f.presentation_group_key=b.presentation_group_key AND f.missing_since IS NULL`, folder.ID, location.ID)
	if err != nil {
		return result, 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM bloem_storage_audio_staged_tracks WHERE location_id=$1`, location.ID); err != nil {
		return result, 0, err
	}
	return result, int(tag.RowsAffected()), nil
}
