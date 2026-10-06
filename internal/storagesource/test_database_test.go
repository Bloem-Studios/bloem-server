//go:build integration

package storagesource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testDatabase(t *testing.T, migrate bool) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("SILO_TEST_DATABASE_URL must identify a disposable storage test database")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid disposable database configuration")
	}
	template := config.ConnConfig.Database
	if !strings.HasPrefix(template, "bloem_storage_test_") {
		t.Fatal("refusing non-test database")
	}
	adminConfig := config.Copy()
	adminConfig.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(context.Background(), adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	name := "bloem_storage_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template}.Sanitize())
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.Database = name
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	// The template may have passed the real host migration check already. Reset
	// only the empty owned tables in this isolated clone before fixture setup.
	var installed *string
	if err = pool.QueryRow(context.Background(), `SELECT to_regclass('bloem_storage_sources')::text`).Scan(&installed); err != nil {
		t.Fatal(err)
	}
	if installed != nil {
		migration(t, pool, false)
	}
	if migrate {
		migration(t, pool, true)
	}
	return pool
}

func migrationSQL(t *testing.T, up bool) string {
	t.Helper()
	paths, err := filepath.Glob("../../migrations/sql/*_bloem_native_storage_sources.sql")
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
