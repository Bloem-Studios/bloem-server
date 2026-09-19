package handlers

import (
	"context"
	"time"

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

func (h *AuthHandler) loginDownloadAllowed(ctx context.Context, user *models.User) bool {
	if h.loginTenants == nil {
		return effectiveDownloadAllowed(ctx, user, h.accessGroups)
	}
	if user == nil || user.ID <= 0 {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// Login has just authenticated user; an ambient profile/tenant context is
	// not authority for that newly authenticated account.
	tenant, err := h.loginTenants.Resolve(checkCtx, user.ID, nil, true)
	if err != nil || tenant.AccountID != user.ID || tenant.OrganizationID == uuid.Nil ||
		tenant.MembershipID == uuid.Nil || !tenant.Legacy || !tenant.OrganizationDefault ||
		tenant.MembershipStatus != tenancy.MembershipActive || tenant.OrganizationStatus == tenancy.OrganizationSuspended ||
		tenant.PolicyRevision <= 0 || tenant.SecurityRevision <= 0 {
		// Account login remains available for administrative context selection
		// even without viewer membership. Never advertise a permissive download
		// capability when viewer policy could not be established.
		return false
	}
	return effectiveDownloadAllowed(tenancy.WithContext(checkCtx, tenant), user, h.accessGroups)
}
