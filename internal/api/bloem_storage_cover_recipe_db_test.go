//go:build integration

package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/artworkurl"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func coverRecipeDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("Task8 requires SILO_TEST_DATABASE_URL")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !bloemtestdb.IsTestDatabaseName(cfg.ConnConfig.Database) {
		t.Fatal("Task8 requires a disposable test database")
	}
	adminConfig := cfg.Copy()
	adminConfig.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(t.Context(), adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	name := "task8_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{cfg.ConnConfig.Database}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	parsed, _ := url.Parse(dsn)
	parsed.Path = "/" + name
	if err = bloemtestdb.Prepare(t.Context(), parsed.String(), bloemtestdb.Options{}); err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		admin.Close()
		if err != nil {
			t.Error(err)
		}
	})
	return pool
}

type coverDBSource struct {
	data  []byte
	reads atomic.Int32
}

func (s *coverDBSource) Stat(_ context.Context, r mediasource.Ref) (mediasource.Info, error) {
	return mediasource.Info{Name: "cover.jpg", Revision: r.Revision, Size: int64(len(s.data))}, nil
}
func (s *coverDBSource) ReadRange(_ context.Context, _ mediasource.Ref, offset, length int64, w io.Writer) error {
	s.reads.Add(1)
	_, err := w.Write(s.data[offset : offset+length])
	return err
}

type coverDBRuntime struct{ lease nativestorage.Lease }

func (s coverDBRuntime) Borrow(context.Context, storageplugin.Snapshot) (nativestorage.Lease, error) {
	return s.lease, nil
}
func (coverDBRuntime) Disable(int) {}

func TestStorageCoverFreshSessionResourceRevocationDB(t *testing.T) {
	for _, mutation := range []string{"session", "profile", "pin", "account", "library", "shared-source", "root", "membership", "malformed-key"} {
		t.Run(mutation, func(t *testing.T) {
			pool := coverRecipeDatabase(t)
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			var uid int
			if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled) VALUES('cover-viewer','cover@example.test','x','user',true) RETURNING id`).Scan(&uid); err != nil {
				t.Fatal(err)
			}
			stores := pgstore.NewPostgresProvider(pool)
			store, err := stores.ForUser(t.Context(), uid)
			if err != nil {
				t.Fatal(err)
			}
			pid := uuid.NewString()
			if err = store.CreateProfile(t.Context(), userstore.Profile{ID: pid, Name: "Cover viewer", PINHash: "synthetic-pin-hash"}); err != nil {
				t.Fatal(err)
			}
			profile, err := store.GetProfile(t.Context(), pid)
			if err != nil {
				t.Fatal(err)
			}
			sid := uuid.NewString()
			if err = auth.NewSessionRepository(pool).Create(t.Context(), models.AuthSession{ID: sid, UserID: uid, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			var folder int
			if err = pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name,enabled) VALUES('ebooks','Recipe fixture',true) RETURNING id`).Scan(&folder); err != nil {
				t.Fatal(err)
			}
			var install int64
			if err = pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,runtime_generation,enabled) VALUES('recipe-fixture','1','/synthetic',1,true) RETURNING id`).Scan(&install); err != nil {
				t.Fatal(err)
			}
			var owner uuid.UUID
			if err = pool.QueryRow(t.Context(), `SELECT owner_id FROM plugin_installations WHERE id=$1`, install).Scan(&owner); err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, install, owner)
			exec(`INSERT INTO organization_entitlements(organization_id,entitlement_kind,root_kind,root_owner_id,plugin_installation_id,status,granted_by_service) SELECT $1,'plugin_availability','plugin_installation',$2,$3,'active','cover-test' WHERE NOT EXISTS(SELECT 1 FROM organization_entitlements WHERE organization_id=$1 AND plugin_installation_id=$3)`, profile.OrganizationID, owner, install)
			repo := storagesource.NewRepository(pool)
			source, err := repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: owner, InstallationID: &install, PluginID: "recipe-fixture", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			location, err := repo.AddLocationTx(t.Context(), tx, source.Key, folder)
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			path, err := storagesource.CatalogLocation(location.ID, "book")
			if err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO media_items(content_id,type,title) VALUES('recipe-book','ebook','Recipe book')`)
			exec(`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES('recipe-book',$1)`, folder)
			var fid int
			if err = pool.QueryRow(t.Context(), `INSERT INTO media_files(content_id,media_folder_id,file_path,container,file_size) VALUES('recipe-book',$1,$2,'epub',4) RETURNING id`, folder, path).Scan(&fid); err != nil {
				t.Fatal(err)
			}
			tx, err = pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err = storagesource.AttachFilesTx(t.Context(), tx, 1, []storagesource.FileRef{{MediaFileID: fid, PersistedRef: storagesource.PersistedRef{LocationID: location.ID, EntryID: "book", Revision: "v1", LogicalPath: "book.epub"}, CoverEntryID: "cover/book", CoverRevision: "c1"}}); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			cipher, err := secret.New([]byte(strings.Repeat("task8-recipe-test-key", 2)))
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{}
			cfg.Auth.JWTSecret = "task8-recipe-jwt"
			signer := artworkurl.NewSigner(cfg.Auth.JWTSecret, time.Hour)
			deps := Dependencies{DB: pool, Config: cfg, SecretCipher: cipher, UserStoreProvider: stores, FileRepo: scanner.NewFileRepository(pool)}
			policy := nativeStorageActorPolicy(deps)
			registry := &coverRecipeRegistry{snapshot: &plugins.NativeStorageSnapshot{Source: source, Installation: &plugins.Installation{ID: int(install), Enabled: true}, Generation: 1, ArtifactChecksum: "approved"}}
			files := &handlers.MediaFileAuthorizer{FileResolver: deps.FileRepo, ItemAccess: catalog.NewItemRepository(pool)}
			service := &nativeStorageArtwork{next: artworkurl.NewServerResolver(signer), signer: signer, cipher: cipher, actors: policy, references: repo, registry: registry, authorizeFile: func(ctx context.Context, ref storagesource.CoverReference) error {
				file, err := files.AuthorizeContext(ctx, ref.MediaFileID, handlers.AccessFilterFromContext(ctx, ""))
				if err != nil {
					return err
				}
				if file.FilePath != ref.FilePath {
					return storagesource.ErrReferenceConflict
				}
				return nil
			}}
			jwtService := auth.NewJWTService(cfg.Auth.JWTSecret, time.Hour, time.Hour)
			token, err := jwtService.GenerateAccessToken(uid, "user", sid)
			if err != nil {
				t.Fatal(err)
			}
			claims, err := jwtService.ValidateToken(token)
			if err != nil {
				t.Fatal(err)
			}
			tenant, err := policy.tenants.ResolveSubjectTenant(t.Context(), uid, pid)
			if err != nil {
				t.Fatal(err)
			}
			pin, _, err := access.NewProfileTokenService(cfg.Auth.JWTSecret, 0).Mint(access.ProfileTokenClaims{UserID: uid, SessionID: sid, ProfileID: pid, PINRevision: profile.PINRevision})
			if err != nil {
				t.Fatal(err)
			}
			ctx := tenancy.WithContext(middleware.SetClaims(t.Context(), claims), tenant)
			scope, err := policy.viewer.ResolveCurrent(ctx, access.ResolveInput{UserID: uid, SessionID: sid, ProfileID: pid, ProfileToken: pin})
			if err != nil {
				t.Fatal(err)
			}
			ctx = access.SetScope(ctx, scope)
			ctx = middleware.SetProfileID(ctx, pid)
			ctx = context.WithValue(ctx, nativeVideoRequestKey{}, nativeVideoRequest{authorization: "Bearer " + token, profileToken: pin, method: "GET", path: "/api/v2/items/recipe-book"})
			key := artworkkey.StorageCoverKey("recipe-book", "cover/book", "c1")
			resolved := service.ResolveURLs(ctx, []string{key})[key]
			if resolved.URL == "" {
				t.Fatal("fresh database viewer did not get recipe")
			}
			host := &nativestorage.Host{}
			host.SetCoverRequestAuthorizer(service.authorizeRequest)
			provider := &coverDBSource{data: append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte("cover"), 16)...)}
			coordinator := &nativestorage.Coordinator{References: repo, Registry: registry, Runtime: coverDBRuntime{nativestorage.Lease{Context: t.Context(), Source: provider, Description: &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: "books", RootEntryId: "root", RevisionPinnedReads: true}}}}}, AuthorizeCover: resourcetenancy.NewStore(pool).RequireStorageScan}
			host.SetCoverReader(coordinator)
			redisURL := os.Getenv("SILO_TEST_REDIS_URL")
			if redisURL == "" {
				t.Fatal("Task8 requires SILO_TEST_REDIS_URL")
			}
			options, err := redis.ParseURL(redisURL)
			if err != nil {
				t.Fatal(err)
			}
			client := redis.NewClient(options)
			defer client.Close()
			host.SetCoverCache(nativestorage.NewRedisCoverCache(client, time.Minute))
			artworkStore, err := blobstore.NewFilesystem(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			handler := apiv2.NewBloemArtworkHandler(artworkStore, signer, nil, host)
			request := func(etag string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, resolved.URL, nil)
				if etag != "" {
					req.Header.Set("If-None-Match", etag)
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec
			}
			first := request("")
			if first.Code != 200 {
				t.Fatalf("fresh cover: %d %s", first.Code, first.Body.String())
			}
			if hit := request(""); hit.Code != 200 || provider.reads.Load() != 1 {
				t.Fatalf("authorized Redis reuse: status=%d provider reads=%d", hit.Code, provider.reads.Load())
			}
			etag := first.Header().Get("ETag")
			if conditional := request(etag); conditional.Code != 304 {
				t.Fatalf("fresh conditional=%d", conditional.Code)
			}
			switch mutation {
			case "session":
				exec(`UPDATE auth_sessions SET revoked_at=now() WHERE id=$1`, sid)
			case "profile":
				exec(`DELETE FROM user_profiles WHERE user_id=$1 AND id=$2`, uid, pid)
			case "pin":
				exec(`UPDATE user_profiles SET pin_revision=pin_revision+1 WHERE user_id=$1 AND id=$2`, uid, pid)
			case "account":
				exec(`UPDATE users SET enabled=false WHERE id=$1`, uid)
			case "library":
				exec(`UPDATE media_folders SET enabled=false WHERE id=$1`, folder)
			case "shared-source":
				exec(`UPDATE organization_entitlements SET status='revoked',revoked_at=now() WHERE organization_id=$1 AND plugin_installation_id=$2`, profile.OrganizationID, install)
			case "root":
				exec(`UPDATE bloem_storage_selected_roots SET enabled=false WHERE location_id=$1`, location.ID)
			case "membership":
				exec(`UPDATE bloem_storage_file_roots SET enabled=false WHERE location_id=$1`, location.ID)
			case "malformed-key":
				exec(`UPDATE media_files SET file_path='bloem-storage:malformed' WHERE id=$1`, fid)
			}
			for _, condition := range []string{"", etag} {
				denied := request(condition)
				if denied.Code != 404 || denied.Header().Get("ETag") != "" || bytes.Contains(denied.Body.Bytes(), provider.data) {
					t.Fatalf("revoked cache/304: status=%d etag=%q", denied.Code, denied.Header().Get("ETag"))
				}
			}
			if provider.reads.Load() != 1 {
				t.Fatalf("denied cover read provider %d times", provider.reads.Load())
			}
		})
	}
}

// TestStorageCoverRecipeChoosesViewersPublicationDB publishes one source and
// cover into two libraries. A profile restricted to the second library still
// receives a recipe, pinned to that library's publication, through the real
// repository, profile library filter, tenancy and resource owners.
func TestStorageCoverRecipeChoosesViewersPublicationDB(t *testing.T) {
	pool := coverRecipeDatabase(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var uid int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled) VALUES('cover-shared','cover-shared@example.test','x','user',true) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	folders := make([]int, 2)
	for i := range folders {
		if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name,enabled) VALUES('ebooks',$1,true) RETURNING id`, fmt.Sprintf("Shared cover %d", i+1)).Scan(&folders[i]); err != nil {
			t.Fatal(err)
		}
	}
	stores := pgstore.NewPostgresProvider(pool)
	store, err := stores.ForUser(t.Context(), uid)
	if err != nil {
		t.Fatal(err)
	}
	pid := uuid.NewString()
	if err = store.CreateProfile(t.Context(), userstore.Profile{ID: pid, Name: "Second library only", PINHash: "synthetic-pin-hash", LibraryRestrictionsEnabled: true, AllowedLibraryIDs: []int{folders[1]}}); err != nil {
		t.Fatal(err)
	}
	profile, err := store.GetProfile(t.Context(), pid)
	if err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if err = auth.NewSessionRepository(pool).Create(t.Context(), models.AuthSession{ID: sid, UserID: uid, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	var install int64
	if err = pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,runtime_generation,enabled) VALUES('recipe-fixture','1','/synthetic',1,true) RETURNING id`).Scan(&install); err != nil {
		t.Fatal(err)
	}
	var owner uuid.UUID
	if err = pool.QueryRow(t.Context(), `SELECT owner_id FROM plugin_installations WHERE id=$1`, install).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, install, owner)
	exec(`INSERT INTO organization_entitlements(organization_id,entitlement_kind,root_kind,root_owner_id,plugin_installation_id,status,granted_by_service) SELECT $1,'plugin_availability','plugin_installation',$2,$3,'active','cover-test' WHERE NOT EXISTS(SELECT 1 FROM organization_entitlements WHERE organization_id=$1 AND plugin_installation_id=$3)`, profile.OrganizationID, owner, install)
	repo := storagesource.NewRepository(pool)
	source, err := repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: owner, InstallationID: &install, PluginID: "recipe-fixture", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO media_items(content_id,type,title) VALUES('shared-book','ebook','Shared book')`)
	files := make([]int, 2)
	locations := make([]storagesource.Location, 2)
	for i, folder := range folders {
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if locations[i], err = repo.AddLocationTx(t.Context(), tx, source.Key, folder); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		path, err := storagesource.CatalogLocation(locations[i].ID, "book")
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES('shared-book',$1)`, folder)
		if err = pool.QueryRow(t.Context(), `INSERT INTO media_files(content_id,media_folder_id,file_path,container,file_size) VALUES('shared-book',$1,$2,'epub',4) RETURNING id`, folder, path).Scan(&files[i]); err != nil {
			t.Fatal(err)
		}
		if tx, err = pool.Begin(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = storagesource.AttachFilesTx(t.Context(), tx, 1, []storagesource.FileRef{{MediaFileID: files[i], PersistedRef: storagesource.PersistedRef{LocationID: locations[i].ID, EntryID: "book", Revision: "v1", LogicalPath: "book.epub"}, CoverEntryID: "cover/book", CoverRevision: "c1"}}); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	revision := artworkkey.StorageCoverRevision("cover/book", "c1")
	candidates, err := repo.CoverReferences(t.Context(), "shared-book", revision)
	if err != nil || len(candidates) != 2 || candidates[0].MediaFileID != files[0] || candidates[1].MediaFileID != files[1] {
		t.Fatalf("both publications in file order: %+v %v", candidates, err)
	}
	cipher, err := secret.New([]byte(strings.Repeat("task8-recipe-test-key", 2)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "task8-recipe-jwt"
	signer := artworkurl.NewSigner(cfg.Auth.JWTSecret, time.Hour)
	deps := Dependencies{DB: pool, Config: cfg, SecretCipher: cipher, UserStoreProvider: stores, FileRepo: scanner.NewFileRepository(pool)}
	policy := nativeStorageActorPolicy(deps)
	registry := &coverRecipeRegistry{snapshot: &plugins.NativeStorageSnapshot{Source: source, Installation: &plugins.Installation{ID: int(install), Enabled: true}, Generation: 1, ArtifactChecksum: "approved"}}
	authorizer := &handlers.MediaFileAuthorizer{FileResolver: deps.FileRepo, ItemAccess: catalog.NewItemRepository(pool)}
	service := &nativeStorageArtwork{next: artworkurl.NewServerResolver(signer), signer: signer, cipher: cipher, actors: policy, references: repo, registry: registry, authorizeFile: func(ctx context.Context, ref storagesource.CoverReference) error {
		file, err := authorizer.AuthorizeContext(ctx, ref.MediaFileID, handlers.AccessFilterFromContext(ctx, ""))
		if err != nil {
			return err
		}
		if file.FilePath != ref.FilePath {
			return storagesource.ErrReferenceConflict
		}
		return nil
	}}
	jwtService := auth.NewJWTService(cfg.Auth.JWTSecret, time.Hour, time.Hour)
	token, err := jwtService.GenerateAccessToken(uid, "user", sid)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := jwtService.ValidateToken(token)
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := policy.tenants.ResolveSubjectTenant(t.Context(), uid, pid)
	if err != nil {
		t.Fatal(err)
	}
	pin, _, err := access.NewProfileTokenService(cfg.Auth.JWTSecret, 0).Mint(access.ProfileTokenClaims{UserID: uid, SessionID: sid, ProfileID: pid, PINRevision: profile.PINRevision})
	if err != nil {
		t.Fatal(err)
	}
	ctx := tenancy.WithContext(middleware.SetClaims(t.Context(), claims), tenant)
	scope, err := policy.viewer.ResolveCurrent(ctx, access.ResolveInput{UserID: uid, SessionID: sid, ProfileID: pid, ProfileToken: pin})
	if err != nil {
		t.Fatal(err)
	}
	if !scope.LibrariesRestricted || len(scope.AllowedLibraryIDs) != 1 || scope.AllowedLibraryIDs[0] != folders[1] {
		t.Fatalf("profile library filter not applied: %+v", scope.AllowedLibraryIDs)
	}
	ctx = access.SetScope(ctx, scope)
	ctx = middleware.SetProfileID(ctx, pid)
	ctx = context.WithValue(ctx, nativeVideoRequestKey{}, nativeVideoRequest{authorization: "Bearer " + token, profileToken: pin, method: "GET", path: "/api/v2/items/shared-book"})
	if service.authorizeFile(ctx, candidates[0]) == nil || service.authorizeFile(ctx, candidates[1]) != nil {
		t.Fatal("viewer filter must deny the first library and admit the second")
	}
	key := artworkkey.StorageCoverKey("shared-book", "cover/book", "c1")
	resolved := service.ResolveURLs(ctx, []string{key})[key]
	if resolved.URL == "" {
		t.Fatal("viewer restricted to the second library lost its cover")
	}
	host := &nativestorage.Host{}
	host.SetCoverRequestAuthorizer(service.authorizeRequest)
	provider := &coverDBSource{data: append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte("cover"), 16)...)}
	host.SetCoverReader(&nativestorage.Coordinator{References: repo, Registry: registry, Runtime: coverDBRuntime{nativestorage.Lease{Context: t.Context(), Source: provider, Description: &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: "books", RootEntryId: "root", RevisionPinnedReads: true}}}}}, AuthorizeCover: resourcetenancy.NewStore(pool).RequireStorageScan})
	artworkStore, err := blobstore.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := apiv2.NewBloemArtworkHandler(artworkStore, signer, nil, host)
	request := func(etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, resolved.URL, nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	first := request("")
	if first.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), provider.data) {
		t.Fatalf("second library cover: %d", first.Code)
	}
	if conditional := request(first.Header().Get("ETag")); conditional.Code != http.StatusNotModified {
		t.Fatalf("second library conditional=%d", conditional.Code)
	}
	// The recipe is pinned to the second publication; removing it never
	// switches the recipe to the first.
	exec(`UPDATE bloem_storage_file_roots SET enabled=false WHERE location_id=$1`, locations[1].ID)
	if denied := request(first.Header().Get("ETag")); denied.Code != http.StatusNotFound {
		t.Fatalf("recipe switched publication: %d", denied.Code)
	}
	if again := service.ResolveURLs(ctx, []string{key})[key]; again.URL != "" {
		t.Fatal("issuer chose a publication outside the viewer's libraries")
	}
}
