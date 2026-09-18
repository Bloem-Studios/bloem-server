package invitations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRepositoryOrganizationInvitationsNeverCrossTenantBoundary(t *testing.T) {
	ctx := context.Background()
	pool := newInvitationOrganizationDatabase(t, ctx)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var defaultOrganizationID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&defaultOrganizationID); err != nil {
		t.Fatal(err)
	}
	foreignOrganizationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,slug,name,status,is_default) VALUES ($1,$2,'Foreign','initializing',false)`, foreignOrganizationID, "invitation-"+foreignOrganizationID.String()); err != nil {
		t.Fatal(err)
	}
	var inviterID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (username,email,password_hash,role,enabled) VALUES ($1,$2,'x','admin',true) RETURNING id`, "inviter-"+foreignOrganizationID.String(), foreignOrganizationID.String()+"@example.test").Scan(&inviterID); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(pool)
	input := models.CreateInvitationInput{Email: "same@example.test", Role: "user", InvitedBy: inviterID, ExpiresAt: time.Now().Add(time.Hour)}
	local, err := repo.CreateForOrganization(ctx, defaultOrganizationID, input, "local-hash")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := repo.CreateForOrganization(ctx, foreignOrganizationID, input, "foreign-hash")
	if err != nil {
		t.Fatal(err)
	}
	if local.OrganizationID != defaultOrganizationID || foreign.OrganizationID != foreignOrganizationID {
		t.Fatalf("organization identities = %s/%s", local.OrganizationID, foreign.OrganizationID)
	}
	items, err := repo.ListForOrganization(ctx, defaultOrganizationID)
	if err != nil || len(items) != 1 || items[0].ID != local.ID || items[0].ID == foreign.ID {
		t.Fatalf("local list = %+v, %v", items, err)
	}
	legacy, err := repo.List(ctx)
	if err != nil || len(legacy) != 1 || legacy[0].ID != local.ID {
		t.Fatalf("legacy list = %+v, %v", legacy, err)
	}
}

func newInvitationOrganizationDatabase(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set; skipping PostgreSQL invitation test")
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "bloem_invitations_" + hex.EncodeToString(random[:])
	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	// Register cleanup immediately: a pool or migration failure must not leak
	// the database. This name was created above and never names the source DB.
	t.Cleanup(func() {
		defer admin.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("drop invitation fixture database %s: %v", name, err)
		}
	})
	testConfig := adminConfig.Copy()
	testConfig.ConnConfig.Database = name
	testConfig.ConnConfig.RuntimeParams["search_path"] = "public"
	testConfig.ConnConfig.RuntimeParams["application_name"] = name
	testConfig.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	testConfig.MaxConns = 12
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
