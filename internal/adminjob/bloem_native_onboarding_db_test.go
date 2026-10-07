//go:build integration

package adminjob

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/sections"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func replayCurrentDatabase(t *testing.T) *pgxpool.Pool {
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

func replayCurrentActor(t *testing.T, pool *pgxpool.Pool) (auth.AdminContextClaims, auth.AdminContextClaims) {
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
func replayCurrentLifecycle(t *testing.T, pool *pgxpool.Pool) (storagesource.SourceConfig, storagesource.Binding, *models.MediaFolder) {
	t.Helper()
	actor, _ := replayCurrentActor(t, pool)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	checksum := hex.EncodeToString(digest[:])
	manifest := &publicv1.PluginManifest{PluginId: "bloem.a-current.scanner", Version: "1.0.0", SiloApiVersion: "v1", Checksum: checksum, SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}, GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "storage", JsonSchema: `{"type":"object","additionalProperties":false}`}}}
	cipher, err := secret.New([]byte(strings.Repeat("a-current-scanner-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{"scanner": {Manifest: manifest, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := nativestorage.NewSourceManagement(pool, registry).Install(t.Context(), actor, nativestorage.InstallCommand{ArtifactKey: "scanner", ProviderSourceID: "books", RootEntryID: "root", Enabled: true, Binary: binary, Config: map[string]map[string]any{"storage": {}}})
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
	l1, err := libraries.Create(t.Context(), actor, nativestorage.LibraryCreateCommand{Name: "Current scanner", MetadataLanguage: "en"})
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
	binding, ok, err := storagesource.NewRepository(pool).FolderBinding(t.Context(), l1.LibraryID)
	if err != nil || !ok {
		t.Fatal("binding absent", err)
	}
	folder, err := folders.GetByID(t.Context(), l1.LibraryID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Source, binding, folder
}

type replayNativeFile struct {
	*bytes.Reader
	info mediasource.Info
}

func (f *replayNativeFile) Info() mediasource.Info { return f.info }
func (f *replayNativeFile) Close() error           { return nil }
func replayNativeItem(t *testing.T, pool *pgxpool.Pool) (string, int) {
	t.Helper()
	source, binding, folder := replayCurrentLifecycle(t, pool)
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	for name, content := range map[string]string{
		"META-INF/container.xml": `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OPS/content.opf":        `<package><metadata><title>Metadata Native</title><language>en</language></metadata></package>`,
	} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	file := &replayNativeFile{Reader: bytes.NewReader(buffer.Bytes()), info: mediasource.Info{Name: "book.epub", LogicalPath: "Books/book.epub", Revision: "v1", Size: int64(buffer.Len())}}
	repo := storagesource.NewRepository(pool)
	run, err := repo.Begin(t.Context(), source.Key, "metadata-discovery", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cp, ok, err := repo.NextDirectory(t.Context(), run)
	if err != nil || !ok {
		t.Fatal("directory", err)
	}
	e := &storagev1.Entry{Id: "book", Name: file.info.Name, LogicalPath: file.info.LogicalPath, Revision: file.info.Revision, Size: file.info.Size, Kind: storagev1.EntryKind_ENTRY_KIND_FILE}
	if err = repo.ApplyPage(t.Context(), run, cp, &storagev1.ListResponse{Entries: []*storagev1.Entry{e}, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err = repo.Complete(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	lease, err := repo.BeginIngestion(t.Context(), run.RunID, binding.ID, "metadata-ingestion", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok, err := repo.NextIngestion(t.Context(), lease)
	if err != nil || !ok {
		t.Fatal("claim", err)
	}
	publisher := scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, false, 0)
	id, err := publisher.PublishAuthorizedNativeEbook(t.Context(), repo, claim, folder, file, scanner.NativeEbookSidecars{Complete: true}, func(ctx context.Context, tx pgx.Tx) error {
		return resourcetenancy.NewStore(pool).RequireNativeScanTx(ctx, tx, binding, source)
	})
	if err != nil {
		t.Fatal("actual current native publication", err)
	}
	replayExec(t, pool, "UPDATE media_items SET status='matched' WHERE content_id=$1", id)
	return id, folder.ID
}
func replayExec(t *testing.T, pool *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}

func replaySnapshot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var snapshot string
	if err := pool.QueryRow(t.Context(), `SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM media_folders x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY folder_id) FROM bloem_native_libraries x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM bloem_storage_bindings x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY content_id) FROM media_items x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM media_files x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY content_id,media_folder_id) FROM media_item_libraries x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY media_file_id) FROM bloem_storage_file_refs x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM organization_entitlements x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM library_collections x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM page_sections x))::text`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

type replayCleaner struct{ calls int }

func (c *replayCleaner) DeleteForLibrary(context.Context, int) int64 { c.calls++; return 0 }

// Stored deletion request replay through the actual executor must refuse before
// any phase0 entitlement/catalog/section/settings cleanup.
func TestNativeOnboardingDeletionReplayDB(t *testing.T) {
	pool := replayCurrentDatabase(t)
	_, folder := replayNativeItem(t, pool)
	var creator int
	if err := pool.QueryRow(t.Context(), "SELECT id FROM users WHERE username='a-current-metadata-admin'").Scan(&creator); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(pool)
	job, err := repo.Create(t.Context(), CreateJobInput{JobType: JobTypeDeleteLibrary, CreatedByUserID: creator, RequestPayload: DeleteLibraryRequest{LibraryID: folder, LibraryName: "Stale saved deletion"}})
	if err != nil {
		t.Fatal(err)
	}
	before := replaySnapshot(t, pool)
	cleaner := &replayCleaner{}
	executor := NewLibraryDeleteExecutor(catalog.NewFolderRepository(pool), sections.NewRepository(pool), cleaner)
	for replay := 0; replay < 2; replay++ {
		loaded, err := repo.GetByID(t.Context(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		request, err := decodeDeleteLibraryRequest(loaded.RequestPayload)
		if err != nil {
			t.Fatal(err)
		}
		_, err = executor.Execute(t.Context(), request, nil)
		var typed *catalog.NativeOnboardingError
		if !errors.As(err, &typed) || typed.Code != "native_library_delete_unsupported" {
			t.Fatalf("retained deletion must refuse actual typed native error, got %T %v (code=%s)", err, err, func() string {
				if typed == nil {
					return ""
				}
				return typed.Code
			}())
		}
		if after := replaySnapshot(t, pool); after != before || cleaner.calls != 0 {
			t.Fatal("stale replay reached destructive cleanup")
		}
	}
	var localFolder int
	if err = pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('movies','Ordinary replay control',bloem_platform_resource_owner_id()) RETURNING id").Scan(&localFolder); err != nil {
		t.Fatal(err)
	}
	if _, err = executor.Execute(t.Context(), DeleteLibraryRequest{LibraryID: localFolder}, nil); err != nil {
		t.Fatal("ordinary local delete", err)
	}
	if cleaner.calls != 1 {
		t.Fatal("local delete did not reach normal settings cleanup")
	}
}
