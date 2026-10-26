package tenancy_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// A suspended or invited membership grants nothing, so it must not name the
// account's organization.
func TestAccountOrganizationUsesOnlyAnActiveMembership(t *testing.T) {
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

	active := fixture.insertAccount(t, "active", "admin")
	insertMembership(tenantID, active, "active")
	suspended := fixture.insertAccount(t, "suspended", "admin")
	insertMembership(defaultOrganization.ID, suspended, "suspended")
	invited := fixture.insertAccount(t, "invited", "admin")
	insertMembership(tenantID, invited, "invited")

	if got, err := store.AccountOrganization(fixture.ctx, active); err != nil || got != tenantID {
		t.Fatalf("AccountOrganization(active) = %v, %v; want tenant %v", got, err, tenantID)
	}
	for name, accountID := range map[string]int{"suspended": suspended, "invited": invited} {
		if got, err := store.AccountOrganization(fixture.ctx, accountID); !errors.Is(err, tenancy.ErrMembershipNotFound) {
			t.Fatalf("AccountOrganization(%s) = %v, %v; want ErrMembershipNotFound", name, got, err)
		}
	}
}
