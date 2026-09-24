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
func (r *ViewerResolver) resolveTenantLibraryIDs(ctx context.Context) ([]int, error) {
	if r.tenantLibraries == nil {
		return nil, fmt.Errorf("resolve viewer scope tenant libraries: missing resolver")
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
