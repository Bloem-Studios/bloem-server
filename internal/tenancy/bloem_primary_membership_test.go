package tenancy_test

// Bloem-owned. AccountOrganization and PrimaryMembershipSQL answer the same
// question by different routes -- one indexed lookup per account, one set-based
// projection over all of them. If they ever disagree, a guard that checks a
// single request and a query that bounds a whole page will reach different
// conclusions for the same account, which is precisely the multi-membership
// case the bound exists for.

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
	// organization must have an owner), and a second membership for the
	// admin account: the account now belongs to two organizations, so the
	// selection rule decides which one owns its requests.
	var secondID uuid.UUID
	if err := fixture.pool.QueryRow(fixture.ctx, `
		INSERT INTO organizations (slug, name, status, is_default)
		VALUES ($1, 'Second', 'initializing', false)
		RETURNING id`, fixture.suffix+"-second").Scan(&secondID); err != nil {
		t.Fatalf("insert second organization: %v", err)
	}
	// Backdated deliberately. The selection rule prefers the default
	// organization FIRST and only then the oldest membership, so the second
	// membership has to be the older one for this fixture to tell the two
	// apart. Seeded the obvious way -- default organization joined first --
	// both orderings agree and the test proves nothing.
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role, created_at)
		SELECT $1, $2, 'active', 'user', now() - interval '1 day'
		WHERE set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`,
		secondID, fixture.adminID); err != nil {
		t.Fatalf("add second membership: %v", err)
	}

	for _, accountID := range []int{fixture.adminID, fixture.otherID} {
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

// A suspended membership grants nothing, so it must not pick the account's
// organization. Before this rule, a suspended default-organization membership
// still won the ordering and kept operator authority for the account.
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
			SELECT $1, $2, $3, 'admin'
			WHERE set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`,
			organizationID, accountID, status); err != nil {
			t.Fatalf("insert %s membership: %v", status, err)
		}
	}

	// Suspended in the default organization, active in a tenant: the tenant
	// is the only organization the account may act for.
	demoted := fixture.insertAccount(t, "demoted", "admin")
	insertMembership(defaultOrganization.ID, demoted, "suspended")
	insertMembership(tenantID, demoted, "active")

	// Suspended and invited only: no organization represents the account.
	inactive := fixture.insertAccount(t, "inactive", "admin")
	insertMembership(defaultOrganization.ID, inactive, "suspended")
	insertMembership(tenantID, inactive, "invited")

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

	got, err := store.AccountOrganization(fixture.ctx, demoted)
	if err != nil || got != tenantID {
		t.Fatalf("AccountOrganization(demoted) = %v, %v; want tenant %v", got, err, tenantID)
	}
	if got, ok := primaryOrganization(demoted); !ok || got != tenantID {
		t.Fatalf("PrimaryMembershipSQL(demoted) = %v, %t; want tenant %v", got, ok, tenantID)
	}

	if got, err := store.AccountOrganization(fixture.ctx, inactive); !errors.Is(err, tenancy.ErrMembershipNotFound) {
		t.Fatalf("AccountOrganization(inactive) = %v, %v; want ErrMembershipNotFound", got, err)
	}
	if got, ok := primaryOrganization(inactive); ok {
		t.Fatalf("PrimaryMembershipSQL(inactive) = %v; want no row", got)
	}
}
