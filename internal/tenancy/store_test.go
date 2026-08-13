package tenancy_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/database"
	. "github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStoreMembershipReadsAndDatabaseConstraints(t *testing.T) {
	store, fixture := newTenancyFixture(t)

	defaultOrganization, err := store.DefaultOrganization(fixture.ctx)
	if err != nil {
		t.Fatalf("DefaultOrganization: %v", err)
	}
	if !defaultOrganization.Default || defaultOrganization.Status != OrganizationInitializing {
		t.Fatalf("default organization = %#v, want initializing default", defaultOrganization)
	}

	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'user')`, defaultOrganization.ID, fixture.adminID); err == nil {
		t.Fatal("duplicate membership insert succeeded")
	} else if !isUniqueViolation(err) {
		t.Fatalf("duplicate membership error = %v, want unique violation", err)
	}

	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO organizations (slug, name, status, is_default)
		VALUES ($1, 'Second Default', 'initializing', true)`, fixture.suffix+"-second-default"); err == nil {
		t.Fatal("second default organization insert succeeded")
	} else if !isUniqueViolation(err) {
		t.Fatalf("second default organization error = %v, want unique violation", err)
	}

	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE organization_memberships
		SET status = 'suspended'
		WHERE organization_id = $1 AND account_id = $2`, defaultOrganization.ID, fixture.otherID); err != nil {
		t.Fatalf("suspend membership: %v", err)
	}

	memberships, err := store.ListMemberships(fixture.ctx, fixture.otherID)
	if err != nil {
		t.Fatalf("ListMemberships: %v", err)
	}
	if len(memberships) != 1 || memberships[0].Status != MembershipSuspended {
		t.Fatalf("memberships = %#v, want one suspended membership", memberships)
	}
	membership, err := store.GetMembership(fixture.ctx, fixture.otherID, defaultOrganization.ID)
	if err != nil {
		t.Fatalf("GetMembership: %v", err)
	}
	if membership.Status != MembershipSuspended {
		t.Fatalf("membership status = %q, want %q", membership.Status, MembershipSuspended)
	}
}

func TestStoreActivateInitialOwnership(t *testing.T) {
	store, fixture := newTenancyFixture(t)

	got, err := store.ActivateInitialOwnership(fixture.ctx, fixture.adminID)
	if err != nil {
		t.Fatalf("ActivateInitialOwnership: %v", err)
	}
	if got.PlatformOwnerAccountID != fixture.adminID || got.Organization.OwnerAccountID == nil || *got.Organization.OwnerAccountID != fixture.adminID {
		t.Fatalf("owners = %#v, want account %d", got, fixture.adminID)
	}
	if got.Organization.Status != OrganizationActive || got.Organization.PolicyRevision != 2 {
		t.Fatalf("organization after activation = %#v, want active revision 2", got.Organization)
	}
	assertOwnershipRevisions(t, fixture, fixture.adminID, 2, 2, 2, false)

	again, err := store.ActivateInitialOwnership(fixture.ctx, fixture.adminID)
	if err != nil {
		t.Fatalf("repeat ActivateInitialOwnership: %v", err)
	}
	if again.PlatformOwnerAccountID != fixture.adminID || again.Organization.OwnerAccountID == nil || *again.Organization.OwnerAccountID != fixture.adminID || again.Organization.Status != OrganizationActive || again.Organization.PolicyRevision != 2 {
		t.Fatalf("repeat activation = %#v, want unchanged active ownership", again)
	}
	assertOwnershipRevisions(t, fixture, fixture.adminID, 2, 2, 2, false)

	_, err = store.ActivateInitialOwnership(fixture.ctx, fixture.otherID)
	if !errors.Is(err, ErrOwnerAlreadyAssigned) {
		t.Fatalf("second owner error = %v, want ErrOwnerAlreadyAssigned", err)
	}
}

func TestStoreProvisionDefaultMembership(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	accountID := fixture.insertAccount(t, "provisioned", "user")
	defaultOrganization := fixture.defaultOrganization(t)

	membership, err := store.ProvisionDefaultMembership(fixture.ctx, accountID, "user")
	if err != nil {
		t.Fatalf("ProvisionDefaultMembership: %v", err)
	}
	if membership.OrganizationID != defaultOrganization.ID || membership.AccountID != accountID || membership.Status != MembershipActive || membership.LegacyRole != "user" {
		t.Fatalf("membership = %#v, want active user membership in default organization", membership)
	}

	again, err := store.ProvisionDefaultMembership(fixture.ctx, accountID, "user")
	if err != nil {
		t.Fatalf("repeat ProvisionDefaultMembership: %v", err)
	}
	if again != membership {
		t.Fatalf("repeat membership = %#v, want %#v", again, membership)
	}

	if _, err := store.ProvisionDefaultMembership(fixture.ctx, accountID, "admin"); !errors.Is(err, ErrMembershipConflict) {
		t.Fatalf("conflicting role error = %v, want ErrMembershipConflict", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE organization_memberships
		SET status = 'suspended'
		WHERE organization_id = $1 AND account_id = $2`, defaultOrganization.ID, accountID); err != nil {
		t.Fatalf("suspend membership: %v", err)
	}
	if _, err := store.ProvisionDefaultMembership(fixture.ctx, accountID, "user"); !errors.Is(err, ErrMembershipConflict) {
		t.Fatalf("conflicting status error = %v, want ErrMembershipConflict", err)
	}

	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organizations SET is_default = false WHERE is_default`); err != nil {
		t.Fatalf("remove default organization marker: %v", err)
	}
	if _, err := store.ProvisionDefaultMembership(fixture.ctx, fixture.insertAccount(t, "without-default", "user"), "user"); !errors.Is(err, ErrOwnershipResolutionRequired) {
		t.Fatalf("missing default organization error = %v, want ErrOwnershipResolutionRequired", err)
	}
}

func TestStoreMissingRowsReturnTypedErrors(t *testing.T) {
	store, fixture := newTenancyFixture(t)

	if _, err := store.GetOrganization(fixture.ctx, uuid.New()); !errors.Is(err, ErrOrganizationNotFound) {
		t.Fatalf("missing organization error = %v, want ErrOrganizationNotFound", err)
	}
	if _, err := store.GetMembership(fixture.ctx, fixture.adminID, uuid.New()); !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("missing membership error = %v, want ErrMembershipNotFound", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organizations SET is_default = false WHERE is_default`); err != nil {
		t.Fatalf("remove default organization marker: %v", err)
	}
	if _, err := store.DefaultOrganization(fixture.ctx); !errors.Is(err, ErrOwnershipResolutionRequired) {
		t.Fatalf("missing default organization error = %v, want ErrOwnershipResolutionRequired", err)
	}
}

func TestStoreGetOrganization(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	want := fixture.defaultOrganization(t)

	got, err := store.GetOrganization(fixture.ctx, want.ID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if got != want {
		t.Fatalf("GetOrganization = %#v, want %#v", got, want)
	}
}

func TestStoreActivateInitialOwnershipRequiresActiveMembership(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	defaultOrganization := fixture.defaultOrganization(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE organization_memberships
		SET status = 'suspended'
		WHERE organization_id = $1 AND account_id = $2`, defaultOrganization.ID, fixture.adminID); err != nil {
		t.Fatalf("suspend membership: %v", err)
	}

	_, err := store.ActivateInitialOwnership(fixture.ctx, fixture.adminID)
	if !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("activation error = %v, want ErrMembershipNotFound", err)
	}
	assertOwnershipRevisions(t, fixture, fixture.adminID, 1, 1, 1, false)
}

func TestStoreActivateInitialOwnershipRequiresEnabledAdmin(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	for _, tt := range []struct {
		name      string
		accountID int
		disable   bool
	}{
		{name: "ordinary member", accountID: fixture.otherID},
		{name: "disabled admin", accountID: fixture.adminID, disable: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.disable {
				if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET enabled = false WHERE id = $1`, tt.accountID); err != nil {
					t.Fatalf("disable account: %v", err)
				}
			}
			_, err := store.ActivateInitialOwnership(fixture.ctx, tt.accountID)
			if !errors.Is(err, ErrOwnerNotEligible) {
				t.Fatalf("activation error = %v, want ErrOwnerNotEligible", err)
			}
			if tt.disable {
				if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET enabled = true WHERE id = $1`, tt.accountID); err != nil {
					t.Fatalf("re-enable account: %v", err)
				}
			}
		})
	}
}

func TestStoreActivateInitialOwnershipClearsAmbiguity(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE platform_security
		SET ownership_resolution_required = true`); err != nil {
		t.Fatalf("mark ownership ambiguous: %v", err)
	}

	if _, err := store.ActivateInitialOwnership(fixture.ctx, fixture.adminID); err != nil {
		t.Fatalf("ActivateInitialOwnership: %v", err)
	}
	assertOwnershipRevisions(t, fixture, fixture.adminID, 2, 2, 2, false)
}

func TestStoreActivateInitialOwnershipConcurrentOwners(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET role = 'admin' WHERE id = $1`, fixture.otherID); err != nil {
		t.Fatalf("make second account an admin: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships SET legacy_role = 'admin' WHERE account_id = $1`, fixture.otherID); err != nil {
		t.Fatalf("make second membership an admin: %v", err)
	}
	results := make(chan error, 2)
	for _, accountID := range []int{fixture.adminID, fixture.otherID} {
		go func() {
			_, err := store.ActivateInitialOwnership(fixture.ctx, accountID)
			results <- err
		}()
	}

	var successes int
	for range 2 {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrOwnerAlreadyAssigned) {
			t.Fatalf("concurrent activation error = %v, want ErrOwnerAlreadyAssigned", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent activations = %d, want 1", successes)
	}
	defaultOrganization := fixture.defaultOrganization(t)
	if defaultOrganization.OwnerAccountID == nil {
		t.Fatal("default organization owner is nil after concurrent activation")
	}
	assertOwnershipRevisions(t, fixture, *defaultOrganization.OwnerAccountID, 2, 2, 2, false)
}

type tenancyFixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	adminID int
	otherID int
	suffix  string
}

func newTenancyFixture(t *testing.T) (*Store, tenancyFixture) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set; skipping local PostgreSQL test")
	}
	ctx := context.Background()
	pool := newTenancyDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	fixture := tenancyFixture{ctx: ctx, pool: pool, suffix: fmt.Sprintf("%d", time.Now().UnixNano())}
	fixture.adminID = fixture.insertAccount(t, "admin", "admin")
	fixture.otherID = fixture.insertAccount(t, "other", "user")
	organization := fixture.defaultOrganization(t)
	for _, account := range []struct {
		id   int
		role string
	}{{fixture.adminID, "admin"}, {fixture.otherID, "user"}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
			VALUES ($1, $2, 'active', $3)`, organization.ID, account.id, account.role); err != nil {
			t.Fatalf("add active membership: %v", err)
		}
	}
	return NewStore(pool), fixture
}

func (f tenancyFixture) insertAccount(t *testing.T, label, role string) int {
	t.Helper()
	var accountID int
	username := fmt.Sprintf("tenancy-test-%s-%s", f.suffix, label)
	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ($1, $2, 'test-password-hash', $3, true)
		RETURNING id`, username, username+"@example.test", role).Scan(&accountID); err != nil {
		t.Fatalf("insert %s account: %v", label, err)
	}
	return accountID
}

func (f tenancyFixture) defaultOrganization(t *testing.T) Organization {
	t.Helper()
	var organization Organization
	if err := f.pool.QueryRow(f.ctx, `
		SELECT id, slug, name, status, owner_account_id, policy_revision, is_default
		FROM organizations WHERE is_default`).Scan(
		&organization.ID,
		&organization.Slug,
		&organization.Name,
		&organization.Status,
		&organization.OwnerAccountID,
		&organization.PolicyRevision,
		&organization.Default,
	); err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	return organization
}

func assertOwnershipRevisions(t *testing.T, fixture tenancyFixture, accountID int, wantPlatform, wantOrganization, wantMembership int64, wantResolution bool) {
	t.Helper()
	var (
		platformRevision     int64
		organizationRevision int64
		membershipRevision   int64
		resolutionRequired   bool
	)
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT p.policy_revision, o.policy_revision, m.security_revision, p.ownership_resolution_required
		FROM platform_security p
		JOIN organizations o ON o.is_default
		JOIN organization_memberships m ON m.organization_id = o.id AND m.account_id = $1`, accountID,
	).Scan(&platformRevision, &organizationRevision, &membershipRevision, &resolutionRequired); err != nil {
		t.Fatalf("load ownership revisions: %v", err)
	}
	if platformRevision != wantPlatform || organizationRevision != wantOrganization || membershipRevision != wantMembership || resolutionRequired != wantResolution {
		t.Fatalf("ownership revisions = platform %d organization %d membership %d resolution %t, want %d %d %d %t", platformRevision, organizationRevision, membershipRevision, resolutionRequired, wantPlatform, wantOrganization, wantMembership, wantResolution)
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func newTenancyDisposableDatabase(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate database name: %v", err)
	}
	name := "vondel_tenancy_" + hex.EncodeToString(random[:])

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
		terminateCtx, cancelTerminate := context.WithTimeout(context.Background(), 30*time.Second)
		_, _ = admin.Exec(terminateCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		cancelTerminate()

		dropCtx, cancelDrop := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelDrop()
		if _, err := admin.Exec(dropCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database %q: %v", name, err)
		}
		admin.Close()
	})
	return pool
}
