package tenancy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

func TestBloemMembershipProvisioningPreservesExistingAuthority(t *testing.T) {
	store, f := newTenancyFixture(t)
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(t.Context(), f.pool); err != nil {
		t.Fatal(err)
	}
	ownership, err := store.ActivateInitialOwnership(f.ctx, f.adminID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, status, role string
		conflict           bool
	}{
		{"active same role", "active", "user", false},
		{"suspended", "suspended", "user", true},
		{"invited", "invited", "user", true},
		{"role mismatch", "active", "admin", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if _, err := tx.Exec(t.Context(), `SELECT set_config('bloem.membership_policy_writer','v1',true)`); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(t.Context(), `UPDATE organization_memberships SET status=$1, download_allowed=false WHERE account_id=$2 AND organization_id=$3`, tc.status, f.otherID, ownership.Organization.ID); err != nil {
				t.Fatal(err)
			}
			read := func() string {
				var row string
				if err := tx.QueryRow(t.Context(), `SELECT to_jsonb(m)::text FROM organization_memberships m WHERE account_id=$1 AND organization_id=$2`, f.otherID, ownership.Organization.ID).Scan(&row); err != nil {
					t.Fatal(err)
				}
				return row
			}
			before := read()
			_, err = store.ProvisionMembershipInTransaction(t.Context(), tx, ownership.Organization.ID, f.otherID, tc.role)
			if tc.conflict && !errors.Is(err, tenancy.ErrMembershipConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
			if !tc.conflict && err != nil {
				t.Fatal(err)
			}
			if after := read(); after != before {
				t.Fatal("existing membership policy/status/revision changed during provisioning")
			}
		})
	}
}
