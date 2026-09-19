package adminjob

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

type bloemLifecycleFixture struct {
	*Repository
	actorID int
}

// Claims, stale-job recovery and active-job uniqueness span the whole queue.
// A dedicated database keeps these tests from consuming another fixture's jobs
// and retains the migrated foreign keys instead of relying on account ID 1.
func newBloemLifecycleFixture(t *testing.T) *bloemLifecycleFixture {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("SILO_TEST_DATABASE_URL is required for admin-job lifecycle tests")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		bloemLifecycleFixtureError(t, "parse maintenance database configuration", err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		bloemLifecycleFixtureError(t, "open maintenance database", err)
	}
	t.Cleanup(admin.Close)
	name := "bloem_adminjob_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		bloemLifecycleFixtureError(t, "create admin-job fixture database", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			// Connection errors can contain the source DSN; report only the type.
			t.Errorf("drop admin-job fixture database: %T", err)
		}
	})
	fixtureConfig := cfg.Copy()
	fixtureConfig.ConnConfig.Database = name
	fixtureConfig.ConnConfig.RuntimeParams["search_path"] = "public"
	fixtureConfig.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	fixtureConfig.MaxConns = 6
	fixtureConfig.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, fixtureConfig)
	if err != nil {
		bloemLifecycleFixtureError(t, "open admin-job fixture database", err)
	}
	t.Cleanup(pool.Close)
	var connectedDatabase string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&connectedDatabase); err != nil {
		bloemLifecycleFixtureError(t, "verify admin-job fixture database", err)
	}
	if connectedDatabase != name {
		t.Fatal("admin-job fixture connected to a different database")
	}
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		bloemLifecycleFixtureError(t, "migrate admin-job fixture database", err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		bloemLifecycleFixtureError(t, "finalize admin-job fixture membership policy", err)
	}
	actor, err := auth.NewUserRepository(pool).Create(ctx, models.CreateUserInput{
		Username: "admin-job-owner",
		Email:    "admin-job-owner@example.test",
		Password: uuid.NewString(),
		Role:     models.RoleAdmin,
	})
	if err != nil {
		bloemLifecycleFixtureError(t, "create admin-job fixture account", err)
	}
	tenants := tenancy.NewStore(pool)
	if _, err := tenants.ProvisionDefaultMembership(ctx, actor.ID, models.RoleAdmin); err != nil {
		bloemLifecycleFixtureError(t, "provision admin-job fixture membership", err)
	}
	if _, err := tenants.ActivateInitialOwnership(ctx, actor.ID); err != nil {
		bloemLifecycleFixtureError(t, "activate admin-job fixture ownership", err)
	}
	return &bloemLifecycleFixture{Repository: NewRepository(pool), actorID: actor.ID}
}

func bloemLifecycleFixtureError(t *testing.T, operation string, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		t.Fatalf("%s: %T (SQLSTATE %s)", operation, err, pgErr.Code)
	}
	t.Fatalf("%s: %T", operation, err)
}
