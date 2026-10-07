//go:build integration

package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/imagecache"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanbatch"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/scanqueue"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func acceptanceDatabase(t *testing.T, tracers ...pgx.QueryTracer) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SILO_TEST_DATABASE_URL") != "" {
		t.Fatal("SILO_TEST_DATABASE_URL must be unset")
	}
	const private = "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	info, err := os.Stat(private)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("task-owned mode-0600 DB configuration required")
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
		t.Fatal("create owned HTTP UUID clone")
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cleanup(ctx); err != nil {
			t.Error("HTTP UUID clone cleanup failed")
		} else {
			t.Log("HTTP UUID clone cleanup verified")
		}
	})
	cfg, err := bloemtestdb.NativeOnboardingPoolConfig(dsn)
	if err != nil {
		t.Fatal("unowned HTTP clone config")
	}
	cfg.MaxConns = 8
	if len(tracers) == 1 {
		cfg.ConnConfig.Tracer = tracers[0]
	}
	pool, err = pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("connect guarded HTTP clone")
	}
	var actual string
	if err = pool.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != cfg.ConnConfig.Database || actual == original.ConnConfig.Database {
		t.Fatal("actual HTTP clone identity unverified")
	}
	t.Log("HTTP fresh UUID clone identity verified")
	if err = bloemtestdb.PrepareNativeOnboardingPool(t.Context(), pool); err != nil {
		t.Fatal("prepare current same-pool HTTP clone")
	}
	if !catalog.NativeStorageSchemaReady(t.Context(), pool) {
		t.Fatal("current native schema not ready")
	}
	return pool
}
func acceptanceSQL(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// TestNativeOnboardingHTTPComponentDB exercises the actual sealed router and
// retained management/queue/consumer/reader stores. Readiness stays false until
// the remaining finite mutation wrappers and authority obligations are complete.
func TestNativeOnboardingHTTPComponentDB(t *testing.T) {
	runNativeOnboardingHTTPComponent(t, "success")
}
func TestNativeOnboardingReaderStateSameContentDB(t *testing.T) {
	runNativeOnboardingHTTPComponent(t, "samecontent")
}
func runNativeOnboardingHTTPComponent(t *testing.T, mode string, afterIngest ...func(*testing.T, nativeOnboardingLifecycleFixture)) {
	runNativeOnboardingHTTPWithTracer(t, mode, nil, afterIngest...)
}

func runNativeOnboardingHTTPWithTracer(t *testing.T, mode string, tracer pgx.QueryTracer, afterIngest ...func(*testing.T, nativeOnboardingLifecycleFixture)) {
	if len(afterIngest) > 1 {
		t.Fatal("only one finite after-ingest callback supported")
	}
	pool := acceptanceDatabase(t, tracer)
	binaryPath := filepath.Join(t.TempDir(), "native-acceptance")
	buildCmd := exec.Command("go", "build", "-p", "1", "-o", binaryPath, "./testdata/nativeacceptance")
	buildCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOMAXPROCS=2")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build executable: %v: %s", err, output)
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	checksum := hex.EncodeToString(digest[:])
	manifest := &publicv1.PluginManifest{PluginId: "bloem.consumer.fixture", Version: "1.0.0", SiloApiVersion: "v1", Checksum: checksum, SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}, GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "source", JsonSchema: `{"type":"object","properties":{"mode":{"type":"string"},"notify":{"type":"string"},"revision":{"type":"string"}},"additionalProperties":false}`}}}
	cipher, err := secret.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{"fixture": {Manifest: manifest, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}})
	if err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(t.TempDir(), "provider-io")
	admin, err := auth.NewUserRepository(pool).Create(t.Context(), models.CreateUserInput{
		Username: "http-admin", Email: "http-admin@example.test", Password: "acceptance-password", Role: "admin"})
	if err != nil {
		t.Fatal("create actual administrative account")
	}
	if _, err = tenancy.NewStore(pool).ActivateInitialOwnership(t.Context(), admin.ID); err != nil {
		t.Fatal("activate actual initial ownership")
	}

	// The default organization is pre-created by migrations; assign this synthetic
	// account through actual membership rows rather than request context setters.
	password, err := bcrypt.GenerateFromPassword([]byte("acceptance-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var account, foreignAccount, organizationAdminAccount int
	for _, row := range []struct {
		name string
		id   *int
	}{{"http-reader", &account}, {"http-foreign", &foreignAccount}, {"http-organization-admin", &organizationAdminAccount}} {
		if err = pool.QueryRow(t.Context(), "INSERT INTO users(username,email,password_hash,role) VALUES($1,$2,$3,'user') RETURNING id", row.name, row.name+"@example.test", string(password)).Scan(row.id); err != nil {
			t.Fatal(err)
		}
	}
	var org, owner uuid.UUID
	if err = pool.QueryRow(t.Context(), "SELECT id FROM organizations WHERE is_default").Scan(&org); err != nil {
		t.Fatal(err)
	}
	acceptanceSQL(t, pool, "UPDATE organizations SET status='active',owner_account_id=$1 WHERE id=$2", account, org)
	acceptanceSQL(t, pool, "INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','user') ON CONFLICT(organization_id,account_id) DO UPDATE SET status='active',legacy_role='user'", org, account)
	acceptanceSQL(t, pool, "INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','admin') ON CONFLICT(organization_id,account_id) DO UPDATE SET status='active',legacy_role='admin'", org, organizationAdminAccount)
	if err = pool.QueryRow(t.Context(), "SELECT id FROM resource_owners WHERE organization_id=$1", org).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	var foreignOrg uuid.UUID
	if err = pool.QueryRow(t.Context(), "INSERT INTO organizations(slug,name,status,owner_account_id) VALUES('http-foreign','HTTP foreign','active',$1) RETURNING id", foreignAccount).Scan(&foreignOrg); err != nil {
		t.Fatal(err)
	}
	acceptanceSQL(t, pool, "INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','user') ON CONFLICT(organization_id,account_id) DO UPDATE SET status='active',legacy_role='user'", foreignOrg, foreignAccount)
	var folder, otherFolder int
	if err = pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('ebook','HTTP unrelated books',$1) RETURNING id", owner).Scan(&otherFolder); err != nil {
		t.Fatal("create unrelated local library")
	}

	var group int64
	if err = pool.QueryRow(t.Context(), "INSERT INTO access_groups(organization_id,name,library_ids,is_default) VALUES($1,'HTTP readers',$2,false) RETURNING id", org, []int{}).Scan(&group); err != nil {
		t.Fatal(err)
	}
	acceptanceSQL(t, pool, "UPDATE organization_memberships SET access_group_id=$1 WHERE organization_id=$2 AND account_id=$3", group, org, account)
	acceptanceSQL(t, pool, "INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id) VALUES('http-reader',$1,'HTTP reader',$2,$3),('http-locked',$1,'HTTP locked',$2,$3)", account, org, group)
	var foreignGroup int64
	if err = pool.QueryRow(t.Context(), "INSERT INTO access_groups(organization_id,name,is_default) VALUES($1,'HTTP foreign readers',false) RETURNING id", foreignOrg).Scan(&foreignGroup); err != nil {
		t.Fatal(err)
	}
	acceptanceSQL(t, pool, "INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id) VALUES('http-foreign',$1,'HTTP foreign',$2,$3)", foreignAccount, foreignOrg, foreignGroup)
	pin, err := bcrypt.GenerateFromPassword([]byte("2468"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	acceptanceSQL(t, pool, "UPDATE user_profiles SET pin_hash=$1 WHERE user_id=$2 AND id='http-locked'", string(pin), account)
	repo := storagesource.NewRepository(pool)

	host := &nativestorage.Host{Registry: registry, Manager: storageplugin.NewManager(storageplugin.Config{})}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	files := scanner.NewFileRepository(pool)
	publisher := scanner.NewScanner(files, "", nil, 1, false, 0)
	// Real cover caching and artwork tracking against the owned clone.
	assets, err := blobstore.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cacher := imagecache.New(assets)
	cacher.SetArtworkRevisionTracker(catalog.NewArtworkRevisionTracker(pool))
	publisher.SetImageCacher(cacher)
	consumer, err := libraryingest.NewNativeConsumer(host, repo, resourcetenancy.NewStore(pool), publisher)
	if err != nil {
		t.Fatal(err)
	}
	folders := catalog.NewFolderRepository(pool)
	executor := libraryingest.NewExecutor(publisher, nil, folders, nil, nil, nil)
	executor.SetNativeIngestor(consumer)
	hub := events.NewHub("http-component", nil)
	observed, unsubscribe := hub.Subscribe()
	t.Cleanup(unsubscribe)
	queueRepo := scanqueue.NewRepository(pool)
	queueCtx, stopQueue := context.WithCancel(t.Context())
	queue := scanqueue.NewService(queueRepo, folders, executor, hub, queueCtx, 1, 1)
	var workerCancel context.CancelFunc
	var workerJoined <-chan struct{}
	t.Cleanup(func() {
		queue.Stop()
		stopQueue()
		if workerCancel != nil {
			workerCancel()
		}
		if workerJoined != nil {
			select {
			case <-workerJoined:
				t.Log("actual queue claim worker joined before host/pool cleanup")
			case <-time.After(15 * time.Second):
				t.Error("actual queue claim worker did not join")
			}
		}
	})

	system := policy.NewSystem(policy.NewPolicyStore(pool), nil, nil)
	if err = system.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(system.Stop)
	cfg, err := config.LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Auth.JWTSecret = "synthetic-http-acceptance-signing-key"
	cfg.Auth.AccessTokenExpiry = time.Hour
	cfg.Auth.RefreshTokenExpiry = 24 * time.Hour
	deps := nativeStorageOnboardingDependencies(Dependencies{DB: pool, Config: cfg, AppContext: t.Context(), SecretCipher: cipher, FileRepo: files, FolderRepo: folders, LibraryScanQueue: queue, UserStoreProvider: pgstore.NewPostgresProvider(pool), NativeStorage: host, PolicySystem: system})
	// All requests enter the actual sealed router, including management.
	var routerMu sync.RWMutex
	var actualRouter http.Handler = NewRouter(deps)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routerMu.RLock()
		defer routerMu.RUnlock()
		actualRouter.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	request := func(method, path, token, profile string, headers map[string]string) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if profile != "" {
			req.Header.Set("X-Profile-Id", profile)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, response.Header, data
	}
	// A real login generates the token, account incarnation and durable session.
	login := func(name string) string {
		t.Helper()
		response, err := server.Client().Post(server.URL+"/api/v1/auth/login", "application/json", strings.NewReader(fmt.Sprintf(`{"username":%q,"password":"acceptance-password"}`, name)))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatalf("actual login status=%d", response.StatusCode)
		}
		var envelope struct {
			AccessToken string `json:"access_token"`
		}
		if err = json.Unmarshal(data, &envelope); err != nil || envelope.AccessToken == "" {
			t.Fatal("missing signed login token")
		}
		if _, err = auth.NewJWTService(cfg.Auth.JWTSecret, time.Hour, time.Hour).ValidateToken(envelope.AccessToken); err != nil {
			t.Fatal(err)
		}
		return envelope.AccessToken
	}
	token := login("http-reader")
	foreignToken := login("http-foreign")
	adminToken := login("http-admin")
	command := func(method, path, bearer, contentType string, body io.Reader, want int, target any) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, body)
		if err != nil {
			t.Fatal("construct management request")
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal("management HTTP transport failed")
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("management %s status=%d want=%d", method, response.StatusCode, want)
		}
		if strings.Contains(path, "/native-storage") && response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("management response missing no-store")
		}
		if target != nil && json.NewDecoder(response.Body).Decode(target) != nil {
			t.Fatal("decode management response")
		}
	}
	body := func(value any) io.Reader {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal("encode synthetic command")
		}
		return bytes.NewReader(data)
	}
	exchange := func(bearer string, value any) string {
		var result struct {
			AccessToken string `json:"access_token"`
		}
		command("POST", "/api/bloem/v1/admin/session", bearer, "application/json", body(value), 200, &result)
		if result.AccessToken == "" {
			t.Fatal("missing actual administrative token")
		}
		return result.AccessToken
	}
	platformToken := exchange(adminToken, map[string]any{"scope": "platform"})
	// Owner identity alone is not the current administrative grant.
	command("POST", "/api/bloem/v1/admin/session", token, "application/json", body(map[string]any{"scope": "organization", "organization_id": org}), 403, nil)
	organizationAdminToken := login("http-organization-admin")
	organizationToken := exchange(organizationAdminToken, map[string]any{"scope": "organization", "organization_id": org})
	const platformPrefix = "/api/bloem/v1/admin/platform/native-storage"
	const organizationPrefix = "/api/bloem/v1/admin/organization/native-storage"
	var capability map[string]any
	command("GET", platformPrefix+"/capabilities", platformToken, "", nil, 200, &capability)
	for _, key := range []string{"source_management", "approved_artifact_install", "binding_mutation", "backend_verified"} {
		if capability[key] != false {
			t.Fatal("component advertised incomplete readiness")
		}
	}
	// Immutable approval, actual multipart bytes; no direct registry install.
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	configPart, err := writer.CreateFormField("request")
	if err != nil {
		t.Fatal("create bounded install configuration part")
	}
	payload := map[string]any{"artifact_key": "fixture", "provider_source_id": "books", "root_entry_id": "root", "enabled": true,
		"config": map[string]any{"source": map[string]any{"mode": mode, "notify": journal, "revision": "v1"}}}
	if err = json.NewEncoder(configPart).Encode(payload); err != nil {
		t.Fatal("encode synthetic install configuration")
	}
	binaryPart, err := writer.CreateFormFile("binary", "native-acceptance")
	if err != nil {
		t.Fatal("create binary part")
	}
	if _, err = binaryPart.Write(binary); err != nil {
		t.Fatal("write approved executable bytes")
	}
	if err = writer.Close(); err != nil {
		t.Fatal("complete install request")
	}
	var installed struct {
		Source nativestorage.SourceView `json:"source"`
	}
	command("POST", platformPrefix+"/installations", platformToken, writer.FormDataContentType(), &upload, 201, &installed)
	source := installed.Source
	if source.SourceKey == uuid.Nil || source.InstallationID == nil || source.ConfigurationRevision != 1 {
		t.Fatal("HTTP install lost retained identity")
	}
	// The existing resource-root trigger grants availability on installation.
	// Assert that actual grant instead of inserting a duplicate entitlement.
	var grants int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM organization_entitlements e JOIN plugin_installations p ON p.id=e.plugin_installation_id AND p.owner_id=e.root_owner_id WHERE e.organization_id=$1 AND e.plugin_installation_id=$2 AND e.entitlement_kind=$3 AND e.root_kind=$4 AND e.status=$5", org, *source.InstallationID, "plugin_availability", "plugin_installation", "active").Scan(&grants); err != nil || grants != 1 {
		t.Fatal("HTTP install lacks its actual active availability grant")
	}
	var created nativestorage.LibraryStatus
	command("POST", organizationPrefix+"/libraries", organizationToken, "application/json", body(map[string]any{"name": "HTTP native books", "metadata_language": "en"}), 201, &created)
	if created.LibraryID <= 0 || created.CreationKey == uuid.Nil || created.LibraryRevision != 1 || created.State != "initialization_required" {
		t.Fatal("HTTP create did not return L1")
	}
	folder = created.LibraryID
	var roots int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM media_folder_paths WHERE media_folder_id=$1", folder).Scan(&roots); err != nil || roots != 0 {
		t.Fatal("L1 was not pathless")
	}
	for _, path := range []string{fmt.Sprintf("%s/libraries/%d", organizationPrefix, folder), organizationPrefix + "/libraries/creation/" + created.CreationKey.String()} {
		var envelope struct {
			Library nativestorage.LibraryStatus `json:"library"`
		}
		command("GET", path, organizationToken, "", nil, 200, &envelope)
		recovered := envelope.Library
		if recovered.LibraryID != folder || recovered.CreationKey != created.CreationKey || recovered.LibraryRevision != 1 {
			t.Fatal("L1 read recovery changed identity")
		}
	}
	var listed nativestorage.LibraryPage
	command("GET", organizationPrefix+"/libraries", organizationToken, "", nil, 200, &listed)
	if len(listed.Libraries) != 1 || listed.Libraries[0].LibraryID != folder {
		t.Fatal("lost-create-response list recovery did not retain L1")
	}
	var initialized nativestorage.LibraryStatus
	initializePath := fmt.Sprintf("%s/libraries/%d/initialize", organizationPrefix, folder)
	command("POST", initializePath, organizationToken, "application/json", body(map[string]any{"expected_library_revision": 1}), 200, &initialized)
	if initialized.LibraryID != folder || initialized.CreationKey != created.CreationKey || initialized.LibraryRevision != 2 || initialized.State != "unbound" {
		t.Fatal("same-ID initialize did not reach L2")
	}
	// Repository/service restart only: reconstruct dependencies and the actual
	// sealed router against retained rows. This is not a process restart.
	restarted := deps
	restarted.NativeStorageManagement = nil
	restarted.FolderRepo = catalog.NewFolderRepository(pool)
	restarted = nativeStorageOnboardingDependencies(restarted)
	routerMu.Lock()
	actualRouter = NewRouter(restarted)
	routerMu.Unlock()
	command("POST", initializePath, organizationToken, "application/json", body(map[string]any{"expected_library_revision": 1}), 200, &initialized)
	if initialized.LibraryID != folder || initialized.LibraryRevision != 2 {
		t.Fatal("repository/service restart changed L2 identity")
	}
	var bound nativestorage.BindResult
	bindPath := fmt.Sprintf("%s/sources/%s/bindings/%d", platformPrefix, source.SourceKey, folder)
	command("PUT", bindPath, platformToken, "application/json", body(map[string]any{"expected_source_revision": 1, "expected_library_revision": 2}), 200, &bound)
	if bound.FolderID != folder || bound.BindingID == uuid.Nil || bound.LibraryRevision != 3 || bound.Repeated {
		t.Fatal("HTTP bind did not acknowledge unique L3 COMMIT")
	}
	var committedBinding uuid.UUID
	if err = pool.QueryRow(t.Context(), "SELECT id FROM bloem_storage_bindings WHERE folder_id=$1 AND source_key=$2", folder, source.SourceKey).Scan(&committedBinding); err != nil || committedBinding != bound.BindingID {
		t.Fatal("binding acknowledgement lacks durable COMMIT witness")
	}
	var repeated nativestorage.BindResult
	command("PUT", bindPath, platformToken, "application/json", body(map[string]any{"expected_source_revision": 1, "expected_library_revision": 2}), 200, &repeated)
	if !repeated.Repeated || repeated.BindingID != bound.BindingID || repeated.LibraryRevision != 3 {
		t.Fatal("acknowledged binding retry changed L3 identity")
	}
	var l3Envelope struct {
		Library nativestorage.LibraryStatus `json:"library"`
	}
	command("GET", fmt.Sprintf("%s/libraries/%d", platformPrefix, folder), platformToken, "", nil, 200, &l3Envelope)
	l3 := l3Envelope.Library
	if l3.LibraryID != folder || l3.CreationKey != created.CreationKey || l3.LibraryRevision != 3 || l3.State != "bound" {
		t.Fatal("authorized L3 read did not retain lifecycle identity")
	}
	acceptanceSQL(t, pool, "UPDATE access_groups SET library_ids=$1,configuration_revision=configuration_revision+1 WHERE id=$2", []int{folder}, group)
	var queued nativestorage.ScanResult
	command("POST", fmt.Sprintf("%s/libraries/%d/scan", platformPrefix, folder), platformToken, "application/json", body(map[string]any{"expected_source_revision": 1, "expected_library_revision": 3}), 202, &queued)
	if queued.LibraryID != folder || queued.ScanRunID == "" || !queued.Created {
		t.Fatal("HTTP scan did not accept current queue run")
	}
	run, err := queueRepo.GetByID(t.Context(), queued.ScanRunID)
	if err != nil || run.Status != "accepted" || run.MediaFolderID != folder {
		t.Fatal("HTTP queue acceptance lacks durable COMMIT witness")
	}
	// Reuse the accepted actual repository-claim/executor worker fixture.
	// Service.Start has no join API and is not exercised or accepted here.
	select {
	case event := <-observed:
		var accepted events.ScanRun
		if event.Event != "scan.accepted" || json.Unmarshal(event.Data, &accepted) != nil || accepted.ID != queued.ScanRunID {
			t.Fatal("actual queue did not publish its acknowledged acceptance")
		}
	default:
		t.Fatal("actual queue acceptance event absent after observed COMMIT")
	}
	run, err = queueRepo.ClaimNextAccepted(t.Context(), 1, 1)
	if err != nil || run == nil || run.ID != queued.ScanRunID {
		t.Fatal("actual queue did not claim the acknowledged run")
	}
	scanFolder, err := folders.GetByID(t.Context(), folder)
	if err != nil || scanFolder == nil {
		t.Fatal("actual queue folder load failed")
	}
	workerCtx, cancelWorker := context.WithCancel(scanbatch.WithRunID(queueCtx, run.ID))
	workerCancel = cancelWorker
	joined := make(chan struct{})
	workerJoined = joined
	completed := make(chan error, 1)
	go func() {
		defer close(joined)
		result, workErr := executor.IngestFolder(workerCtx, scanFolder)
		if workErr == nil {
			if result == nil || result.ScanResult == nil {
				workErr = fmt.Errorf("actual queue consumer returned no scan result")
			} else {
				_, workErr = queueRepo.Complete(workerCtx, run.ID, &events.ScanRunResult{New: result.ScanResult.New, Updated: result.ScanResult.Updated, Unchanged: result.ScanResult.Unchanged, Errors: result.ScanResult.Errors})
			}
		}
		completed <- workErr
	}()
	select {
	case workErr := <-completed:
		if workErr != nil {
			// Fixed categories only: do not expose connection strings, rows or paths.
			for cause := workErr; cause != nil; cause = errors.Unwrap(cause) {
				t.Logf("actual worker error type=%T", cause)
			}
			var sqlErr *pgconn.PgError
			if errors.As(workErr, &sqlErr) {
				t.Logf("actual worker SQLSTATE=%s constraint=%s", sqlErr.Code, sqlErr.ConstraintName)
			}
			var nativeErr *catalog.NativeOnboardingError
			if errors.As(workErr, &nativeErr) {
				t.Logf("actual worker native code=%s", nativeErr.Code)
			}
			for _, category := range []string{"scan scan run row", "update native last scanned", "imagecache", "invalid native manifest", "installed executable", "error verifying checksum"} {
				if strings.HasPrefix(workErr.Error(), category) {
					t.Logf("actual worker boundary=%s", category)
				}
			}
			t.Fatalf("actual queue/executor/consumer failed (%T)", workErr)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("actual queue claim worker completion was not observed")
	case <-t.Context().Done():
		t.Fatal("HTTP acceptance cancelled")
	}
	<-joined
	run, err = queueRepo.GetByID(t.Context(), queued.ScanRunID)
	if err != nil || run.Status != "completed" {
		t.Fatal("queue completion lacks durable row")
	}
	type book struct {
		file                   int
		content, entry, format string
		size                   int64
		expected               []byte
	}
	var books []book
	rows, err := pool.Query(t.Context(), "SELECT f.id,f.content_id,r.entry_id,f.container,f.file_size FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.binding_id=$1 ORDER BY r.entry_id", bound.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var b book
		if err = rows.Scan(&b.file, &b.content, &b.entry, &b.format, &b.size); err != nil {
			t.Fatal(err)
		}
		b.expected = acceptanceBytes(t, b.format, mode)
		books = append(books, b)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 {
		t.Fatalf("catalog files=%d", len(books))
	}
	formats := make(map[string]int)
	for _, b := range books {
		formats[b.format]++
	}
	if len(formats) != 2 || formats["epub"] != 1 || formats["pdf"] != 1 {
		t.Fatalf("catalog formats=%v, want {epub:1, pdf:1}", formats)
	}
	for _, b := range books {
		acceptanceSQL(t, pool, "INSERT INTO ebook_reader_progress(user_id,profile_id,content_id,file_id,location,progress) VALUES($1,'http-reader',$2,$3,'acceptance-location',0.25) ON CONFLICT(user_id,profile_id,content_id) DO NOTHING", account, b.content, b.file)
	}
	if len(afterIngest) == 1 {
		afterIngest[0](t, nativeOnboardingLifecycleFixture{pool: pool, host: host, server: server,
			journal: journal, source: source, bound: bound, created: created, account: account,
			readerToken: token, platformToken: platformToken, organizationToken: organizationToken,
			request: request, command: command, body: body, completedScan: queued.ScanRunID,
			support: nativeOnboardingTestSupport{deps: restarted, queue: queue, stopQueue: stopQueue, observed: observed,
				rebuild: func(next Dependencies, wrap func(http.Handler) http.Handler) {
					routerMu.Lock()
					defer routerMu.Unlock()
					actualRouter = NewRouter(next)
					if wrap != nil {
						actualRouter = wrap(actualRouter)
					}
				}}})
		return
	}
	t.Run("reader-state-current-marked-files", func(t *testing.T) {
		store := handlers.NewPGEbookReaderProgressStore(pool)
		for _, b := range books {
			stamp := models.NormalizeFileModifiedAt(time.Now().Add(-time.Hour))
			p := handlers.EbookReaderProgress{UserID: account, ProfileID: "http-reader", ContentID: b.content, FileID: b.file, Location: "chapter1", Progress: 0.4, UpdatedAt: stamp}
			if err := store.Upsert(t.Context(), p); err != nil {
				t.Fatal("initial current reader save refused")
			}
			p.Progress, p.Location, p.UpdatedAt = models.EbookFinishedProgressThreshold, "finished", stamp.Add(time.Second)
			if err := store.UpsertNewer(t.Context(), p); err != nil {
				t.Fatal("finished current reader save refused")
			}
			p.Progress, p.Location, p.UpdatedAt = 0.1, "reopened", stamp.Add(2*time.Second)
			if err := store.Upsert(t.Context(), p); err != nil {
				t.Fatal("retained reader reopen refused")
			}
			saved, err := store.Get(t.Context(), account, "http-reader", b.content)
			if err != nil || saved == nil || saved.FileID != b.file || saved.Progress < models.EbookFinishedProgressThreshold {
				t.Fatal("sticky finished save regressed")
			}
			for _, delta := range []time.Duration{0, -time.Second} {
				p.UpdatedAt, p.Location = saved.UpdatedAt.Add(delta), "stale"
				if err := store.UpsertNewer(t.Context(), p); err != nil {
					t.Fatal("stale event execution failed")
				}
				again, err := store.Get(t.Context(), account, "http-reader", b.content)
				if err != nil || again == nil || again.Location != saved.Location || !again.UpdatedAt.Equal(saved.UpdatedAt) {
					t.Fatal("equal or stale event replaced newer saved state")
				}
			}
			if err := store.Delete(t.Context(), account, "http-reader", b.content); err != nil {
				t.Fatal("explicit unread refused")
			}
			p.UpdatedAt = stamp.Add(3 * time.Second)
			if err := store.Upsert(t.Context(), p); err != nil {
				t.Fatal("fresh unread save refused")
			}
			unread, err := store.Get(t.Context(), account, "http-reader", b.content)
			if err != nil || unread == nil || unread.Progress != 0.1 {
				t.Fatal("explicit unread retained finished progress")
			}
		}
	})

	if mode == "samecontent" {
		t.Run("same-content-EPUB-PDF-switch", func(t *testing.T) {
			if len(books) != 2 || books[0].content != books[1].content || books[0].file == books[1].file {
				t.Fatal("real consumer did not publish distinct formats under one content ID")
			}
			var epub, pdf book
			for _, b := range books {
				if b.format == "epub" {
					epub = b
				} else {
					pdf = b
				}
			}
			store := handlers.NewPGEbookReaderProgressStore(pool)
			stamp := models.NormalizeFileModifiedAt(time.Now().Add(-time.Hour))
			p := handlers.EbookReaderProgress{UserID: account, ProfileID: "http-reader", ContentID: epub.content, FileID: epub.file, Location: "chapter1", Progress: 0.4, UpdatedAt: stamp}
			if err := store.Upsert(t.Context(), p); err != nil {
				t.Fatal("EPUB reader save refused")
			}
			p.FileID, p.Location, p.Progress, p.UpdatedAt = pdf.file, "page2", models.EbookFinishedProgressThreshold, stamp.Add(time.Second)
			if err := store.UpsertNewer(t.Context(), p); err != nil {
				t.Fatal("same-content PDF switch refused")
			}
			switched, err := store.Get(t.Context(), account, "http-reader", epub.content)
			if err != nil || switched == nil || switched.FileID != pdf.file || switched.Location != "page2" {
				t.Fatal("same-content PDF switch lost saved file")
			}
			p.FileID, p.Location, p.Progress, p.UpdatedAt = epub.file, "chapter3", 0.1, stamp.Add(2*time.Second)
			if err := store.Upsert(t.Context(), p); err != nil {
				t.Fatal("same-content EPUB switch refused")
			}
			saved, err := store.Get(t.Context(), account, "http-reader", epub.content)
			if err != nil || saved == nil || saved.FileID != epub.file || saved.Progress < models.EbookFinishedProgressThreshold {
				t.Fatal("same-content switch lost sticky finished state")
			}
			for _, delta := range []time.Duration{0, -time.Second} {
				p.FileID, p.Location, p.UpdatedAt = pdf.file, "stale-page", saved.UpdatedAt.Add(delta)
				if err := store.UpsertNewer(t.Context(), p); err != nil {
					t.Fatal("old alternate-format event execution failed")
				}
				again, err := store.Get(t.Context(), account, "http-reader", epub.content)
				if err != nil || again == nil || again.FileID != epub.file || again.Location != saved.Location || !again.UpdatedAt.Equal(saved.UpdatedAt) {
					t.Fatal("equal or stale alternate-format event replaced current state")
				}
			}
			if err := store.Delete(t.Context(), account, "http-reader", epub.content); err != nil {
				t.Fatal("same-content explicit unread refused")
			}
			p.Progress, p.UpdatedAt = 0.1, stamp.Add(3*time.Second)
			if err := store.Upsert(t.Context(), p); err != nil {
				t.Fatal("fresh alternate-format unread save refused")
			}
			unread, err := store.Get(t.Context(), account, "http-reader", epub.content)
			if err != nil || unread == nil || unread.FileID != pdf.file || unread.Progress != 0.1 {
				t.Fatal("explicit unread retained prior-format finished state")
			}
		})
	}

	// Real PIN verification authorizes the protected profile. Never substitute
	// claims or an unrestricted filter for the current profile-token facility.
	t.Run("verified-PIN-reader", func(t *testing.T) {
		var verified struct {
			Valid        bool
			ProfileToken string `json:"profile_token"`
		}
		command("POST", "/api/v1/profiles/http-locked/verify-pin", token, "application/json", body(map[string]any{"pin": "2468"}), 200, &verified)
		if !verified.Valid || verified.ProfileToken == "" {
			t.Fatal("actual PIN did not issue profile authorization")
		}
		for _, prefix := range []string{"/api/v1", "/api/v2"} {
			b := books[0]
			status, _, data := request("GET", fmt.Sprintf("%s/ebooks/%s/files/%d/read", prefix, b.content, b.file), token, "http-locked", map[string]string{"X-Profile-Token": verified.ProfileToken})
			if status != 200 || !bytes.Equal(data, b.expected) {
				t.Fatal("verified protected profile could not read its retained file")
			}
		}
	})
	t.Run("authorized-reader-save-and-denials", func(t *testing.T) {
		store := handlers.NewPGEbookReaderProgressStore(pool)
		b := books[0]
		save := func(profile, content string, file int, want int) {
			t.Helper()
			req, err := http.NewRequestWithContext(t.Context(), "PUT", server.URL+"/api/v1/ebooks/"+content+"/progress", body(map[string]any{"file_id": file, "location": "http-save", "progress": 0.35}))
			if err != nil {
				t.Fatal("construct reader save")
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Profile-Id", profile)
			req.Header.Set("Content-Type", "application/json")
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal("reader save transport failed")
			}
			defer response.Body.Close()
			if response.StatusCode != want {
				t.Fatalf("reader save status=%d want=%d", response.StatusCode, want)
			}
		}
		save("http-reader", b.content, b.file, 200)
		saved, err := store.Get(t.Context(), account, "http-reader", b.content)
		if err != nil || saved == nil || saved.FileID != b.file || saved.Location != "http-save" {
			t.Fatal("HTTP save did not reach actual reader store")
		}
		for _, rejected := range []struct {
			profile, content string
			file, want       int
		}{
			{"http-locked", b.content, b.file, 403},
			{"http-foreign", b.content, b.file, 404},
			{"http-reader", b.content, books[1].file, 404},
		} {
			if rejected.profile == "http-reader" && books[1].content == b.content {
				continue
			}
			save(rejected.profile, rejected.content, rejected.file, rejected.want)
			again, err := store.Get(t.Context(), account, "http-reader", b.content)
			if err != nil || again == nil || again.FileID != saved.FileID || again.Location != saved.Location || !again.UpdatedAt.Equal(saved.UpdatedAt) {
				t.Fatal("denied profile or foreign-content save changed progress")
			}
		}
	})
	journalBytes := func() []byte {
		t.Helper()
		data, err := os.ReadFile(journal)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return data
	}
	state := func() string {
		t.Helper()
		var s string
		if err := pool.QueryRow(t.Context(), `SELECT json_build_array((SELECT jsonb_agg(to_jsonb(f) ORDER BY id) FROM media_files f),(SELECT jsonb_agg(to_jsonb(i) ORDER BY content_id) FROM media_items i),(SELECT jsonb_agg(to_jsonb(p) ORDER BY content_id) FROM ebook_reader_progress p))::text`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, prefix := range []string{"/api/v1", "/api/v2"} {
		for _, b := range books {
			path := fmt.Sprintf("%s/ebooks/%s/files/%d/read", prefix, b.content, b.file)
			t.Run(prefix+"/"+b.format+"/GET", func(t *testing.T) {
				status, h, data := request("GET", path, token, "http-reader", nil)
				if status != 200 || !bytes.Equal(data, b.expected) || h.Get("Content-Type") != map[string]string{"epub": "application/epub+zip", "pdf": "application/pdf"}[b.format] {
					t.Fatalf("GET=%d headers=%v bytes=%d expected=%d body=%q", status, h, len(data), len(b.expected), data)
				}
			})
			t.Run(prefix+"/"+b.format+"/HEAD", func(t *testing.T) {
				before := journalBytes()
				status, h, data := request("HEAD", path, token, "http-reader", nil)
				delta := string(journalBytes()[len(before):])
				if status != 200 || len(data) != 0 || h.Get("Content-Length") != fmt.Sprint(len(b.expected)) || strings.Contains(delta, "read ") {
					t.Fatalf("HEAD=%d headers=%v body=%d provider=%q", status, h, len(data), delta)
				}
			})
			t.Run(prefix+"/"+b.format+"/range", func(t *testing.T) {
				status, h, data := request("GET", path, token, "http-reader", map[string]string{"Range": "bytes=3-11"})
				if status != 206 || !bytes.Equal(data, b.expected[3:12]) || h.Get("Content-Range") != fmt.Sprintf("bytes 3-11/%d", len(b.expected)) {
					t.Fatalf("range=%d headers=%v body=%q", status, h, data)
				}
			})
			t.Run(prefix+"/"+b.format+"/multipart-range", func(t *testing.T) {
				status, header, data := request("GET", path, token, "http-reader", map[string]string{"Range": "bytes=0-2,5-9"})
				kind, params, err := mime.ParseMediaType(header.Get("Content-Type"))
				if status != 206 || err != nil || kind != "multipart/byteranges" {
					t.Fatal("multipart reader response missing")
				}
				reader := multipart.NewReader(bytes.NewReader(data), params["boundary"])
				for _, span := range [][2]int{{0, 2}, {5, 9}} {
					part, err := reader.NextPart()
					if err != nil {
						t.Fatal("missing multipart range part")
					}
					payload, err := io.ReadAll(part)
					if err != nil || !bytes.Equal(payload, b.expected[span[0]:span[1]+1]) || part.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", span[0], span[1], len(b.expected)) {
						t.Fatal("multipart range bytes changed")
					}
				}
				if _, err := reader.NextPart(); err != io.EOF {
					t.Fatal("unexpected multipart range part")
				}
			})
			t.Run(prefix+"/"+b.format+"/conditional", func(t *testing.T) {
				_, h, _ := request("HEAD", path, token, "http-reader", nil)
				before := journalBytes()
				status, _, data := request("GET", path, token, "http-reader", map[string]string{"If-None-Match": h.Get("ETag")})
				delta := string(journalBytes()[len(before):])
				if status != 304 || len(data) != 0 || h.Get("ETag") == "" || strings.Contains(delta, "read ") {
					t.Fatalf("conditional=%d bytes=%d provider=%q", status, len(data), delta)
				}
			})
		}
	}
	deny := func(t *testing.T, prefix, bearer, profile string, want int) {
		t.Helper()
		b := books[0]
		before, stateBefore := journalBytes(), state()
		status, _, data := request("GET", fmt.Sprintf("%s/ebooks/%s/files/%d/read", prefix, b.content, b.file), bearer, profile, nil)
		if status != want {
			t.Fatalf("denial=%d body=%s", status, data)
		}
		if !bytes.Equal(before, journalBytes()) {
			t.Fatal("denied request touched provider data")
		}
		if stateBefore != state() {
			t.Fatal("denied request changed catalog/progress")
		}
	}
	for _, prefix := range []string{"/api/v1", "/api/v2"} {
		for _, c := range []struct {
			name, bearer, profile string
			want                  int
		}{{"no-token", "", "http-reader", 401}, {"missing-profile", token, "", map[string]int{"/api/v1": 400, "/api/v2": 422}[prefix]}, {"wrong-profile", token, "http-foreign", 404}, {"other-organization", foreignToken, "http-foreign", 401}, {"PIN-required", token, "http-locked", 403}} {
			t.Run(prefix+"/"+c.name, func(t *testing.T) { deny(t, prefix, c.bearer, c.profile, c.want) })
		}
		t.Run(prefix+"/other-library", func(t *testing.T) {
			acceptanceSQL(t, pool, "UPDATE access_groups SET library_ids=$1,configuration_revision=configuration_revision+1 WHERE id=$2", []int{otherFolder}, group)
			defer acceptanceSQL(t, pool, "UPDATE access_groups SET library_ids=$1,configuration_revision=configuration_revision+1 WHERE id=$2", []int{folder}, group)
			deny(t, prefix, token, "http-reader", 404)
		})
		t.Run(prefix+"/revoked-membership", func(t *testing.T) {
			acceptanceSQL(t, pool, "UPDATE organization_memberships SET status='suspended' WHERE account_id=$1 AND organization_id=$2", account, org)
			defer acceptanceSQL(t, pool, "UPDATE organization_memberships SET status='active' WHERE account_id=$1 AND organization_id=$2", account, org)
			deny(t, prefix, token, "http-reader", 403)
		})
		t.Run(prefix+"/revoked-installation-entitlement", func(t *testing.T) {
			acceptanceSQL(t, pool, "UPDATE organization_entitlements SET status='revoked',revoked_at=now(),security_revision=security_revision+1 WHERE organization_id=$1 AND plugin_installation_id=$2", org, *source.InstallationID)
			defer acceptanceSQL(t, pool, "UPDATE organization_entitlements SET status='active',revoked_at=NULL,security_revision=security_revision+1 WHERE organization_id=$1 AND plugin_installation_id=$2", org, *source.InstallationID)
			deny(t, prefix, token, "http-reader", 403)
		})
	}
	for _, mode := range []string{"disabled", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			before := state()
			if mode == "disabled" {
				acceptanceSQL(t, pool, "UPDATE plugin_installations SET enabled=false WHERE id=$1", *source.InstallationID)
				defer acceptanceSQL(t, pool, "UPDATE plugin_installations SET enabled=true WHERE id=$1", *source.InstallationID)
			} else {
				host.Manager.Disable(int(*source.InstallationID))
				if err := os.Remove(binaryPath); err != nil {
					t.Fatal(err)
				} // Remove the approved installed artifact, not a local ebook fallback.
				var installedPath string
				if err := pool.QueryRow(t.Context(), "SELECT install_path FROM plugin_installations WHERE id=$1", *source.InstallationID).Scan(&installedPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(installedPath); err != nil {
					t.Fatal(err)
				}
			}
			for _, prefix := range []string{"/api/v1", "/api/v2"} {
				b := books[0]
				status, _, data := request("GET", fmt.Sprintf("%s/ebooks/%s/files/%d/read", prefix, b.content, b.file), token, "http-reader", nil)
				if status != 503 {
					t.Errorf("%s %s=%d body=%s", mode, prefix, status, data)
				}
			}
			if before != state() {
				t.Fatal("provider failure changed catalog/progress")
			}
		})
	}
}

// Reconstruct the finite fixture's known payload independently of HTTP reads.
func acceptanceBytes(t *testing.T, format string, mode ...string) []byte {
	t.Helper()
	if format == "epub" {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		for _, f := range []struct{ name, data string }{
			{"mimetype", "application/epub+zip"},
			{"META-INF/container.xml", `<?xml version="1.0"?><container><rootfiles><rootfile full-path="OPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
			{"OPS/book.opf", `<package><metadata><title>Embedded EPUB</title><creator>Ada Writer</creator><language>en</language><date>2024-01-02</date></metadata></package>`},
		} {
			w, err := z.Create(f.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write([]byte(f.data)); err != nil {
				t.Fatal(err)
			}
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	if format != "pdf" {
		t.Fatalf("unexpected published format %q", format)
	}
	pdf := []byte("%PDF-1.7\n1 0 obj\n<< /Title (Native PDF) /Author (Ben Writer) /CreationDate (D:20250102000000Z) >>\nendobj\n")
	if len(mode) == 1 && mode[0] == "samecontent" {
		pdf = bytes.ReplaceAll(pdf, []byte("Native PDF"), []byte("Sidecar EPUB"))
		pdf = bytes.ReplaceAll(pdf, []byte("Ben Writer"), []byte("Sidecar Writer"))
		pdf = bytes.ReplaceAll(pdf, []byte("D:20250102000000Z"), []byte("D:20260203000000Z"))
	}
	xref := len(pdf)
	offset := bytes.Index(pdf, []byte("1 0 obj"))
	return fmt.Appendf(pdf, "xref\n0 2\n0000000000 65535 f \n%010d 00000 n \ntrailer\n<< /Size 2 >>\nstartxref\n%d\n%%%%EOF\n", offset, xref)
}
