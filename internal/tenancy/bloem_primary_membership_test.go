package tenancy_test

// Bloem-owned. AccountOrganization and PrimaryMembershipSQL answer the same
// question by different routes -- one indexed lookup per account, one set-based
// projection over all of them. If they ever disagree, a guard that checks a
// single request and a query that bounds a whole page will reach different
// conclusions for the same account.

import (
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPrimaryMembershipSQLAgreesWithAccountOrganization(t *testing.T) {
	store, fixture := newTenancyFixture(t)

	// A second, non-default organization ('initializing' because an active
	// organization must have an owner) with its own member: the accounts
	// belong to different organizations.
	var secondID uuid.UUID
	if err := fixture.pool.QueryRow(fixture.ctx, `
		INSERT INTO organizations (slug, name, status, is_default)
		VALUES ($1, 'Second', 'initializing', false)
		RETURNING id`, fixture.suffix+"-second").Scan(&secondID); err != nil {
		t.Fatalf("insert second organization: %v", err)
	}
	secondMember := fixture.insertAccount(t, "second-member", "user")
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'user')`, secondID, secondMember); err != nil {
		t.Fatalf("add second-organization membership: %v", err)
	}

	for _, accountID := range []int{fixture.adminID, fixture.otherID, secondMember} {
		want, err := store.AccountOrganization(fixture.ctx, accountID)
		if err != nil {
			t.Fatalf("AccountOrganization(%d): %v", accountID, err)
		}

		var got uuid.UUID
		if err := fixture.pool.QueryRow(fixture.ctx, `
			SELECT primary_membership.organization_id
			FROM (`+tenancy.PrimaryMembershipSQL+`
			) AS primary_membership
			WHERE primary_membership.account_id = $1`, accountID).Scan(&got); err != nil {
			t.Fatalf("PrimaryMembershipSQL for account %d: %v", accountID, err)
		}

		if got != want {
			t.Fatalf("account %d: PrimaryMembershipSQL = %v, AccountOrganization = %v", accountID, got, want)
		}
	}
}

// A suspended or invited membership grants nothing, so it must not pick the
// account's organization.
func TestPrimaryMembershipSelectsOnlyActiveMemberships(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	defaultOrganization := fixture.defaultOrganization(t)

	var tenantID uuid.UUID
	if err := fixture.pool.QueryRow(fixture.ctx, `
		INSERT INTO organizations (slug, name, status, is_default)
		VALUES ($1, 'Tenant', 'initializing', false)
		RETURNING id`, fixture.suffix+"-tenant").Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant organization: %v", err)
	}

	insertMembership := func(organizationID uuid.UUID, accountID int, status string) {
		t.Helper()
		if _, err := fixture.pool.Exec(fixture.ctx, `
			INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
			SELECT $1, $2, $3, 'admin'`,
			organizationID, accountID, status); err != nil {
			t.Fatalf("insert %s membership: %v", status, err)
		}
	}

	// Active in a tenant: the tenant is the organization the account acts for.
	active := fixture.insertAccount(t, "active", "admin")
	insertMembership(tenantID, active, "active")

	// Suspended, or invited only: no organization represents the account.
	suspended := fixture.insertAccount(t, "suspended", "admin")
	insertMembership(defaultOrganization.ID, suspended, "suspended")
	invited := fixture.insertAccount(t, "invited", "admin")
	insertMembership(tenantID, invited, "invited")

	primaryOrganization := func(accountID int) (uuid.UUID, bool) {
		t.Helper()
		var organizationID uuid.UUID
		err := fixture.pool.QueryRow(fixture.ctx, `
			SELECT primary_membership.organization_id
			FROM (`+tenancy.PrimaryMembershipSQL+`
			) AS primary_membership
			WHERE primary_membership.account_id = $1`, accountID).Scan(&organizationID)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false
		}
		if err != nil {
			t.Fatalf("PrimaryMembershipSQL for account %d: %v", accountID, err)
		}
		return organizationID, true
	}

	got, err := store.AccountOrganization(fixture.ctx, active)
	if err != nil || got != tenantID {
		t.Fatalf("AccountOrganization(active) = %v, %v; want tenant %v", got, err, tenantID)
	}
	if got, ok := primaryOrganization(active); !ok || got != tenantID {
		t.Fatalf("PrimaryMembershipSQL(active) = %v, %t; want tenant %v", got, ok, tenantID)
	}

	for name, accountID := range map[string]int{"suspended": suspended, "invited": invited} {
		if got, err := store.AccountOrganization(fixture.ctx, accountID); !errors.Is(err, tenancy.ErrMembershipNotFound) {
			t.Fatalf("AccountOrganization(%s) = %v, %v; want ErrMembershipNotFound", name, got, err)
		}
		if got, ok := primaryOrganization(accountID); ok {
			t.Fatalf("PrimaryMembershipSQL(%s) = %v; want no row", name, got)
		}
	}
}
