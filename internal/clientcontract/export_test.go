package clientcontract

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/cache"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

const officialSiloBaselineCommit = "1dcdd4b27ab5fcd697a32fc20f20c2400ca24688"

var disposableDatabasePattern = regexp.MustCompile(`^vondel_client_contract_[a-f0-9]{16}$`)

type disposableDatabase struct {
	name    string
	dsn     string
	admin   *pgxpool.Pool
	pool    *pgxpool.Pool
	mu      sync.Mutex
	dropped bool
}

func newDisposableDatabaseName() (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate database suffix: %w", err)
	}
	return "vondel_client_contract_" + hex.EncodeToString(random), nil
}

func validateDisposableDatabaseName(name string) error {
	if !disposableDatabasePattern.MatchString(name) {
		return fmt.Errorf("refusing database operation outside disposable namespace: %q", name)
	}
	return nil
}

func createDisposableDatabase(t *testing.T, ctx context.Context, adminURL string) *disposableDatabase {
	t.Helper()
	name, err := newDisposableDatabaseName()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDisposableDatabaseName(name); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect admin database: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping admin database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database %q: %v", name, err)
	}
	parsed, err := pgxpool.ParseConfig(adminURL)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		t.Fatalf("parse admin database URL: %v", err)
	}
	parsed.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, parsed)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		t.Fatalf("connect disposable database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		t.Fatalf("ping disposable database: %v", err)
	}
	return &disposableDatabase{name: name, dsn: parsed.ConnString(), admin: admin, pool: pool}
}

func (db *disposableDatabase) cleanup(t *testing.T, ctx context.Context) {
	t.Helper()
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.dropped {
		return
	}
	if err := validateDisposableDatabaseName(db.name); err != nil {
		t.Error(err)
		return
	}
	if db.pool != nil {
		db.pool.Close()
	}
	_, _ = db.admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, db.name)
	if _, err := db.admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{db.name}.Sanitize()); err != nil {
		t.Errorf("drop disposable database %q: %v", db.name, err)
		return
	}
	db.dropped = true
}

func databaseExists(t *testing.T, ctx context.Context, admin *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		t.Fatalf("query database existence: %v", err)
	}
	return exists
}

func migrateAndSeed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	migrationCtx, cancel := database.MigrationContext(ctx)
	defer cancel()
	if err := database.RunMigrations(migrationCtx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	seedInventedMedia(t, ctx, pool)
}

func seedInventedMedia(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	fixtures := []struct {
		id, mediaType, title string
	}{
		{"4242", "movie", "The Invented Crossing"},
		{"8080", "series", "Eight Quiet Rooms"},
		{"313", "album", "Prime Meridian"},
		{"2718", "audiobook", "The Exponential Garden"},
		{"1618", "ebook", "Golden Margins"},
		{"5772", "manga", "Panel Horizon"},
	}
	for _, fixture := range fixtures {
		if _, err := pool.Exec(ctx, `
INSERT INTO media_items (content_id, type, title, status, genres, keywords)
VALUES ($1, $2, $3, 'matched', '{}'::text[], '{}'::text[])
ON CONFLICT (content_id) DO NOTHING`, fixture.id, fixture.mediaType, fixture.title); err != nil {
			t.Fatalf("seed %s fixture %s: %v", fixture.mediaType, fixture.id, err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM media_items WHERE content_id = ANY($1)`, []string{"4242", "8080", "313", "2718", "1618", "5772"}).Scan(&count); err != nil {
		t.Fatalf("validate invented seed: %v", err)
	}
	if count != len(fixtures) {
		t.Fatalf("invented seed count = %d, want %d", count, len(fixtures))
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_folders (id, type, name, enabled) VALUES (4242, 'movie', 'Invented Playback Files', true)`); err != nil {
		t.Fatalf("seed playback media folder: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_item_libraries (content_id, media_folder_id) SELECT unnest($1::text[]), 4242`, []string{"4242", "8080", "313", "2718", "1618", "5772"}); err != nil {
		t.Fatalf("seed contract library memberships: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT setval('media_files_id_seq', 4242000, true)`); err != nil {
		t.Fatalf("reserve invented playback file id: %v", err)
	}
	playbackPath := filepath.Join(t.TempDir(), "the-invented-crossing.mp4")
	if err := os.WriteFile(playbackPath, []byte("invented contract media bytes\n"), 0o600); err != nil {
		t.Fatalf("create invented playback media: %v", err)
	}
	file, err := scanner.NewFileRepository(pool).Upsert(ctx, models.MediaFile{
		ContentID: "4242", MediaFolderID: 4242, FilePath: playbackPath,
		FileSize: 1048576, CodecVideo: "h264", CodecAudio: "aac", Resolution: "1080p",
		AudioChannels: 2, Container: "mp4", Duration: 6480, Bitrate: 8000,
	})
	if err != nil {
		t.Fatalf("seed playback media file: %v", err)
	}
	if file.ID != 4242001 {
		t.Fatalf("playback media file id = %d, want 4242001", file.ID)
	}
}

func migrateOfficialSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool, worktree string) {
	t.Helper()
	sqlDB := stdlib.OpenDB(*pool.Config().ConnConfig)
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS(filepath.Join(worktree, "migrations", "sql")), goose.WithTableName("public.goose_db_version"), goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatalf("create pinned official migration provider: %v", err)
	}
	defer provider.Close()
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("migrate pinned official schema: %v", err)
	}
}

func seedAccountScopedFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var userID int
	var profileID string
	if err := pool.QueryRow(ctx, `SELECT u.id, p.id FROM users u JOIN user_profiles p ON p.user_id = u.id WHERE u.username = 'fixture.viewer' ORDER BY p.is_primary DESC LIMIT 1`).Scan(&userID, &profileID); err != nil {
		t.Fatalf("resolve fixture account scope: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_devices (user_id, profile_id, device_id, device_name, device_platform) VALUES ($1, $2, 'fixture-device-0001', 'Invented Device', 'conformance') ON CONFLICT DO NOTHING`, userID, profileID); err != nil {
		t.Fatalf("seed fixture device: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO downloads (id, user_id, profile_id, device_id, media_file_id, content_id, kind, status, format, quality, effective_quality, revision, file_size)
VALUES ('fixture-download-1618', $1, $2, 'fixture-device-0001', 4242001, '1618', 'direct', 'ready', 'original', 'original', 'original', 1, 1048576)
ON CONFLICT (id) DO NOTHING`, userID, profileID); err != nil {
		t.Fatalf("seed offline fixture: %v", err)
	}
}

func startVondelServer(t *testing.T, pool *pgxpool.Pool) *httptest.Server {
	t.Helper()
	cfg, err := config.LoadFromDB(map[string]string{
		"auth.jwt_secret":                "fixture-jwt-secret-at-least-thirty-two-characters",
		"rate_limit.enabled":             "false",
		"download.enabled":               "true",
		"jellyfin_compat.server_name":    "Bloem Contract Fixture",
		"jellyfin_compat.server_id":      "fixture-server-0001",
		"playback.transcode_enabled":     "false",
		"download.transcode_enabled":     "false",
		"recommendations.enabled":        "false",
		"recommendations.worker_enabled": "false",
	})
	if err != nil {
		t.Fatalf("build fixture config: %v", err)
	}
	cipher, err := secret.New([]byte("fixture-master-key-at-least-thirty-two-characters"))
	if err != nil {
		t.Fatalf("build fixture cipher: %v", err)
	}
	appCtx, cancel := context.WithCancel(context.Background())
	provider := pgstore.NewPostgresProvider(pool)
	hub := evt.NewHub("fixture-node", &cache.NoopEventBus{})
	server := httptest.NewUnstartedServer(api.NewRouter(api.Dependencies{
		Config:            cfg,
		AppContext:        appCtx,
		DB:                pool,
		SecretCipher:      cipher,
		UserStoreProvider: provider,
		FileRepo:          scanner.NewFileRepository(pool),
		SessionMgr:        playback.NewSessionManager(6, 2),
		EventsHub:         hub,
		NodeID:            "fixture-node",
	}))
	if host, _, err := net.SplitHostPort(server.Listener.Addr().String()); err != nil || host != "127.0.0.1" {
		cancel()
		server.Close()
		t.Fatalf("fixture listener is not loopback-only: %s", server.Listener.Addr())
	}
	server.Start()
	t.Cleanup(func() {
		server.Close()
		cancel()
		_ = provider.Close()
	})
	return server
}

func setupFixtureAccount(t *testing.T, baseURL string) {
	t.Helper()
	status, responseBody := setupFixtureAccountResponse(t, baseURL)
	if status != http.StatusCreated {
		t.Fatalf("setup fixture account status = %d, want %d: %s", status, http.StatusCreated, responseBody)
	}
}

func setupFixtureAccountResponse(t *testing.T, baseURL string) (int, string) {
	t.Helper()
	body := `{"username":"fixture.viewer","email":"fixture.viewer@example.invalid","password":"invented-password-for-local-conformance","create_default_profile":true,"default_profile_name":"Fixture Viewer"}`
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/setup", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("setup fixture account: %v", err)
	}
	defer resp.Body.Close()
	var responseBody bytes.Buffer
	_, _ = responseBody.ReadFrom(resp.Body)
	return resp.StatusCode, responseBody.String()
}

func contractsRepository(t *testing.T) string {
	t.Helper()
	path := os.Getenv("VONDEL_CLIENT_CONTRACTS_DIR")
	if path == "" {
		path = filepath.Join("..", "..", "..", "vondel-client-contracts")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		t.Skipf("contracts repository unavailable at %s", abs)
	}
	return abs
}

func runContractsCLI(t *testing.T, contractsDir, baseURL string) {
	t.Helper()
	report := runContractsReport(t, contractsDir, baseURL)
	if !report.Passed {
		t.Fatal("contracts full matrix failed")
	}
}

type contractsReport struct {
	Passed           bool
	OfficialBaseline bool
	Results          []struct{ Name, Status string }
}

func runContractsReport(t *testing.T, contractsDir, baseURL string) contractsReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/vondel-client-conformance", "-base-url", baseURL, "-fixtures", contractsDir, "-timeout", "30s")
	cmd.Dir = contractsDir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := stdout.Bytes()
	var report struct {
		OfficialBaselinePassed bool `json:"official_baseline_passed"`
		Results                []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode contracts report: %v\nstdout:\n%s\nstderr:\n%s", err, output, stderr.String())
	}
	if len(report.Results) == 0 {
		t.Fatal("contracts report contained no cases")
	}
	if len(report.Results) != 14 {
		t.Fatalf("contracts report cases = %d, want 14", len(report.Results))
	}
	parsed := contractsReport{Passed: err == nil, OfficialBaseline: report.OfficialBaselinePassed}
	for _, result := range report.Results {
		parsed.Results = append(parsed.Results, struct{ Name, Status string }{result.Name, result.Status})
	}
	return parsed
}

func runOfficialSiloBaseline(t *testing.T, ctx context.Context, adminURL, contractsDir string) {
	t.Helper()
	if err := exec.Command("git", "cat-file", "-e", officialSiloBaselineCommit+"^{commit}").Run(); err != nil {
		t.Fatalf("official Silo baseline commit is unavailable: %v", err)
	}
	worktree := filepath.Join(t.TempDir(), "silo-official")
	if output, err := exec.Command("git", "worktree", "add", "--detach", worktree, officialSiloBaselineCommit).CombinedOutput(); err != nil {
		t.Fatalf("create official Silo worktree: %v\n%s", err, output)
	}
	t.Cleanup(func() { _, _ = exec.Command("git", "worktree", "remove", "--force", worktree).CombinedOutput() })

	db := createDisposableDatabase(t, ctx, adminURL)
	t.Cleanup(func() {
		db.cleanup(t, context.Background())
		db.admin.Close()
	})

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve official baseline port: %v", err)
	}
	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	_ = listener.Close()
	if _, err := strconv.Atoi(portText); err != nil {
		t.Fatalf("invalid reserved port %q", portText)
	}
	binary := filepath.Join(t.TempDir(), "silo-server")
	webDist := filepath.Join(worktree, "web", "dist")
	if err := os.MkdirAll(webDist, 0o755); err != nil {
		t.Fatalf("create official Silo embed directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(webDist, "index.html"), []byte("<!doctype html><title>contract fixture</title>\n"), 0o644); err != nil {
		t.Fatalf("create official Silo embed fixture: %v", err)
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/silo")
	build.Dir = worktree
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build official Silo baseline: %v\n%s", err, output)
	}
	officialEnv := environmentWithOverrides(os.Environ(), map[string]string{
		"DATABASE_URL":  db.dsn,
		"SECRET_KEY":    "fixture-official-silo-master-key-at-least-thirty-two",
		"PORT":          portText,
		"MODE":          "integrated",
		"POSTGRES_TUNE": "off",
		"REDIS_URL":     "",
	})
	migrateOfficialSchema(t, ctx, db.pool, worktree)
	seedInventedMedia(t, ctx, db.pool)
	provisioner := startVondelServer(t, db.pool)
	setupFixtureAccount(t, provisioner.URL)
	seedAccountScopedFixtures(t, ctx, db.pool)
	provisioner.Close()
	for _, pluginID := range []string{"silo.tmdb", "silo.tvdb"} {
		if _, err := db.pool.Exec(ctx, `
INSERT INTO plugin_installations (repository_id, plugin_id, version, install_path, enabled, kind, update_policy)
VALUES (NULL, $1, '999.0.0', '', false, 'plugin', 'manual')
ON CONFLICT DO NOTHING`, pluginID); err != nil {
			t.Fatalf("seed disabled official default plugin %s: %v", pluginID, err)
		}
	}
	var logs bytes.Buffer
	command := exec.Command(binary, "-env", "")
	command.Dir = worktree
	command.Env = officialEnv
	command.Stdout = &logs
	command.Stderr = &logs
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatalf("start official Silo baseline: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		_, _ = command.Process.Wait()
	})
	baseURL := "http://127.0.0.1:" + portText
	waitForHealth(t, baseURL, &logs)
	report := runContractsReport(t, contractsDir, baseURL)
	if !report.OfficialBaseline {
		t.Fatal("pinned official authenticated baseline subset failed")
	}
	if report.Passed {
		t.Fatal("pinned official full matrix unexpectedly passed; update the recorded compatibility verdict")
	}
	t.Logf("pinned official authenticated baseline passed; full matrix incompatibilities: %v", report.Results)
}

func environmentWithOverrides(base []string, overrides map[string]string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, overridden := overrides[key]; !overridden {
			result = append(result, entry)
		}
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func waitForHealth(t *testing.T, baseURL string, logs *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/api/v1/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("official Silo baseline did not become healthy\n%s", logs.String())
}

func databaseNameFromURL(t *testing.T, dsn string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(parsed.Path, "/")
}
