//go:build integration

package libraryingest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanbatch"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/scanqueue"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func queueCurrentDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SILO_TEST_DATABASE_URL") != "" {
		t.Fatal("SILO_TEST_DATABASE_URL must be unset")
	}
	const private = "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	st, err := os.Stat(private)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private mode-0600 fixture required")
	}
	data, err := os.ReadFile(private)
	if err != nil {
		t.Fatal("read private fixture")
	}
	original, err := pgxpool.ParseConfig(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("invalid private fixture")
	}
	dsn, cleanup, err := bloemtestdb.CloneNativeOnboarding(t.Context(), strings.TrimSpace(string(data)), false)
	if err != nil {
		t.Fatal(err)
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cleanup(ctx); err != nil {
			t.Error(err)
		} else {
			t.Log("A current UUID clone cleanup verified")
		}
	})
	cfg, err := bloemtestdb.NativeOnboardingPoolConfig(dsn)
	if err != nil {
		t.Fatal("unowned clone config")
	}
	cfg.MaxConns = 8
	pool, err = pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("open guarded clone")
	}
	var actual string
	if err = pool.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != cfg.ConnConfig.Database || actual == original.ConnConfig.Database {
		t.Fatal("actual clone identity unverified")
	}
	t.Logf("A current actual UUID clone: %s", actual)
	if err = bloemtestdb.PrepareNativeOnboardingPool(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if !catalog.NativeStorageSchemaReady(t.Context(), pool) {
		t.Fatal("current schema inventory not ready")
	}
	return pool
}

func queueCurrentActor(t *testing.T, pool *pgxpool.Pool) (auth.AdminContextClaims, auth.AdminContextClaims) {
	t.Helper()
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	user, err := users.Create(ctx, models.CreateUserInput{Username: "a-current-metadata-admin", Email: "a-current-metadata-admin@example.test", Password: "domain-fixture-password", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	tenants := tenancy.NewStore(pool)
	if _, err = tenants.ActivateInitialOwnership(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionRepository(pool)
	jwt := auth.NewJWTService("native-domain-fixture-signing", time.Hour, 24*time.Hour)
	svc := auth.NewService(auth.NewLocalProvider(users, sessions), jwt, sessions, users, auth.NewInviteCodeRepository(pool), nil, nil)
	pair, actual, err := svc.Login(ctx, user.Username, "domain-fixture-password", "native-domain", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	login, err := jwt.ValidateToken(pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewAdminContextTokenService("native-domain-fixture-signing")
	// Claims are derived from the actual enabled account and actual login session,
	// then round-tripped through the signed native administrative context service.
	platformToken, err := tokens.Mint(auth.AdminContextClaims{AccountID: actual.ID, AccountIncarnationID: actual.AccountIncarnationID, SessionID: login.SessionID, Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal(err)
	}
	platform, err := tokens.Parse(platformToken)
	if err != nil {
		t.Fatal(err)
	}
	var orgID uuid.UUID
	if err = pool.QueryRow(ctx, "SELECT id FROM organizations WHERE is_default").Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	org, err := tenants.GetOrganization(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	member, err := tenants.GetMembership(ctx, user.ID, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	orgToken, err := tokens.Mint(auth.AdminContextClaims{AccountID: actual.ID, AccountIncarnationID: actual.AccountIncarnationID, SessionID: login.SessionID,
		Scope: auth.AdminScopeOrganization, OrganizationID: org.ID, MembershipID: member.ID, PolicyRevision: org.PolicyRevision, SecurityRevision: member.SecurityRevision, EffectiveAuthority: "organization_admin"})
	if err != nil {
		t.Fatal(err)
	}
	organization, err := tokens.Parse(orgToken)
	if err != nil {
		t.Fatal(err)
	}
	return platform, organization
}

// Scanner-only parsed-entry controls use actual domain commands/retained rows;
// the actual RPC executable chain is independently exercised by libraryingest.

func queueCurrentLifecycle(t *testing.T, pool *pgxpool.Pool, queue *scanqueue.Service, binary []byte, registry *plugins.NativeStorageRegistry) (auth.AdminContextClaims, *nativestorage.LibraryManagement, storagesource.SourceConfig, *models.MediaFolder) {
	t.Helper()
	actor, _ := queueCurrentActor(t, pool)
	source, err := nativestorage.NewSourceManagement(pool, registry).Install(t.Context(), actor, nativestorage.InstallCommand{ArtifactKey: "consumer", ProviderSourceID: "books", RootEntryID: "root", Enabled: true, Binary: binary, Config: map[string]map[string]any{"source": {"mode": "success", "notify": filepath.Join(t.TempDir(), "notify"), "revision": "v1"}}})
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
	libraries := nativestorage.NewLibraryManagement(pool, folders, sections.NewRepository(pool), resourcetenancy.NewStore(pool), queue)
	l1, err := libraries.Create(t.Context(), actor, nativestorage.LibraryCreateCommand{Name: "Current real queue", MetadataLanguage: "en"})
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
		t.Fatal("identity replaced")
	}
	folder, err := folders.GetByID(t.Context(), l1.LibraryID)
	if err != nil {
		t.Fatal(err)
	}
	return actor, libraries, snapshot.Source, folder
}

type queueCoverCache struct{}

func (queueCoverCache) CacheEbookCover(_ context.Context, data []byte, id string) (string, string, error) {
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return "", "", err
	}
	return "local/ebooks/" + id + "/cover.png", "fixture-cover", nil
}
func (queueCoverCache) CacheAudiobookCover(context.Context, []byte, string) (string, string, error) {
	panic("unexpected audiobook")
}
func TestNativeOnboardingActualQueueConsumerDB(t *testing.T) {
	pool := queueCurrentDatabase(t)
	path := filepath.Join(t.TempDir(), "consumer")
	command := exec.Command("go", "build", "-p", "1", "-o", path, "./testdata/nativeconsumer")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("actual fixture build: %s", output)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	checksum := hex.EncodeToString(digest[:])
	manifest := &publicv1.PluginManifest{PluginId: "bloem.consumer.fixture", Version: "1.0.0", SiloApiVersion: "v1", Checksum: checksum,
		SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}},
		GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "source", JsonSchema: `{"type":"object","properties":{"mode":{"type":"string"},"notify":{"type":"string"},"revision":{"type":"string"}},"additionalProperties":false}`}}}
	cipher, err := secret.New([]byte(strings.Repeat("a-current-queue-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{"consumer": {Manifest: manifest, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}})
	if err != nil {
		t.Fatal(err)
	}
	host := &nativestorage.Host{Registry: registry, Manager: storageplugin.NewManager(storageplugin.Config{})}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	folders := catalog.NewFolderRepository(pool)
	publisher := scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, false, 0)
	publisher.SetImageCacher(queueCoverCache{})
	consumer, err := libraryingest.NewNativeConsumer(host, storagesource.NewRepository(pool), resourcetenancy.NewStore(pool), publisher)
	if err != nil {
		t.Fatal(err)
	}
	executor := libraryingest.NewExecutor(publisher, nil, folders, nil, nil, nil)
	executor.SetNativeIngestor(consumer)
	repository := scanqueue.NewRepository(pool)
	queue := scanqueue.NewService(repository, folders, executor, nil, t.Context(), 1, 1)
	actor, libraries, source, folder := queueCurrentLifecycle(t, pool, queue, binary, registry)
	accepted, err := libraries.Scan(t.Context(), actor, folder.ID, 3, source.ConfigurationRevision)
	if err != nil || !accepted.Created {
		t.Fatal("actual B Scan into existing queue", err)
	}
	run, err := repository.ClaimNextAccepted(t.Context(), 1, 1)
	if err != nil || run == nil || run.ID != accepted.ScanRunID {
		t.Fatal("actual same-queue accepted claim", err)
	}
	// Drive the real accepted queue claim through the actual executor and join
	// its worker before host/pool cleanup. Service.Start is not exercised here.
	ctx, cancel := context.WithCancel(scanbatch.WithRunID(t.Context(), run.ID))
	defer cancel()
	done := make(chan error, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		result, err := executor.IngestFolder(ctx, folder)
		if err == nil {
			_, err = repository.Complete(ctx, run.ID, &events.ScanRunResult{New: result.ScanResult.New, Updated: result.ScanResult.Updated, Unchanged: result.ScanResult.Unchanged, Errors: result.ScanResult.Errors})
		}
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(15 * time.Second):
			t.Error("queue claim worker did not join")
		}
	})
	if err = <-done; err != nil {
		t.Fatal("actual queue/executor/consumer/scanner", err)
	}
	var files, refs, permits int
	if err = pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM media_files WHERE media_folder_id=$1),(SELECT count(*) FROM bloem_storage_file_refs r JOIN media_files f ON f.id=r.media_file_id WHERE f.media_folder_id=$1),(SELECT count(*) FROM bloem_native_publication_permits)", folder.ID).Scan(&files, &refs, &permits); err != nil || files != 2 || refs != 2 || permits != 0 {
		t.Fatal("actual same-queue publication incomplete", err)
	}
	actual, err := repository.GetByID(t.Context(), run.ID)
	if err != nil || actual.Status != "completed" {
		t.Fatal("queue checkpoint incomplete", err)
	}
}
