package tenancy_test

// Bloem-owned. AccountOrganization and PrimaryMembershipSQL answer the same
// question by different routes -- one indexed lookup per account, one set-based
// projection over all of them. If they ever disagree, a guard that checks a
// single request and a query that bounds a whole page will reach different
// conclusions for the same account, which is precisely the multi-membership
// case the bound exists for.

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
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
