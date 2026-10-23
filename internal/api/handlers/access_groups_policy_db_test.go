package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
)

func TestAccessGroupUpdateLibraryPolicyDB(t *testing.T) {
	f := newPagingIntegrationFixture(t)
	f.exec(t, `UPDATE users SET email=username||'@example.invalid', password_hash='' WHERE id=$1`, f.account)
	// Bloem: groups belong to an organization and are assigned on the
	// account's membership, so the policy runs through Bloem's tenant store.
	var org uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM organizations WHERE is_default`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	store := access.NewTenantGroupStore(f.pool)
	group, err := store.Create(t.Context(), org, access.CreateGroupInput{Name: fmt.Sprintf("Library policy %d", f.account)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		execMembershipPolicy(t, ctx, f.pool, `UPDATE organization_memberships SET access_group_id=NULL WHERE account_id=$1 AND organization_id=$2`, f.account, org)
		if err := store.Delete(ctx, org, group.ID); err != nil {
			t.Error(err)
		}
	})
	execMembershipPolicy(t, t.Context(), f.pool, `UPDATE organization_memberships SET access_group_id=$2 WHERE account_id=$1 AND organization_id=$3`, f.account, group.ID, org)
	h := NewTenantAccessGroupHandler(store)
	resolver := access.NewResolver(auth.NewUserRepository(f.pool), pgstore.NewPostgresProvider(f.pool), nil, store)
	executor := &catalog.QueryExecutor{Pool: f.pool}
	for _, tc := range []struct {
		name  string
		body  string
		want  []int
		count int
	}{
		{"omitted", `{}`, []int{f.library}, 4},
		{"null", `{"library_ids":null}`, nil, 5},
		{"empty", `{"library_ids":[]}`, []int{}, 0},
		{"populated", fmt.Sprintf(`{"library_ids":[%d]}`, f.hidden), []int{f.hidden}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.Update(t.Context(), org, group.ID, access.UpdateGroupInput{LibraryIDs: new([]int{f.library})}); err != nil {
				t.Fatal(err)
			}
			id := strconv.FormatInt(group.ID, 10)
			rec := httptest.NewRecorder()
			req := accessGroupRequestWithID(http.MethodPut, "/api/v1/admin/access-groups/"+id, strings.NewReader(tc.body), id)
			h.HandleUpdate(rec, req.WithContext(tenancy.WithContext(req.Context(), tenancy.Context{OrganizationID: org, AccountID: f.account, Legacy: true})))
			if rec.Code != http.StatusOK {
				t.Fatalf("update status = %d, body %s", rec.Code, rec.Body.String())
			}
			if got := decodeAccessGroupResponse(t, rec).LibraryIDs; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("response library_ids = %#v, want %#v", got, tc.want)
			}
			var ids []int
			if err := f.pool.QueryRow(t.Context(), `SELECT library_ids FROM access_groups WHERE id=$1`, group.ID).Scan(&ids); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Errorf("stored library_ids = %#v, want %#v", ids, tc.want)
			}
			scope, err := resolver.Resolve(tenancy.WithContext(t.Context(), tenancy.Context{OrganizationID: org, AccountID: f.account, Legacy: true}), access.ResolveInput{UserID: f.account})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(scope.AllowedLibraryIDs, tc.want) || scope.LibrariesRestricted != (tc.want != nil) {
				t.Errorf("scope library_ids = %#v, restricted = %t; want %#v", scope.AllowedLibraryIDs, scope.LibrariesRestricted, tc.want)
			}
			// Restrict the query to this fixture's two synthetic libraries.
			count, err := executor.Count(t.Context(), catalog.QueryDefinition{LibraryIDs: []int{f.library, f.hidden}}, catalog.AccessFilter{AllowedLibraryIDs: scope.AllowedLibraryIDs})
			if err != nil {
				t.Fatal(err)
			}
			if count != tc.count {
				t.Errorf("visible synthetic titles = %d, want %d", count, tc.count)
			}
		})
	}
}
