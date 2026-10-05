package handlers

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

func accessGroupOrganizationID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	tenant, ok := tenancy.FromContext(r.Context())
	if !ok || tenant.OrganizationID == uuid.Nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return uuid.Nil, false
	}
	return tenant.OrganizationID, true
}

// RequireAccessGroupTenant retains the legacy availability response before storage.
func RequireAccessGroupTenant(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := accessGroupOrganizationID(w, r); !ok {
			return
		}
		next(w, r)
	}
}

type tenantAccessGroupAdapter struct{ store *access.TenantGroupStore }

func NewTenantAccessGroupHandler(store *access.TenantGroupStore) *AccessGroupHandler {
	return NewAccessGroupHandler(&tenantAccessGroupAdapter{store: store})
}

func (a *tenantAccessGroupAdapter) organization(ctx context.Context) (uuid.UUID, error) {
	org, err := adminGroupOrganization(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if a == nil || a.store == nil {
		return uuid.Nil, ErrAccessGroupUnavailable
	}
	return org, nil
}
func tenantGroupProjection(g *access.TenantGroup, err error) (*access.Group, error) {
	if err != nil {
		return nil, err
	}
	return &g.Group, nil
}
func tenantGroupsProjection(groups []access.TenantGroup) []access.Group {
	out := make([]access.Group, len(groups))
	for i := range groups {
		out[i] = groups[i].Group
	}
	return out
}
func (a *tenantAccessGroupAdapter) List(ctx context.Context) ([]access.Group, error) {
	org, err := a.organization(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := a.store.List(ctx, org)
	if err != nil {
		return nil, err
	}
	return tenantGroupsProjection(groups), nil
}
func (a *tenantAccessGroupAdapter) Get(ctx context.Context, id int64) (*access.Group, error) {
	org, err := a.organization(ctx)
	if err != nil {
		return nil, err
	}
	return tenantGroupProjection(a.store.Get(ctx, org, id))
}
func (a *tenantAccessGroupAdapter) Create(ctx context.Context, in access.CreateGroupInput) (*access.Group, error) {
	org, err := a.organization(ctx)
	if err != nil {
		return nil, err
	}
	return tenantGroupProjection(a.store.Create(ctx, org, in))
}
func (a *tenantAccessGroupAdapter) Update(ctx context.Context, id int64, in access.UpdateGroupInput) (*access.Group, error) {
	org, err := a.organization(ctx)
	if err != nil {
		return nil, err
	}
	return tenantGroupProjection(a.store.Update(ctx, org, id, in))
}
func (a *tenantAccessGroupAdapter) Delete(ctx context.Context, id int64) error {
	org, err := a.organization(ctx)
	if err != nil {
		return err
	}
	return a.store.Delete(ctx, org, id)
}
func (a *tenantAccessGroupAdapter) ListPage(ctx context.Context, after *access.GroupPageKey, limit int) ([]access.Group, bool, error) {
	org, err := a.organization(ctx)
	if err != nil {
		return nil, false, err
	}
	groups, more, err := a.store.ListPage(ctx, org, after, limit)
	if err != nil {
		return nil, false, err
	}
	return tenantGroupsProjection(groups), more, nil
}
func (a *tenantAccessGroupAdapter) UpdateConditional(ctx context.Context, id int64, in access.UpdateGroupInput, guard access.GroupPrecondition) (*access.Group, error) {
	org, err := a.organization(ctx)
	if err != nil {
		return nil, err
	}
	return tenantGroupProjection(a.store.UpdateConditional(ctx, org, id, in, guard))
}
func (a *tenantAccessGroupAdapter) DeleteConditional(ctx context.Context, id int64, guard access.GroupPrecondition) error {
	org, err := a.organization(ctx)
	if err != nil {
		return err
	}
	return a.store.DeleteConditional(ctx, org, id, guard)
}
func (a *tenantAccessGroupAdapter) DeleteMovingMembers(ctx context.Context, id int64, guard access.GroupPrecondition) error {
	org, err := a.organization(ctx)
	if err != nil {
		return err
	}
	_, err = a.store.DeleteMovingMembers(ctx, org, id, guard, nil)
	return err
}

var _ AccessGroupStore = (*tenantAccessGroupAdapter)(nil)
var _ guardedAccessGroupStore = (*tenantAccessGroupAdapter)(nil)
var _ memberMovingGroupStore = (*tenantAccessGroupAdapter)(nil)
