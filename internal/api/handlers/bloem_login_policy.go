package handlers

import (
	"context"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

// SetLoginTenantResolver restores tenant policy for the public password-login
// response. No organization selection is accepted from that request: the issued
// account session uses the same default-organization projection as legacy auth.
func (h *AuthHandler) SetLoginTenantResolver(resolver apimw.TenantResolver) {
	h.loginTenants = resolver
}

// bloemLoginPolicyContext returns the context Login resolves the signed-in
// account's policy in (Silo's effectiveAccountPolicy). The login request has
// no tenant, and an ambient profile/tenant context is not authority for the
// account it has just authenticated, so the account's own default-organization
// tenant is bound instead. When that viewer tenant cannot be established the
// policy fails closed (no downloads, no permissions) rather than fall back to
// the permissive no-group policy: account login stays available for
// administrative context selection without a viewer membership. Without a
// resolver ctx is returned unchanged. The caller cancels the returned context.
func (h *AuthHandler) bloemLoginPolicyContext(ctx context.Context, user *models.User) (context.Context, context.CancelFunc) {
	if h.loginTenants == nil {
		return ctx, func() {}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	tenant := tenancy.Context{}
	if user != nil && user.ID > 0 {
		if resolved, err := h.loginTenants.Resolve(checkCtx, user.ID, nil, true); err == nil && loginViewerTenant(resolved, user.ID) {
			tenant = resolved
		}
	}
	return access.RequireTenantPolicy(tenancy.WithContext(checkCtx, tenant)), cancel
}

// loginViewerTenant reports whether tenant is accountID's active membership of
// the default organization, the only tenant a password login projects.
func loginViewerTenant(tenant tenancy.Context, accountID int) bool {
	return tenant.AccountID == accountID && tenant.OrganizationID != uuid.Nil &&
		tenant.MembershipID != uuid.Nil && tenant.Legacy && tenant.OrganizationDefault &&
		tenant.MembershipStatus == tenancy.MembershipActive && tenant.OrganizationStatus != tenancy.OrganizationSuspended &&
		tenant.PolicyRevision > 0 && tenant.SecurityRevision > 0
}
