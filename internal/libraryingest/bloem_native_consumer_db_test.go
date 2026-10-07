//go:build integration

package libraryingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/ebooks"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CURRENT default schema only. The real artifact/host/consumer remains intact;
// real B Install/Create/Initialize/Bind supplies every native authority witness.
func consumerDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return nativeExecutorDatabase(t)
}
func consumerSQL(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

var consumerBinary struct {
	once sync.Once
	data []byte
	err  error
}

func consumerExecutable(t *testing.T) []byte {
	t.Helper()
	consumerBinary.once.Do(func() {
		path := filepath.Join(t.TempDir(), "native-consumer")
		command := exec.Command("go", "build", "-p", "1", "-o", path, "./testdata/nativeconsumer")
		command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOMAXPROCS=2")
		if output, err := command.CombinedOutput(); err != nil {
			consumerBinary.err = errors.New(string(output))
			return
		}
		consumerBinary.data, consumerBinary.err = os.ReadFile(path)
	})
	if consumerBinary.err != nil {
		t.Fatal(consumerBinary.err)
	}
	return consumerBinary.data
}

type consumerCoverCache struct {
	bytes          []byte
	started, allow chan struct{}
	fail           error
}

func (c *consumerCoverCache) CacheEbookCover(ctx context.Context, data []byte, id string) (string, string, error) {
	if c.started != nil {
		close(c.started)
		select {
		case <-c.allow:
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
	if c.fail != nil {
		return "", "", c.fail
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return "", "", err
	}
	c.bytes = bytes.Clone(data)
	return "local/ebooks/" + id + "/cover.png", "fixture-hash", nil
}
func (c *consumerCoverCache) CacheAudiobookCover(context.Context, []byte, string) (string, string, error) {
	return "", "", errors.New("unexpected poster")
}

type consumerFixture struct {
	c          *NativeConsumer
	pool       *pgxpool.Pool
	source     storagesource.SourceConfig
	binding    storagesource.Binding
	folder     *models.MediaFolder
	cache      *consumerCoverCache
	notify     string
	actor      auth.AdminContextClaims
	management *nativestorage.SourceManagement
	libraries  *nativestorage.LibraryManagement
}

func newConsumerFixture(t *testing.T, mode string) *consumerFixture {
	t.Helper()
	pool := consumerDatabase(t)
	binary := consumerExecutable(t)
	sum := sha256.Sum256(binary)
	checksum := hex.EncodeToString(sum[:])
	manifest := consumerApprovedManifest(checksum)
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-fixture-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{"fixture": {Manifest: manifest, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}})
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := consumerCurrentActor(t, pool)
	notify := filepath.Join(t.TempDir(), "provider-started")
	management := nativestorage.NewSourceManagement(pool, registry)
	source, err := management.Install(t.Context(), actor, nativestorage.InstallCommand{ArtifactKey: "fixture", Binary: binary, ProviderSourceID: "books", RootEntryID: "root", Enabled: true, Config: map[string]map[string]any{"source": {"mode": mode, "notify": notify, "revision": "v1"}}})
	if err != nil {
		t.Fatal("actual Install", err)
	}
	var owner uuid.UUID
	if err = pool.QueryRow(t.Context(), "SELECT owner_id FROM bloem_storage_sources WHERE key=$1", source.SourceKey).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot(t.Context(), source.SourceKey, owner)
	if err != nil {
		t.Fatal(err)
	}
	folders := catalog.NewFolderRepository(pool)
	libraries := nativestorage.NewLibraryManagement(pool, folders, sections.NewRepository(pool), resourcetenancy.NewStore(pool), nil)
	l1, err := libraries.Create(t.Context(), actor, nativestorage.LibraryCreateCommand{Name: "Owned consumer fixture", MetadataLanguage: "en"})
	if err != nil {
		t.Fatal("actual Create", err)
	}
	l2, err := libraries.Initialize(t.Context(), actor, l1.LibraryID, l1.LibraryRevision)
	if err != nil {
		t.Fatal("actual Initialize", err)
	}
	bound, err := libraries.Bind(t.Context(), actor, source.SourceKey, l2.LibraryID, source.ConfigurationRevision, l2.LibraryRevision)
	if err != nil {
		t.Fatal("actual Bind", err)
	}
	if bound.FolderID != l1.LibraryID || l2.CreationKey != l1.CreationKey {
		t.Fatal("lifecycle replaced library identity")
	}
	repo := storagesource.NewRepository(pool)
	binding, ok, err := repo.FolderBinding(t.Context(), l1.LibraryID)
	if err != nil || !ok {
		t.Fatal("actual binding absent", err)
	}
	folder, err := folders.GetByID(t.Context(), l1.LibraryID)
	if err != nil {
		t.Fatal(err)
	}
	host := &nativestorage.Host{Registry: registry, Manager: storageplugin.NewManager(storageplugin.Config{HealthInterval: 50 * time.Millisecond, HealthFailureLimit: 1})}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	publisher := scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, false, 0)
	cache := &consumerCoverCache{}
	publisher.SetImageCacher(cache)
	c, err := NewNativeConsumer(host, repo, resourcetenancy.NewStore(pool), publisher)
	if err != nil {
		t.Fatal(err)
	}
	c.leaseTTL = 2 * time.Second
	return &consumerFixture{c: c, pool: pool, source: snapshot.Source, binding: binding, folder: folder, cache: cache, notify: notify, actor: actor, management: management, libraries: libraries}
}
func (x *consumerFixture) catalogFiles(t *testing.T) int {
	t.Helper()
	var n int
	if err := x.pool.QueryRow(t.Context(), "SELECT count(*) FROM media_files WHERE media_folder_id=$1", x.folder.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func (x *consumerFixture) pending(t *testing.T) uuid.UUID {
	t.Helper()
	run, ok, err := x.c.sources.PendingIngestionRun(t.Context(), x.binding.ID)
	if err != nil || !ok {
		t.Fatalf("retryable generation missing: %v", err)
	}
	return run
}
func consumerWait(t *testing.T, ctx context.Context, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatal("observable state never reached")
		case <-ticker.C:
		}
	}
}
func (x *consumerFixture) runAsync(t *testing.T, parent context.Context) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	done, joined := make(chan error, 1), make(chan struct{})
	go func() { defer close(joined); _, err := x.c.IngestNativeFolder(ctx, x.folder); done <- err }()
	// Registered after fixture cleanup: cancel/join before shutdown or pool drop,
	// even when an observable-state wait calls Fatal before the normal join.
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(15 * time.Second):
			t.Error("consumer failed to join before fixture cleanup")
		}
	})
	return done
}

func TestNativeConsumerExecutableMetadataRestartAndCountsDB(t *testing.T) {
	x := newConsumerFixture(t, "success")
	var progressUpdate ProgressUpdate
	result, err := x.c.IngestNativeFolder(WithProgressReporter(t.Context(), func(p ProgressUpdate) { progressUpdate = p }), x.folder)
	if err != nil {
		t.Fatal(err)
	}
	if progressUpdate.FilesDiscovered != 5 || progressUpdate.FilesProcessed != 5 || progressUpdate.New != 2 || progressUpdate.Unchanged != 3 {
		t.Fatalf("actual progress counts lost: %+v", progressUpdate)
	}
	if result.ScanResult.New != 2 || result.ScanResult.Updated != 0 || result.ScanResult.Unchanged != 3 || x.catalogFiles(t) != 2 {
		t.Fatalf("incorrect actual counts: %+v", result.ScanResult)
	}
	var title, language, author, poster string
	if err = x.pool.QueryRow(t.Context(), `SELECT i.title,i.original_language,p.name,i.poster_path FROM media_items i JOIN media_files f ON f.content_id=i.content_id JOIN bloem_storage_file_refs r ON r.media_file_id=f.id JOIN item_people ip ON ip.content_id=i.content_id AND ip.kind=7 JOIN people p ON p.id=ip.person_id WHERE r.binding_id=$1 AND r.entry_id='01-epub'`, x.binding.ID).Scan(&title, &language, &author, &poster); err != nil {
		t.Fatal(err)
	}
	if title != "Sidecar EPUB" || language != "nl" || author != "Sidecar Writer" || !strings.HasPrefix(poster, "local/ebooks/") || len(x.cache.bytes) == 0 {
		t.Fatalf("actual sidecar metadata/cover absent: %s %s %s", title, language, author)
	}
	var pdfTitle, container string
	if err = x.pool.QueryRow(t.Context(), `SELECT i.title,f.container FROM media_items i JOIN media_files f ON f.content_id=i.content_id JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.binding_id=$1 AND r.entry_id='02-pdf'`, x.binding.ID).Scan(&pdfTitle, &container); err != nil || pdfTitle != "Native PDF" || container != "pdf" {
		t.Fatalf("PDF metadata/format absent: %s/%s %v", pdfTitle, container, err)
	}
	var fileID int
	var contentID string
	if err = x.pool.QueryRow(t.Context(), "SELECT media_file_id,f.content_id FROM bloem_storage_file_refs r JOIN media_files f ON f.id=r.media_file_id WHERE r.binding_id=$1 AND r.entry_id='01-epub'", x.binding.ID).Scan(&fileID, &contentID); err != nil {
		t.Fatal(err)
	}
	// Restart repositories and replay a current completed generation. This models
	// a crash after publication but before the completed checkpoint was observed.
	consumerSQL(t, x.pool, "UPDATE bloem_storage_ingestion SET complete=false,last_entry_id='',lease_until=clock_timestamp()-interval '1 second' WHERE binding_id=$1", x.binding.ID)
	consumerSQL(t, x.pool, "INSERT INTO users(id,username,password_hash,role) VALUES(96006,'consumer-reader','fixture','user')")
	consumerSQL(t, x.pool, "INSERT INTO ebook_reader_progress(user_id,profile_id,content_id,file_id,location,progress) VALUES(96006,'consumer',$1,$2,'chapter-2',0.4)", contentID, fileID)
	consumerSQL(t, x.pool, "UPDATE media_items SET title='Curated',status='matched' WHERE content_id=$1", contentID)
	run := x.pending(t)
	x.c.sources = storagesource.NewRepository(x.pool)
	replay, err := x.c.IngestNativeFolder(t.Context(), x.folder)
	if err != nil {
		t.Fatal(err)
	}
	if replay.ScanResult.New != 0 || replay.ScanResult.Updated != 0 || replay.ScanResult.Unchanged != 5 || x.catalogFiles(t) != 2 {
		t.Fatalf("replay doubled counts: %+v", replay.ScanResult)
	}
	var currentRun uuid.UUID
	if err = x.pool.QueryRow(t.Context(), "SELECT discovery_run_id FROM bloem_storage_sources WHERE key=$1", x.source.Key).Scan(&currentRun); err != nil || currentRun != run {
		t.Fatal("completed retry unnecessarily rediscovered")
	}
	var progress float64
	var savedID int
	var curated string
	if err = x.pool.QueryRow(t.Context(), "SELECT p.file_id,p.progress,i.title FROM ebook_reader_progress p JOIN media_items i USING(content_id) WHERE p.content_id=$1", contentID).Scan(&savedID, &progress, &curated); err != nil || savedID != fileID || progress != 0.4 || curated != "Curated" {
		t.Fatal("restart lost curation/identity/progress")
	}
	// A genuinely new configuration/generation revises the same object identity.
	_, err = x.c.host.Registry.ReplaceConfiguration(t.Context(), x.source.Key, x.source.OwnerID, map[string]map[string]any{"source": {"mode": "success", "revision": "v2"}})
	if err != nil {
		t.Fatal(err)
	}
	revised, err := x.c.IngestNativeFolder(t.Context(), x.folder)
	if err != nil {
		t.Fatal(err)
	}
	if revised.ScanResult.New != 0 || revised.ScanResult.Updated != 2 || revised.ScanResult.Unchanged != 3 {
		t.Fatalf("revised counts: %+v", revised.ScanResult)
	}
	// Open the actual published reference through the same pinned source adapter;
	// authenticated HTTP endpoint acceptance remains the reader worker/root gate.
	file, err := scanner.NewFileRepository(x.pool).GetByID(t.Context(), fileID)
	if err != nil {
		t.Fatal(err)
	}
	source, ref, err := x.c.sources.FileReference(t.Context(), file.ID, x.folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := x.c.host.Registry.Snapshot(t.Context(), source.Key, source.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := x.c.host.Manager.Ensure(t.Context(), storageplugin.Snapshot{InstallationID: snap.Installation.ID, Generation: snap.Generation, BinaryPath: snap.Installation.InstallPath, ExpectedChecksum: snap.ArtifactChecksum, Manifest: snap.Manifest, Config: snap.Config, Enabled: true, NativeOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := mediasource.Open(t.Context(), mediasource.NewPluginSource(session.Provider()), mediasource.Ref{SourceID: source.ProviderSourceID, EntryID: ref.EntryID, Revision: ref.Revision})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	head := make([]byte, 2)
	if _, err = opened.ReadAt(head, 0); err != nil || string(head) != "PK" {
		t.Fatal("published reference did not read actual EPUB bytes")
	}
}
func TestNativeConsumerFalseCapabilitiesAndRetainedBindingDB(t *testing.T) {
	x := newConsumerFixture(t, "false")
	if _, err := x.c.IngestNativeFolder(t.Context(), x.folder); !errors.Is(err, storagesource.ErrSourceUnavailable) {
		t.Fatalf("false capabilities accepted: %v", err)
	}
	var runs int
	if err := x.pool.QueryRow(t.Context(), "SELECT count(*) FROM bloem_storage_scan_runs WHERE source_key=$1", x.source.Key).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("discovery started without pinned capability")
	}
	consumerSQL(t, x.pool, "UPDATE bloem_storage_sources SET enabled=false WHERE key=$1", x.source.Key)
	bound, err := x.c.HasNativeBinding(t.Context(), x.folder.ID)
	if err != nil || !bound {
		t.Fatal("disabled source fell back locally")
	}
	if _, err = x.c.IngestNativeFolder(t.Context(), x.folder); err == nil {
		t.Fatal("disabled source ingested")
	}
}
func TestNativeConsumerProviderFailureKeepsClaimDB(t *testing.T) {
	for _, mode := range []string{"late", "denied", "crash"} {
		t.Run(mode, func(t *testing.T) {
			x := newConsumerFixture(t, mode)
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			result, err := x.c.IngestNativeFolder(ctx, x.folder)
			if err == nil || result.ScanResult.New != 0 || x.catalogFiles(t) != 0 {
				t.Fatal("partial provider failure became full success")
			}
			if mode == "denied" && status.Code(err) != codes.PermissionDenied {
				t.Fatalf("permission classification lost: %v", err)
			}
			x.pending(t)
		})
	}
}
func TestNativeConsumerCancellationAndAuthorityChangeDB(t *testing.T) {
	for _, mode := range []string{"job", "session", "source", "permission", "lease"} {
		t.Run(mode, func(t *testing.T) {
			x := newConsumerFixture(t, "readblock")
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			done := x.runAsync(t, ctx)
			consumerWait(t, ctx, func() bool { _, err := os.Stat(x.notify); return err == nil })
			switch mode {
			case "job":
				cancel()
			case "session":
				x.c.host.Manager.Disable(int(*x.source.InstallationID))
			case "source":
				consumerSQL(t, x.pool, "UPDATE bloem_storage_sources SET configuration_revision=configuration_revision+1 WHERE key=$1", x.source.Key)
			case "permission":
				consumerSQL(t, x.pool, "UPDATE plugin_installations SET enabled=false WHERE id=$1", *x.source.InstallationID)
			case "lease":
				consumerSQL(t, x.pool, "UPDATE bloem_storage_ingestion SET owner='competitor',lease_epoch=lease_epoch+1 WHERE binding_id=$1", x.binding.ID)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancellation/authority change succeeded")
				}
			case <-ctx.Done():
				if mode != "job" {
					t.Fatal("blocked read did not stop")
				}
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("canceled job succeeded")
					}
				case <-time.After(15 * time.Second):
					t.Fatal("canceled worker leaked")
				}
			}
			if x.catalogFiles(t) != 0 {
				t.Fatal("failed worker published")
			}
			if mode != "source" {
				x.pending(t)
			}
			// All job admission must be released even while an isolated session lives.
			shutdownCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
			defer stop()
			if err := x.c.host.Shutdown(shutdownCtx); err != nil {
				t.Fatalf("consumer admission leaked: %v", err)
			}
		})
	}
}
func TestNativeConsumerRenewsThroughoutCoverIODB(t *testing.T) {
	x := newConsumerFixture(t, "success")
	// Measured race archive authority checks cost 2.3–2.6s, exceeding the old
	// entire 2s lease. Allow bounded real work; still hold I/O beyond its expiry.
	x.c.leaseTTL = 8 * time.Second
	x.cache.started = make(chan struct{})
	x.cache.allow = make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	done := x.runAsync(t, ctx)
	select {
	case <-x.cache.started:
	case <-ctx.Done():
		t.Fatal("cover I/O not reached")
	}
	var initial time.Time
	if err := x.pool.QueryRow(ctx, "SELECT lease_until FROM bloem_storage_ingestion WHERE binding_id=$1", x.binding.ID).Scan(&initial); err != nil {
		t.Fatal(err)
	}
	consumerWait(t, ctx, func() bool {
		select {
		case err := <-done:
			t.Fatalf("worker exited during cover I/O: %v", err)
		default:
		}
		var renewed bool
		if err := x.pool.QueryRow(ctx, "SELECT lease_until>$2::timestamptz+interval '2500 milliseconds' AND clock_timestamp()>$2::timestamptz AND lease_until>clock_timestamp() FROM bloem_storage_ingestion WHERE binding_id=$1", x.binding.ID, initial).Scan(&renewed); err != nil {
			t.Fatal(err)
		}
		return renewed
	})
	close(x.cache.allow)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if x.catalogFiles(t) != 2 {
		t.Fatal("long cover I/O lost publication")
	}
}
func TestNativeConsumerLateSQLFailureAndCompletedRestartDB(t *testing.T) {
	x := newConsumerFixture(t, "success")
	consumerSQL(t, x.pool, "ALTER TABLE bloem_storage_ingestion ADD CONSTRAINT consumer_late CHECK(last_entry_id='')")
	if _, err := x.c.IngestNativeFolder(t.Context(), x.folder); err == nil {
		t.Fatal("late SQL failure absent")
	}
	if x.catalogFiles(t) != 0 {
		t.Fatal("partial catalog escaped rollback")
	}
	run := x.pending(t)
	consumerSQL(t, x.pool, "ALTER TABLE bloem_storage_ingestion DROP CONSTRAINT consumer_late")
	consumerSQL(t, x.pool, "UPDATE bloem_storage_ingestion SET lease_until=clock_timestamp()-interval '1 second' WHERE binding_id=$1", x.binding.ID)
	x.c.sources = storagesource.NewRepository(x.pool)
	result, err := x.c.IngestNativeFolder(t.Context(), x.folder)
	if err != nil || result.ScanResult.New != 2 || x.catalogFiles(t) != 2 {
		t.Fatalf("completed generation retry failed: %+v %v", result, err)
	}
	var actual uuid.UUID
	if err = x.pool.QueryRow(t.Context(), "SELECT discovery_run_id FROM bloem_storage_sources WHERE key=$1", x.source.Key).Scan(&actual); err != nil || actual != run {
		t.Fatal("retry replaced completed discovery")
	}
}
func TestNativeConsumerActualFolderIgnoresCallerAssertionsDB(t *testing.T) {
	x := newConsumerFixture(t, "success")
	consumerSQL(t, x.pool, "UPDATE media_folders SET type='movies' WHERE id=$1", x.folder.ID)
	if _, err := x.c.IngestNativeFolder(t.Context(), x.folder); err == nil {
		t.Fatal("caller ebooks assertion bypassed actual folder")
	}
	if x.catalogFiles(t) != 0 {
		t.Fatal("wrong actual folder published")
	}
}

func TestNativeConsumerPublicationReauthorizesAfterCoverIODB(t *testing.T) {
	x := newConsumerFixture(t, "success")
	x.cache.started = make(chan struct{})
	x.cache.allow = make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	done := x.runAsync(t, ctx)
	select {
	case <-x.cache.started:
	case <-ctx.Done():
		t.Fatal("cover preparation not reached")
	}
	consumerSQL(t, x.pool, "UPDATE plugin_installations SET enabled=false WHERE id=$1", *x.source.InstallationID)
	close(x.cache.allow)
	select {
	case err := <-done:
		if !errors.Is(err, resourcetenancy.ErrResourceHidden) {
			t.Fatalf("revocation during preparation authorized: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("revoked publication worker did not finish")
	}
	if x.catalogFiles(t) != 0 {
		t.Fatal("SQL publication bypassed actual installation authority")
	}
	x.pending(t)
}
func TestNativeConsumerUnsupportedAcknowledgementRequiresSQLAuthorityDB(t *testing.T) {
	x := newConsumerFixture(t, "unsupported")
	// Change authority in the real NextIngestion transaction after the consumer's
	// pre-claim check. Only the mandatory publication policy can reject this ack.
	consumerSQL(t, x.pool, `CREATE FUNCTION consumer_revoke_after_claim() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.pending_entry_id<>'' THEN
 UPDATE media_folders SET enabled=false WHERE id=(SELECT folder_id FROM bloem_storage_bindings WHERE id=NEW.binding_id);
 END IF; RETURN NEW; END $$;
 CREATE TRIGGER consumer_revoke_after_claim AFTER UPDATE OF pending_entry_id ON bloem_storage_ingestion
 FOR EACH ROW EXECUTE FUNCTION consumer_revoke_after_claim()`)
	result, err := x.c.IngestNativeFolder(t.Context(), x.folder)
	if !errors.Is(err, resourcetenancy.ErrResourceHidden) || result.ScanResult.Unchanged != 0 {
		t.Fatalf("unsupported entry acknowledged without authority: %+v %v", result.ScanResult, err)
	}
	var pending, last string
	if err = x.pool.QueryRow(t.Context(), "SELECT pending_entry_id,last_entry_id FROM bloem_storage_ingestion WHERE binding_id=$1", x.binding.ID).Scan(&pending, &last); err != nil || pending != "05-mobi" || last != "" {
		t.Fatal("failed unsupported claim advanced")
	}
	if x.catalogFiles(t) != 0 {
		t.Fatal("unsupported format converted/published")
	}
}
func TestNativeConsumerDiscoveryLeaseCoversBlockedPageDB(t *testing.T) {
	x := newConsumerFixture(t, "listblock")
	x.c.leaseTTL = 8 * time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	done := x.runAsync(t, ctx)
	consumerWait(t, ctx, func() bool { _, err := os.Stat(x.notify); return err == nil })
	var initial time.Time
	if err := x.pool.QueryRow(ctx, "SELECT lease_until FROM bloem_storage_scan_runs WHERE source_key=$1 AND state='running'", x.source.Key).Scan(&initial); err != nil {
		t.Fatal(err)
	}
	consumerWait(t, ctx, func() bool {
		select {
		case err := <-done:
			t.Fatalf("worker exited during discovery I/O: %v", err)
		default:
		}
		var renewed bool
		if err := x.pool.QueryRow(ctx, "SELECT lease_until>$2::timestamptz+interval '2500 milliseconds' AND clock_timestamp()>$2::timestamptz AND lease_until>clock_timestamp() FROM bloem_storage_scan_runs WHERE source_key=$1 AND state='running'", x.source.Key, initial).Scan(&renewed); err != nil {
			t.Fatal(err)
		}
		return renewed
	})
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("discovery cancel lost: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("blocked page leaked")
	}
	if x.catalogFiles(t) != 0 {
		t.Fatal("canceled discovery published")
	}
}
func TestNativeConsumerOrganizationEntitlementAndRevocationDB(t *testing.T) {
	x := newConsumerFixture(t, "readblock")
	var account int
	if err := x.pool.QueryRow(t.Context(), "INSERT INTO users(username,email,password_hash,role) VALUES('consumer-org','consumer@example.test','fixture','admin') RETURNING id").Scan(&account); err != nil {
		t.Fatal(err)
	}
	var org, owner uuid.UUID
	if err := x.pool.QueryRow(t.Context(), "INSERT INTO organizations(slug,name,status,owner_account_id) VALUES('consumer-org','Consumer','active',$1) RETURNING id", account).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := x.pool.QueryRow(t.Context(), "SELECT id FROM resource_owners WHERE organization_id=$1", org).Scan(&owner); err != nil {
		t.Fatal(err)
	}

	// Create the organization-owned resource directly. Existing platform folders
	// carry automatic entitlement foreign keys and cannot have their owner swapped.
	consumerSQL(t, x.pool, "DELETE FROM bloem_storage_bindings WHERE id=$1", x.binding.ID)
	var ownedFolder int
	if err := x.pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('ebooks','Owned org consumer',$1) RETURNING id", owner).Scan(&ownedFolder); err != nil {
		t.Fatal(err)
	}
	var bindErr error
	x.binding, bindErr = x.c.sources.Bind(t.Context(), x.source.Key, ownedFolder)
	if bindErr != nil {
		t.Fatal(bindErr)
	}
	x.folder.ID = ownedFolder
	if _, err := x.c.IngestNativeFolder(t.Context(), x.folder); !errors.Is(err, resourcetenancy.ErrResourceHidden) {
		t.Fatalf("unentitled organization scanned platform source: %v", err)
	}
	consumerSQL(t, x.pool, "INSERT INTO organization_entitlements(organization_id,entitlement_kind,root_kind,root_owner_id,plugin_installation_id,status,granted_by_service) VALUES($1,'plugin_availability','plugin_installation',$2,$3,'active','consumer-test')", org, x.source.OwnerID, *x.source.InstallationID)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	done := x.runAsync(t, ctx)
	consumerWait(t, ctx, func() bool { _, err := os.Stat(x.notify); return err == nil })
	consumerSQL(t, x.pool, "UPDATE organization_entitlements SET status='revoked',revoked_at=now(),security_revision=security_revision+1 WHERE organization_id=$1", org)
	select {
	case err := <-done:
		if !errors.Is(err, resourcetenancy.ErrResourceHidden) {
			t.Fatalf("revoked entitlement did not cancel blocked I/O: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("revoked entitlement worker remained live")
	}
	if x.catalogFiles(t) != 0 {
		t.Fatal("revoked organization published")
	}
}

func TestNativeConsumerUnsupportedSiblingSuppressesGenericCoverDB(t *testing.T) {
	x := newConsumerFixture(t, "multibook")
	result, err := x.c.IngestNativeFolder(t.Context(), x.folder)
	if err != nil {
		t.Fatal(err)
	}
	if result.ScanResult.New != 2 || len(x.cache.bytes) != 0 {
		t.Fatal("generic cover assigned in directory containing unsupported fb2.zip")
	}
	var poster *string
	if err = x.pool.QueryRow(t.Context(), "SELECT i.poster_path FROM media_items i JOIN media_files f ON f.content_id=i.content_id JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.binding_id=$1 AND r.entry_id='01-epub'", x.binding.ID).Scan(&poster); err != nil {
		t.Fatal(err)
	}
	if poster != nil && *poster != "" {
		t.Fatal("multi-book generic cover persisted")
	}
}

func TestNativeConsumerLiteralRootSidecarsExecutableDB(t *testing.T) {
	x := newConsumerFixture(t, "roots")
	result, err := x.c.IngestNativeFolder(t.Context(), x.folder)
	if err != nil {
		t.Fatal(err)
	}
	if result.ScanResult.New != 3 || result.ScanResult.Unchanged != 3 {
		t.Fatalf("literal roots not independently consumed: %+v", result.ScanResult)
	}
	for _, book := range []struct{ id, title, path string }{
		{"01-bare", "Bare Root", "Book.epub"},
		{"02-slash", "Slash Root", "/Book.epub"},
		{"03-double", "Double Root", "//Book.epub"},
	} {
		var title, logical string
		if err = x.pool.QueryRow(t.Context(), "SELECT i.title,r.logical_path FROM media_items i JOIN media_files f ON f.content_id=i.content_id JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.binding_id=$1 AND r.entry_id=$2", x.binding.ID, book.id).Scan(&title, &logical); err != nil {
			t.Fatal(err)
		}
		if title != book.title || logical != book.path {
			t.Fatalf("root sidecar crossed namespace for %s: %s %s", book.id, title, logical)
		}
	}
}

// Only the enrichment enqueue boundary is synthetic; process, parsed input,
// SQL authority, publication, checkpoints, and restart are real.
type consumerCancelEnrichment struct{ cancel context.CancelFunc }

func (q consumerCancelEnrichment) Enqueue(context.Context, string, int) error {
	q.cancel()
	return errors.New("synthetic post-commit enrichment interruption")
}
func (consumerCancelEnrichment) ReconcileMissing(context.Context, int, int, int) (int, int, bool, error) {
	return 0, 0, false, nil
}
func TestNativeConsumerPostCommitCancellationResumesWithoutDuplicateDB(t *testing.T) {
	x := newConsumerFixture(t, "success")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	x.c.scanner.SetEbookEnrichmentQueue(consumerCancelEnrichment{cancel})
	result, err := x.c.IngestNativeFolder(ctx, x.folder)
	if !errors.Is(err, context.Canceled) || result.ScanResult.New != 1 || x.catalogFiles(t) != 1 {
		t.Fatalf("post-commit cancellation/counts lost: %+v %v", result.ScanResult, err)
	}
	run := x.pending(t)
	var originalFile int
	if err = x.pool.QueryRow(t.Context(), "SELECT media_file_id FROM bloem_storage_file_refs WHERE binding_id=$1 AND entry_id='01-epub'", x.binding.ID).Scan(&originalFile); err != nil {
		t.Fatal(err)
	}
	consumerSQL(t, x.pool, "UPDATE bloem_storage_ingestion SET lease_until=clock_timestamp()-interval '1 second' WHERE binding_id=$1", x.binding.ID)
	x.c.scanner.SetEbookEnrichmentQueue(nil)
	x.c.sources = storagesource.NewRepository(x.pool)
	resumed, err := x.c.IngestNativeFolder(t.Context(), x.folder)
	if err != nil || resumed.ScanResult.New != 1 || resumed.ScanResult.Unchanged != 3 || x.catalogFiles(t) != 2 {
		t.Fatalf("resume republished committed entry: %+v %v", resumed.ScanResult, err)
	}
	var actualRun uuid.UUID
	var actualFile int
	if err = x.pool.QueryRow(t.Context(), "SELECT s.discovery_run_id,r.media_file_id FROM bloem_storage_sources s JOIN bloem_storage_bindings b ON b.source_key=s.key JOIN bloem_storage_file_refs r ON r.binding_id=b.id WHERE b.id=$1 AND r.entry_id='01-epub'", x.binding.ID).Scan(&actualRun, &actualFile); err != nil || actualRun != run || actualFile != originalFile {
		t.Fatal("post-commit restart changed generation/file identity")
	}
}

// Keep the real SQL enqueue and durable reconciliation. Only recovery of the
// injected SQL outage and optional interruption are controlled at this boundary.
type consumerFailEnrichment struct {
	queue  *ebooks.EnrichmentQueue
	pool   *pgxpool.Pool
	cancel context.CancelFunc
	failed error
}

func (q *consumerFailEnrichment) Enqueue(ctx context.Context, id string, priority int) error {
	err := q.queue.Enqueue(ctx, id, priority)
	if err != nil {
		q.failed = err
		_, dropErr := q.pool.Exec(context.Background(), "DROP TRIGGER consumer_enqueue_outage ON ebook_enrichment_state")
		if dropErr != nil {
			return errors.Join(err, dropErr)
		}
		if q.cancel != nil {
			q.cancel()
		}
	}
	return err
}
func (q *consumerFailEnrichment) ReconcileMissing(ctx context.Context, folder, priority, limit int) (int, int, bool, error) {
	return q.queue.ReconcileMissing(ctx, folder, priority, limit)
}
func consumerQueueOutage(t *testing.T, x *consumerFixture, cancel context.CancelFunc) *consumerFailEnrichment {
	t.Helper()
	consumerSQL(t, x.pool, `CREATE FUNCTION consumer_enqueue_outage() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'owned transient queue outage'; END $$;
 CREATE TRIGGER consumer_enqueue_outage BEFORE INSERT ON ebook_enrichment_state
 FOR EACH ROW EXECUTE FUNCTION consumer_enqueue_outage()`)
	q := &consumerFailEnrichment{queue: ebooks.NewEnrichmentQueue(x.pool), pool: x.pool, cancel: cancel}
	x.c.scanner.SetEbookEnrichmentQueue(q)
	return q
}
func consumerAssertQueue(t *testing.T, x *consumerFixture, want int) {
	t.Helper()
	var count int
	if err := x.pool.QueryRow(t.Context(), `SELECT count(*) FROM ebook_enrichment_state q JOIN media_files f USING(content_id) WHERE f.media_folder_id=$1 AND q.status='pending' AND q.priority=100`, x.folder.ID).Scan(&count); err != nil || count != want {
		t.Fatalf("actual durable queue rows = %d, want %d: %v", count, want, err)
	}
}
func TestNativeConsumerEnrichmentCompletionRepairsSQLFailureDB(t *testing.T) {
	x := newConsumerFixture(t, "success")
	q := consumerQueueOutage(t, x, nil)
	result, err := x.c.IngestNativeFolder(t.Context(), x.folder)
	if err != nil || result.ScanResult.New != 2 || x.catalogFiles(t) != 2 || q.failed == nil {
		t.Fatalf("actual postcommit enqueue failure/counts absent: %+v %v failure=%v", result.ScanResult, err, q.failed)
	}
	consumerAssertQueue(t, x, 2)
}
func TestNativeConsumerEnrichmentStartupRepairsBeforeUnavailableProviderDB(t *testing.T) {
	x := newConsumerFixture(t, "success")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	q := consumerQueueOutage(t, x, cancel)
	result, err := x.c.IngestNativeFolder(ctx, x.folder)
	if !errors.Is(err, context.Canceled) || result.ScanResult.New != 1 || q.failed == nil || x.catalogFiles(t) != 1 {
		t.Fatalf("actual postcommit interruption absent: %+v %v", result.ScanResult, err)
	}
	consumerAssertQueue(t, x, 0)
	run := x.pending(t)
	var file int
	var content string
	if err = x.pool.QueryRow(t.Context(), "SELECT id,content_id FROM media_files WHERE media_folder_id=$1", x.folder.ID).Scan(&file, &content); err != nil {
		t.Fatal(err)
	}
	consumerSQL(t, x.pool, "INSERT INTO users(id,username,password_hash,role) VALUES(96007,'queue-reader','fixture','user')")
	consumerSQL(t, x.pool, "INSERT INTO ebook_reader_progress(user_id,profile_id,content_id,file_id,location,progress) VALUES(96007,'consumer',$1,$2,'chapter-3',0.6)", content, file)
	// Fresh denied authority must prevent queue repair, even for committed data.
	consumerSQL(t, x.pool, "UPDATE media_folders SET enabled=false WHERE id=$1", x.folder.ID)
	x.c.scanner.SetEbookEnrichmentQueue(ebooks.NewEnrichmentQueue(x.pool))
	if _, denyErr := x.c.IngestNativeFolder(t.Context(), x.folder); !errors.Is(denyErr, resourcetenancy.ErrResourceHidden) {
		t.Fatalf("disabled folder repair accepted: %v", denyErr)
	}
	consumerAssertQueue(t, x, 0)
	consumerSQL(t, x.pool, "UPDATE media_folders SET enabled=true WHERE id=$1", x.folder.ID)
	before, err := os.ReadFile(x.notify + ".io")
	if err != nil {
		t.Fatal(err)
	}
	// Process restart; retain the approved source/configuration and durable run.
	shutdown, stop := context.WithTimeout(t.Context(), 15*time.Second)
	defer stop()
	if err = x.c.host.Manager.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	x.c.host.Manager = storageplugin.NewManager(storageplugin.Config{HealthInterval: 50 * time.Millisecond, HealthFailureLimit: 1})
	x.c.sources = storagesource.NewRepository(x.pool)
	x.c.scanner.SetEbookEnrichmentQueue(ebooks.NewEnrichmentQueue(x.pool))
	if err = os.WriteFile(x.notify+".offline", []byte("unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = x.c.IngestNativeFolder(t.Context(), x.folder); err == nil {
		t.Fatal("unavailable provider unexpectedly started")
	}
	// Repair precedes runtime Configure; no provider listing/stat/read is possible.
	consumerAssertQueue(t, x, 1)
	after, err := os.ReadFile(x.notify + ".io")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("startup repair reread provider entries")
	}
	if x.catalogFiles(t) != 1 || x.pending(t) != run {
		t.Fatal("startup repair changed durable file/run")
	}
	var savedFile int
	var progress float64
	if err = x.pool.QueryRow(t.Context(), "SELECT file_id,progress FROM ebook_reader_progress WHERE content_id=$1", content).Scan(&savedFile, &progress); err != nil || savedFile != file || progress != 0.6 {
		t.Fatal("startup repair lost reader progress")
	}
	// Retry resumes only the remaining claims, preserving the first committed file.
	if err = os.Remove(x.notify + ".offline"); err != nil {
		t.Fatal(err)
	}
	// A second process restart clears the failed runtime's deliberate backoff;
	// do not sleep or disable production backoff to force a retry.
	if err = x.c.host.Manager.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	x.c.host.Manager = storageplugin.NewManager(storageplugin.Config{HealthInterval: 50 * time.Millisecond, HealthFailureLimit: 1})

	consumerSQL(t, x.pool, "UPDATE bloem_storage_ingestion SET lease_until=clock_timestamp()-interval '1 second' WHERE binding_id=$1", x.binding.ID)
	resumed, err := x.c.IngestNativeFolder(t.Context(), x.folder)
	if err != nil || resumed.ScanResult.New != 1 || resumed.ScanResult.Unchanged != 3 || x.catalogFiles(t) != 2 {
		t.Fatalf("resume counts/identity lost: %+v %v", resumed.ScanResult, err)
	}
	consumerAssertQueue(t, x, 2)
	after, err = os.ReadFile(x.notify + ".io")
	if err != nil || strings.Count(string(after), "list\n") != strings.Count(string(before), "list\n") {
		t.Fatal("recovery rediscovered completed provider generation")
	}
	if strings.Count(string(after), "read 01-epub\n") != strings.Count(string(before), "read 01-epub\n") {
		t.Fatal("restart reread already committed ebook")
	}
	var actualRun uuid.UUID
	var actualFile int
	if err = x.pool.QueryRow(t.Context(), "SELECT s.discovery_run_id,r.media_file_id FROM bloem_storage_sources s JOIN bloem_storage_bindings b ON b.source_key=s.key JOIN bloem_storage_file_refs r ON r.binding_id=b.id WHERE b.id=$1 AND r.entry_id='01-epub'", x.binding.ID).Scan(&actualRun, &actualFile); err != nil || actualRun != run || actualFile != file {
		t.Fatal("recovery changed original generation/file")
	}
	if err = x.pool.QueryRow(t.Context(), "SELECT file_id,progress FROM ebook_reader_progress WHERE content_id=$1", content).Scan(&savedFile, &progress); err != nil || savedFile != file || progress != 0.6 {
		t.Fatal("resumed ingestion lost reader progress")
	}

}

func TestNativeConsumerMalformedDurableLeaseAndClaimDeniedDB(t *testing.T) {
	x := newConsumerFixture(t, "success")
	if _, err := x.c.IngestNativeFolder(t.Context(), x.folder); err != nil {
		t.Fatal(err)
	}
	consumerSQL(t, x.pool, "UPDATE bloem_storage_ingestion SET complete=false,last_entry_id='',lease_until=clock_timestamp()-interval '1 second' WHERE binding_id=$1", x.binding.ID)
	run := x.pending(t)
	lease, err := x.c.sources.BeginIngestion(t.Context(), run, x.binding.ID, "malformed-fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claim, more, err := x.c.sources.NextIngestion(t.Context(), lease)
	if err != nil || !more {
		t.Fatalf("actual durable claim absent: %v", err)
	}
	before, err := os.ReadFile(x.notify + ".io")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"binding", "source"} {
		t.Run(mode, func(t *testing.T) {
			malformed := claim
			if mode == "binding" {
				malformed.Lease.BindingID = uuid.New()
			} else {
				malformed.Lease.SourceKey = uuid.New()
			}
			if !errors.Is(nativeConsumerLease(malformed.Lease, x.binding, x.source), storagesource.ErrStaleLease) {
				t.Fatal("actual malformed lease passed provider boundary")
			}
			policyCalled := false
			err := x.c.sources.PublishAuthorizedIngestion(t.Context(), malformed, func(ctx context.Context, tx pgx.Tx) error {
				if err := nativeConsumerClaim(malformed, lease, x.binding, x.source); err != nil {
					return err
				}
				policyCalled = true
				return x.c.resources.RequireNativeScanTx(ctx, tx, x.binding, x.source)
			}, nil)
			if !errors.Is(err, storagesource.ErrStaleLease) || policyCalled {
				t.Fatalf("malformed durable claim reached retained policy: %v", err)
			}
		})
	}
	var pending, last string
	if err := x.pool.QueryRow(t.Context(), "SELECT pending_entry_id,last_entry_id FROM bloem_storage_ingestion WHERE binding_id=$1", x.binding.ID).Scan(&pending, &last); err != nil || pending != "01-epub" || last != "" {
		t.Fatal("denied malformed claim advanced durable progress")
	}
	after, err := os.ReadFile(x.notify + ".io")
	if err != nil || !bytes.Equal(before, after) || x.catalogFiles(t) != 2 {
		t.Fatal("malformed denial touched provider/catalog")
	}
}

func TestNativeConsumerStopCancelsActualSQLDB(t *testing.T) {
	pool := consumerDatabase(t)
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	started := make(chan struct{})
	stop := startNativeRenewal(ctx, cancel, time.Millisecond, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		// Use the actual pgconn error from an already-canceled SQL call, including
		// its timeout/context-already-done wrappers, not a hand-built surrogate.
		_, err := conn.Exec(ctx, "SELECT 1")
		return err
	})
	<-started
	stop()
	if context.Cause(ctx) != nil {
		t.Fatalf("deliberate SQL Stop cancellation became failure: %v", context.Cause(ctx))
	}
}
