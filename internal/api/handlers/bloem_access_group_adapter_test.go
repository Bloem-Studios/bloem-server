package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTenantAccessGroupAdapterRejectsMissingAndZeroContext(t *testing.T) {
	a := &tenantAccessGroupAdapter{}
	for _, ctx := range []context.Context{t.Context(), tenancy.WithContext(t.Context(), tenancy.Context{AccountID: 1})} {
		if _, err := a.Get(ctx, 1); err == nil {
			t.Fatal("Get accepted unavailable tenant")
		}
		if _, _, err := a.ListPage(ctx, nil, 10); err == nil {
			t.Fatal("ListPage accepted unavailable tenant")
		}
		if err := a.DeleteMovingMembers(ctx, 1, access.GroupPrecondition{Any: true}); err == nil {
			t.Fatal("DeleteMovingMembers accepted unavailable tenant")
		}
		h := NewTenantAccessGroupHandler(nil)
		for _, handle := range []http.HandlerFunc{h.HandleList, h.HandleCreate, h.HandleGet, h.HandleUpdate, h.HandleDelete} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/access-groups", nil).WithContext(ctx)
			RequireAccessGroupTenant(handle)(rec, req)
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if rec.Code != 503 || body["error"] != "tenant_unavailable" {
				t.Fatalf("availability response: status=%d body=%s", rec.Code, rec.Body)
			}
		}
		if _, err := h.GetAdminAccessGroup(ctx, 1); err == nil {
			t.Fatal("v2 service accepted unavailable tenant")
		}
	}
}

func TestTenantAccessGroupAdapterReusesActiveOrganizationDB(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := access.NewTenantGroupStore(pool)
	a := &tenantAccessGroupAdapter{store: store}
	var orgs [2]uuid.UUID
	var groups [2]*access.TenantGroup
	for i := range orgs {
		if err := pool.QueryRow(ctx, "INSERT INTO organizations (slug,name,status) VALUES ($1,$2,'initializing') RETURNING id", "adapter-"+uuid.NewString(), "Adapter").Scan(&orgs[i]); err != nil {
			t.Fatal(err)
		}
		org := orgs[i]
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM organizations WHERE id=$1", org) })
		groups[i], err = store.CreatePolicy(ctx, org, access.TenantCreateGroupInput{CreateGroupInput: access.CreateGroupInput{Name: "Scoped", LibraryIDs: []int{}, AllowedPermissions: []string{}}, PlaybackAllowed: new(false), MaxProfiles: 3})
		if err != nil {
			t.Fatal(err)
		}
		id := groups[i].ID
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), "DELETE FROM access_groups WHERE organization_id=$1 AND id=$2", org, id)
		})
	}
	for i := range orgs {
		scoped := tenancy.WithContext(ctx, tenancy.Context{OrganizationID: orgs[i], AccountID: 1})
		listed, err := a.List(scoped)
		if err != nil || len(listed) != 1 || listed[0].ID != groups[i].ID {
			t.Fatalf("scoped list: %v %v", listed, err)
		}
		page, more, err := a.ListPage(scoped, nil, 1)
		if err != nil || more || len(page) != 1 || page[0].ID != groups[i].ID {
			t.Fatalf("scoped page: %v %v %v", page, more, err)
		}
		foreign := groups[1-i].ID
		if _, err := a.Get(scoped, foreign); !errors.Is(err, access.ErrGroupNotFound) {
			t.Fatalf("foreign Get: %v", err)
		}
		if _, err := a.Update(scoped, foreign, access.UpdateGroupInput{Name: new("Foreign")}); !errors.Is(err, access.ErrGroupNotFound) {
			t.Fatalf("foreign Update: %v", err)
		}
		if err := a.Delete(scoped, foreign); !errors.Is(err, access.ErrGroupNotFound) {
			t.Fatalf("foreign Delete: %v", err)
		}
		current, err := a.UpdateConditional(scoped, groups[i].ID, access.UpdateGroupInput{Description: new("Updated")}, access.GroupPrecondition{Revision: groups[i].Revision})
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.UpdateConditional(scoped, current.ID, access.UpdateGroupInput{Name: new("Stale")}, access.GroupPrecondition{Revision: groups[i].Revision})
		var conflict *access.GroupRevisionConflict
		if !errors.As(err, &conflict) || conflict.Current.ID != current.ID || conflict.Current.Revision != current.Revision {
			t.Fatalf("stale revision projection: %v", err)
		}
		persisted, err := store.Get(scoped, orgs[i], current.ID)
		if err != nil || persisted.PlaybackAllowed || persisted.MaxProfiles != 3 {
			t.Fatalf("common update changed tenant policy: %v %v", persisted, err)
		}
		policy := persisted.Policy()
		view := toAdminUserResponse(&models.User{ID: 1, Role: models.RoleUser, AccessGroupID: &current.ID, MaxProfiles: 5}, &policy)
		if view.EffectivePolicy.PlaybackAllowed || view.EffectivePolicy.MaxProfiles != 3 {
			t.Fatalf("account effective policy lost tenant fields: %+v", view.EffectivePolicy)
		}
	}
}

func TestTenantAccessGroupLegacyProjectionPreservesNullArrays(t *testing.T) {
	for _, group := range []access.Group{{ID: 1, LibraryIDs: nil, AllowedPermissions: nil}, {ID: 1, LibraryIDs: []int{}, AllowedPermissions: []string{}}} {
		wire, err := json.Marshal(toAccessGroupResponse(group))
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(wire, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 17 || fields["library_ids"] != nil || fields["allowed_permissions"] != nil {
			t.Fatalf("legacy projection changed: %s", wire)
		}
		for _, key := range []string{"organization_id", "playback_allowed", "max_profiles", "managed_template_key"} {
			if _, ok := fields[key]; ok {
				t.Fatalf("storage field %s reached group wire", key)
			}
		}
	}
}
