//go:build integration

package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/imagecache"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func acceptanceDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	// This exact task-owned private file is configuration input, never output.
	path := "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("task-owned mode-0600 DB configuration required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read owned DB configuration")
	}
	cfg, err := pgxpool.ParseConfig(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("invalid owned DB configuration")
	}
	template := cfg.ConnConfig.Database
	if !strings.HasPrefix(template, "bloem_storage_test_") {
		t.Fatal("refusing non-owned template")
	}
	adminCfg := cfg.Copy()
	adminCfg.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(t.Context(), adminCfg)
	if err != nil {
		t.Fatal("connect owned clone admin")
	}
	name := "bloem_storage_test_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal("create owned HTTP clone")
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 6
	cfg.ConnConfig.RuntimeParams["bloem.membership_policy_writer"] = "v1"
	cfg.ConnConfig.RuntimeParams["bloem.schema_capability_writer"] = "v1"
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("connect owned HTTP clone")
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error("drop owned HTTP clone")
		}
		admin.Close()
	})
	for _, m := range []struct{ object, glob string }{
		{"bloem_storage_sources", "*_bloem_native_storage_sources.sql"},
		{"bloem_storage_installations", "*_bloem_native_storage_registry.sql"},
		{"bloem_storage_ingestion", "*_bloem_native_storage_ingestion.sql"},
		{"bloem_storage_entries_sidecar_name_idx", "20261006074157_bloem_native_storage_sidecar_lookup.sql"},
	} {
		var existing *string
		if err = pool.QueryRow(t.Context(), "SELECT to_regclass($1)::text", m.object).Scan(&existing); err != nil {
			t.Fatal("inspect owned clone schema")
		}
		if existing != nil {
			continue
		}
		paths, err := filepath.Glob("../../migrations/sql/" + m.glob)
		if err != nil || len(paths) != 1 {
			t.Fatal("owned migration missing")
		}
		sql, err := os.ReadFile(paths[0])
		if err != nil {
			t.Fatal("read owned migration")
		}
		acceptanceSQL(t, pool, strings.Split(string(sql), "-- +goose Down")[0])
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
func acceptanceSQL(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// TestNativeStorageHTTPAcceptanceDB composes the approved executable, durable
// registry, consumer, ordinary catalog and full HTTP router with real stores.
func TestNativeStorageHTTPAcceptanceDB(t *testing.T) {
	pool := acceptanceDatabase(t)
	binaryPath := filepath.Join(t.TempDir(), "native-acceptance")
	command := exec.Command("go", "build", "-p", "1", "-o", binaryPath, "./testdata/nativeacceptance")
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOMAXPROCS=2")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build executable: %v: %s", err, output)
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	checksum := hex.EncodeToString(digest[:])
	manifest := &publicv1.PluginManifest{PluginId: "bloem.consumer.fixture", Version: "1.0.0", SiloApiVersion: "v1", Checksum: checksum, SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}
	cipher, err := secret.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{"fixture": {Manifest: manifest, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}})
	if err != nil {
		t.Fatal(err)
	}
	var platform uuid.UUID
	if err = pool.QueryRow(t.Context(), "SELECT bloem_platform_resource_owner_id()").Scan(&platform); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(t.TempDir(), "provider-io")
	snapshot, err := registry.Install(t.Context(), plugins.NativeStorageInstallRequest{ArtifactKey: "fixture", Binary: binary, Source: storagesource.SourceConfig{OwnerID: platform, ProviderSourceID: "books", RootEntryID: "root", Enabled: true}, Config: map[string]map[string]any{"source": {"mode": "success", "notify": journal, "revision": "v1"}}})
	if err != nil {
		t.Fatal(err)
	}

	// The default organization is pre-created by migrations; assign this synthetic
	// account through actual membership rows rather than request context setters.
	password, err := bcrypt.GenerateFromPassword([]byte("acceptance-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var account, foreignAccount int
	for _, row := range []struct {
		name string
		id   *int
	}{{"http-reader", &account}, {"http-foreign", &foreignAccount}} {
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
	if err = pool.QueryRow(t.Context(), "SELECT id FROM resource_owners WHERE organization_id=$1", org).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	var foreignOrg uuid.UUID
	if err = pool.QueryRow(t.Context(), "INSERT INTO organizations(slug,name,status,owner_account_id) VALUES('http-foreign','HTTP foreign','active',$1) RETURNING id", foreignAccount).Scan(&foreignOrg); err != nil {
		t.Fatal(err)
	}
	acceptanceSQL(t, pool, "INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','user') ON CONFLICT(organization_id,account_id) DO UPDATE SET status='active',legacy_role='user'", foreignOrg, foreignAccount)
	var folder, otherFolder int
	for _, row := range []struct {
		name string
		id   *int
	}{{"HTTP native books", &folder}, {"HTTP unrelated books", &otherFolder}} {
		if err = pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('ebooks',$1,$2) RETURNING id", row.name, owner).Scan(row.id); err != nil {
			t.Fatal(err)
		}
	}
	var group int64
	if err = pool.QueryRow(t.Context(), "INSERT INTO access_groups(organization_id,name,library_ids,is_default) VALUES($1,'HTTP readers',$2,false) RETURNING id", org, []int{folder}).Scan(&group); err != nil {
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
	acceptanceSQL(t, pool, "INSERT INTO organization_entitlements(organization_id,entitlement_kind,root_kind,root_owner_id,plugin_installation_id,status,granted_by_service) SELECT $1,'plugin_availability','plugin_installation',$2,$3,'active','http-acceptance' WHERE NOT EXISTS(SELECT 1 FROM organization_entitlements WHERE organization_id=$1 AND plugin_installation_id=$3 AND status='active')", org, platform, *snapshot.Source.InstallationID)
	repo := storagesource.NewRepository(pool)
	binding, err := repo.Bind(t.Context(), snapshot.Source.Key, folder)
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
	result, err := consumer.IngestNativeFolder(t.Context(), &models.MediaFolder{ID: folder, Type: "ebooks"})
	if err != nil || result.ScanResult.New != 2 {
		t.Fatalf("publish: %+v %v", result, err)
	}
	type book struct {
		file                   int
		content, entry, format string
		size                   int64
		expected               []byte
	}
	var books []book
	rows, err := pool.Query(t.Context(), "SELECT f.id,f.content_id,r.entry_id,f.container,f.file_size FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.binding_id=$1 ORDER BY r.entry_id", binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var b book
		if err = rows.Scan(&b.file, &b.content, &b.entry, &b.format, &b.size); err != nil {
			t.Fatal(err)
		}
		b.expected = acceptanceBytes(t, b.format)
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
		acceptanceSQL(t, pool, "INSERT INTO ebook_reader_progress(user_id,profile_id,content_id,file_id,location,progress) VALUES($1,'http-reader',$2,$3,'acceptance-location',0.25)", account, b.content, b.file)
	}
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
	server := httptest.NewServer(NewRouter(Dependencies{DB: pool, Config: cfg, AppContext: t.Context(), SecretCipher: cipher, FileRepo: files, UserStoreProvider: pgstore.NewPostgresProvider(pool), NativeStorage: host, PolicySystem: system}))
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
			t.Fatalf("login=%d %s", response.StatusCode, data)
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
			acceptanceSQL(t, pool, "UPDATE organization_entitlements SET status='revoked',revoked_at=now(),security_revision=security_revision+1 WHERE organization_id=$1 AND plugin_installation_id=$2", org, *snapshot.Source.InstallationID)
			defer acceptanceSQL(t, pool, "UPDATE organization_entitlements SET status='active',revoked_at=NULL,security_revision=security_revision+1 WHERE organization_id=$1 AND plugin_installation_id=$2", org, *snapshot.Source.InstallationID)
			deny(t, prefix, token, "http-reader", 403)
		})
	}
	for _, mode := range []string{"disabled", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			before := state()
			if mode == "disabled" {
				acceptanceSQL(t, pool, "UPDATE plugin_installations SET enabled=false WHERE id=$1", *snapshot.Source.InstallationID)
				defer acceptanceSQL(t, pool, "UPDATE plugin_installations SET enabled=true WHERE id=$1", *snapshot.Source.InstallationID)
			} else {
				host.Manager.Disable(int(*snapshot.Source.InstallationID))
				if err := os.Remove(binaryPath); err != nil {
					t.Fatal(err)
				} // Remove the approved installed artifact, not a local ebook fallback.
				var installedPath string
				if err := pool.QueryRow(t.Context(), "SELECT install_path FROM plugin_installations WHERE id=$1", *snapshot.Source.InstallationID).Scan(&installedPath); err != nil {
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
func acceptanceBytes(t *testing.T, format string) []byte {
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
	xref := len(pdf)
	offset := bytes.Index(pdf, []byte("1 0 obj"))
	return fmt.Appendf(pdf, "xref\n0 2\n0000000000 65535 f \n%010d 00000 n \ntrailer\n<< /Size 2 >>\nstartxref\n%d\n%%%%EOF\n", offset, xref)
}
