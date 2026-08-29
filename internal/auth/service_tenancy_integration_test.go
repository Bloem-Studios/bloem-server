package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type tenancyProvisioningAdapter struct {
	store *tenancy.Store
}

type failingMembershipProvisioner struct {
	err error
}

type staticAuthSettings map[string]string

func (s staticAuthSettings) Get(_ context.Context, key string) (string, error) { return s[key], nil }

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

func (a tenancyProvisioningAdapter) ProvisionDefaultMembershipInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	accountID int,
	legacyRole string,
) (uuid.UUID, uuid.UUID, error) {
	membership, err := a.store.ProvisionDefaultMembershipInTransaction(ctx, tx, accountID, legacyRole)
	return membership.OrganizationID, membership.ID, err
}

func (a tenancyProvisioningAdapter) ActivateInitialOwnershipInTransaction(ctx context.Context, tx pgx.Tx, accountID int) error {
	_, err := a.store.ActivateInitialOwnershipInTransaction(ctx, tx, accountID)
	return err
}

func TestAccountProvisionerCreateAccountInTransactionRollsBackEveryGeneratedTarget(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newAuthTenancyDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	provisioner := NewAccountProvisioner(NewUserRepository(pool), pgstore.NewPostgresProvider(pool))
	provisioner.SetMembershipProvisioner(tenancyProvisioningAdapter{store: tenancy.NewStore(pool)})
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	created, err := provisioner.CreateAccountInTransaction(ctx, tx, CreateAccountInput{
		User: models.CreateUserInput{
			Username: "transactional-account",
			Email:    "transactional-account@example.test",
			Password: "password",
			Role:     models.RoleAdmin,
		},
		DefaultProfile: DefaultProfileOptions{Enabled: true, Name: "Transactional"},
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("CreateAccountInTransaction: %v", err)
	}
	if created.User == nil || created.User.AccountIncarnationID == uuid.Nil ||
		created.OrganizationID == uuid.Nil || created.MembershipID == uuid.Nil || created.ProfileID == "" {
		_ = tx.Rollback(ctx)
		t.Fatalf("generated targets incomplete: %+v", created)
	}
	sessionID := uuid.NewString()
	if err := NewSessionRepository(pool).CreateInTransaction(ctx, tx, models.AuthSession{
		ID: sessionID, UserID: created.User.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("CreateInTransaction session: %v", err)
	}
	if err := (tenancyProvisioningAdapter{store: tenancy.NewStore(pool)}).
		ActivateInitialOwnershipInTransaction(ctx, tx, created.User.ID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("ActivateInitialOwnershipInTransaction: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	for name, query := range map[string]string{
		"account":    `SELECT count(*) FROM users WHERE username='transactional-account'`,
		"membership": `SELECT count(*) FROM organization_memberships WHERE id='` + created.MembershipID.String() + `'`,
		"profile":    `SELECT count(*) FROM user_profiles WHERE id='` + created.ProfileID + `'`,
		"session":    `SELECT count(*) FROM auth_sessions WHERE id='` + sessionID + `'`,
	} {
		var count int
		if err := pool.QueryRow(ctx, query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("%s survived rollback", name)
		}
	}
	var owner *int
	if err := pool.QueryRow(ctx, `SELECT owner_account_id FROM platform_security WHERE singleton`).Scan(&owner); err != nil {
		t.Fatalf("read platform owner: %v", err)
	}
	if owner != nil {
		t.Fatalf("platform ownership survived rollback: %d", *owner)
	}
}

func TestSetupInitialUserInTransactionReturnsReplayTargetsAndCommitsAtomically(t *testing.T) {
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
		pgstore.NewPostgresProvider(pool),
	)
	adapter := tenancyProvisioningAdapter{store: tenancy.NewStore(pool)}
	service.SetMembershipProvisioner(adapter)
	service.SetOwnershipBootstrapper(adapter)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	pair, created, err := service.SetupInitialUserInTransaction(
		ctx, tx, "setup-tx", "setup-tx@example.test", "password", true, "Owner", "browser", "127.0.0.1",
	)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("SetupInitialUserInTransaction: %v", err)
	}
	if pair == nil || pair.AccessToken == "" || pair.RefreshToken == "" || created.User == nil ||
		created.OrganizationID == uuid.Nil || created.MembershipID == uuid.Nil || created.ProfileID == "" {
		_ = tx.Rollback(ctx)
		t.Fatalf("incomplete setup result: pair=%+v created=%+v", pair, created)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var usersAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&usersAfter); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if usersAfter != 0 {
		t.Fatalf("setup escaped caller transaction: %d users", usersAfter)
	}
}

func TestSignupInTransactionRollsBackInviteAccountAndSessionTogether(t *testing.T) {
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
	owner, err := users.Create(ctx, models.CreateUserInput{
		Username: "invite-owner", Email: "invite-owner@example.test", Password: "password", Role: models.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("create invite owner: %v", err)
	}
	invites := NewInviteCodeRepository(pool)
	if _, err := invites.Create(ctx, models.CreateInviteCodeInput{Code: "TXSIGNUP", MaxUses: 1, CreatedBy: owner.ID}); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	sessions := NewSessionRepository(pool)
	service := NewService(
		NewLocalProvider(users, sessions), NewJWTService("test-secret", time.Hour, 24*time.Hour),
		sessions, users, invites, staticAuthSettings{"signup.enabled": "true"}, pgstore.NewPostgresProvider(pool),
	)
	service.SetMembershipProvisioner(tenancyProvisioningAdapter{store: tenancy.NewStore(pool)})

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	pair, created, err := service.SignupInTransaction(
		ctx, tx, "signed-up", "signed-up@example.test", "password", "TXSIGNUP", true, "Viewer", "phone", "127.0.0.1",
	)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("SignupInTransaction: %v", err)
	}
	if pair == nil || pair.RefreshToken == "" || created.User == nil || created.MembershipID == uuid.Nil || created.ProfileID == "" {
		_ = tx.Rollback(ctx)
		t.Fatalf("incomplete signup result: pair=%+v created=%+v", pair, created)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var useCount, accountCount, sessionCount int
	if err := pool.QueryRow(ctx, `SELECT use_count FROM invite_codes WHERE code='TXSIGNUP'`).Scan(&useCount); err != nil {
		t.Fatalf("read invite: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE username='signed-up'`).Scan(&accountCount); err != nil {
		t.Fatalf("count account: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM auth_sessions WHERE user_id <> $1`, owner.ID).Scan(&sessionCount); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if useCount != 0 || accountCount != 0 || sessionCount != 0 {
		t.Fatalf("signup escaped rollback: use_count=%d accounts=%d sessions=%d", useCount, accountCount, sessionCount)
	}
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
	name := "bloem_auth_tenancy_" + hex.EncodeToString(random[:])

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
