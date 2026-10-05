package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/promotions"
	"github.com/Silo-Server/silo-server/migrations"
)

// Exercise the actual application service wiring with the existing host DB:
// an available worker delivers rows, while a missing worker omits presentation
// and leaves the same services' transactional CRUD available.
func TestBloemPresentationApplicationWiringUsesWorkers(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool := presentationDisposableDatabase(t, dsn)
	if err := database.RunMigrations(t.Context(), pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var userID int
	if err := pool.QueryRow(t.Context(), "INSERT INTO users (username,email,password_hash,role,enabled) VALUES ('plugin-viewer','plugin@example.test','x','user',true) RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	t.Setenv("BLOEM_PRESENTATION_PLUGIN_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	unavailable := api.Dependencies{}
	if liveTV := bloemWireServices(ctx, &unavailable, pool, nil, "", nil); liveTV != nil {
		t.Fatal("unexpected Live TV wiring without deps DB")
	}
	campaign, err := unavailable.Promotions.Create(t.Context(), userID, promotions.Input{
		Surfaces: []string{"home"}, Kicker: "New", Headline: "Plugin campaign",
		ImageURL: "https://cdn.example.test/campaign.jpg", Deeplink: "bloem://collection/campaign",
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	pack, err := unavailable.Ambience.Create(t.Context(), userID, ambience.Input{EffectID: "snow", Window: ambience.Window{StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	viewer := promotions.Viewer{UserID: userID, ProfileID: "profile-1"}
	cards, _, err := unavailable.Promotions.ActiveHome(t.Context(), viewer)
	if err != nil || len(cards) != 0 {
		t.Fatalf("missing promotion worker should omit: %+v, %v", cards, err)
	}
	packs, err := unavailable.Ambience.ActivePublic(t.Context())
	if err != nil || len(packs) != 0 {
		t.Fatalf("missing ambience worker should omit: %+v, %v", packs, err)
	}
	if rows, err := unavailable.Promotions.List(t.Context()); err != nil || len(rows) != 1 {
		t.Fatalf("missing worker broke promotion CRUD: %v", err)
	}
	if rows, err := unavailable.Ambience.List(t.Context()); err != nil || len(rows) != 1 {
		t.Fatalf("missing worker broke ambience CRUD: %v", err)
	}
	directory := buildBundledPresentationWorkers(t)
	t.Setenv("BLOEM_PRESENTATION_PLUGIN_DIR", directory)
	available := api.Dependencies{}
	bloemWireServices(ctx, &available, pool, nil, "", nil)
	cards, _, err = available.Promotions.ActiveHome(t.Context(), viewer)
	if err != nil || len(cards) != 1 || cards[0].ID != campaign.ID {
		t.Fatalf("application did not use promotion worker: %+v, %v", cards, err)
	}
	packs, err = available.Ambience.ActivePublic(t.Context())
	if err != nil || len(packs) != 1 || packs[0].ID != pack.ID {
		t.Fatalf("application did not use ambience worker: %+v, %v", packs, err)
	}
}

func presentationDisposableDatabase(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "bloem_plugin_host_" + hex.EncodeToString(random[:])
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("parse protected fixture DSN")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("connect maintenance database")
	}
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database: %v", err)
	}
	child := cfg.Copy()
	child.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(t.Context(), child)
	if err != nil {
		_, _ = admin.Exec(t.Context(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		t.Fatal("connect disposable database")
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("drop disposable database: %v", err)
		}
		admin.Close()
	})
	return pool
}
