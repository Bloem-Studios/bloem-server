package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type retirementFailingAccounts struct {
	*auth.AccountProvisioner
	blockedID int
}

func (a *retirementFailingAccounts) DeleteUserInTransaction(ctx context.Context, tx pgx.Tx, userID int) error {
	if userID == a.blockedID {
		return errors.New("account deletion unavailable")
	}
	return a.AccountProvisioner.DeleteUserInTransaction(ctx, tx, userID)
}

func TestTenantRetirementFailureRollsBackAllMembersAndOrganization(t *testing.T) {
	for _, lifecycle := range []bool{false, true} {
		name := "legacy"
		if lifecycle {
			name = "lifecycle"
		}
		t.Run(name, func(t *testing.T) {
			pool, store, users, _, baseRouter := tenantOrganizationLifecycleHarness(t)
			ctx := context.Background()
			tenant, err := store.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
				Name: "Atomic retirement", ExternalServiceID: "atomic-" + uuid.NewString(), Slots: 2, Transcodes: 0,
			})
			if err != nil {
				t.Fatal(err)
			}
			accounts := &retirementFailingAccounts{AccountProvisioner: auth.NewAccountProvisioner(users, nil)}
			members := tenancy.NewMemberService(pool, accounts, users, auth.NewSessionRepository(pool))
			var ids []int
			for range 2 {
				member, _, err := members.Create(ctx, tenant.ID, "atomic-member-"+uuid.NewString(), tenancy.CreateMemberInput{
					Username: "atomic-" + uuid.NewString(), Email: uuid.NewString() + "@retirement.test", Password: "test-password",
				})
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, member.ID)
			}
			t.Cleanup(func() {
				_ = store.DeleteTenantOrganization(context.Background(), tenant.ID)
				for _, id := range ids {
					_ = users.Delete(context.Background(), id)
				}
			})
			accounts.blockedID = ids[1]
			handler := handlers.NewAdminTenantsHandler(store, members)
			if lifecycle {
				secret := []byte("tenant-organization-lifecycle-test")
				handler.SetLifecycleIdempotency(lifecycleidempotency.NewCoordinator(lifecycleidempotency.NewPostgresStore(pool), lifecycleidempotency.NewHMACKeyDigester(secret)), lifecycleidempotency.NewRequestDigester(secret))
			}
			// Keep the fixture's real actor-claim middleware for the lifecycle
			// path, while dispatching to the failing-account handler.
			router := chi.NewRouter()
			if lifecycle {
				base := baseRouter.(*chi.Mux)
				for _, mw := range base.Middlewares() {
					router.Use(mw)
				}
			}
			router.Delete("/api/v1/admin/tenants/{id}", handler.HandleDelete)
			got := lifecycleTenantRequest(t, router, http.MethodDelete, "/api/v1/admin/tenants/"+tenant.ID.String(), "atomic-retire-"+uuid.NewString(), nil)
			if got.Code != http.StatusInternalServerError {
				t.Fatalf("retirement status=%d, want500", got.Code)
			}
			current, err := store.GetTenantOrganization(ctx, tenant.ID)
			if err != nil || current.Frozen || current.SlotsUsed != 2 {
				t.Fatalf("failed retirement changed organization: %+v, %v", current, err)
			}
			for _, id := range ids {
				var present bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN organization_memberships m ON m.account_id=u.id WHERE u.id=$1 AND m.organization_id=$2)`, id, tenant.ID).Scan(&present); err != nil || !present {
					t.Fatalf("failed retirement removed member%d: present=%v,err=%v", id, present, err)
				}
			}
		})
	}
}
