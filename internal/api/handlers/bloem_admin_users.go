package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/sessioninvalidation"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type DirectEntitlementProvisioner interface {
	ApplyDefaultAccountTemplate(context.Context, int, int, string, int64, bool) (entitlements.ApplyResult, error)
}

// AccountPolicyReader exposes only authoritative Server-side policy state.
// Callers cannot supply an expected template or cohort identity through this
// boundary.
type AccountPolicyReader interface {
	GetAccountPolicy(context.Context, uuid.UUID, int) (entitlements.AccountPolicySnapshot, error)
	GetAccountPolicies(context.Context, uuid.UUID, []int) ([]entitlements.AccountPolicySnapshotResult, time.Time, error)
}

// SetProfileHandler wires the same fully configured profile handler used by
// the native profile routes. Admin profile mutations delegate to its shared
// lifecycle so avatar and shared device/download cleanup cannot drift.
func (h *AdminHandler) SetProfileHandler(profileHandler *ProfileHandler) {
	h.profileHandler = profileHandler
}

// SetTenantStore wires the park tenant slot gate into user creation.
func (h *AdminHandler) SetTenantStore(store *tenancy.Store) { h.tenantStore = store }

// SetDirectEntitlements wires direct-product account provisioning to exact
// entitlement template revisions in the deployment default organization.
func (h *AdminHandler) SetDirectEntitlements(store DirectEntitlementProvisioner) {
	h.directEntitlements = store
}

// SetAccountPolicies wires authoritative platform account-policy reads.
func (h *AdminHandler) SetAccountPolicies(store AccountPolicyReader) {
	h.accountPolicies = store
}

// SetPlatformEntitlementAuthorizer wires the current platform-admin check used
// by long-lived scoped API keys. Admin-context tokens are revalidated by their
// middleware instead.
func (h *AdminHandler) SetPlatformEntitlementAuthorizer(authorizer auth.PlatformAdminAuthorizer) {
	h.platformEntitlementAuthorizer = authorizer
}

// SetPlatformEntitlementBulk wires generic platform cohort discovery and the
// policy-specific durable people workflow. The boundary intentionally exposes
// no generic people bulk-job methods.
func (h *AdminHandler) SetPlatformEntitlementBulk(
	cohorts PlatformEntitlementBulkCohortStore,
	people PlatformEntitlementBulkPeopleService,
	organizations PlatformEntitlementBulkOrganizationStore,
	authorizer auth.PlatformAdminAuthorizer,
	worker AdminPeopleWorkerWake,
) {
	h.platformEntitlementCohorts = cohorts
	h.platformEntitlementPeople = people
	h.platformEntitlementOrganizations = organizations
	h.platformEntitlementAuthorizer = authorizer
	h.platformEntitlementWorker = worker
}

var errTenantMembership = errors.New("tenant membership")

// bloemAdminHandlerExt holds every Bloem-only AdminHandler dependency so the
// Silo-owned struct carries a single embedded line.
type bloemAdminHandlerExt struct {
	sessionRepo    adminUserSessionRepository
	profileHandler *ProfileHandler
	// tenantStore gates tenant-scoped account creation (bloem-park growth
	// G2); nil means tenants are not wired and an organization_id request
	// is refused.
	tenantStore                      *tenancy.Store
	directEntitlements               DirectEntitlementProvisioner
	accountPolicies                  AccountPolicyReader
	platformEntitlementCohorts       PlatformEntitlementBulkCohortStore
	platformEntitlementPeople        PlatformEntitlementBulkPeopleService
	platformEntitlementOrganizations PlatformEntitlementBulkOrganizationStore
	platformEntitlementAuthorizer    auth.PlatformAdminAuthorizer
	platformEntitlementWorker        AdminPeopleWorkerWake
}

// withAppliedEntitlementRevision stamps the applied direct-entitlement
// template revision onto an admin user response (zero omits it).
func withAppliedEntitlementRevision(resp AdminUserView, revision int64) AdminUserView {
	resp.AppliedEntitlementRevision = revision
	return resp
}

// requestAccountID is the authenticated account making the request, or 0:
// the account behind the session or API key, or behind a Platform context on
// the native account routes.
func requestAccountID(r *http.Request) int {
	if claims := apimw.GetClaims(r.Context()); claims != nil {
		return claims.UserID
	}
	if claims, ok := apimw.GetAdminContextClaims(r.Context()); ok && claims.Scope == auth.AdminScopePlatform {
		return claims.AccountID
	}
	return 0
}

// bloemAdminGroupOrganization resolves the organization whose access groups
// validate an admin account mutation: the request tenant, else the
// deployment default organization.
func bloemAdminGroupOrganization(ctx context.Context, tx pgx.Tx) (uuid.UUID, error) {
	var organizationID uuid.UUID
	if tenant, ok := tenancy.FromContext(ctx); ok {
		return tenant.OrganizationID, nil
	}
	if err := tx.QueryRow(ctx, `SELECT public.bloem_default_organization_id()`).Scan(&organizationID); err != nil {
		return uuid.Nil, err
	}
	return organizationID, nil
}

// bloemRevokeUserSessions revokes every login session of the account (and
// those it impersonates through) and fans the revocation out, all under the
// session-invalidation guard so a failing callback fails the mutation.
func (h *AdminHandler) bloemRevokeUserSessions(ctx context.Context, userID int) error {
	return sessioninvalidation.Run(ctx, func(invalidationCtx context.Context) error {
		if h.sessionRepo != nil {
			if err := h.sessionRepo.RevokeAllByUser(invalidationCtx, userID); err != nil {
				return err
			}
			if err := h.sessionRepo.RevokeAllByImpersonator(invalidationCtx, userID); err != nil {
				return err
			}
		}
		if h.BloemOnUserSessionsRevoked != nil {
			if err := h.BloemOnUserSessionsRevoked(invalidationCtx, userID); err != nil {
				return err
			}
		}
		return nil
	})
}
