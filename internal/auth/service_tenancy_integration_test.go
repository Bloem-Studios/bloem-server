package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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

type failingMembershipProvisioner struct {
	err error
}

func (p failingMembershipProvisioner) ProvisionDefaultMembership(context.Context, int, string) error {
	return p.err
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
			Username: "moderator-user",
			Email:    "moderator-user@example.test",
			Password: "password",
			Role:     "moderator",
		},
	})
	if err != nil {
		t.Fatalf("CreateAccount moderator user: %v", err)
	}
	if ordinary.Role != "moderator" {
		t.Fatalf("created user role = %q, want moderator", ordinary.Role)
	}
	membership, err = adapter.store.GetMembership(ctx, ordinary.ID, organization.ID)
	if err != nil {
		t.Fatalf("GetMembership for ordinary user: %v", err)
	}
	if membership.Status != tenancy.MembershipActive || membership.LegacyRole != "user" {
		t.Fatalf("moderator user membership = %#v, want active user", membership)
	}
}

func TestSetupInitialUserOwnership_CleansUpAccountWhenMembershipProvisioningFails(t *testing.T) {
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
	provisionErr := errors.New("default membership unavailable")
	service.SetMembershipProvisioner(failingMembershipProvisioner{err: provisionErr})

	pair, user, err := service.SetupInitialUser(
		ctx, "setup-admin", "setup-admin@example.test", "password", false, "", "browser", "127.0.0.1",
	)
	if !errors.Is(err, provisionErr) {
		t.Fatalf("SetupInitialUser error = %v, want membership provisioning error", err)
	}
	if pair != nil || user != nil {
		t.Fatalf("setup result = (%#v, %#v), want no tokens or user", pair, user)
	}
	needsSetup, err := service.NeedsSetup(ctx)
	if err != nil {
		t.Fatalf("NeedsSetup: %v", err)
	}
	if !needsSetup {
		t.Fatal("failed setup left an account behind")
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
