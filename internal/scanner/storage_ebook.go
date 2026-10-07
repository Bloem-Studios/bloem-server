package scanner

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/idgen"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/titleutil"
	"github.com/jackc/pgx/v5"
)

// StorageEbook is one listed storage file, with the metadata to publish it.
type StorageEbook struct {
	Entry *storagev1.Entry
	Book  parsedEbook
	// Cover names the provider's cover image entry; empty when the book has
	// none. Covers are fetched after publication, never during a scan.
	CoverEntryID, CoverRevision, CoverThumbhash string
}

// StorageEbookFromEntry builds a StorageEbook from provider-held metadata. It
// reports false when the entry carries none and the file must be parsed.
func StorageEbookFromEntry(e *storagev1.Entry) (StorageEbook, bool) {
	meta := e.GetEbook()
	if meta == nil {
		return StorageEbook{}, false
	}
	book := parsedEbook{
		Format:      ebookFileFormat(e.GetName()),
		Title:       meta.GetTitle(),
		Authors:     boundedStrings(meta.GetAuthors()),
		Description: meta.GetDescription(),
		Publisher:   meta.GetPublisher(),
		Language:    meta.GetLanguage(),
		ISBN:        meta.GetIsbn(),
		Series:      meta.GetSeries(),
		SeriesIndex: meta.GetSeriesIndex(),
		Genres:      boundedStrings(meta.GetGenres()),
		PageCount:   int(meta.GetPageCount()),
	}
	if published, ok := parseEbookDate(meta.GetPublishedDate()); ok {
		book.PublishedAt = published
	}
	book.sanitize()
	out := StorageEbook{Entry: e, Book: book, CoverThumbhash: strings.TrimSpace(meta.GetCoverThumbhash())}
	if meta.GetCoverEntryId() != "" && meta.GetCoverRevision() != "" {
		out.CoverEntryID, out.CoverRevision = meta.GetCoverEntryId(), meta.GetCoverRevision()
	}
	return out, true
}

// ParseStorageEbook reads an EPUB or PDF from a provider that supplies no
// metadata. The file must already be pinned to the entry's revision.
func ParseStorageEbook(ctx context.Context, e *storagev1.Entry, file mediasource.File) (StorageEbook, error) {
	book, err := ParseNativeEbook(ctx, file)
	if err != nil {
		return StorageEbook{}, err
	}
	// Embedded covers are not extracted on this path: cover bytes would have
	// to be uploaded inside the scan. A later cover pass can read them.
	book.Cover = nil
	return StorageEbook{Entry: e, Book: book}, nil
}

func boundedStrings(values []string) []string {
	if len(values) > 64 {
		values = values[:64]
	}
	return values
}

// StoragePublishResult counts one page's publication outcome.
type StoragePublishResult struct{ New, Updated, Unchanged int }

// storagePublishedStatus marks provider-held metadata. A storage source is the
// authority for its books, so enrichment skips them and every scan reapplies
// the provider's current metadata.
const storagePublishedStatus = "matched"

// PublishStorageEbooksTx publishes one listed page of a storage library's books
// inside the transaction that records the page. It issues a fixed number of
// statements per page, independent of the page's size.
func (s *Scanner) PublishStorageEbooksTx(ctx context.Context, tx pgx.Tx, folder *models.MediaFolder, location storagesource.Location, configurationRevision int64, books []StorageEbook) (StoragePublishResult, error) {
	var result StoragePublishResult
	if len(books) == 0 {
		return result, nil
	}
	if s == nil || s.itemRepo == nil || s.fileRepo == nil || folder == nil || folder.ID != location.FolderID {
		return result, fmt.Errorf("storage ebook publication: scanner not configured")
	}
	paths := make([]string, len(books))
	for i, b := range books {
		path, err := storagesource.CatalogLocation(location.ID, b.Entry.GetId())
		if err != nil {
			return result, err
		}
		paths[i] = path
	}

	type existingFile struct{ contentID, revision string }
	existing := map[string]existingFile{}
	rows, err := tx.Query(ctx, `SELECT f.file_path, f.content_id, COALESCE(r.revision, '')
		FROM media_files f LEFT JOIN bloem_storage_file_refs r ON r.media_file_id = f.id
		WHERE f.media_folder_id = $1 AND f.file_path = ANY($2)`, folder.ID, paths)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var path string
		var f existingFile
		if err := rows.Scan(&path, &f.contentID, &f.revision); err != nil {
			rows.Close()
			return result, err
		}
		existing[path] = f
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}

	// New files join an existing item with the same identity (an EPUB and a
	// PDF of one ISBN), first in this page, then in the library.
	groupKeys := make([]string, len(books))
	var unresolved []string
	for i, b := range books {
		groupKeys[i] = ebookContentGroupKey(&books[i].Book, b.Entry.GetLogicalPath())
		if _, ok := existing[paths[i]]; !ok && groupKeys[i] != "" {
			unresolved = append(unresolved, groupKeys[i])
		}
	}
	byGroup := map[string]string{}
	if len(unresolved) > 0 {
		rows, err := tx.Query(ctx, `SELECT content_group_key, min(content_id) FROM media_files
			WHERE media_folder_id = $1 AND content_group_key = ANY($2) GROUP BY content_group_key`, folder.ID, unresolved)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var key, id string
			if err := rows.Scan(&key, &id); err != nil {
				rows.Close()
				return result, err
			}
			byGroup[key] = id
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return result, err
		}
	}
	contentIDs := make([]string, len(books))
	for i := range books {
		if f, ok := existing[paths[i]]; ok {
			contentIDs[i] = f.contentID
			if f.revision == books[i].Entry.GetRevision() {
				result.Unchanged++
			} else {
				result.Updated++
			}
			continue
		}
		result.New++
		if id, ok := byGroup[groupKeys[i]]; ok && groupKeys[i] != "" {
			contentIDs[i] = id
			continue
		}
		id, err := idgen.NextID()
		if err != nil {
			return result, fmt.Errorf("generate content_id: %w", err)
		}
		contentIDs[i] = id
		if groupKeys[i] != "" {
			byGroup[groupKeys[i]] = id
		}
	}

	// Items: the first book of each item supplies its metadata, applied over
	// the stored item so artwork and other writers' fields survive.
	order := make([]string, 0, len(books))
	bookFor := map[string]*StorageEbook{}
	for i := range books {
		if _, ok := bookFor[contentIDs[i]]; !ok {
			bookFor[contentIDs[i]] = &books[i]
			order = append(order, contentIDs[i])
		}
	}
	stored, err := s.itemRepo.GetByIDs(ctx, order)
	if err != nil {
		return result, fmt.Errorf("load storage ebook items: %w", err)
	}
	storedByID := make(map[string]*models.MediaItem, len(stored))
	for _, item := range stored {
		if item != nil {
			storedByID[item.ContentID] = item
		}
	}
	now := time.Now().UTC()
	items := make([]*models.MediaItem, 0, len(order))
	for _, id := range order {
		b := bookFor[id]
		item := storedByID[id]
		if item == nil {
			item = &models.MediaItem{ContentID: id, Year: b.Book.Year}
		}
		applyEbookToMediaItem(item, &b.Book)
		if strings.TrimSpace(item.Title) == "" {
			item.Title = ebookTitleFromPath(b.Entry.GetName())
		}
		item.SortTitle = titleutil.DeriveDefaultSortTitle(item.Title)
		item.Status = storagePublishedStatus
		if item.MatchedAt == nil {
			item.MatchedAt = &now
		}
		if item.PosterPath == "" && b.CoverThumbhash != "" {
			item.PosterThumbhash = b.CoverThumbhash
		}
		items = append(items, item)
	}
	if err := s.itemRepo.UpsertBatchTx(ctx, tx, items); err != nil {
		return result, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO media_item_libraries (content_id, media_folder_id, first_seen_at)
		SELECT id, $2, NOW() FROM unnest($1::text[]) AS t(id)
		ON CONFLICT (content_id, media_folder_id) DO NOTHING`, order, folder.ID); err != nil {
		return result, fmt.Errorf("storage ebook library membership: %w", err)
	}

	files := make([]models.MediaFile, len(books))
	for i, b := range books {
		modified := normalizeFileModifiedAt(time.Unix(0, b.Entry.GetModifiedUnixNano()))
		files[i] = buildEbookMediaFile(folder, contentIDs[i], paths[i], b.Entry.GetSize(), modified, &books[i].Book, groupKeys[i])
		files[i].ProbeSource = "storage"
	}
	if err := s.fileRepo.UpsertBatchTx(ctx, tx, files); err != nil {
		return result, err
	}
	fileIDs := map[string]int{}
	rows, err = tx.Query(ctx, `SELECT id, file_path FROM media_files WHERE media_folder_id = $1 AND file_path = ANY($2)`, folder.ID, paths)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id int
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			rows.Close()
			return result, err
		}
		fileIDs[path] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}
	refs := make([]storagesource.FileRef, len(books))
	for i, b := range books {
		refs[i] = storagesource.FileRef{MediaFileID: fileIDs[paths[i]], PersistedRef: storagesource.PersistedRef{
			LocationID: location.ID, EntryID: b.Entry.GetId(), Revision: b.Entry.GetRevision(), LogicalPath: b.Entry.GetLogicalPath(),
		}}
	}
	if err := storagesource.AttachFilesTx(ctx, tx, configurationRevision, refs); err != nil {
		return result, fmt.Errorf("attach storage files: %w", err)
	}

	if err := publishStorageEbookAuthors(ctx, tx, order, bookFor); err != nil {
		return result, err
	}
	if err := publishStorageEbookSeries(ctx, tx, order, bookFor); err != nil {
		return result, err
	}
	if err := publishStorageEbookISBNs(ctx, tx, order, bookFor); err != nil {
		return result, err
	}
	return result, nil
}

// publishStorageEbookAuthors replaces the author credits of every published
// item with the provider's list, resolving people by name as file scans do.
func publishStorageEbookAuthors(ctx context.Context, tx pgx.Tx, order []string, bookFor map[string]*StorageEbook) error {
	var credited []string
	names := map[string]string{}
	for _, id := range order {
		authors := bookFor[id].Book.Authors
		if len(authors) == 0 {
			continue
		}
		credited = append(credited, id)
		for _, name := range authors {
			names[strings.ToLower(name)] = name
		}
	}
	if len(credited) == 0 {
		return nil
	}
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Concurrent publishers resolving the same new author must not create it
	// twice; locks are taken in sorted order to avoid deadlocks.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('native-ebook-author:' || k, 0))
		FROM unnest($1::text[]) WITH ORDINALITY AS t(k, n) ORDER BY n`, keys); err != nil {
		return fmt.Errorf("lock ebook authors: %w", err)
	}
	people := map[string]int64{}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (lower(name)) lower(name), id FROM people
		WHERE lower(name) = ANY($1) ORDER BY lower(name), id`, keys)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		var id int64
		if err := rows.Scan(&key, &id); err != nil {
			rows.Close()
			return err
		}
		people[key] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var newIDs []int64
	var newNames []string
	for _, key := range keys {
		if _, ok := people[key]; ok {
			continue
		}
		id, err := nextNumericID()
		if err != nil {
			return err
		}
		people[key] = id
		newIDs = append(newIDs, id)
		newNames = append(newNames, names[key])
	}
	if len(newIDs) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO people (id, name) SELECT * FROM unnest($1::bigint[], $2::text[])`, newIDs, newNames); err != nil {
			return fmt.Errorf("insert ebook authors: %w", err)
		}
	}
	var creditIDs, personIDs []int64
	var creditItems []string
	var sortOrders []int
	for _, id := range credited {
		seen := map[int64]bool{}
		for _, name := range bookFor[id].Book.Authors {
			person := people[strings.ToLower(name)]
			if seen[person] {
				continue
			}
			seen[person] = true
			creditID, err := nextNumericID()
			if err != nil {
				return err
			}
			creditIDs = append(creditIDs, creditID)
			creditItems = append(creditItems, id)
			personIDs = append(personIDs, person)
			sortOrders = append(sortOrders, len(seen)-1)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM item_people WHERE content_id = ANY($1) AND kind = $2`, credited, models.PersonKindAuthor); err != nil {
		return fmt.Errorf("clear ebook authors: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO item_people (id, content_id, person_id, kind, character, sort_order)
		SELECT c, i, p, $5, '', o FROM unnest($1::bigint[], $2::text[], $3::bigint[], $4::int[]) AS t(c, i, p, o)
		ON CONFLICT (content_id, person_id, kind, character) DO UPDATE SET sort_order = EXCLUDED.sort_order`,
		creditIDs, creditItems, personIDs, sortOrders, models.PersonKindAuthor); err != nil {
		return fmt.Errorf("credit ebook authors: %w", err)
	}
	return nil
}

func publishStorageEbookSeries(ctx context.Context, tx pgx.Tx, order []string, bookFor map[string]*StorageEbook) error {
	var setIDs, setNames, clearIDs []string
	var setIndexes []*float64
	for _, id := range order {
		name, index := ebookSeriesDesired(&bookFor[id].Book)
		if name == "" {
			clearIDs = append(clearIDs, id)
			continue
		}
		setIDs, setNames, setIndexes = append(setIDs, id), append(setNames, name), append(setIndexes, index)
	}
	if len(clearIDs) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM ebook_series WHERE content_id = ANY($1)`, clearIDs); err != nil {
			return fmt.Errorf("clear ebook series: %w", err)
		}
	}
	if len(setIDs) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO ebook_series (content_id, series_name, series_index, updated_at)
			SELECT * , NOW() FROM unnest($1::text[], $2::text[], $3::float8[])
			ON CONFLICT (content_id) DO UPDATE SET series_name = EXCLUDED.series_name, series_index = EXCLUDED.series_index, updated_at = NOW()`,
			setIDs, setNames, setIndexes); err != nil {
			return fmt.Errorf("publish ebook series: %w", err)
		}
	}
	return nil
}

// publishStorageEbookISBNs records each item's ISBN. An ISBN another item
// already owns stays with that item, as in file scans.
func publishStorageEbookISBNs(ctx context.Context, tx pgx.Tx, order []string, bookFor map[string]*StorageEbook) error {
	var ids, isbns []string
	seen := map[string]bool{}
	for _, id := range order {
		isbn := bookFor[id].Book.ISBN
		if isbn == "" || seen[isbn] {
			continue
		}
		seen[isbn] = true
		ids, isbns = append(ids, id), append(isbns, isbn)
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO media_item_provider_ids (content_id, provider, provider_id, item_type)
		SELECT t.id, 'isbn', t.isbn, 'ebook' FROM unnest($1::text[], $2::text[]) AS t(id, isbn)
		WHERE NOT EXISTS (SELECT 1 FROM media_item_provider_ids o
			WHERE o.provider = 'isbn' AND o.provider_id = t.isbn AND o.content_id <> t.id)
		ON CONFLICT (content_id, provider) DO UPDATE SET provider_id = EXCLUDED.provider_id, updated_at = NOW()
		WHERE media_item_provider_ids.provider_id IS DISTINCT FROM EXCLUDED.provider_id`, ids, isbns)
	if err != nil {
		return fmt.Errorf("publish ebook isbns: %w", err)
	}
	return nil
}

// MarkStorageEbooksRemovedTx marks the files of entries the provider reports
// removed as missing. SweepStorageLibrary removes them after the library's
// grace period, as file scans do; an entry listed again before then clears
// the mark when its file is republished.
func (s *Scanner) MarkStorageEbooksRemovedTx(ctx context.Context, tx pgx.Tx, location storagesource.Location, entryIDs []string) (int, error) {
	if len(entryIDs) == 0 {
		return 0, nil
	}
	tag, err := tx.Exec(ctx, `UPDATE media_files f SET missing_since = NOW()
		FROM bloem_storage_file_refs r
		WHERE r.media_file_id = f.id AND r.location_id = $1 AND r.entry_id = ANY($2) AND f.missing_since IS NULL`, location.ID, entryIDs)
	if err != nil {
		return 0, fmt.Errorf("mark removed storage ebooks: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// SweepStorageLibrary removes files marked missing past the grace period and
// reconciles the library's memberships and items, as file scans do.
func (s *Scanner) SweepStorageLibrary(ctx context.Context, folder *models.MediaFolder) (int, error) {
	trashed, _, _, err := s.sweepMissingAndReconcile(ctx, folder, true)
	return trashed, err
}

func nextNumericID() (int64, error) {
	text, err := idgen.NextID()
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(text, 10, 64)
}
