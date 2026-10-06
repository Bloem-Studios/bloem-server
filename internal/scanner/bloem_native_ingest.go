package scanner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/idgen"
	"github.com/Silo-Server/silo-server/internal/imageutil"
	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/titleutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/proto"
)

var (
	ErrNativeEbookSidecarsIncomplete = errors.New("native ebook sidecar discovery incomplete")
	ErrNativeEbookIdentityChanged    = errors.New("native ebook catalog identity changed; retry preparation")
)

// NativeEbookSidecars is an explicit, completed discovery result. Nil entries
// mean confirmed absence only when Complete is true. The host selects eligible
// same-book sidecars and supplies an authorized revision-pinned opener. Opened
// sidecars are closed here; the main ebook remains caller-owned. Generic cover
// names are eligible only in a directory with one discovered ebook.
// No source/library authority is established by this input or this publisher.
type NativeEbookSidecars struct {
	Complete               bool
	OPF, Cover             *storagev1.Entry
	SingleEbookInDirectory bool
	Open                   func(context.Context, storagesource.PersistedRef) (mediasource.File, error)
}

type nativeEbookPrepared struct {
	book                                      parsedEbook
	groupKey, location, existingID, contentID string
	itemVersion                               time.Time
	posterPath, posterHash                    string
	sidecars                                  []*storagev1.Entry
}

// PublishNativeEbook is the trusted internal/fixture publisher. Production
// consumers must use PublishAuthorizedNativeEbook with the host SQL authorizer.
// It publishes actual parsed EPUB/PDF metadata for an already authorized claim. Parsing, pinned sidecar reads and image-cache I/O all
// finish before PublishIngestion acquires SQL locks. Failures keep the durable
// claim retryable. This method neither opens a native logical path locally nor
// runs a converter, discovers sidecars, reconciles missing files or wires routes.
func (s *Scanner) PublishNativeEbook(ctx context.Context, sources *storagesource.Repository, claim storagesource.IngestionClaim, folder *models.MediaFolder, file mediasource.File, sidecars NativeEbookSidecars) (string, error) {
	return s.publishNativeEbook(ctx, sources, claim, folder, file, sidecars, nil)
}

// PublishAuthorizedNativeEbook is the production consumer entrypoint. The
// mandatory SQL-only host authorizer runs before source fencing in the same
// transaction as catalog publication, preserving installation -> source order.
// Preparation and cover I/O finish before either hook acquires SQL locks.
func (s *Scanner) PublishAuthorizedNativeEbook(ctx context.Context, sources *storagesource.Repository, claim storagesource.IngestionClaim, folder *models.MediaFolder, file mediasource.File, sidecars NativeEbookSidecars, authorize func(context.Context, pgx.Tx) error) (string, error) {
	if authorize == nil {
		return "", storagesource.ErrIngestionAuthorizationRequired
	}
	return s.publishNativeEbook(ctx, sources, claim, folder, file, sidecars, authorize)
}

func (s *Scanner) publishNativeEbook(ctx context.Context, sources *storagesource.Repository, claim storagesource.IngestionClaim, folder *models.MediaFolder, file mediasource.File, sidecars NativeEbookSidecars, authorize func(context.Context, pgx.Tx) error) (string, error) {
	if sources == nil {
		return "", fmt.Errorf("native storage repository required")
	}
	prepared, err := s.prepareNativeEbook(ctx, claim, folder, file, sidecars)
	if err != nil {
		return "", err
	}
	if authorize != nil {
		err = sources.PublishAuthorizedIngestion(ctx, claim, authorize, s.nativeEbookPublication(sources, claim, folder, prepared))
	} else {
		err = s.publishPreparedNativeEbook(ctx, sources, claim, folder, prepared)
	}
	if err != nil {
		return "", err
	}
	// Existing durable enrichment reconciliation repairs a process interruption
	// between publication and this normal scanner hook. Never enqueue on failure.
	if err = s.enqueueEbookEnrichment(ctx, prepared.contentID); err != nil {
		return prepared.contentID, err
	}
	return prepared.contentID, nil
}

func nativePinnedEntry(file mediasource.File, e *storagev1.Entry) error {
	if file == nil || e == nil || e.Kind != storagev1.EntryKind_ENTRY_KIND_FILE || e.Id == "" || e.Revision == "" {
		return storagesource.ErrReferenceConflict
	}
	info := file.Info()
	if info.Name != e.Name || info.Revision != e.Revision || info.LogicalPath != e.LogicalPath || info.Size != e.Size || info.Size < 0 {
		return storagesource.ErrReferenceConflict
	}
	return nil
}

func parseNativeIngest(ctx context.Context, claim storagesource.IngestionClaim, file mediasource.File, input NativeEbookSidecars) (parsedEbook, []*storagev1.Entry, error) {
	var book parsedEbook
	if !input.Complete {
		return book, nil, ErrNativeEbookSidecarsIncomplete
	}
	if err := nativePinnedEntry(file, claim.Entry); err != nil {
		return book, nil, err
	}
	book, err := ParseNativeEbook(ctx, file)
	if err != nil {
		return book, nil, err
	}
	var refs []*storagev1.Entry
	for _, sidecar := range []struct {
		entry *storagev1.Entry
		opf   bool
	}{{input.OPF, true}, {input.Cover, false}} {
		if sidecar.entry == nil {
			continue
		}
		e := proto.CloneOf(sidecar.entry)
		if input.Open == nil || e.Id == "" || e.Id == claim.Entry.Id || e.Revision == "" || e.Kind != storagev1.EntryKind_ENTRY_KIND_FILE || e.Size <= 0 || e.Size > maxEPUBMetadataEntrySize {
			return book, nil, storagesource.ErrReferenceConflict
		}
		if !nativeEbookSidecarEligible(claim.Entry, e, sidecar.opf, input.SingleEbookInDirectory) {
			return book, nil, storagesource.ErrReferenceConflict
		}
		ref := storagesource.PersistedRef{BindingID: claim.Lease.BindingID, EntryID: e.Id, Revision: e.Revision, LogicalPath: e.LogicalPath}
		sf, err := input.Open(ctx, ref)
		if err != nil {
			return book, nil, fmt.Errorf("open native sidecar: %w", err)
		}
		if err = nativePinnedEntry(sf, e); err != nil {
			if sf != nil {
				_ = sf.Close()
			}
			return book, nil, err
		}
		data := make([]byte, int(e.Size))
		err = readNativeEbookWindow(nativeEbookReaderAt{ctx: ctx, reader: sf}, data, 0)
		closeErr := sf.Close()
		if err != nil || closeErr != nil {
			return book, nil, errors.Join(err, closeErr)
		}
		if sidecar.opf {
			var metadata parsedEbook
			if err = parseEPUBOPFMetadata(data, &metadata); err != nil {
				return book, nil, err
			}
			metadata.sanitize()
			applyEbookSidecarMetadata(&book, metadata)
		} else {
			book.Cover = &parsedEbookCover{ContentType: ebookImageContentType(e.Name), Bytes: data}
		}
		refs = append(refs, e)
	}
	book.sanitize()
	if book.Title == "" {
		book.Title = ebookTitleFromPath(claim.Entry.Name)
	}
	return book, refs, nil
}

// nativeEbookLiteralParent preserves S3 key prefixes byte-for-byte, including
// double slashes, dot segments and leading slashes. Retaining the final slash
// also distinguishes an empty prefix from the slash-prefixed object root.
func nativeEbookLiteralParent(logicalPath string) string {
	if i := strings.LastIndexByte(logicalPath, '/'); i >= 0 {
		return logicalPath[:i+1]
	}
	return ""
}

// Native groups belong to one retained binding. Ordinary ebook keys always
// begin ebook:, so neither writer can select the other's cross-format group.
// Only ISBN and title/author semantics may ignore the literal object directory.
func nativeEbookContentGroupKey(bindingID uuid.UUID, book *parsedEbook, logicalPath string) string {
	key := ebookContentGroupKey(book, logicalPath)
	if strings.HasPrefix(key, "ebook:title:") {
		parentHash := sha256.Sum256([]byte(nativeEbookLiteralParent(logicalPath)))
		key = key[:strings.LastIndex(key, "|dir:")] + fmt.Sprintf("|dir-sha256:%x", parentHash)
	} else if key == "" || strings.HasPrefix(key, "ebook:path:") {
		key = fmt.Sprintf("ebook:path-sha256:%x", sha256.Sum256([]byte(logicalPath)))
	}
	return "bloem-native:" + bindingID.String() + ":" + key
}

func nativeEbookSidecarEligible(book, e *storagev1.Entry, opf, single bool) bool {
	parent := nativeEbookLiteralParent(e.LogicalPath)
	if nativeEbookLiteralParent(book.LogicalPath) != parent || e.LogicalPath[len(parent):] != e.Name {
		return false
	}
	stem := strings.ToLower(ebookTitleFromPath(book.Name))
	name := strings.ToLower(e.Name)
	if opf {
		return name == stem+".opf"
	}
	ext := strings.ToLower(path.Ext(name))
	valid := false
	for _, candidate := range sidecarCoverExtensions {
		if ext == candidate {
			valid = true
			break
		}
	}
	if !valid {
		return false
	}
	base := strings.TrimSuffix(name, ext)
	if base == stem {
		return true
	}
	if single {
		for _, candidate := range sidecarCoverNames {
			if base == candidate {
				return true
			}
		}
	}
	return false
}

type nativeEbookQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func nativeEbookIdentity(ctx context.Context, q nativeEbookQueryer, folderID int, location, groupKey, format string) (string, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT mf.content_id FROM media_files mf JOIN media_items mi ON mi.content_id=mf.content_id
 WHERE mf.media_folder_id=$1 AND mf.file_path=$2 AND mi.type='ebook'`, folderID, location).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	err = q.QueryRow(ctx, `SELECT mf.content_id FROM media_files mf JOIN media_items mi ON mi.content_id=mf.content_id
 WHERE mf.media_folder_id=$1 AND mf.group_key_version=$2 AND mf.content_group_key=$3 AND mf.missing_since IS NULL AND mi.type='ebook'
 AND NOT EXISTS(SELECT 1 FROM media_files dup WHERE dup.content_id=mf.content_id AND dup.missing_since IS NULL
 AND lower(dup.container)=lower($4) AND dup.file_path<>$5)
 ORDER BY CASE WHEN lower(trim(mi.status))='matched' THEN 0 ELSE 1 END,mf.id LIMIT 1`, folderID, ebookGroupKeyVersion, groupKey, format, location).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Scanner) prepareNativeEbook(ctx context.Context, claim storagesource.IngestionClaim, folder *models.MediaFolder, file mediasource.File, sidecars NativeEbookSidecars) (*nativeEbookPrepared, error) {
	if ctx == nil || s == nil || s.fileRepo == nil || s.itemRepo == nil || s.personRepo == nil || folder == nil || folder.ID <= 0 || !librarykind.IsEbook(folder.Type) {
		return nil, fmt.Errorf("native ebook publisher requires ebook folder and repositories")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	book, refs, err := parseNativeIngest(ctx, claim, file, sidecars)
	if err != nil {
		return nil, err
	}
	location, err := storagesource.CatalogLocation(claim.Lease.BindingID, claim.Entry.Id)
	if err != nil {
		return nil, err
	}
	p := &nativeEbookPrepared{book: book, location: location, groupKey: nativeEbookContentGroupKey(claim.Lease.BindingID, &book, claim.Entry.LogicalPath), sidecars: refs}
	p.existingID, err = nativeEbookIdentity(ctx, s.fileRepo.Pool(), folder.ID, location, p.groupKey, book.Format)
	if err != nil {
		return nil, err
	}
	p.contentID = p.existingID
	var existing *models.MediaItem
	if p.existingID != "" {
		existing, err = s.itemRepo.GetByID(ctx, p.existingID)
		if err != nil {
			return nil, err
		}
		p.itemVersion = existing.UpdatedAt
	} else {
		p.contentID, err = idgen.NextID()
		if err != nil {
			return nil, err
		}
	}
	if book.Cover == nil || len(book.Cover.Bytes) == 0 {
		return p, nil
	}
	if len(book.Cover.Bytes) > maxEPUBMetadataEntrySize {
		return nil, fmt.Errorf("native ebook cover exceeds bound")
	}
	if existing != nil && existing.PosterPath != "" {
		if ebookItemHasCuratedMetadata(existing) || !strings.HasPrefix(existing.PosterPath, localEbookPosterPrefix) {
			return p, nil
		}
		hash, err := imageutil.Thumbhash(book.Cover.Bytes)
		if err == nil && hash == existing.PosterThumbhash {
			return p, nil
		}
	}
	if s.imageCacher == nil {
		return nil, fmt.Errorf("native ebook cover cache required")
	}
	p.posterPath, p.posterHash, err = s.imageCacher.CacheEbookCover(ctx, book.Cover.Bytes, p.contentID)
	if err != nil {
		return nil, fmt.Errorf("cache native ebook cover: %w", err)
	}
	if !strings.HasPrefix(p.posterPath, localEbookPosterPrefix) || p.posterHash == "" {
		return nil, fmt.Errorf("invalid cached native ebook cover")
	}
	return p, nil
}

func (s *Scanner) publishPreparedNativeEbook(ctx context.Context, sources *storagesource.Repository, claim storagesource.IngestionClaim, folder *models.MediaFolder, p *nativeEbookPrepared) error {
	return sources.PublishIngestion(ctx, claim, s.nativeEbookPublication(sources, claim, folder, p))
}

// Both entrypoints share this SQL publication callback. Source/run/checkpoint
// and main-entry fences are held before it is invoked by either repository API.
func (s *Scanner) nativeEbookPublication(sources *storagesource.Repository, claim storagesource.IngestionClaim, folder *models.MediaFolder, p *nativeEbookPrepared) func(context.Context, pgx.Tx, *storagev1.Entry) error {
	return func(ctx context.Context, tx pgx.Tx, entry *storagev1.Entry) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", fmt.Sprintf("native-ebook-folder:%d", folder.ID)); err != nil {
			return err
		}
		// PublishIngestion already holds source/run/checkpoint/main-entry locks.
		// Lock binding before folder and catalog rows; AttachFileTx later needs
		// the same UPDATE lock, so do not acquire a weaker lock and upgrade it.
		var persistedFolderID int
		err := tx.QueryRow(ctx, `SELECT folder_id FROM bloem_storage_bindings
 WHERE id=$1 AND source_key=$2 AND folder_id=$3 FOR UPDATE`, claim.Lease.BindingID, claim.Lease.SourceKey, folder.ID).Scan(&persistedFolderID)
		if errors.Is(err, pgx.ErrNoRows) {
			return storagesource.ErrReferenceConflict
		}
		if err != nil {
			return err
		}
		// A foreign-key KEY SHARE lock does not prevent a non-key type update.
		// SHARE protects persisted kind through the entire publication commit.
		var persistedType string
		err = tx.QueryRow(ctx, "SELECT type FROM media_folders WHERE id=$1 FOR SHARE", persistedFolderID).Scan(&persistedType)
		if errors.Is(err, pgx.ErrNoRows) {
			return storagesource.ErrReferenceConflict
		}
		if err != nil {
			return err
		}
		if !librarykind.IsEbook(persistedType) {
			return storagesource.ErrReferenceConflict
		}
		for _, e := range p.sidecars {
			var current storagev1.Entry
			err := tx.QueryRow(ctx, `SELECT entry_id,name,logical_path,kind,size,modified_unix_nano,revision FROM bloem_storage_entries
 WHERE source_key=$1 AND last_seen_run=$2 AND configuration_revision=$3 AND entry_id=$4 FOR UPDATE`, claim.Lease.SourceKey, claim.Lease.RunID, claim.Lease.ConfigurationRevision, e.Id).Scan(&current.Id, &current.Name, &current.LogicalPath, &current.Kind, &current.Size, &current.ModifiedUnixNano, &current.Revision)
			if errors.Is(err, pgx.ErrNoRows) {
				return storagesource.ErrCheckpointConflict
			}
			if err != nil {
				return err
			}
			if !proto.Equal(&current, e) {
				return storagesource.ErrCheckpointConflict
			}
		}
		currentID, err := nativeEbookIdentity(ctx, tx, folder.ID, p.location, p.groupKey, p.book.Format)
		if err != nil {
			return err
		}
		if currentID != p.existingID {
			return ErrNativeEbookIdentityChanged
		}
		item := &models.MediaItem{ContentID: p.contentID, Type: "ebook", Status: "pending", Title: p.book.Title, SortTitle: titleutil.DeriveDefaultSortTitle(p.book.Title)}
		if currentID != "" {
			var version time.Time
			if err = tx.QueryRow(ctx, "SELECT updated_at FROM media_items WHERE content_id=$1 FOR UPDATE", currentID).Scan(&version); err != nil {
				return err
			}
			if !version.Equal(p.itemVersion) {
				return ErrNativeEbookIdentityChanged
			}
			// Recheck group after the item lock, including a format added while waiting.
			id, err := nativeEbookIdentity(ctx, tx, folder.ID, p.location, p.groupKey, p.book.Format)
			if err != nil {
				return err
			}
			if id != currentID {
				return ErrNativeEbookIdentityChanged
			}
			item, err = s.itemRepo.GetByIDTx(ctx, tx, currentID)
			if err != nil {
				return err
			}
		}
		curated := ebookItemHasCuratedMetadata(item)
		if !curated {
			applyEbookToMediaItem(item, &p.book)
			if item.SortTitle == "" {
				item.SortTitle = titleutil.DeriveDefaultSortTitle(item.Title)
			}
		}
		if p.posterPath != "" {
			if item.PosterPath != "" && (curated || !strings.HasPrefix(item.PosterPath, localEbookPosterPrefix)) {
				return ErrNativeEbookIdentityChanged
			}
			item.PosterPath = p.posterPath
			item.PosterThumbhash = p.posterHash
		}
		if err = s.itemRepo.UpsertTx(ctx, tx, item); err != nil {
			return err
		}
		if err = s.publishNativeEbookPeople(ctx, tx, item.ContentID, &p.book, curated); err != nil {
			return err
		}
		if err = publishNativeEbookSeries(ctx, tx, item.ContentID, &p.book, curated); err != nil {
			return err
		}
		if err = insertEbookLibraryMembership(ctx, tx, item.ContentID, folder.ID); err != nil {
			return err
		}
		if err = publishNativeEbookISBN(ctx, tx, item.ContentID, p.book.ISBN, curated); err != nil {
			return err
		}
		modified := normalizeFileModifiedAt(time.Unix(0, entry.ModifiedUnixNano))
		mf := buildEbookMediaFile(folder, item.ContentID, p.location, entry.Size, modified, &p.book, p.groupKey)
		mf.ProbeSource = "native"
		saved, err := s.fileRepo.UpsertTx(ctx, tx, mf)
		if err != nil {
			return err
		}
		return sources.AttachFileTx(ctx, tx, saved.ID, storagesource.PersistedRef{BindingID: claim.Lease.BindingID, EntryID: entry.Id, Revision: entry.Revision, LogicalPath: entry.LogicalPath})
	}
}

func (s *Scanner) publishNativeEbookPeople(ctx context.Context, tx pgx.Tx, id string, book *parsedEbook, curated bool) error {
	if len(book.Authors) == 0 {
		return nil
	}
	existing, err := s.itemRepo.NativeEbookPeopleTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if !ebookPeopleWriteAllowed(curated, existing) {
		return nil
	}
	desired := make([]ebookCredit, 0, len(book.Authors))
	for _, name := range book.Authors {
		desired = append(desired, ebookCredit{Name: name, Kind: models.PersonKindAuthor})
	}
	if ebookPeopleCreditsEqual(existing, desired) {
		return nil
	}
	names := append([]string(nil), book.Authors...)
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	resolved := map[string]int64{}
	for _, name := range names {
		key := strings.ToLower(name)
		if _, ok := resolved[key]; ok {
			continue
		}
		pid, err := s.personRepo.FindNativeEbookAuthorTx(ctx, tx, name)
		if err != nil {
			return err
		}
		resolved[key] = pid
	}
	authors := make([]ebookResolvedAuthor, 0, len(book.Authors))
	for _, name := range book.Authors {
		authors = append(authors, ebookResolvedAuthor{ID: resolved[strings.ToLower(name)], Name: name})
	}
	return s.itemRepo.ReplaceNativeEbookPeopleTx(ctx, tx, id, mergeEbookPeople(existing, authors))
}
func publishNativeEbookSeries(ctx context.Context, tx pgx.Tx, id string, book *parsedEbook, curated bool) error {
	var name *string
	var index *float64
	err := tx.QueryRow(ctx, "SELECT series_name,series_index FROM ebook_series WHERE content_id=$1", id).Scan(&name, &index)
	plan, err := planEbookSeriesWrite(book, name, index, err, curated)
	if err != nil {
		return err
	}
	switch plan.Kind {
	case ebookSeriesWriteNone:
		return nil
	case ebookSeriesWriteDelete:
		_, err = tx.Exec(ctx, "DELETE FROM ebook_series WHERE content_id=$1", id)
	case ebookSeriesWriteUpsert:
		_, err = tx.Exec(ctx, `INSERT INTO ebook_series(content_id,series_name,series_index,updated_at) VALUES($1,$2,$3,now())
 ON CONFLICT(content_id) DO UPDATE SET series_name=EXCLUDED.series_name,series_index=EXCLUDED.series_index,updated_at=now()`, id, plan.Name, plan.Index)
	default:
		return fmt.Errorf("unknown ebook series plan")
	}
	return err
}
func publishNativeEbookISBN(ctx context.Context, tx pgx.Tx, id, isbn string, curated bool) error {
	if isbn == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "native-ebook-isbn:"+isbn); err != nil {
		return err
	}
	var occupied bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_item_provider_ids WHERE provider='isbn' AND
 ((provider_id=$2 AND content_id<>$1) OR ($3 AND content_id=$1)))`, id, isbn, curated).Scan(&occupied); err != nil {
		return err
	}
	if occupied {
		return nil
	}
	// The local ISBN helper deliberately tolerates another item owning this ISBN.
	// Its swallowed uniqueness error must not poison the surrounding transaction.
	if _, err := tx.Exec(ctx, "SAVEPOINT native_ebook_isbn"); err != nil {
		return err
	}
	err := insertEbookISBNProviderID(ctx, tx, id, isbn)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "RELEASE SAVEPOINT native_ebook_isbn"); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "25P02" {
			return err
		}
		if _, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT native_ebook_isbn"); rollbackErr != nil {
			return rollbackErr
		}
		// A helper-suppressed 23505 is the only expected aborted transaction here.
		_, releaseErr := tx.Exec(ctx, "RELEASE SAVEPOINT native_ebook_isbn")
		return releaseErr
	}
	return nil
}
