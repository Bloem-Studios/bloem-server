package migrations

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// Upstream's account-policy regression runs in the mirrored phase, where
// Silo's original columns remain writable. Finalized membership pruning has
// separate coverage in internal/database.
func bloemPruningFixtureDSN(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("SILO_TEST_DATABASE_URL")
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/postgres"
	admin, err := pgx.Connect(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	name := "bloem_pruning_test_" + uuid.NewString()[:8]
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = admin.Close(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		_ = admin.Close(context.Background())
	})
	u.Path = "/" + name
	config, err := pgx.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*config)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, mustSub(t, "sql"))
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_, err = provider.Up(t.Context())
	_ = provider.Close()
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tenancy.EnableSiloSwitching(t.Context(), pool)
	if err == nil {
		// These libraries model Silo account allowlists, without tenant
		// entitlements whose deliberate RESTRICT rule would retain a library.
		_, err = pool.Exec(t.Context(), `ALTER TABLE media_folders DISABLE TRIGGER media_folders_entitle_default_organization`)
	}
	pool.Close()
	if err != nil {
		t.Fatal(err)
	}
	return u.String()
}
