package apiv2

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBloemRequestGroupLimitTransportOrganizationBoundaryDB(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var orgs [3]uuid.UUID
	for i := range 2 {
		if err := pool.QueryRow(t.Context(), `INSERT INTO organizations (slug,name,status) VALUES ($1,'Request limit transport','initializing') RETURNING id`, "request-limit-"+uuid.NewString()).Scan(&orgs[i]); err != nil {
			t.Fatal(err)
		}
		org := orgs[i]
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org) })
	}
	if err := pool.QueryRow(t.Context(), `SELECT id FROM organizations WHERE is_default`).Scan(&orgs[2]); err != nil {
		t.Fatal(err)
	}
	repo := mediarequests.NewRepository(pool, nil)
	var groups [3]int64
	for i, org := range orgs {
		if err := pool.QueryRow(t.Context(), `INSERT INTO access_groups (organization_id,name) VALUES ($1,$2) RETURNING id`, org, "Request limit "+uuid.NewString()).Scan(&groups[i]); err != nil {
			t.Fatal(err)
		}
		groupID := groups[i]
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM access_groups WHERE id=$1`, groupID) })
		if _, err := repo.UpsertGroupLimitConditional(t.Context(), mediarequests.GroupLimit{GroupID: groupID, LimitMode: mediarequests.LimitModeUnlimited, ApprovalMode: mediarequests.ApprovalModeManual}, 0); err != nil {
			t.Fatal(err)
		}
	}
	requestHandler := handlers.NewRequestsHandler(mediarequests.NewService(repo, nil, nil))
	if err := requestHandler.EnableTenantGroupLimits(); err != nil {
		t.Fatal(err)
	}
	service := requestHandler.Service()
	newHandler := func(org uuid.UUID) http.Handler {
		deps := requestDeps(fixtureRequests())
		deps.AdminRequests = service
		if org != uuid.Nil {
			deps.TenantIdentity = func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					tenant := tenancy.Context{AccountID: claimsFrom(r.Context()).UserID, OrganizationID: org, MembershipID: uuid.New(), OrganizationStatus: tenancy.OrganizationActive, MembershipStatus: tenancy.MembershipActive, PolicyRevision: 7, SecurityRevision: 11, OrganizationDefault: org == orgs[2]}
					if org == orgs[2] {
						tenant.Legacy, tenant.OrganizationStatus = true, tenancy.OrganizationInitializing
					}
					ctx := tenancy.WithContext(r.Context(), tenant)
					next.ServeHTTP(w, r.WithContext(ctx))
				})
			}
		}
		return NewHandler(deps)
	}
	body := `{"limit_mode":"custom","max_requests":3,"window_days":14,"approval_mode":"auto"}`
	for i, org := range orgs {
		h := newHandler(org)
		for j, groupID := range groups {
			t.Run(fmt.Sprintf("organization-%d/group-%d", i, j), func(t *testing.T) {
				path := fmt.Sprintf("%s/admin/request-groups/%d/limit", Prefix, groupID)
				before, err := repo.GetGroupLimit(t.Context(), groupID)
				if err != nil {
					t.Fatal(err)
				}
				read := do(t, h, http.MethodGet, path, "", actingRequestAdmin)
				write := do(t, h, http.MethodPut, path, body, with(actingRequestAdmin, "If-Match", "*"))
				if i == j {
					if read.Code != http.StatusOK || read.Header().Get("ETag") == "" || write.Code != http.StatusOK {
						t.Fatalf("own group: read=%d %s write=%d %s", read.Code, read.Body, write.Code, write.Body)
					}
					return
				}
				if read.Code != http.StatusNotFound || write.Code != http.StatusNotFound {
					t.Errorf("foreign group: read=%d %s write=%d %s", read.Code, read.Body, write.Code, write.Body)
				}
				after, err := repo.GetGroupLimit(t.Context(), groupID)
				if err != nil || after.Revision != before.Revision {
					t.Fatalf("foreign write changed revision: before=%+v after=%+v error=%v", before, after, err)
				}
			})
		}
	}
	path := fmt.Sprintf("%s/admin/request-groups/%d/limit", Prefix, groups[0])
	h := newHandler(uuid.Nil)
	requireProblem(t, do(t, h, http.MethodGet, path, "", actingRequestAdmin), TypePermissionDenied)
	requireProblem(t, do(t, h, http.MethodPut, path, body, with(actingRequestAdmin, "If-Match", "*")), TypePermissionDenied)
	h = newHandler(orgs[0])
	requireProblem(t, do(t, h, http.MethodGet, path, "", requestOwner), TypePermissionDenied)
	requireProblem(t, do(t, h, http.MethodPut, path, body, with(requestOwner, "If-Match", "*")), TypePermissionDenied)
}
