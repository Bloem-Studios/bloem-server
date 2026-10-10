package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

func TestTenantOrganizationDeletePreservesSharedAccounts(t *testing.T) {
	for _, lifecycle := range []bool{true, false} {
		name := "legacy"
		if lifecycle {
			name = "lifecycle"
		}
		t.Run(name, func(t *testing.T) {
			pool, store, users, _, router := tenantOrganizationLifecycleHarness(t)
			ctx := context.Background()
			if !lifecycle {
				r := chi.NewRouter()
				h := handlers.NewAdminTenantsHandler(store, users)
				r.Delete("/api/v1/admin/tenants/{id}", h.HandleDelete)
				router = r
			}
			createTenant := func() tenancy.TenantOrganization {
				t.Helper()
				v, err := store.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
					Name: "Retirement isolation", ExternalServiceID: "retirement-" + uuid.NewString(), Slots: 3, Transcodes: 1,
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.DeleteTenantOrganization(context.Background(), v.ID) })
				return v
			}
			a, b := createTenant(), createTenant()
			members := tenancy.NewMemberService(pool, auth.NewAccountProvisioner(users, nil), users, auth.NewSessionRepository(pool))
			member, _, err := members.Create(ctx, a.ID, "shared-retirement-"+uuid.NewString(), tenancy.CreateMemberInput{
				Username: "retirement-member-" + uuid.NewString(), Email: uuid.NewString() + "@retirement.test", Password: "test-password",
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = users.Delete(context.Background(), member.ID) })
			if _, err := store.ProvisionTenantMembership(ctx, b.ID, member.ID, "user"); err != nil {
				t.Fatal(err)
			}
			var group int64
			if err := pool.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, b.ID).Scan(&group); err != nil {
				t.Fatal(err)
			}
			profile := uuid.NewString()
			if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id) VALUES ($1,$2,'Surviving profile',$3,$4)`, profile, member.ID, b.ID, group); err != nil {
				t.Fatal(err)
			}
			path := "/api/v1/admin/tenants/" + a.ID.String()
			key := "retire-shared-" + uuid.NewString()
			got := lifecycleTenantRequest(t, router, http.MethodDelete, path, key, nil)
			if got.Code != http.StatusNoContent {
				t.Fatalf("tenant delete = %d: %s", got.Code, got.Body.String())
			}
			var account, membership, keptProfile, removedMembership bool
			if err := pool.QueryRow(ctx, `SELECT
				EXISTS(SELECT 1 FROM users WHERE id=$1),
				EXISTS(SELECT 1 FROM organization_memberships WHERE organization_id=$2 AND account_id=$1),
				EXISTS(SELECT 1 FROM user_profiles WHERE id=$3 AND user_id=$1 AND organization_id=$2),
				EXISTS(SELECT 1 FROM organization_memberships WHERE organization_id=$4 AND account_id=$1)`, member.ID, b.ID, profile, a.ID).
				Scan(&account, &membership, &keptProfile, &removedMembership); err != nil {
				t.Fatal(err)
			}
			if !account || !membership || !keptProfile || removedMembership {
				t.Fatalf("retirement crossed organization boundary: account=%v other_membership=%v other_profile=%v retired_membership=%v", account, membership, keptProfile, removedMembership)
			}
		})
	}
}
