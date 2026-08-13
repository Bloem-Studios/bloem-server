package database

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

const liveTVPreviousMigration int64 = 20260813180000

func TestLiveTVPrairieMigrationsUpDownUp(t *testing.T) {
	db := newDisposableMigrationDatabase(t)
	ctx := context.Background()

	if err := RunMigrations(ctx, db, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate Live TV up: %v", err)
	}
	assertLiveTVSchema(t, ctx, db)

	if err := MigrateDownTo(ctx, db, migrations.FS, "sql", liveTVPreviousMigration); err != nil {
		t.Fatalf("migrate Live TV down: %v", err)
	}
	var exists bool
	if err := db.QueryRow(ctx, `SELECT to_regclass('public.livetv_tuners') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("livetv_tuners remains after rollback")
	}

	if err := RunMigrations(ctx, db, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate Live TV up again: %v", err)
	}
	assertLiveTVSchema(t, ctx, db)
}

func assertLiveTVSchema(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
	t.Helper()
	for _, table := range []string{
		"livetv_tuners", "livetv_channels", "livetv_guide_sources", "livetv_programs",
		"livetv_sessions", "livetv_recordings", "livetv_series_rules", "livetv_artwork_cache",
	} {
		var exists bool
		if err := db.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("inspect %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("expected table %s", table)
		}
	}
}
