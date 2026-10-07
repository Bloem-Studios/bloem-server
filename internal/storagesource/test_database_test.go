//go:build integration

package storagesource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const preModeBaseline = "ccc18d1b100bbdda3798de2e9a4999e21b2edb7b"
const preModeProviderSHA256 = "08478b81299d8e4b4a023eb2c8f37541a5b8ca14bd5a075947bd4f2632c41088"
const preModeManifestSHA256 = "9d56da02d8f4f7556ddc64c00d7309911e41cc8ca2d00ebb7dbfd4eb6ef4a978"
const preModeWorkDir = "../../.superpowers/sdd/2026-10-06-native-storage-onboarding-implementation-plan-4"
const preModeSourceDir = preModeWorkDir + "/pre-mode-source-" + preModeBaseline

type preModeFixtureInput struct {
	DSN      string `json:"dsn"`
	Commit   string `json:"commit"`
	Manifest string `json:"manifestSHA256"`
	Schema   string `json:"schemaSHA256"`
}

func preModeFixturePath() string {
	if path := os.Getenv("BLOEM_PRE_MODE_FIXTURE"); path != "" {
		return path
	}
	return preModeWorkDir + "/pre-mode-database.json"
}

func readPreModeFixture(path string) (preModeFixtureInput, error) {
	var input preModeFixtureInput
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return input, fmt.Errorf("separate private mode-0600 PRE-MODE fixture required")
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &input) != nil {
		return input, fmt.Errorf("versioned PRE-MODE fixture input required")
	}
	if input.Commit != preModeBaseline || input.Manifest != preModeManifestSHA256 || len(input.Schema) != 64 {
		return input, fmt.Errorf("exact historical PRE-MODE source provenance required")
	}
	cfg, err := pgxpool.ParseConfig(input.DSN)
	if err != nil || !preModeUUIDName(cfg.ConnConfig.Database, "bloem_storage_test_pre_mode_") {
		return input, fmt.Errorf("owned historical PRE-MODE source UUID required")
	}
	return input, nil
}

func preModeUUIDName(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(name, prefix)
	if len(suffix) != 32 {
		return false
	}
	_, err := hex.DecodeString(suffix)
	return err == nil
}

// The complete manifest covers every original file in git archive of the pinned
// commit. Added scratch bootstrap code is outside the migration/provider paths.
func verifyPreModeSource() error {
	provider, err := os.ReadFile(preModeWorkDir + "/pre-mode-fixture-bootstrap")
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(provider)) != preModeProviderSHA256 {
		return fmt.Errorf("pinned historical provider binary mismatch")
	}
	manifest, err := os.ReadFile(preModeWorkDir + "/pre-mode-source-manifest.sha256")
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(manifest)) != preModeManifestSHA256 {
		return fmt.Errorf("historical complete source manifest mismatch")
	}
	expected := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || filepath.IsAbs(parts[1]) || strings.Contains(parts[1], "..") {
			return fmt.Errorf("historical manifest entry invalid")
		}
		data, err := os.ReadFile(filepath.Join(preModeSourceDir, parts[1]))
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != parts[0] {
			return fmt.Errorf("historical source bytes differ from pinned manifest")
		}
		expected[parts[1]] = true
	}
	for _, dir := range []string{"migrations", "internal/database"} {
		err := filepath.WalkDir(filepath.Join(preModeSourceDir, dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(preModeSourceDir, path)
			if err != nil || !expected[filepath.ToSlash(rel)] {
				return fmt.Errorf("unversioned historical provider source")
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("historical provider source inventory mismatch")
		}
	}
	return nil
}

func guardPreModeAcquisitions(cfg *pgxpool.Config, expected string) {
	cfg.BeforeAcquire = func(ctx context.Context, conn *pgx.Conn) bool {
		var actual string
		return conn.QueryRow(ctx, "SELECT current_database()").Scan(&actual) == nil && actual == expected
	}
}

func requirePreModeSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var nativeMode *string
	if err := pool.QueryRow(context.Background(), "SELECT to_regclass('public.bloem_native_libraries')::text").Scan(&nativeMode); err != nil || nativeMode != nil {
		t.Fatal("PRE-MODE fixture cannot reset a CURRENT native-mode schema")
	}
}

// preModeDatabase consumes only the separately supplied, attested historical
// input. Every disposable caller starts at template0 and is prepared by a binary
// built from the complete pinned archive, including all three Go migrations.
// This is PRE-MODE primitive evidence; it supplies no CURRENT lifecycle authority.
func preModeDatabase(t *testing.T, migrate bool) *pgxpool.Pool {
	t.Helper()
	input, err := readPreModeFixture(preModeFixturePath())
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPreModeSource(); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(input.DSN)
	if err != nil {
		t.Fatal("invalid private PRE-MODE configuration")
	}
	sourceName := config.ConnConfig.Database
	guardPreModeAcquisitions(config, sourceName)
	source, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal("open historical PRE-MODE source failed")
	}
	t.Cleanup(source.Close)
	// Read only: verify the actual source connection and bootstrap attestation.
	var actual, comment string
	if err := source.QueryRow(context.Background(), "SELECT current_database(), coalesce(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=current_database()").Scan(&actual, &comment); err != nil || actual != sourceName || comment != "PRE-MODE "+preModeBaseline+" "+preModeManifestSHA256+" "+input.Schema {
		source.Close()
		t.Fatal("actual historical source identity/provenance unverified")
	}
	requirePreModeSchema(t, source)
	source.Close()
	adminConfig := config.Copy()
	adminConfig.ConnConfig.Database = "postgres"
	guardPreModeAcquisitions(adminConfig, "postgres")
	admin, err := pgxpool.NewWithConfig(context.Background(), adminConfig)
	if err != nil {
		t.Fatal("open PRE-MODE maintenance pool failed")
	}
	var maintenance string
	if err := admin.QueryRow(context.Background(), "SELECT current_database()").Scan(&maintenance); err != nil || maintenance != "postgres" {
		admin.Close()
		t.Fatal("actual PRE-MODE maintenance identity unverified")
	}
	name := "bloem_storage_test_pm_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	var pool *pgxpool.Pool
	created := false
	// Registered before CREATE or opening the caller. No FORCE/session termination.
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		defer admin.Close()
		if !created {
			return
		}
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Error("PRE-MODE owned UUID cleanup failed")
			return
		}
		var exists bool
		if err := admin.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists); err != nil || exists {
			t.Error("PRE-MODE owned UUID cleanup unverified")
		} else {
			t.Logf("PRE-MODE owned UUID %s cleanup verified", name)
		}
	})
	if _, err := admin.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatal("create PRE-MODE owned UUID from template0 failed")
	}
	created = true
	// Reconstruct the URI explicitly before supplying a DSN to another process.
	uri, err := url.Parse(input.DSN)
	if err != nil || (uri.Scheme != "postgres" && uri.Scheme != "postgresql") || uri.Query().Has("dbname") || uri.Query().Has("database") {
		t.Fatal("unambiguous historical PostgreSQL URI required")
	}
	uri.Path, uri.RawPath = "/"+name, ""
	cloneInput := input
	cloneInput.DSN = uri.String()
	cfg, err := pgxpool.ParseConfig(cloneInput.DSN)
	if err != nil || cfg.ConnConfig.Database != name || name == sourceName {
		t.Fatal("PRE-MODE rewritten caller UUID unverified")
	}
	cfg.MaxConns = 4
	guardPreModeAcquisitions(cfg, name)
	pool, err = pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal("open PRE-MODE owned caller failed")
	}
	if err := pool.QueryRow(context.Background(), "SELECT current_database()").Scan(&actual); err != nil || actual != name || actual == sourceName {
		t.Fatal("actual PRE-MODE caller identity unverified")
	}
	data, _ := json.Marshal(cloneInput)
	binary, err := filepath.Abs(preModeWorkDir + "/pre-mode-fixture-bootstrap")
	if err != nil {
		t.Fatal("historical provider binary path failed")
	}
	work, _ := filepath.Abs(preModeWorkDir)
	repo, _ := filepath.Abs("../..")
	cmd := exec.Command(binary, "prepare", work, repo)
	cmd.Dir = preModeSourceDir
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PRE-MODE historical provider setup failed (separate from assertions): %s", output)
	}
	pool.Reset() // Reopen with the historical fixture database settings.
	t.Log(strings.TrimSpace(string(output)))
	t.Logf("PRE-MODE actual caller=%s source=%s baseline=%s completeManifest=%s", actual, sourceName, preModeBaseline, preModeManifestSHA256)
	resetPreModeStorage(t, pool, migrate)
	return pool
}

func resetPreModeStorage(t *testing.T, pool *pgxpool.Pool, migrate bool) {
	t.Helper()
	requirePreModeSchema(t, pool)
	var installed *string
	if err := pool.QueryRow(context.Background(), "SELECT to_regclass('bloem_storage_sources')::text").Scan(&installed); err != nil {
		t.Fatal("PRE-MODE installed schema query failed")
	}
	if installed != nil {
		var ingestion *string
		if err := pool.QueryRow(context.Background(), "SELECT to_regclass('bloem_storage_ingestion')::text").Scan(&ingestion); err != nil {
			t.Fatal(err)
		}
		if ingestion != nil {
			execSQL(t, pool, ingestionMigrationSQL(t, false))
		}
		var registry *string
		if err := pool.QueryRow(context.Background(), "SELECT to_regclass('bloem_storage_installations')::text").Scan(&registry); err != nil {
			t.Fatal(err)
		}
		if registry != nil {
			ownershipMigration(t, pool, false)
		}
		migration(t, pool, false)
	}
	if migrate {
		migration(t, pool, true)
	}
}

func migrationSQL(t *testing.T, up bool) string {
	t.Helper()
	paths, err := filepath.Glob(preModeSourceDir + "/migrations/sql/*_bloem_native_storage_sources.sql")
	if err != nil || len(paths) != 1 {
		t.Fatal("storage migration absent or ambiguous")
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("missing reversible migration")
	}
	if up {
		return parts[0]
	}
	return parts[1]
}

func migration(t *testing.T, pool *pgxpool.Pool, up bool) {
	t.Helper()
	if !up {
		var ingestion *string
		if err := pool.QueryRow(context.Background(), "SELECT to_regclass('bloem_storage_ingestion')::text").Scan(&ingestion); err != nil {
			t.Fatal(err)
		}
		if ingestion != nil {
			execSQL(t, pool, ingestionMigrationSQL(t, false))
		}
		var registry *string
		if err := pool.QueryRow(context.Background(), `SELECT to_regclass('bloem_storage_installations')::text`).Scan(&registry); err != nil {
			t.Fatal(err)
		}
		if registry != nil {
			ownershipMigration(t, pool, false)
		}
	}
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(context.Background(), migrationSQL(t, up)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if up {
		ownershipMigration(t, pool, true)
		execSQL(t, pool, ingestionMigrationSQL(t, true))
		execSQL(t, pool, sidecarMigrationSQL(t, true))
	}
}

func execSQL(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func fixtureFolder(t *testing.T, pool *pgxpool.Pool, id int) {
	t.Helper()
	execSQL(t, pool, `INSERT INTO media_folders(id,type,name) VALUES($1,'ebooks','Storage test')`, id)
}

func preservationSnapshot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var snapshot string
	err := pool.QueryRow(context.Background(), `SELECT jsonb_build_array(
 (SELECT to_jsonb(u) FROM users u WHERE id=91001),
 (SELECT to_jsonb(s) FROM server_settings s WHERE key='storage-preservation-sentinel'),
 (SELECT to_jsonb(p) FROM user_watch_progress p WHERE user_id=91001),
 (SELECT to_jsonb(p) FROM ebook_reader_progress p WHERE user_id=91001),
 (SELECT to_jsonb(f) FROM media_files f WHERE id=91001)
 )::text`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func fixtureSource(t *testing.T, pool *pgxpool.Pool) (SourceConfig, *Repository) {
	t.Helper()
	r := NewRepository(pool)
	s, err := r.CreateSource(context.Background(), SourceConfig{PluginID: "fixture", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, r
}

func ownershipMigrationSQL(t *testing.T, up bool) string {
	t.Helper()
	paths, err := filepath.Glob(preModeSourceDir + "/migrations/sql/*_bloem_native_storage_registry.sql")
	if err != nil || len(paths) != 1 {
		t.Fatal("registry migration absent or ambiguous")
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("registry Down absent")
	}
	if up {
		return parts[0]
	}
	return parts[1]
}
func ownershipMigration(t *testing.T, pool *pgxpool.Pool, up bool) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(context.Background(), ownershipMigrationSQL(t, up)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func sidecarMigrationSQL(t *testing.T, up bool) string {
	t.Helper()
	paths, err := filepath.Glob(preModeSourceDir + "/migrations/sql/*_bloem_native_storage_sidecar_lookup.sql")
	if err != nil || len(paths) != 1 {
		t.Fatal("sidecar lookup migration absent or ambiguous")
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("sidecar lookup Down absent")
	}
	if up {
		return parts[0]
	}
	return parts[1]
}
