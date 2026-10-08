package policy

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// TenantLibraryResolver resolves the media folders visible to an authoritative
// tenant context.
type TenantLibraryResolver interface {
	AvailableMediaFolderIDs(context.Context, tenancy.Context) ([]int, error)
}

// resolveTenantLibraryIDs returns the media folders available to the
// validated request tenant.
// WithBloemTenancy turns on Bloem tenancy: every resolved scope then requires
// the request's complete tenant and is bounded by the libraries tenantLibraries
// makes available to it, failing closed otherwise. Without it the resolver
// keeps Silo's single-tenant behavior. Production wiring always sets it
// (TestProductionViewerResolversUseBloemTenancy).
func (r *ViewerResolver) WithBloemTenancy(tenantLibraries TenantLibraryResolver) *ViewerResolver {
	if tenantLibraries == nil {
		panic("policy: WithBloemTenancy requires a tenant library resolver")
	}
	r.tenantLibraries = tenantLibraries
	return r
}

// bloemTenantFacts returns the request's tenant facts when tenancy is on, and
// no facts in Silo mode.
func (r *ViewerResolver) bloemTenantFacts(ctx context.Context, userID int) (TenantFacts, error) {
	if r.tenantLibraries == nil {
		return TenantFacts{}, nil
	}
	facts, err := TenantFactsFromContext(ctx, userID)
	if err != nil {
		return TenantFacts{}, fmt.Errorf("resolve viewer scope tenant facts: %w", err)
	}
	return facts, nil
}

func (r *ViewerResolver) resolveTenantLibraryIDs(ctx context.Context) ([]int, error) {
	if r.tenantLibraries == nil {
		return nil, nil
	}
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("resolve viewer scope tenant libraries: %w", ErrTenantFactsUnavailable)
	}
	tenantLibraryIDs, err := r.tenantLibraries.AvailableMediaFolderIDs(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("resolve viewer scope tenant libraries: %w", err)
	}
	return tenantLibraryIDs, nil
}
