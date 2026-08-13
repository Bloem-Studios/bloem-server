package policy

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type SubjectTenantResolver interface {
	ResolveSubjectTenant(context.Context, int, string) (tenancy.Context, error)
}

type AccessScopeResolver interface {
	Resolve(context.Context, access.ResolveInput) (access.Scope, error)
}

// TenantViewerResolver composes out-of-request viewer surfaces with the same
// authoritative tenant resolution used by the HTTP middleware. Any context a
// caller supplied is overwritten after subject validation.
type TenantViewerResolver struct {
	viewer  AccessScopeResolver
	tenants SubjectTenantResolver
}

func NewTenantViewerResolver(viewer AccessScopeResolver, tenants SubjectTenantResolver) *TenantViewerResolver {
	return &TenantViewerResolver{viewer: viewer, tenants: tenants}
}

func (r *TenantViewerResolver) Resolve(ctx context.Context, input access.ResolveInput) (access.Scope, error) {
	if r == nil || r.viewer == nil || r.tenants == nil {
		return access.Scope{}, fmt.Errorf("resolve viewer tenant: missing resolver")
	}
	tenant, err := r.tenants.ResolveSubjectTenant(ctx, input.UserID, input.ProfileID)
	if err != nil {
		return access.Scope{}, fmt.Errorf("resolve viewer tenant: %w", err)
	}
	return r.viewer.Resolve(tenancy.WithContext(ctx, tenant), input)
}
