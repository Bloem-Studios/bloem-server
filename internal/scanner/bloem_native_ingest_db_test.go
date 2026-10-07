//go:build integration

package scanner

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/sections"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CURRENT default schema, guarded caller pool and actual B lifecycle commands.
func nativeIngestDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := nativeAdmissionDatabase(t)
	return pool
}
func nativeIngestSQL(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

type nativeIngestFixture struct {
	s       *Scanner
	r       *storagesource.Repository
	pool    *pgxpool.Pool
	folder  *models.MediaFolder
	source  storagesource.SourceConfig
	binding storagesource.Binding
	claim   storagesource.IngestionClaim
	file    *nativeTestFile
}

func nativeIngestSetup(t *testing.T) *nativeIngestFixture {
	t.Helper()
	pool := nativeIngestDatabase(t)
	source, binding, folder := nativeCurrentLifecycle(t, pool)
	r := storagesource.NewRepository(pool)
	_, file := nativeIngestInput(t)
	x := &nativeIngestFixture{s: NewScanner(NewFileRepository(pool), "", nil, 1, false, 0), r: r, pool: pool, folder: folder, source: source, binding: binding, file: file}
	x.discover(t, "v1")
	return x
}
func (x *nativeIngestFixture) discover(t *testing.T, revision string) {
	t.Helper()
	ctx := context.Background()
	run, err := x.r.Begin(ctx, x.source.Key, "task4-discovery", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cp, ok, err := x.r.NextDirectory(ctx, run)
	if err != nil || !ok {
		t.Fatal(err)
	}
	x.file.info.Revision = revision
	e := &storagev1.Entry{Id: "book", Name: x.file.info.Name, LogicalPath: x.file.info.LogicalPath, Revision: revision, Size: x.file.info.Size, Kind: storagev1.EntryKind_ENTRY_KIND_FILE}
	if err = x.r.ApplyPage(ctx, run, cp, &storagev1.ListResponse{Entries: []*storagev1.Entry{e}, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err = x.r.Complete(ctx, run); err != nil {
		t.Fatal(err)
	}
	x.source, err = x.r.Source(ctx, x.source.Key)
	if err != nil {
		t.Fatal("reload actual source after discovery/config revision", err)
	}
	lease, err := x.r.BeginIngestion(ctx, run.RunID, x.binding.ID, "task4-ingestion", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	x.claim, ok, err = x.r.NextIngestion(ctx, lease)
	if err != nil || !ok {
		t.Fatal(err)
	}
}
func (x *nativeIngestFixture) publish(t *testing.T) string {
	t.Helper()
	id, err := x.s.PublishAuthorizedNativeEbook(t.Context(), x.r, x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true}, x.authorize)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (x *nativeIngestFixture) pending(t *testing.T) {
	t.Helper()
	c, ok, err := x.r.NextIngestion(context.Background(), x.claim.Lease)
	if err != nil || !ok || c.Token != x.claim.Token {
		t.Fatalf("claim advanced: %v", err)
	}
}
func (x *nativeIngestFixture) empty(t *testing.T) {
	t.Helper()
	var n int
	if err := x.pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM media_files WHERE media_folder_id=$1)+(SELECT count(*) FROM media_item_libraries WHERE media_folder_id=$1)+(SELECT count(*) FROM bloem_storage_file_refs WHERE binding_id=$2)", x.folder.ID, x.binding.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial catalog publication: %d %v", n, err)
	}
}

func TestNativeIngestAtomicPublicationAndRevisionDB(t *testing.T) {
	x := nativeIngestSetup(t)
	ctx := context.Background()
	nativeIngestSQL(t, x.pool, "ALTER TABLE bloem_storage_ingestion ADD CONSTRAINT task4_late CHECK(last_entry_id='')")
	if _, err := x.s.PublishAuthorizedNativeEbook(ctx, x.r, x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true}, x.authorize); err == nil {
		t.Fatal("late failure absent")
	}
	x.empty(t)
	x.pending(t)
	var items int
	if err := x.pool.QueryRow(ctx, "SELECT count(*) FROM media_items WHERE title='The Test Ebook'").Scan(&items); err != nil || items != 0 {
		t.Fatalf("metadata escaped rollback %d %v", items, err)
	}
	nativeIngestSQL(t, x.pool, "ALTER TABLE bloem_storage_ingestion DROP CONSTRAINT task4_late")
	id := x.publish(t)
	var fileID int
	var location, root, container, probe string
	if err := x.pool.QueryRow(ctx, "SELECT id,file_path,canonical_root_path,container,probe_source FROM media_files WHERE media_folder_id=$1", x.folder.ID).Scan(&fileID, &location, &root, &container, &probe); err != nil {
		t.Fatal(err)
	}
	expected, _ := storagesource.CatalogLocation(x.binding.ID, "book")
	if location != expected || root != expected || container != "epub" || probe != "native" {
		t.Fatalf("wrong location/format %s %s %s %s", location, root, container, probe)
	}
	item, err := x.s.itemRepo.GetByID(ctx, id)
	if err != nil || item.Title != "The Test Ebook" || item.Year == 0 || item.OriginalLanguage == "" {
		t.Fatalf("metadata %+v %v", item, err)
	}
	var authorCount int
	if err = x.pool.QueryRow(ctx, "SELECT count(*) FROM item_people WHERE content_id=$1 AND kind=7", id).Scan(&authorCount); err != nil || authorCount == 0 {
		t.Fatalf("authors missing %v", err)
	}
	reader, err := auth.NewUserRepository(x.pool).Create(ctx, models.CreateUserInput{Username: "a-current-reader", Email: "a-current-reader@example.test", Password: "a-current-reader-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := pgstore.NewPostgresProvider(x.pool).ForUser(ctx, reader.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile := uuid.NewString()
	if err = store.CreateProfile(ctx, userstore.Profile{ID: profile, Name: "Current retained progress"}); err != nil {
		t.Fatal(err)
	}
	nativeIngestSQL(t, x.pool, "INSERT INTO ebook_reader_progress(user_id,profile_id,content_id,file_id,location,progress) VALUES($1,$2,$3,$4,'chapter-2',0.4)", reader.ID, profile, id, fileID)
	nativeIngestSQL(t, x.pool, "UPDATE media_items SET title='Curated',status='matched',poster_path='manual/cover.jpg' WHERE content_id=$1", id)
	nativeIngestSQL(t, x.pool, "INSERT INTO ebook_series(content_id,series_name,series_index) VALUES($1,'Curated Series',7) ON CONFLICT(content_id) DO UPDATE SET series_name='Curated Series',series_index=7", id)
	var originalAuthors string
	if err = x.pool.QueryRow(ctx, "SELECT string_agg(person_id::text,',' ORDER BY person_id) FROM item_people WHERE content_id=$1 AND kind=7", id).Scan(&originalAuthors); err != nil {
		t.Fatal(err)
	}
	changed, err := os.ReadFile(writeTestEPUBWithOPFBytes(t, []byte(`<package><metadata><title>Changed Embedded Title</title><creator>Different Embedded Author</creator><language>fr</language><date>2026-02-03</date><identifier>ISBN: 9780140328721</identifier><meta name="calibre:series" content="Changed Embedded Series"/><meta name="calibre:series_index" content="1"/></metadata></package>`)))
	if err != nil {
		t.Fatal(err)
	}
	x.file.Reader = bytes.NewReader(changed)
	x.file.info.Size = int64(len(changed))
	// Configuration/credential replacement preserves the retained binding and
	// reserved file location; revised metadata must resolve that location first.
	nativeIngestSQL(t, x.pool, "UPDATE bloem_storage_sources SET configuration_revision=2 WHERE key=$1", x.source.Key)
	x.discover(t, "v2")
	restarted := storagesource.NewRepository(x.pool)
	lease, err := restarted.BeginIngestion(ctx, x.claim.Lease.RunID, x.binding.ID, "task4-ingestion", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var ok bool
	x.claim, ok, err = restarted.NextIngestion(ctx, lease)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got := x.publish(t); got != id {
		t.Fatal("catalog id changed")
	}
	var savedFile int
	var progress float64
	var title, series, poster string
	if err = x.pool.QueryRow(ctx, "SELECT p.file_id,p.progress,i.title,s.series_name,i.poster_path FROM ebook_reader_progress p JOIN media_items i USING(content_id) JOIN ebook_series s USING(content_id) WHERE p.content_id=$1", id).Scan(&savedFile, &progress, &title, &series, &poster); err != nil || savedFile != fileID || progress != 0.4 || title != "Curated" || series != "Curated Series" || poster != "manual/cover.jpg" {
		t.Fatalf("curation/progress lost %v", err)
	}
	restored, err := x.s.itemRepo.GetByID(ctx, id)
	if err != nil || restored.Year != 2024 || restored.OriginalLanguage != "en" {
		t.Fatalf("curated scalar metadata changed: %+v %v", restored, err)
	}
	var savedAuthors, isbn string
	if err = x.pool.QueryRow(ctx, "SELECT string_agg(person_id::text,',' ORDER BY person_id) FROM item_people WHERE content_id=$1 AND kind=7", id).Scan(&savedAuthors); err != nil || savedAuthors != originalAuthors {
		t.Fatalf("curated author IDs changed: %s/%s %v", originalAuthors, savedAuthors, err)
	}
	if err = x.pool.QueryRow(ctx, "SELECT provider_id FROM media_item_provider_ids WHERE content_id=$1 AND provider='isbn'", id).Scan(&isbn); err != nil || isbn != "9780306406157" {
		t.Fatalf("curated ISBN changed: %s %v", isbn, err)
	}

}

func TestNativeIngestFailureAndFencingDB(t *testing.T) {
	for _, mode := range []string{"parse", "cover", "binding", "revision", "identity"} {
		t.Run(mode, func(t *testing.T) {
			x := nativeIngestSetup(t)
			ctx := context.Background()
			switch mode {
			case "parse":
				x.file.failure = errors.New("provider outage")
			case "cover":
				data, err := os.ReadFile(writeTestEPUBWithCover(t, "cover.jpg", "image/jpeg", []byte("not-an-image")))
				if err != nil {
					t.Fatal(err)
				}
				x.file.Reader = bytes.NewReader(data)
				x.file.info.Size = int64(len(data))
				x.discover(t, "cover-v1")
				x.s.imageCacher = nativeIngestFailCacher{check: func() {
					tx, err := x.pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = tx.Rollback(ctx) }()
					if _, err = tx.Exec(ctx, "SELECT key FROM bloem_storage_sources WHERE key=$1 FOR UPDATE NOWAIT", x.source.Key); err != nil {
						t.Fatalf("cover I/O under source SQL lock: %v", err)
					}
				}}
			case "binding":
				x.claim.Lease.BindingID = uuid.New()
			case "revision":
				nativeIngestSQL(t, x.pool, "UPDATE bloem_storage_entries SET revision='new' WHERE source_key=$1 AND entry_id='book'", x.source.Key)
			case "identity":
				id := x.publish(t)
				x.discover(t, "v2")
				prepared, err := x.s.prepareNativeEbook(ctx, x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true})
				if err != nil {
					t.Fatal(err)
				}
				nativeIngestSQL(t, x.pool, "UPDATE media_items SET title='Concurrent Curation',status='matched',updated_at=clock_timestamp() WHERE content_id=$1", id)
				err = x.publishPreparedAuthorized(ctx, prepared)
				var typed *catalog.NativeOnboardingError
				if !errors.As(err, &typed) || typed.Code != "native_storage_unavailable" {
					t.Fatalf("changed prepared identity must refuse current permit: %T %v", err, err)
				}
				x.pending(t)
				return
			}
			if _, err := x.s.PublishAuthorizedNativeEbook(ctx, x.r, x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true}, x.authorize); err == nil {
				t.Fatal("failure published")
			}
			x.empty(t)
			if mode != "binding" && mode != "revision" {
				x.pending(t)
			}
		})
	}
}

type nativeIngestFailCacher struct{ check func() }

func (c nativeIngestFailCacher) CacheEbookCover(context.Context, []byte, string) (string, string, error) {
	if c.check != nil {
		c.check()
	}
	return "", "", errors.New("cover store unavailable")
}
func (nativeIngestFailCacher) CacheAudiobookCover(context.Context, []byte, string) (string, string, error) {
	return "", "", errors.New("cover store unavailable")
}

func nativeIngestClaimEntry(t *testing.T, x *nativeIngestFixture, id string) {
	t.Helper()
	ctx := context.Background()
	run, err := x.r.Begin(ctx, x.source.Key, "task4-discovery", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cp, ok, err := x.r.NextDirectory(ctx, run)
	if err != nil || !ok {
		t.Fatal(err)
	}
	e := &storagev1.Entry{Id: id, Name: x.file.info.Name, LogicalPath: x.file.info.LogicalPath, Revision: x.file.info.Revision, Size: x.file.info.Size, Kind: storagev1.EntryKind_ENTRY_KIND_FILE}
	if err = x.r.ApplyPage(ctx, run, cp, &storagev1.ListResponse{Entries: []*storagev1.Entry{e}, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err = x.r.Complete(ctx, run); err != nil {
		t.Fatal(err)
	}
	x.source, err = x.r.Source(ctx, x.source.Key)
	if err != nil {
		t.Fatal("reload actual source after discovery/config revision", err)
	}
	lease, err := x.r.BeginIngestion(ctx, run.RunID, x.binding.ID, "task4-ingestion", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	x.claim, ok, err = x.r.NextIngestion(ctx, lease)
	if err != nil || !ok {
		t.Fatal(err)
	}
}

func TestNativeIngestPDFGroupingAndSameFormatCollisionDB(t *testing.T) {
	x := nativeIngestSetup(t)
	ctx := context.Background()
	epubID := x.publish(t)
	data := completePDFMetadataFixture([]byte("%PDF-1.7\n1 0 obj\n<< /Title (The Test Ebook) /Author (Ada Writer; Ben Author) /ISBN (9780306406157) /Subject (Useful PDF description) /Keywords (Fiction, Adventure) /CreationDate (D:20240310000000Z) >>\nendobj\n"))
	x.file = nativeBytes("book.pdf", data)
	x.file.info.Revision = "pdf-v1"
	x.file.info.LogicalPath = "Books/book.pdf"
	nativeIngestClaimEntry(t, x, "pdf")
	if id := x.publish(t); id != epubID {
		t.Fatalf("cross-format grouping lost %s/%s", epubID, id)
	}
	var count int
	if err := x.pool.QueryRow(ctx, "SELECT count(*) FROM media_files WHERE content_id=$1", epubID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("formats missing: %d %v", count, err)
	}
	var container string
	if err := x.pool.QueryRow(ctx, "SELECT f.container FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.binding_id=$1 AND r.entry_id='pdf'", x.binding.ID).Scan(&container); err != nil || container != "pdf" {
		t.Fatalf("opaque PDF format lost: %v", err)
	}
	_, x.file = nativeIngestInput(t)
	x.file.info.Name = "copy.epub"
	x.file.info.LogicalPath = "Books/copy.epub"
	nativeIngestClaimEntry(t, x, "copy")
	if id := x.publish(t); id == epubID {
		t.Fatal("same-format collision merged a distinct object")
	}
}

func TestNativeIngestSidecarPublicationAndRevisionFenceDB(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "metadata", true: "stale"}[stale], func(t *testing.T) {
			x := nativeIngestSetup(t)
			ctx := context.Background()
			opf := []byte(`<package><metadata><title>Native Sidecar Title</title><creator>Native Sidecar Author</creator><language>nl</language><date>2025-01-02</date><meta name="calibre:series" content="Sidecar Series"/><meta name="calibre:series_index" content="4"/></metadata></package>`)
			e := &storagev1.Entry{Id: "sidecar", Name: "book.opf", LogicalPath: "Books/book.opf", Revision: "opf-v1", Size: int64(len(opf)), Kind: storagev1.EntryKind_ENTRY_KIND_FILE}
			nativeIngestSQL(t, x.pool, `INSERT INTO bloem_storage_entries(source_key,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,last_seen_run) VALUES($1,$2,$3,$4,1,$5,0,$6,$7,$8)`, x.source.Key, e.Id, e.Name, e.LogicalPath, e.Size, e.Revision, x.claim.Lease.ConfigurationRevision, x.claim.Lease.RunID)
			sidecars := NativeEbookSidecars{Complete: true, OPF: e, Open: func(context.Context, storagesource.PersistedRef) (mediasource.File, error) {
				f := nativeBytes(e.Name, opf)
				f.info.Revision = e.Revision
				f.info.LogicalPath = e.LogicalPath
				return f, nil
			}}
			prepared, err := x.s.prepareNativeEbook(ctx, x.claim, x.folder, x.file, sidecars)
			if err != nil {
				t.Fatal(err)
			}
			if stale {
				nativeIngestSQL(t, x.pool, "UPDATE bloem_storage_entries SET revision='opf-v2' WHERE source_key=$1 AND entry_id='sidecar'", x.source.Key)
			}
			err = x.publishPreparedAuthorized(ctx, prepared)
			if stale {
				var typed *catalog.NativeOnboardingError
				if !errors.As(err, &typed) || typed.Code != "native_storage_unavailable" {
					t.Fatalf("stale sidecar must refuse current permit: %T %v", err, err)
				}
				x.empty(t)
				x.pending(t)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			item, err := x.s.itemRepo.GetByID(ctx, prepared.contentID)
			if err != nil || item.Title != "Native Sidecar Title" || item.Year != 2025 || item.OriginalLanguage != "nl" {
				t.Fatalf("sidecar metadata %+v %v", item, err)
			}
			var author, series string
			var index float64
			if err = x.pool.QueryRow(ctx, "SELECT p.name,s.series_name,s.series_index FROM item_people ip JOIN people p ON p.id=ip.person_id JOIN ebook_series s ON s.content_id=ip.content_id WHERE ip.content_id=$1 AND ip.kind=7", prepared.contentID).Scan(&author, &series, &index); err != nil || author != "Native Sidecar Author" || series != "Sidecar Series" || index != 4 {
				t.Fatalf("sidecar credits/series %s/%s/%v %v", author, series, index, err)
			}
		})
	}
}

func TestNativeIngestFolderAliasesDB(t *testing.T) {
	for _, kind := range []string{" ebook ", "EBooks"} {
		t.Run(kind, func(t *testing.T) {
			x := nativeIngestSetup(t)
			_, err := x.pool.Exec(t.Context(), "UPDATE media_folders SET type=$2 WHERE id=$1", x.folder.ID, kind)
			currentAdmissionState(t, err, "BN001")
			// Parser/model aliases remain independent of immutable durable kind.
			x.folder.Type = kind
			x.publish(t)
		})
	}
}

func TestNativeIngestUnknownSidecarsLeaveClaimRetryableDB(t *testing.T) {
	x := nativeIngestSetup(t)
	if _, err := x.s.PublishAuthorizedNativeEbook(context.Background(), x.r, x.claim, x.folder, x.file, NativeEbookSidecars{}, x.authorize); !errors.Is(err, ErrNativeEbookSidecarsIncomplete) {
		t.Fatalf("unknown discovery did not fail closed: %v", err)
	}
	x.empty(t)
	x.pending(t)
}

// The marked durable type is immutable before and after real publication.
func TestNativeIngestFolderTypePublicationFenceDB(t *testing.T) {
	x := nativeIngestSetup(t)
	for _, when := range []string{"beforePublication", "afterPublication"} {
		t.Run(when, func(t *testing.T) {
			if when == "afterPublication" {
				x.publish(t)
			}
			_, err := x.pool.Exec(t.Context(), "UPDATE media_folders SET type='movies' WHERE id=$1", x.folder.ID)
			currentAdmissionState(t, err, "BN001")
			var kind string
			if err = x.pool.QueryRow(t.Context(), "SELECT type FROM media_folders WHERE id=$1", x.folder.ID).Scan(&kind); err != nil || kind != "ebook" {
				t.Fatal("marked type changed", err)
			}
		})
	}
}

func nativeIngestWaitForBlocker(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blockerPID int) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)))", blockerPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("publisher never reached SQL barrier")
		case <-ticker.C:
		}
	}
}

// Native and ordinary writers select their groups before either file commits.
// Removing the native namespace makes the ordinary writer select the native PDF
// and commit a second EPUB into that group after the paused native write.
func TestNativeLocalWriterGroupingPreventionDB(t *testing.T) {
	x := nativeIngestSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Start with a PDF group, then prepare a native EPUB joining that group.
	data := completePDFMetadataFixture([]byte("%PDF-1.7\n1 0 obj\n<< /Title (The Test Ebook) /Author (Ada Writer; Ben Author) /ISBN (9780306406157) >>\nendobj\n"))
	x.file = nativeBytes("book.pdf", data)
	x.file.info.Revision = "pdf-v1"
	x.file.info.LogicalPath = "Books/book.pdf"
	nativeIngestClaimEntry(t, x, "pdf")
	id := x.publish(t)
	_, x.file = nativeIngestInput(t)
	nativeIngestClaimEntry(t, x, "epub")
	// Pause after native group/item validation, before its media-file insert.
	nativeIngestSQL(t, x.pool, `CREATE FUNCTION task4_pause_native_file() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.probe_source='native' THEN PERFORM pg_advisory_xact_lock(6100605); END IF; RETURN NEW; END $$;
 CREATE TRIGGER task4_pause_native_file BEFORE INSERT ON media_files
 FOR EACH ROW EXECUTE FUNCTION task4_pause_native_file();
 CREATE FUNCTION task4_pause_local_item() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF current_setting('application_name')='task4-local-writer' THEN PERFORM pg_advisory_xact_lock(6100606); END IF; RETURN NEW; END $$;
 CREATE TRIGGER task4_pause_local_item BEFORE INSERT ON media_items
 FOR EACH ROW EXECUTE FUNCTION task4_pause_local_item()`)
	barrier, err := x.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = barrier.Rollback(context.Background()) }()
	if _, err = barrier.Exec(ctx, "SELECT pg_advisory_xact_lock(6100605)"); err != nil {
		t.Fatal(err)
	}
	var barrierPID int
	if err = barrier.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&barrierPID); err != nil {
		t.Fatal(err)
	}
	nativeDone := make(chan error, 1)
	go func() {
		_, err := x.s.PublishAuthorizedNativeEbook(ctx, x.r, x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true}, x.authorize)
		nativeDone <- err
	}()
	nativeIngestWaitForBlocker(t, ctx, x.pool, barrierPID)
	// The local trigger is BEFORE its item upsert's row-lock wait, but after its
	// real group lookup. It works with both the old unsafe choice and the fixed
	// independent item; the local writer must no longer wait on the native item.
	localBarrier, err := x.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = localBarrier.Rollback(context.Background()) }()
	if _, err = localBarrier.Exec(ctx, "SELECT pg_advisory_xact_lock(6100606)"); err != nil {
		t.Fatal(err)
	}
	var localBarrierPID int
	if err = localBarrier.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&localBarrierPID); err != nil {
		t.Fatal(err)
	}
	localConfig := x.pool.Config()
	localConfig.ConnConfig.RuntimeParams["application_name"] = "task4-local-writer"
	localPool, err := pgxpool.NewWithConfig(ctx, localConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer localPool.Close()
	localScanner := &Scanner{fileRepo: NewFileRepository(localPool), itemRepo: catalog.NewItemRepository(localPool), personRepo: catalog.NewPersonRepository(localPool)}
	localPath := writeTestEPUB(t, []string{"ISBN: 978-0-306-40615-7"})
	localDone := make(chan error, 1)
	go func() {
		var skipped int64
		localDone <- localScanner.reconcileEbookFile(ctx, x.folder, localPath, &skipped, newEbookGroupLocks())
	}()
	nativeIngestWaitForBlocker(t, ctx, x.pool, localBarrierPID)
	if err = barrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-nativeDone; err != nil {
		t.Fatalf("native publication: %v", err)
	}
	if err = localBarrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-localDone; err != nil {
		t.Fatalf("local reconciliation: %v", err)
	}
	var epubCount, nativeCount, localCount int
	if err = x.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE probe_source='native'),count(*) FILTER(WHERE file_path=$2)
 FROM media_files WHERE content_id=$1 AND lower(container)='epub' AND missing_since IS NULL`, id, localPath).Scan(&epubCount, &nativeCount, &localCount); err != nil {
		t.Fatal(err)
	}
	if epubCount != 1 || nativeCount != 1 || localCount != 0 {
		t.Fatalf("native/local grouping collision: epubs=%d native=%d local=%d", epubCount, nativeCount, localCount)
	}
	var localID, localKey, nativeKey string
	if err = x.pool.QueryRow(ctx, "SELECT content_id,content_group_key FROM media_files WHERE media_folder_id=$1 AND file_path=$2", x.folder.ID, localPath).Scan(&localID, &localKey); err != nil {
		t.Fatal(err)
	}
	if err = x.pool.QueryRow(ctx, "SELECT content_group_key FROM media_files WHERE content_id=$1 LIMIT 1", id).Scan(&nativeKey); err != nil {
		t.Fatal(err)
	}
	if localID == id || localKey != "ebook:isbn:9780306406157" || nativeKey != "bloem-native:"+x.binding.ID.String()+":ebook:isbn:9780306406157" {
		t.Fatalf("writer identities intersect: local=%s/%s native=%s/%s", localID, localKey, id, nativeKey)
	}
	t.Log("PREVENTED: native PDF/EPUB group and ordinary EPUB remain independent")
}

// Equal metadata in another retained binding must not join the first binding's
// item in a separately initialized library; a second source into the first folder refuses. A distinct semantic key must not join either.
func TestNativeIngestBindingAndSemanticGroupIsolationDB(t *testing.T) {
	x := nativeIngestSetup(t)
	ctx := context.Background()
	firstID := x.publish(t)
	secondSource, secondBinding, secondFolder := nativeCurrentLifecycle(t, x.pool)
	actor, _ := nativeCurrentActor(t, x.pool)
	libraries := nativestorage.NewLibraryManagement(x.pool, catalog.NewFolderRepository(x.pool), sections.NewRepository(x.pool), resourcetenancy.NewStore(x.pool), nil)
	_, err := libraries.Bind(ctx, actor, secondSource.Key, x.folder.ID, secondSource.ConfigurationRevision, 3)
	if err == nil {
		t.Fatal("second source bound into already bound marked library")
	}
	y := &nativeIngestFixture{s: x.s, r: x.r, pool: x.pool, folder: secondFolder, source: secondSource, binding: secondBinding}
	y.file = nativeBytes("book.pdf", completePDFMetadataFixture([]byte("%PDF-1.7\n1 0 obj\n<< /Title (The Test Ebook) /Author (Ada Writer; Ben Author) /ISBN (9780306406157) >>\nendobj\n")))
	y.file.info.Revision = "pdf-v1"
	y.file.info.LogicalPath = "Books/book.pdf"
	nativeIngestClaimEntry(t, y, "pdf")
	secondID := y.publish(t)
	if secondID == firstID {
		t.Fatal("cross-binding metadata collision merged items")
	}
	var firstKey, secondKey string
	if err = x.pool.QueryRow(ctx, "SELECT content_group_key FROM media_files WHERE content_id=$1", firstID).Scan(&firstKey); err != nil {
		t.Fatal(err)
	}
	if err = x.pool.QueryRow(ctx, "SELECT content_group_key FROM media_files WHERE content_id=$1", secondID).Scan(&secondKey); err != nil {
		t.Fatal(err)
	}
	if firstKey != "bloem-native:"+x.binding.ID.String()+":ebook:isbn:9780306406157" || secondKey != "bloem-native:"+secondBinding.ID.String()+":ebook:isbn:9780306406157" {
		t.Fatalf("binding identity missing from group keys: %s / %s", firstKey, secondKey)
	}
	x.file = nativeBytes("different.pdf", completePDFMetadataFixture([]byte("%PDF-1.7\n1 0 obj\n<< /Title (The Test Ebook) /Author (Ada Writer; Ben Author) /ISBN (9780140328721) >>\nendobj\n")))
	x.file.info.Revision = "different-v1"
	x.file.info.LogicalPath = "Books/different.pdf"
	nativeIngestClaimEntry(t, x, "different")
	if id := x.publish(t); id == firstID || id == secondID {
		t.Fatal("different semantic group key merged items")
	}
}

// Authorless title fallback must distinguish literal parents, including paths
// that filesystem normalization would collapse or identity normalization folds.
func TestNativeIngestSparseGroupsPreserveLiteralDirectoriesDB(t *testing.T) {
	x := nativeIngestSetup(t)
	ctx := context.Background()
	epub, err := os.ReadFile(writeTestEPUBWithOPFBytes(t, []byte("<package><metadata><title>Sparse Title</title></metadata></package>")))
	if err != nil {
		t.Fatal(err)
	}
	pdf := completePDFMetadataFixture([]byte("%PDF-1.7\n1 0 obj\n<< /Title (Sparse Title) >>\nendobj\n"))
	seen := map[string]string{}
	for index, parent := range []string{"Books/", "Books//", "Books/../Shelf/", "Shelf/", "Books/./", "books/", "", "/"} {
		var epubID string
		for _, format := range []string{"epub", "pdf"} {
			data := epub
			if format == "pdf" {
				data = pdf
			}
			x.file = nativeBytes("book."+format, data)
			x.file.info.Revision = "v1"
			x.file.info.LogicalPath = parent + "book." + format
			nativeIngestClaimEntry(t, x, fmt.Sprintf("sparse-%d-%s", index, format))
			id := x.publish(t)
			if format == "epub" {
				for previous, otherID := range seen {
					if id == otherID {
						t.Fatalf("literal directories merged: %q / %q", previous, parent)
					}
				}
				epubID = id
				seen[parent] = id
			} else if id != epubID {
				t.Fatalf("same literal directory lost multi-format grouping: %q", parent)
			}
		}
	}
	var files, items int
	if err = x.pool.QueryRow(ctx, "SELECT count(*),count(DISTINCT content_id) FROM media_files WHERE media_folder_id=$1", x.folder.ID).Scan(&files, &items); err != nil || files != 16 || items != 8 {
		t.Fatalf("literal grouping files=%d items=%d: %v", files, items, err)
	}
}

func TestNativeIngestAuthorizedDenialRetainsCatalogAndClaimDB(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-%v", existing), func(t *testing.T) {
			x := nativeIngestSetup(t)
			ctx := context.Background()
			var id string
			if existing {
				id = x.publish(t)
				x.discover(t, "v2")
			}
			denied := errors.New("host authority denied")
			called := false
			got, err := x.s.PublishAuthorizedNativeEbook(ctx, x.r, x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true}, func(ctx context.Context, tx pgx.Tx) error {
				called = true
				if existing {
					_, err := tx.Exec(ctx, "UPDATE media_items SET title='Denied mutation' WHERE content_id=$1", id)
					if err != nil {
						return err
					}
				} else {
					_, err := tx.Exec(ctx, "INSERT INTO media_items(content_id,type,title) VALUES('denied-native','ebook','Denied mutation')")
					if err != nil {
						return err
					}
				}
				return denied
			})
			if !called || !errors.Is(err, denied) || got != "" {
				t.Fatalf("denial was bypassed: called=%v id=%s err=%v", called, got, err)
			}
			x.pending(t)
			var deniedItems int
			if err = x.pool.QueryRow(ctx, "SELECT count(*) FROM media_items WHERE title='Denied mutation'").Scan(&deniedItems); err != nil || deniedItems != 0 {
				t.Fatalf("authorization changes escaped rollback: %d %v", deniedItems, err)
			}
			if existing {
				var title, revision string
				if err = x.pool.QueryRow(ctx, "SELECT i.title,r.revision FROM media_items i JOIN media_files f USING(content_id) JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE i.content_id=$1", id).Scan(&title, &revision); err != nil || title != "The Test Ebook" || revision != "v1" {
					t.Fatalf("denial changed existing catalog: %s/%s %v", title, revision, err)
				}
			} else {
				x.empty(t)
			}
		})
	}
}

// Moving authorization into the after-source callback must fail this NOWAIT
// probe. The successful path must publish actual metadata and advance the claim.
func TestNativeIngestAuthorizedCallbackPrecedesSourceFenceDB(t *testing.T) {
	x := nativeIngestSetup(t)
	ctx := context.Background()
	called := false
	id, err := x.s.PublishAuthorizedNativeEbook(ctx, x.r, x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true}, func(ctx context.Context, publicationTx pgx.Tx) error {
		called = true
		tx, err := x.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		_, err = tx.Exec(ctx, "SELECT key FROM bloem_storage_sources WHERE key=$1 FOR UPDATE NOWAIT", x.source.Key)
		if err != nil {
			return err
		}
		if err = tx.Rollback(ctx); err != nil {
			return err
		}
		return x.authorize(ctx, publicationTx)
	})
	if !called || err != nil || id == "" {
		t.Fatalf("authorization did not precede source fence: called=%v id=%s err=%v", called, id, err)
	}
	item, err := x.s.itemRepo.GetByID(ctx, id)
	if err != nil || item.Title != "The Test Ebook" {
		t.Fatalf("authorized metadata publication failed: %+v %v", item, err)
	}
	if _, ok, err := x.r.NextIngestion(ctx, x.claim.Lease); err != nil || ok {
		t.Fatalf("authorized claim did not advance: %v", err)
	}
}

func TestNativeIngestAuthorizedPreparationFailureKeepsClaimDB(t *testing.T) {
	x := nativeIngestSetup(t)
	x.file.failure = errors.New("pinned provider unavailable")
	called := false
	_, err := x.s.PublishAuthorizedNativeEbook(context.Background(), x.r, x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true}, func(ctx context.Context, tx pgx.Tx) error { called = true; return x.authorize(ctx, tx) })
	if err == nil || called {
		t.Fatalf("SQL authority ran during failed preparation: called=%v err=%v", called, err)
	}
	x.empty(t)
	x.pending(t)
}

func TestNativeIngestGroupStableAcrossConfigurationRevisionDB(t *testing.T) {
	x := nativeIngestSetup(t)
	ctx := context.Background()
	id := x.publish(t)
	var beforeKey string
	var beforeFile int
	if err := x.pool.QueryRow(ctx, "SELECT id,content_group_key FROM media_files WHERE content_id=$1", id).Scan(&beforeFile, &beforeKey); err != nil {
		t.Fatal(err)
	}
	nativeIngestSQL(t, x.pool, "UPDATE bloem_storage_sources SET configuration_revision=2 WHERE key=$1", x.source.Key)
	x.discover(t, "v2")
	if nextID := x.publish(t); nextID != id {
		t.Fatal("configuration change replaced native item")
	}
	var afterKey string
	var afterFile int
	if err := x.pool.QueryRow(ctx, "SELECT id,content_group_key FROM media_files WHERE content_id=$1", id).Scan(&afterFile, &afterKey); err != nil {
		t.Fatal(err)
	}
	if beforeFile != afterFile || beforeKey != afterKey || afterKey != "bloem-native:"+x.binding.ID.String()+":ebook:isbn:9780306406157" {
		t.Fatalf("retained binding/config identity changed: files=%d/%d keys=%s/%s", beforeFile, afterFile, beforeKey, afterKey)
	}
}

func (x *nativeIngestFixture) publishPreparedAuthorized(ctx context.Context, p *nativeEbookPrepared) error {
	return x.r.PublishAuthorizedIngestion(ctx, x.claim, x.authorize, func(ctx context.Context, tx pgx.Tx, entry *storagev1.Entry) error {
		if err := catalog.BeginNativePublicationPermitTx(ctx, tx, currentPublicationInput(x, p)); err != nil {
			return err
		}
		if err := x.s.nativeEbookPublication(x.r, x.claim, x.folder, p)(ctx, tx, entry); err != nil {
			return err
		}
		file, err := catalog.NativePublicationStoredFileTx(ctx, tx)
		if err != nil {
			return err
		}
		return catalog.FinishNativePublicationPermitTx(ctx, tx, file)
	})
}
