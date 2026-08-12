package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/Vondel-Media/vondel-server/internal/database"
	"github.com/Vondel-Media/vondel-server/internal/models"
	"github.com/Vondel-Media/vondel-server/internal/tenancy"
	"github.com/Vondel-Media/vondel-server/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type tenancyProvisioningAdapter struct {
	store *tenancy.Store
}

func (a tenancyProvisioningAdapter) ActivateInitialOwnership(ctx context.Context, accountID int) error {
	_, err := a.store.ActivateInitialOwnership(ctx, accountID)
	return err
}

func (a tenancyProvisioningAdapter) ProvisionDefaultMembership(ctx context.Context, accountID int, legacyRole string) error {
	_, err := a.store.ProvisionDefaultMembership(ctx, accountID, legacyRole)
	return err
}

func TestSetupInitialUserOwnership_ProvisionsDefaultMembershipAndActivatesOwnership(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newAuthTenancyDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	users := NewUserRepository(pool)
	sessions := NewSessionRepository(pool)
	service := NewService(
		NewLocalProvider(users, sessions),
		NewJWTService("test-secret", time.Hour, 24*time.Hour),
		sessions,
		users,
		NewInviteCodeRepository(pool),
		nil,
		nil,
	)
	adapter := tenancyProvisioningAdapter{store: tenancy.NewStore(pool)}
	service.SetMembershipProvisioner(adapter)
	service.SetOwnershipBootstrapper(adapter)

	pair, admin, err := service.SetupInitialUser(
		ctx, "setup-admin", "setup-admin@example.test", "password", false, "", "browser", "127.0.0.1",
	)
	if err != nil {
		t.Fatalf("SetupInitialUser: %v", err)
	}
	if pair == nil || pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("token pair = %#v, want issued tokens", pair)
	}
	organization, err := adapter.store.DefaultOrganization(ctx)
	if err != nil {
		t.Fatalf("DefaultOrganization: %v", err)
	}
	if organization.OwnerAccountID == nil || *organization.OwnerAccountID != admin.ID || organization.Status != tenancy.OrganizationActive {
		t.Fatalf("organization after setup = %#v, want active owner %d", organization, admin.ID)
	}
	membership, err := adapter.store.GetMembership(ctx, admin.ID, organization.ID)
	if err != nil {
		t.Fatalf("GetMembership for setup admin: %v", err)
	}
	if membership.Status != tenancy.MembershipActive || membership.LegacyRole != "admin" {
		t.Fatalf("setup admin membership = %#v, want active admin", membership)
	}

	provisioner := NewAccountProvisioner(users, nil)
	provisioner.SetMembershipProvisioner(adapter)
	ordinary, err := provisioner.CreateAccount(ctx, CreateAccountInput{
		User: models.CreateUserInput{
			Username: "ordinary-user",
			Email:    "ordinary-user@example.test",
			Password: "password",
			Role:     "user",
		},
	})
	if err != nil {
		t.Fatalf("CreateAccount ordinary user: %v", err)
	}
	membership, err = adapter.store.GetMembership(ctx, ordinary.ID, organization.ID)
	if err != nil {
		t.Fatalf("GetMembership for ordinary user: %v", err)
	}
	if membership.Status != tenancy.MembershipActive || membership.LegacyRole != "user" {
		t.Fatalf("ordinary user membership = %#v, want active user", membership)
	}
}

func newAuthTenancyDisposableDatabase(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate database name: %v", err)
	}
	name := "vondel_auth_tenancy_" + hex.EncodeToString(random[:])

	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database %q: %v", name, err)
	}

	testConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("parse disposable database URL: %v", err)
	}
	testConfig.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		t.Fatalf("connect disposable database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		if err := admin.Ping(cleanupCtx); err == nil {
			if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
				t.Errorf("drop disposable database %q: %v", name, err)
			}
		}
		admin.Close()
	})
	return pool
}
