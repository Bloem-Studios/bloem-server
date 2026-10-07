//go:build integration

package metadata

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
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
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/internal/watchsync"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func metadataCurrentDatabase(t *testing.T) *pgxpool.Pool {
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

func metadataCurrentActor(t *testing.T, pool *pgxpool.Pool) (auth.AdminContextClaims, auth.AdminContextClaims) {
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
func metadataCurrentLifecycle(t *testing.T, pool *pgxpool.Pool) (storagesource.SourceConfig, storagesource.Binding, *models.MediaFolder) {
	t.Helper()
	actor, _ := metadataCurrentActor(t, pool)
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

type metadataNativeFile struct {
	*bytes.Reader
	info mediasource.Info
}

func (f *metadataNativeFile) Info() mediasource.Info { return f.info }
func (f *metadataNativeFile) Close() error           { return nil }
func metadataNativeItem(t *testing.T, pool *pgxpool.Pool) (string, int) {
	t.Helper()
	source, binding, folder := metadataCurrentLifecycle(t, pool)
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
	file := &metadataNativeFile{Reader: bytes.NewReader(buffer.Bytes()), info: mediasource.Info{Name: "book.epub", LogicalPath: "Books/book.epub", Revision: "v1", Size: int64(buffer.Len())}}
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
	metadataExec(t, pool, "UPDATE media_items SET status='matched' WHERE content_id=$1", id)
	return id, folder.ID
}
func metadataExec(t *testing.T, pool *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func metadataState(t *testing.T, err error, state string) {
	t.Helper()
	var sql *pgconn.PgError
	if !errors.As(err, &sql) || sql.Code != state {
		t.Fatalf("expected SQLSTATE %s, actual %T %v", state, err, err)
	}
}

type metadataHoldKey struct{}
type metadataRowHold struct {
	held, release chan struct{}
	once          sync.Once
}

func (q *metadataRowHold) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if ctx.Value(metadataHoldKey{}) == true && strings.Contains(d.SQL, "FOR UPDATE") && strings.Contains(d.SQL, "media_items") {
		return context.WithValue(ctx, q, true)
	}
	return ctx
}
func (q *metadataRowHold) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	if ctx.Value(q) == true && d.Err == nil {
		q.once.Do(func() {
			close(q.held)
			select {
			case <-q.release:
			case <-ctx.Done():
			}
		})
	}
}
func TestNativeOnboardingLocalConflictDB(t *testing.T) {
	pool := metadataCurrentDatabase(t)
	// Actual native L1 is unrelated, so catalog-wide native presence cannot gate local rescan.
	_, _, _ = metadataCurrentLifecycle(t, pool)
	var folder int
	if err := pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('movies','Local conflict',bloem_platform_resource_owner_id()) RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	metadataExec(t, pool, `CREATE TABLE a_current_file_events(op text,content text,episode text,extra text);
 CREATE FUNCTION a_current_file_probe() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 INSERT INTO a_current_file_events VALUES(TG_OP,NEW.content_id,NEW.episode_id,NEW.extra_id);RETURN NEW; END $$;
 CREATE TRIGGER z_a_current_file_probe BEFORE INSERT OR UPDATE ON media_files FOR EACH ROW EXECUTE FUNCTION a_current_file_probe();`)
	for _, entrypoint := range []string{"chooseCanonical", "explicitCanonical"} {
		t.Run(entrypoint, func(t *testing.T) {
			a, b := "a-"+uuid.NewString(), "b-"+uuid.NewString()
			metadataExec(t, pool, "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'movie','matched','A'),($2,'movie','matched','B')", a, b)
			file, err := scanner.NewFileRepository(pool).Upsert(t.Context(), models.MediaFile{ContentID: a, MediaFolderID: folder, FilePath: "/local/" + a + ".mkv"})
			if err != nil {
				t.Fatal(err)
			}
			metadataExec(t, pool, "TRUNCATE a_current_file_events")
			hold := &metadataRowHold{held: make(chan struct{}), release: make(chan struct{})}
			cfg := pool.Config()
			cfg.ConnConfig.Tracer = hold
			other, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			var actual string
			if err = other.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != cfg.ConnConfig.Database {
				t.Fatal("duplicate pool identity", err)
			}
			ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), metadataHoldKey{}, true), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				if entrypoint == "chooseCanonical" {
					_, err = canonicalizeProviderIDDuplicate(ctx, other, a, b, true)
				} else {
					_, err = canonicalizeProviderIDDuplicateInto(ctx, other, b, a, true)
				}
				done <- err
			}()
			select {
			case <-hold.held:
			case <-ctx.Done():
				t.Fatal("actual duplicate item row lock was not observed")
			}
			// Incoming NULL association must retain stored A while the actual duplicate owns A.
			writeCtx, stop := context.WithTimeout(t.Context(), 2*time.Second)
			saved, writeErr := scanner.NewFileRepository(pool).Upsert(writeCtx, models.MediaFile{MediaFolderID: folder, FilePath: file.FilePath})
			stop()
			close(hold.release)
			duplicateErr := <-done
			if writeErr != nil || saved.ID != file.ID || saved.ContentID != a {
				t.Fatalf("ordinary NULL conflict must retain A without new item acquisition: %T %v", writeErr, writeErr)
			}
			var inserts, updates, rawNull int
			if err = pool.QueryRow(t.Context(), "SELECT count(*) FILTER(WHERE op='INSERT'),count(*) FILTER(WHERE op='UPDATE'),count(*) FILTER(WHERE op='INSERT' AND content IS NULL AND episode IS NULL AND extra IS NULL) FROM a_current_file_events").Scan(&inserts, &updates, &rawNull); err != nil || inserts != 1 || updates != 1 || rawNull != 1 {
				t.Fatal("actual INSERT/UPDATE or unchanged incoming I not proved", err)
			}
			t.Log("actual duplicate retained A; INSERT raw NULL I and conflict UPDATE both occurred while A locked; stored A/ID retained")
			if duplicateErr != nil {
				t.Fatal("local duplicate completion remains blocked by actual writer SQL defect", duplicateErr)
			}
		})
	}
}
func TestNativeOnboardingProviderDuplicateDB(t *testing.T) {
	pool := metadataCurrentDatabase(t)
	native, _ := metadataNativeItem(t, pool)
	for _, entrypoint := range []string{"chooseCanonical", "explicitCanonical"} {
		t.Run(entrypoint, func(t *testing.T) {
			local := "fileless-" + uuid.NewString()
			metadataExec(t, pool, "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'ebook','matched','Fileless local')", local)
			// No local file/member/provider/progress cover masks the actual native target stamp/source-delete path.
			var before string
			query := "SELECT jsonb_agg(to_jsonb(i) ORDER BY content_id)::text FROM media_items i WHERE content_id=ANY($1::text[])"
			if err := pool.QueryRow(t.Context(), query, []string{local, native}).Scan(&before); err != nil {
				t.Fatal(err)
			}
			var err error
			switch entrypoint {
			case "chooseCanonical":
				_, err = canonicalizeProviderIDDuplicate(t.Context(), pool, local, native, true)
			case "explicitCanonical":
				_, err = canonicalizeProviderIDDuplicateInto(t.Context(), pool, local, native, true)
			}
			metadataState(t, err, "BN001")
			var after string
			if err = pool.QueryRow(t.Context(), query, []string{local, native}).Scan(&after); err != nil || after != before {
				t.Fatal("actual writer did not roll back complete item state", err)
			}
		})
	}
}

// Real account/membership-policy and production profile creation. Never bare
// obsolete profile SQL or disabled membership-policy triggers.
func metadataCurrentProfile(t *testing.T, pool *pgxpool.Pool) (int, string, userstore.UserStore) {
	t.Helper()
	user, err := auth.NewUserRepository(pool).Create(t.Context(), models.CreateUserInput{Username: "metadata-profile-" + uuid.NewString(), Email: uuid.NewString() + "@example.test", Password: "metadata-profile-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	provider := pgstore.NewPostgresProvider(pool)
	store, err := provider.ForUser(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if err = store.CreateProfile(t.Context(), userstore.Profile{ID: id, Name: "Current metadata profile"}); err != nil {
		t.Fatal("actual current profile Create", err)
	}
	var count int
	if err = pool.QueryRow(t.Context(), `SELECT count(*) FROM user_profiles p JOIN organization_memberships m ON m.organization_id=p.organization_id AND m.account_id=p.user_id WHERE p.user_id=$1 AND p.id=$2 AND m.status='active' AND m.security_revision>0`, user.ID, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("current membership/profile witness missing", err)
	}
	return user.ID, id, store
}
func TestNativeOnboardingBookDropDB(t *testing.T) {
	pool := metadataCurrentDatabase(t)
	native, _ := metadataNativeItem(t, pool)
	user, profile, store := metadataCurrentProfile(t, pool)
	_ = store
	local := "local-series-" + uuid.NewString()
	ebook := "raw-local-ebook-" + uuid.NewString()
	metadataExec(t, pool, "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'series','matched','Local'),($2,'ebook','unmatched','Local ebook')", local, ebook)
	cipher, err := secret.New([]byte(strings.Repeat("a-current-provider-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	provider := watchsync.NewPostgresRepository(pool, cipher)
	connection, err := provider.UpsertConnection(t.Context(), watchsync.Connection{Provider: "a-current", UserID: user, ProfileID: profile, ProviderAccountID: "account", AccessToken: "synthetic", SyncDroppedEnabled: true})
	if err != nil {
		t.Fatal("actual current provider connection", err)
	}
	drops := catalog.NewDroppedSeriesRepo(pool)
	for _, key := range []string{local, ebook} {
		t.Run("local-"+key, func(t *testing.T) {
			if err := drops.Drop(t.Context(), user, profile, key); err != nil {
				t.Fatal(err)
			}
			var stamp time.Time
			if err := pool.QueryRow(t.Context(), "SELECT dropped_at FROM user_dropped_series WHERE user_id=$1 AND profile_id=$2 AND series_id=$3", user, profile, key).Scan(&stamp); err != nil {
				t.Fatal(err)
			}
			if changed, err := drops.ImportDrop(t.Context(), user, profile, key, stamp.Add(time.Second), &stamp); err != nil || !changed {
				t.Fatal("actual timestamp CAS", err)
			}
			state := watchsync.DroppedSyncState{ConnectionID: connection.ID, ProviderAccountID: "account", SeriesID: key, ProviderItemKey: "key", RemoteSeen: false}
			if err := provider.UpsertDroppedSyncStates(t.Context(), []watchsync.DroppedSyncState{state}); err != nil {
				t.Fatal(err)
			}
			state.ProviderItemKey = "revised"
			state.RemoteSeen = true
			if err := provider.UpsertDroppedSyncStates(t.Context(), []watchsync.DroppedSyncState{state}); err != nil {
				t.Fatal("actual same-key fields", err)
			}
			states, err := provider.ListDroppedSyncStates(t.Context(), connection.ID, "account", []string{key})
			if err != nil || len(states) != 1 || states[0].ProviderItemKey != "revised" || !states[0].RemoteSeen {
				t.Fatal("actual provider fields missing", err)
			}
		})
	}
	for _, state := range []string{"native", "disabledSource"} {
		t.Run(state, func(t *testing.T) {
			if state == "disabledSource" {
				metadataExec(t, pool, "UPDATE bloem_storage_sources SET enabled=false")
			}
			metadataState(t, drops.Drop(t.Context(), user, profile, native), "BN001")
			_, err := drops.ImportDrop(t.Context(), user, profile, native, time.Now(), nil)
			metadataState(t, err, "BN001")
			metadataState(t, provider.UpsertDroppedSyncStates(t.Context(), []watchsync.DroppedSyncState{{ConnectionID: connection.ID, ProviderAccountID: "account", SeriesID: native, RemoteSeen: true}}), "BN001")
			var count int
			if err = pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM user_dropped_series WHERE series_id=$1)+(SELECT count(*) FROM watch_provider_dropped_items WHERE series_id=$1)", native).Scan(&count); err != nil || count != 0 {
				t.Fatal("native drop mutation escaped rollback", err)
			}
		})
	}
	if err = provider.DeleteDroppedSyncStates(t.Context(), connection.ID, "account", []string{local, ebook}); err != nil {
		t.Fatal("provider DELETE cleanup", err)
	}
	if err = drops.Undrop(t.Context(), user, profile, local); err != nil {
		t.Fatal("Drop DELETE cleanup", err)
	}
}

func TestNativeOnboardingRebindCollisionDB(t *testing.T) {
	pool := metadataCurrentDatabase(t)
	native, _ := metadataNativeItem(t, pool)
	user, profile, _ := metadataCurrentProfile(t, pool)
	for _, mode := range []string{"postUpLocalOnly", "retainedCorruptCollision"} {
		t.Run(mode, func(t *testing.T) {
			local := "rebind-local-" + uuid.NewString()
			metadataExec(t, pool, "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'ebook','matched','Fileless matched local')", local)
			stamp := time.Now().UTC().Truncate(time.Microsecond)
			metadataExec(t, pool, "INSERT INTO user_dropped_series(user_id,profile_id,series_id,dropped_at) VALUES($1,$2,$3,$4)", user, profile, local, stamp)
			if mode == "retainedCorruptCollision" {
				// Retained-corruption resilience only: seed an impossible OLD native drop
				// with exactly that guard absent, reinstall the original exact trigger
				// definition before calling the real private helper. No Up rerun here.
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				var definition string
				if err = tx.QueryRow(t.Context(), "SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname='bloem_native_user_dropped_series_book'").Scan(&definition); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(t.Context(), "DROP TRIGGER bloem_native_user_dropped_series_book ON user_dropped_series"); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(t.Context(), "INSERT INTO user_dropped_series(user_id,profile_id,series_id,dropped_at) VALUES($1,$2,$3,$4)", user, profile, native, stamp.Add(-time.Hour)); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(t.Context(), definition); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
				if !catalog.NativeStorageSchemaReady(t.Context(), pool) {
					t.Fatal("exact final graph not restored")
				}
			}
			var before string
			query := "SELECT jsonb_agg(to_jsonb(d) ORDER BY series_id)::text FROM user_dropped_series d WHERE series_id=ANY($1::text[])"
			if err := pool.QueryRow(t.Context(), query, []string{local, native}).Scan(&before); err != nil {
				t.Fatal(err)
			}
			err := (&MetadataService{dbPool: pool}).rebindItemToExistingItem(t.Context(), local, native, false)
			metadataState(t, err, "BN001")
			var after string
			if err = pool.QueryRow(t.Context(), query, []string{local, native}).Scan(&after); err != nil || after != before {
				t.Fatal("private false-flag rebind changed drops", err)
			}
			// Cleanup is legal even for retained impossible evidence.
			metadataExec(t, pool, "DELETE FROM user_dropped_series WHERE series_id=ANY($1::text[])", []string{local, native})
		})
	}
}

// Exact per-column guard cases are isolated in their own rollback transaction.
// Every local source is fileless; unrelated required file/collection/profile
// witnesses belong to distinct keys and cannot mask the tested identity site.
func TestNativeOnboardingIdentityGraphDB(t *testing.T) {
	pool := metadataCurrentDatabase(t)
	native, folder := metadataNativeItem(t, pool)
	user, profile, _ := metadataCurrentProfile(t, pool)
	local := "identity-local-" + uuid.NewString()
	metadataExec(t, pool, "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'ebook','matched','Fileless local')", local)
	var localFolder int
	if err := pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('movies','Identity prerequisites',bloem_platform_resource_owner_id()) RETURNING id").Scan(&localFolder); err != nil {
		t.Fatal(err)
	}
	unrelated := "unrelated-" + uuid.NewString()
	metadataExec(t, pool, "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'movie','matched','Unrelated')", unrelated)
	file, err := scanner.NewFileRepository(pool).Upsert(t.Context(), models.MediaFile{ContentID: unrelated, MediaFolderID: localFolder, FilePath: "/local/" + unrelated})
	if err != nil {
		t.Fatal(err)
	}
	collection := "library-" + uuid.NewString()
	personal := "personal-" + uuid.NewString()
	metadataExec(t, pool, "INSERT INTO library_collections(id,library_id,slug,title,collection_type) VALUES($1,$2,$1,'Identity','manual')", collection, localFolder)
	metadataExec(t, pool, "INSERT INTO user_personal_collections(id,user_id,profile_id,name) VALUES($1,$2,$3,'Identity')", personal, user, profile)
	cipher, err := secret.New([]byte(strings.Repeat("identity-provider-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := watchsync.NewPostgresRepository(pool, cipher).UpsertConnection(t.Context(), watchsync.Connection{Provider: "identity", UserID: user, ProfileID: profile, ProviderAccountID: "identity-account", AccessToken: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	sites := map[string][]string{
		"admin_playback_history": {"media_item_id"}, "user_downloads": {"media_item_id"}, "downloads": {"content_id", "episode_id"},
		"user_watch_history": {"media_item_id"}, "user_watch_progress": {"media_item_id"}, "user_favorites": {"media_item_id"}, "user_watchlist": {"media_item_id"}, "user_ratings": {"media_item_id"},
		"user_personal_collection_items": {"media_item_id"}, "library_collection_items": {"media_item_id"}, "user_home_item_dismissals": {"media_item_id", "series_id"}, "user_history_hidden_items": {"media_item_id"},
		"user_audio_preferences": {"series_id"}, "user_subtitle_preferences": {"series_id"}, "user_series_playback_preferences": {"series_id"}, "user_dropped_series": {"series_id"},
		"watch_provider_rating_items": {"media_item_id"}, "watch_provider_dropped_items": {"series_id"}, "ebook_reader_progress": {"content_id"}, "media_item_provider_ids": {"content_id", "item_type"}}
	var tables []string
	for table := range sites {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		for _, column := range sites[table] {
			for _, direction := range []string{"localToNative", "nativeToLocal"} {
				t.Run(table+"/"+column+"/"+direction, func(t *testing.T) {
					tx, err := pool.Begin(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(context.Background())
					start, end := local, native
					if direction == "nativeToLocal" {
						start, end = native, local
					}
					if column == "item_type" {
						start = native
					}
					// Drop INSERT is deliberately impossible for native N; source-side
					// retained corrupt evidence is seeded with its exact book guard absent
					// and restored in this disposable transaction before the UPDATE probe.
					var triggerDef string
					if direction == "nativeToLocal" && (table == "user_dropped_series" || table == "watch_provider_dropped_items") {
						trigger := "bloem_native_" + table + "_book"
						if err = tx.QueryRow(t.Context(), "SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname=$1", trigger).Scan(&triggerDef); err != nil {
							t.Fatal(err)
						}
						if _, err = tx.Exec(t.Context(), "DROP TRIGGER "+pgx.Identifier{trigger}.Sanitize()+" ON "+pgx.Identifier{table}.Sanitize()); err != nil {
							t.Fatal(err)
						}
					}
					values := map[string]any{"user_id": user, "profile_id": profile, "id": uuid.NewString(), "session_id": uuid.NewString(), "media_item_id": start, "series_id": start, "content_id": start, "episode_id": start, "item_type": "ebook", "media_file_id": file.ID, "file_id": file.ID, "location": "chapter", "connection_id": conn.ID, "provider_account_id": "identity-account", "provider": "fixture", "provider_id": uuid.NewString(), "kind": "ebook", "synced_rating": 3, "rating": 3, "surface": "continue_watching", "play_method": "direct", "started_at": time.Now(), "ended_at": time.Now(), "collection_id": collection}
					if table == "user_personal_collection_items" {
						values["collection_id"] = personal
					}
					if column == "item_type" && direction == "nativeToLocal" {
						values["item_type"] = "movie"
					}
					// Include the tested optional column and every actual required column
					// without a default. The live current schema owns this finite row shape.
					rows, err := tx.Query(t.Context(), "SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND ((is_nullable='NO' AND column_default IS NULL) OR column_name=$2) ORDER BY ordinal_position", table, column)
					if err != nil {
						t.Fatal(err)
					}
					var names, marks []string
					var args []any
					for rows.Next() {
						var name string
						if err = rows.Scan(&name); err != nil {
							t.Fatal(err)
						}
						value, ok := values[name]
						if !ok {
							t.Fatalf("named fixture lacks required %s.%s", table, name)
						}
						names = append(names, pgx.Identifier{name}.Sanitize())
						args = append(args, value)
						marks = append(marks, fmt.Sprintf("$%d", len(args)))
					}
					if err = rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					if _, err = tx.Exec(t.Context(), "INSERT INTO "+pgx.Identifier{table}.Sanitize()+"("+strings.Join(names, ",")+") VALUES("+strings.Join(marks, ",")+")", args...); err != nil {
						t.Fatal("isolated row setup", err)
					}
					if triggerDef != "" {
						if _, err = tx.Exec(t.Context(), triggerDef); err != nil {
							t.Fatal(err)
						}
					}
					if column == "item_type" {
						targetType := "movie"
						if direction == "nativeToLocal" {
							targetType = "ebook"
						}
						_, err = tx.Exec(t.Context(), "UPDATE media_item_provider_ids SET item_type=$1 WHERE content_id=$2", targetType, native)
					} else {
						_, err = tx.Exec(t.Context(), "UPDATE "+pgx.Identifier{table}.Sanitize()+" SET "+pgx.Identifier{column}.Sanitize()+"=$1", end)
					}
					metadataState(t, err, "BN001")
				})
			}
		}
	}
	_ = folder
}
