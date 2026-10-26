package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/models"
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

// createTenantUser creates an account inside a specific tenant organization
// (bloem-park growth G2) rather than the deployment's default one —
// AccountProvisioner.CreateAccount always provisions the default
// organization, so this replicates its steps against tenancy.Store's
// tenant-specific ones instead, with the same cleanup-on-failure shape.
//
// The account and its tenant membership commit together, the membership under
// the tenant's slot fence. The default profile is a separate step, so the slot
// quota is still checked before AND recounted after: the recount removes an
// account that broke the quota — the breach self-heals instead of standing.
//
// Returns nil once it has already written the HTTP response itself, so the
// caller's own error-writing path is not duplicated for this branch.
func (h *AdminHandler) createTenantUser(ctx context.Context, w http.ResponseWriter, organizationID uuid.UUID, input auth.CreateAccountInput) *models.User {
	if h.tenantStore == nil {
		writeError(w, http.StatusUnprocessableEntity, "validation", "Tenants are not enabled on this server")
		return nil
	}
	if err := h.tenantStore.TenantSlotFree(ctx, organizationID); err != nil {
		switch {
		case errors.Is(err, tenancy.ErrTenantOrganizationNotFound):
			writeError(w, http.StatusUnprocessableEntity, "validation", "No such tenant")
		case errors.Is(err, tenancy.ErrTenantSlotsExhausted):
			writeError(w, http.StatusConflict, "tenant_slots_exhausted",
				"This tenant has no free account slots (or is frozen)")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to check tenant capacity")
		}
		return nil
	}

	user, err := h.createTenantAccount(ctx, organizationID, input.User)
	if err != nil {
		if apiErr := busyError(err); apiErr != nil {
			writeAPIError(w, apiErr)
			return nil
		}
		if errors.Is(err, errTenantMembership) {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to provision tenant membership")
			return nil
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create user")
		return nil
	}

	if input.DefaultProfile.Enabled {
		if err := h.accountProvisioner.CreateDefaultProfile(ctx, user.ID, input); err != nil {
			if deleteErr := h.userRepo.Delete(ctx, user.ID); deleteErr != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to enforce tenant capacity")
				return nil
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create default profile")
			return nil
		}
	}

	// The recount half of the gate: this create may have lost a race with
	// another create against the same tenant.
	if over, err := h.tenantStore.TenantOverQuota(ctx, organizationID); err == nil && over {
		if deleteErr := h.userRepo.Delete(ctx, user.ID); deleteErr != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to enforce tenant capacity")
			return nil
		}
		writeError(w, http.StatusConflict, "tenant_slots_exhausted", "This tenant has no free account slots")
		return nil
	}

	return user
}

var errTenantMembership = errors.New("tenant membership")

// createTenantAccount creates the account and its tenant membership in one
// transaction, so the account never commits without the membership.
func (h *AdminHandler) createTenantAccount(ctx context.Context, organizationID uuid.UUID, input models.CreateUserInput) (*models.User, error) {
	if h.pool == nil {
		return nil, errors.New("tenant account creation needs a database pool")
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, conflict, err := h.accountProvisioner.CreateUserForOrganizationInTransaction(ctx, tx, organizationID, input)
	if err != nil {
		return nil, err
	}
	if conflict {
		return nil, auth.ErrDuplicate
	}
	if _, err := h.tenantStore.ProvisionTenantMembershipInTransaction(ctx, tx, organizationID, user.ID, auth.MembershipLegacyRole(input.Role)); err != nil {
		return nil, fmt.Errorf("%w: %w", errTenantMembership, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return user, nil
}

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

// bloemValidateCreateUserEntitlement normalizes and validates the Bloem
// direct-entitlement fields on POST /admin/users.
func (h *AdminHandler) bloemValidateCreateUserEntitlement(w http.ResponseWriter, req *createUserRequest) (bool, bool) {
	req.EntitlementTemplateKey = strings.TrimSpace(req.EntitlementTemplateKey)
	directEntitlementRequested := req.EntitlementTemplateKey != "" || req.EntitlementTemplateRevision != 0
	if directEntitlementRequested && (req.EntitlementTemplateKey == "" || req.EntitlementTemplateRevision <= 0) {
		writeError(w, http.StatusBadRequest, "bad_request", "entitlement_template_key and a positive entitlement_template_revision are required together")
		return false, false
	}
	if directEntitlementRequested && req.OrganizationID != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "direct entitlement templates cannot be combined with organization_id")
		return false, false
	}
	if directEntitlementRequested && h.directEntitlements == nil {
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", "Entitlement templates are not configured")
		return false, false
	}
	return directEntitlementRequested, true
}

// bloemCreateUserGroupOrganization resolves the organization whose access
// groups validate a create request's access_group_id.
func bloemCreateUserGroupOrganization(w http.ResponseWriter, r *http.Request, req createUserRequest) (uuid.UUID, bool) {
	groupOrgID := req.OrganizationID
	if groupOrgID == nil {
		if tenant, ok := tenancy.FromContext(r.Context()); ok {
			groupOrgID = &tenant.OrganizationID
		}
	}
	if groupOrgID == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to validate access group")
		return uuid.Nil, false
	}
	return *groupOrgID, true
}

// bloemCreateUser performs the Bloem account creation paths (tenant
// organizations, direct entitlement templates). It returns ok=false once it
// has written the response itself.
func (h *AdminHandler) bloemCreateUser(w http.ResponseWriter, r *http.Request, req createUserRequest, accountInput auth.CreateAccountInput, directEntitlementRequested bool) (*models.User, int64, bool) {
	var err error

	var user *models.User
	var appliedEntitlementRevision int64
	if req.OrganizationID != nil {
		user = h.createTenantUser(r.Context(), w, *req.OrganizationID, accountInput)
		if user == nil {
			return nil, 0, false // createTenantUser already wrote the response.
		}
	} else if !directEntitlementRequested {
		user, err = h.accountProvisioner.CreateAccount(r.Context(), accountInput)
		if err != nil {
			if auth.IsDuplicate(err) {
				writeError(w, http.StatusConflict, "duplicate", "A user with that username or email already exists")
				return nil, 0, false
			}
			if apiErr := busyError(err); apiErr != nil {
				writeAPIError(w, apiErr)
				return nil, 0, false
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create user")
			return nil, 0, false
		}
	} else {
		defaultProfile := accountInput.DefaultProfile
		accountInput.DefaultProfile.Enabled = false
		user, err = h.accountProvisioner.CreateAccount(r.Context(), accountInput)
		if err != nil {
			if apiErr := busyError(err); apiErr != nil {
				writeAPIError(w, apiErr)
				return nil, 0, false
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create user")
			return nil, 0, false
		}
		applied, applyErr := h.directEntitlements.ApplyDefaultAccountTemplate(
			r.Context(), requestAccountID(r), user.ID, req.EntitlementTemplateKey, req.EntitlementTemplateRevision, false,
		)
		if applyErr != nil {
			_ = h.userRepo.Delete(r.Context(), user.ID)
			switch {
			case errors.Is(applyErr, entitlements.ErrTemplateNotFound), errors.Is(applyErr, entitlements.ErrTemplateUnavailable):
				writeError(w, http.StatusUnprocessableEntity, "entitlement_template_unavailable", "Entitlement template revision is unavailable")
			default:
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to apply entitlement template")
			}
			return nil, 0, false
		}
		user.AccessGroupID = &applied.GroupID
		appliedEntitlementRevision = applied.TemplateRevision
		if defaultProfile.Enabled {
			accountInput.DefaultProfile = defaultProfile
			if err := h.accountProvisioner.CreateDefaultProfile(r.Context(), user.ID, accountInput); err != nil {
				_ = h.userRepo.Delete(r.Context(), user.ID)
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create default profile")
				return nil, 0, false
			}
		}
	}
	return user, appliedEntitlementRevision, true
}

// bloemPrepareUpdateUser runs the scoped-API-key, grouped-admin and
// tenant-scoped access-group checks for the update path.
func (h *AdminHandler) bloemPrepareUpdateUser(w http.ResponseWriter, r *http.Request, id int, req *updateUserRequest) (*models.User, bool) {
	currentUser, blocked := h.rejectScopedAPIKeyUpdate(w, r, id, req)
	if blocked {
		return currentUser, true
	}
	if req.AccessGroupID.Set {
		if h.rejectGroupedAdmin(w, r, id, req, currentUser) {
			return currentUser, true
		}
		if req.AccessGroupID.Value != nil {
			if h.AccessGroups == nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "Access groups are not configured")
				return currentUser, true
			}
			tenant, ok := tenancy.FromContext(r.Context())
			if !ok || tenant.OrganizationID == uuid.Nil {
				writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
				return currentUser, true
			}
			if _, err := h.AccessGroups.Get(r.Context(), tenant.OrganizationID, *req.AccessGroupID.Value); err != nil {
				if errors.Is(err, access.ErrGroupNotFound) {
					writeError(w, http.StatusUnprocessableEntity, "unprocessable_entity", "Invalid access_group_id")
					return currentUser, true
				}
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to validate access group")
				return currentUser, true
			}
		}
	}
	return currentUser, false
}

// requestAccountID is the authenticated account making the request, or 0.
func requestAccountID(r *http.Request) int {
	if claims := apimw.GetClaims(r.Context()); claims != nil {
		return claims.UserID
	}
	return 0
}

// bloemApplyUpdateUserEntitlement applies a requested direct entitlement
// template after the account update. ok=false means it wrote the response.
func (h *AdminHandler) bloemApplyUpdateUserEntitlement(w http.ResponseWriter, r *http.Request, id int, req updateUserRequest, directEntitlementRequested bool) (int64, bool) {
	var appliedEntitlementRevision int64
	if directEntitlementRequested {
		applied, applyErr := h.directEntitlements.ApplyDefaultAccountTemplate(r.Context(), requestAccountID(r), id, req.EntitlementTemplateKey, req.EntitlementTemplateRevision, false)
		if applyErr != nil {
			switch {
			case errors.Is(applyErr, entitlements.ErrTemplateNotFound), errors.Is(applyErr, entitlements.ErrTemplateUnavailable):
				writeError(w, http.StatusUnprocessableEntity, "entitlement_template_unavailable", "Entitlement template revision is unavailable")
			default:
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to apply entitlement template")
			}
			return 0, false
		}
		appliedEntitlementRevision = applied.TemplateRevision
	}
	return appliedEntitlementRevision, true
}

// bloemValidateUpdateUserEntitlement normalizes and validates the Bloem
// direct-entitlement fields on PUT /admin/users/{id}.
func (h *AdminHandler) bloemValidateUpdateUserEntitlement(w http.ResponseWriter, req *updateUserRequest) (bool, bool) {
	req.EntitlementTemplateKey = strings.TrimSpace(req.EntitlementTemplateKey)
	directEntitlementRequested := req.EntitlementTemplateKey != "" || req.EntitlementTemplateRevision != 0
	if directEntitlementRequested && (req.EntitlementTemplateKey == "" || req.EntitlementTemplateRevision <= 0) {
		writeError(w, http.StatusBadRequest, "bad_request", "entitlement_template_key and a positive entitlement_template_revision are required together")
		return false, false
	}
	if directEntitlementRequested && h.directEntitlements == nil {
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", "Entitlement templates are not configured")
		return false, false
	}
	return directEntitlementRequested, true
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
		if h.OnUserSessionsRevoked != nil {
			if err := h.OnUserSessionsRevoked(invalidationCtx, userID); err != nil {
				return err
			}
		}
		return nil
	})
}
