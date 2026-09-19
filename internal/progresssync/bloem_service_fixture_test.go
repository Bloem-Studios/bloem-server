package progresssync

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

// Snapshot reads require active organization and membership authority. Give
// each service fixture its own complete database so establishing ownership
// cannot alter the supplied maintenance database or another test's policy.
func bloemProgressDatabase(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("parse progress fixture database configuration")
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal("open progress fixture maintenance pool")
	}
	t.Cleanup(admin.Close)
	name := "bloem_progress_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create progress fixture database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("drop progress fixture database: %v", err)
		}
	})
	config.ConnConfig.Database = name
	config.ConnConfig.RuntimeParams["search_path"] = "public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("open progress fixture database")
	}
	t.Cleanup(pool.Close)
	var selected string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&selected); err != nil {
		t.Fatalf("verify progress fixture database: %v", err)
	}
	if selected != name {
		t.Fatal("progress fixture selected a different database")
	}
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate progress fixture database: %v", err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize progress fixture membership policy: %v", err)
	}
	owner, err := auth.NewUserRepository(pool).Create(ctx, models.CreateUserInput{
		Username: "bootstrap-owner", Email: "bootstrap-owner@example.test",
		Password: "synthetic-bootstrap-owner-password", Role: models.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("create progress fixture owner: %v", err)
	}
	tenants := tenancy.NewStore(pool)
	if _, err := tenants.ProvisionDefaultMembership(ctx, owner.ID, models.RoleAdmin); err != nil {
		t.Fatalf("provision progress fixture owner membership: %v", err)
	}
	if _, err := tenants.ActivateInitialOwnership(ctx, owner.ID); err != nil {
		t.Fatalf("activate progress fixture organization: %v", err)
	}
	return pool
}

func bloemProgressTenantContext(t *testing.T, pool *pgxpool.Pool, accountID int, profileID string) context.Context {
	t.Helper()
	store := tenancy.NewStore(pool)
	resolver := tenancy.NewSubjectResolver(tenancy.NewResolver(store), store)
	tenant, err := resolver.ResolveSubjectTenant(t.Context(), accountID, profileID)
	if err != nil {
		t.Fatalf("resolve progress fixture tenant: %v", err)
	}
	return tenancy.WithContext(t.Context(), tenant)
}

func bloemProgressLibrary(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		t.Fatal("progress fixture library requires resolved tenancy")
	}
	var folderID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_folders (name, type, owner_id)
		SELECT 'Bootstrap movies', 'movies', id
		FROM resource_owners
		WHERE kind='organization' AND organization_id=$1
		RETURNING id`, tenant.OrganizationID).Scan(&folderID); err != nil {
		t.Fatalf("create progress fixture library: %v", err)
	}
	return folderID
}
