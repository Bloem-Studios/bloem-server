package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/cache"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/sessioninvalidation"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type DirectEntitlementProvisioner interface {
	ApplyDefaultAccountTemplate(context.Context, int, string, int64, bool) (entitlements.ApplyResult, error)
}

type transactionalDirectEntitlementProvisioner interface {
	ApplyDefaultAccountTemplateInTransaction(context.Context, pgx.Tx, int, string, int64, bool) (entitlements.ApplyResult, error)
}

type transactionalAdminUserRepository interface {
	GetByIDInTransaction(context.Context, pgx.Tx, int) (*models.User, error)
}

type transactionalAccessGroupValidator interface {
	GetInTransaction(context.Context, pgx.Tx, uuid.UUID, int64) (*access.Group, error)
}

// AccountPolicyReader exposes only authoritative Server-side policy state.
// Callers cannot supply an expected template or cohort identity through this
// boundary.
type AccountPolicyReader interface {
	GetAccountPolicy(context.Context, uuid.UUID, int) (entitlements.AccountPolicySnapshot, error)
	GetAccountPolicies(context.Context, uuid.UUID, []int) ([]entitlements.AccountPolicySnapshotResult, time.Time, error)
}

type transactionalImpersonationService interface {
	StartImpersonationInTransaction(context.Context, pgx.Tx, int, int, string, string) (*auth.TokenPair, *models.User, *models.User, error)
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

// SetMembershipProvisioner configures default-organization provisioning for
// accounts created by the admin API.
func (h *AdminHandler) SetMembershipProvisioner(provisioner auth.MembershipProvisioner) {
	h.accountProvisioner.SetMembershipProvisioner(provisioner)
}

// SetLifecycleIdempotency installs the durable receipt coordinator used by
// lifecycle mutation handlers. Both dependencies are required together.
func (h *AdminHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, digester lifecycleidempotency.RequestDigester) {
	h.lifecycle = coordinator
	h.lifecycleDigest = digester
}

// createTenantUser creates an account inside a specific tenant organization
// (bloem-park growth G2) rather than the deployment's default one —
// AccountProvisioner.CreateAccount always provisions the default
// organization, so this replicates its steps against tenancy.Store's
// tenant-specific ones instead, with the same cleanup-on-failure shape.
//
// Creating an account is a multi-step application operation that cannot run
// inside a tenancy transaction, so the slot quota is checked before AND
// recounted after: two racing creates both pass the pre-check at the
// boundary, and the recount removes the account that broke the quota — the
// breach self-heals instead of standing.
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

	user, err := h.userRepo.Create(ctx, input.User)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create user")
		return nil
	}

	legacyRole := auth.MembershipLegacyRole(input.User.Role)
	if _, err := h.tenantStore.ProvisionTenantMembership(ctx, organizationID, user.ID, legacyRole); err != nil {
		if deleteErr := h.userRepo.Delete(ctx, user.ID); deleteErr != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to enforce tenant capacity")
			return nil
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to provision tenant membership")
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

var errLifecycleUpdateInsufficientScope = errors.New("lifecycle account update insufficient scope")

var errLifecycleUpdateGroupedAdmin = errors.New("lifecycle account update grouped admin")

var errLifecycleUpdateTenantUnavailable = errors.New("lifecycle account update tenant unavailable")

var errLifecycleUpdateAccessUnavailable = errors.New("lifecycle account update access groups unavailable")

func (h *AdminHandler) handleLifecycleUpdateUser(
	w http.ResponseWriter,
	r *http.Request,
	id int,
	selector string,
	body []byte,
	req updateUserRequest,
	updateInput models.UpdateUserInput,
	directEntitlementRequested bool,
) {
	claims := apimw.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	actorIncarnation, err := uuid.Parse(claims.AccountIncarnationID)
	if err != nil || actorIncarnation == uuid.Nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authenticated account identity is incomplete")
		return
	}
	actorID := claims.UserID
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &actorIncarnation, Method: r.Method, RouteID: "account.update",
			RequestHash:  h.lifecycleDigest(r.Method, "account.update", map[string]string{"id": selector}, r.URL.Query(), body),
			TargetSource: lifecycleidempotency.TargetPathAccount,
		},
		ResolveTargets: func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
			if directEntitlementRequested {
				rows, err := tx.Query(ctx, `
					SELECT organizations.id
					FROM organizations
					JOIN organization_memberships ON organization_memberships.organization_id=organizations.id
					WHERE organization_memberships.account_id=$1
					ORDER BY organizations.id
					FOR UPDATE OF organizations`, id)
				if err != nil {
					return nil, fmt.Errorf("lock account organizations: %w", err)
				}
				for rows.Next() {
					var organizationID uuid.UUID
					if err := rows.Scan(&organizationID); err != nil {
						rows.Close()
						return nil, err
					}
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					return nil, err
				}
				rows.Close()
			}
			return lifecycleidempotency.ResolveAccountTargets(ctx, tx, id)
		},
	}
	var revokeSessions bool
	result, err := h.lifecycle.Execute(r.Context(), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		users, ok := h.userRepo.(transactionalAdminUserRepository)
		if !ok {
			return lifecycleidempotency.Result{}, errors.New("account repository does not support caller-owned transactions")
		}
		current, err := users.GetByIDInTransaction(ctx, tx, id)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		if len(claims.APIKeyScopes) > 0 {
			if req.Role != nil && *req.Role == roleAdmin {
				return lifecycleidempotency.Result{}, errLifecycleUpdateInsufficientScope
			}
			if (req.Password != nil || req.Role != nil) && current.Role == roleAdmin {
				return lifecycleidempotency.Result{}, errLifecycleUpdateInsufficientScope
			}
		}
		if req.AccessGroupID.Set && req.AccessGroupID.Value != nil {
			resultingRole := current.Role
			if req.Role != nil {
				resultingRole = *req.Role
			}
			if resultingRole == roleAdmin {
				return lifecycleidempotency.Result{}, errLifecycleUpdateGroupedAdmin
			}
			if h.AccessGroups == nil {
				return lifecycleidempotency.Result{}, errLifecycleUpdateAccessUnavailable
			}
			tenant, exists := tenancy.FromContext(ctx)
			if !exists || tenant.OrganizationID == uuid.Nil {
				return lifecycleidempotency.Result{}, errLifecycleUpdateTenantUnavailable
			}
			groups, ok := h.AccessGroups.(transactionalAccessGroupValidator)
			if !ok {
				return lifecycleidempotency.Result{}, errLifecycleUpdateAccessUnavailable
			}
			if _, err := groups.GetInTransaction(ctx, tx, tenant.OrganizationID, *req.AccessGroupID.Value); err != nil {
				return lifecycleidempotency.Result{}, err
			}
		}
		revokeSessions = updateRequiresSessionRevocation(current, updateInput)
		if _, err := h.accountProvisioner.UpdateUserInTransaction(ctx, tx, id, updateInput); err != nil {
			return lifecycleidempotency.Result{}, err
		}
		var appliedEntitlementRevision int64
		if directEntitlementRequested {
			entitlementWriter, ok := h.directEntitlements.(transactionalDirectEntitlementProvisioner)
			if !ok {
				return lifecycleidempotency.Result{}, errors.New("entitlement store does not support caller-owned transactions")
			}
			applied, err := entitlementWriter.ApplyDefaultAccountTemplateInTransaction(ctx, tx, id, req.EntitlementTemplateKey, req.EntitlementTemplateRevision, false)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			appliedEntitlementRevision = applied.TemplateRevision
		}
		if revokeSessions {
			if h.pool == nil {
				return lifecycleidempotency.Result{}, errors.New("session repository does not support caller-owned transactions")
			}
			sessions := auth.NewSessionRepository(h.pool)
			if err := sessions.RevokeAllByUserInTransaction(ctx, tx, id); err != nil {
				return lifecycleidempotency.Result{}, err
			}
			if err := sessions.RevokeAllByImpersonatorInTransaction(ctx, tx, id); err != nil {
				return lifecycleidempotency.Result{}, err
			}
		}
		updated, err := users.GetByIDInTransaction(ctx, tx, id)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		var groupPolicy *access.GroupPolicy
		if access.GroupApplies(updated) && h.AccessGroups != nil {
			if tenant, exists := tenancy.FromContext(ctx); exists {
				groups, ok := h.AccessGroups.(transactionalAccessGroupValidator)
				if !ok {
					return lifecycleidempotency.Result{}, errLifecycleUpdateAccessUnavailable
				}
				group, err := groups.GetInTransaction(ctx, tx, tenant.OrganizationID, *updated.AccessGroupID)
				if err != nil && !errors.Is(err, access.ErrGroupNotFound) {
					return lifecycleidempotency.Result{}, err
				}
				if group != nil {
					policy := group.Policy()
					groupPolicy = &policy
				}
			}
		}
		response := toAdminUserResponse(updated, groupPolicy)
		response.AppliedEntitlementRevision = appliedEntitlementRevision
		payload, err := json.Marshal(response)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		return lifecycleidempotency.Result{Status: http.StatusOK, Body: payload, Headers: map[string][]string{"Content-Type": {"application/json"}}}, nil
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "lifecycle account update failed", "component", "api", "user_id", id, "error", err)
		switch {
		case errors.Is(err, errLifecycleUpdateInsufficientScope):
			writeError(w, http.StatusForbidden, "insufficient_scope", "A scoped API key may not perform this account update")
		case errors.Is(err, errLifecycleUpdateGroupedAdmin):
			writeError(w, http.StatusUnprocessableEntity, "unprocessable_entity", "Admin accounts cannot belong to an access group")
		case errors.Is(err, errLifecycleUpdateTenantUnavailable):
			writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		case errors.Is(err, errLifecycleUpdateAccessUnavailable):
			writeError(w, http.StatusInternalServerError, "internal_error", "Access groups are not configured")
		case errors.Is(err, access.ErrGroupNotFound):
			writeError(w, http.StatusUnprocessableEntity, "unprocessable_entity", "Invalid access_group_id")
		case errors.Is(err, entitlements.ErrTemplateNotFound), errors.Is(err, entitlements.ErrTemplateUnavailable):
			writeError(w, http.StatusUnprocessableEntity, "entitlement_template_unavailable", "Entitlement template revision is unavailable")
		case errors.Is(err, lifecycleidempotency.ErrKeyRequired):
			writeError(w, http.StatusPreconditionRequired, "idempotency_key_required", "Idempotency-Key is required for this lifecycle mutation")
		case errors.Is(err, lifecycleidempotency.ErrKeyMalformed):
			writeError(w, http.StatusBadRequest, "idempotency_key_invalid", "Idempotency-Key must be a bounded opaque ASCII value")
		case errors.Is(err, lifecycleidempotency.ErrConflict):
			writeError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key conflicts with its original lifecycle request")
		case errors.Is(err, lifecycleidempotency.ErrTargetNotFound), auth.IsNotFound(err):
			writeError(w, http.StatusNotFound, "not_found", "User not found")
		case errors.Is(err, lifecycleidempotency.ErrPending):
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusServiceUnavailable, "lifecycle_request_pending", "Lifecycle request completion is pending")
		case errors.Is(err, lifecycleidempotency.ErrInvalidBinding):
			writeError(w, http.StatusUnauthorized, "unauthorized", "Lifecycle request identity is no longer valid")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update user")
		}
		return
	}
	if !result.Replayed && revokeSessions {
		if h.OnUserSessionsRevoked != nil {
			err := sessioninvalidation.Run(r.Context(), func(callbackCtx context.Context) error {
				return h.OnUserSessionsRevoked(callbackCtx, id)
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to revoke updated user sessions")
				return
			}
		}
	}
	for key, values := range result.Headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}

func (h *AdminHandler) handleLifecycleDeleteUser(w http.ResponseWriter, r *http.Request, id int, selector string) {
	claims := apimw.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	actorIncarnation, err := uuid.Parse(claims.AccountIncarnationID)
	if err != nil || actorIncarnation == uuid.Nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authenticated account identity is incomplete")
		return
	}
	actorID := claims.UserID
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &actorIncarnation, Method: r.Method, RouteID: "account.delete",
			RequestHash:  h.lifecycleDigest(r.Method, "account.delete", map[string]string{"id": selector}, r.URL.Query(), nil),
			TargetSource: lifecycleidempotency.TargetPathAccount,
		},
		ResolveTargets: func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
			return lifecycleidempotency.ResolveAccountTargets(ctx, tx, id)
		},
	}
	result, err := h.lifecycle.Execute(r.Context(), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		if err := h.accountProvisioner.DeleteUserInTransaction(ctx, tx, id); err != nil {
			return lifecycleidempotency.Result{}, err
		}
		return lifecycleidempotency.Result{Status: http.StatusNoContent}, nil
	})
	if err != nil {
		h.writeLifecycleMutationError(w, err)
		return
	}
	if !result.Replayed {
		if err := h.revokeUserSessions(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to revoke deleted user sessions")
			return
		}
		h.invalidateStats(r.Context(), cache.ChannelAdmin, cache.EventAdminStatsInvalidated, strconv.Itoa(id))
	}
	for key, values := range result.Headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := result.Status
	if status == 0 {
		status = http.StatusNoContent
	}
	w.WriteHeader(status)
	if len(result.Body) > 0 {
		_, _ = w.Write(result.Body)
	}
}

func (h *AdminHandler) writeLifecycleMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lifecycleidempotency.ErrKeyRequired):
		writeError(w, http.StatusPreconditionRequired, "idempotency_key_required", "Idempotency-Key is required for this lifecycle mutation")
	case errors.Is(err, lifecycleidempotency.ErrKeyMalformed):
		writeError(w, http.StatusBadRequest, "idempotency_key_invalid", "Idempotency-Key must be a bounded opaque ASCII value")
	case errors.Is(err, lifecycleidempotency.ErrConflict):
		writeError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key conflicts with its original lifecycle request")
	case errors.Is(err, lifecycleidempotency.ErrTargetNotFound), auth.IsNotFound(err):
		writeError(w, http.StatusNotFound, "not_found", "User not found")
	case errors.Is(err, lifecycleidempotency.ErrPending):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "lifecycle_request_pending", "Lifecycle request completion is pending")
	case errors.Is(err, lifecycleidempotency.ErrInvalidBinding):
		writeError(w, http.StatusUnauthorized, "unauthorized", "Lifecycle request identity is no longer valid")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to delete user")
	}
}

// bloemAdminHandlerExt holds every Bloem-only AdminHandler dependency so the
// Silo-owned struct carries a single embedded line.
type bloemAdminHandlerExt struct {
	sessionRepo     adminUserSessionRepository
	profileHandler  *ProfileHandler
	lifecycle       lifecycleidempotency.Coordinator
	lifecycleDigest lifecycleidempotency.RequestDigester
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

// readBloemRequestBody reads the whole admin request body so lifecycle receipts
// can digest it. A read failure answers the handler's usual 400.
func readBloemRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return nil, false
	}
	return body, true
}

// bufferBloemRequestBody reads the whole body like readBloemRequestBody and puts
// it back on the request, so the Silo decoder below it reads the same bytes.
func bufferBloemRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, ok := readBloemRequestBody(w, r)
	if !ok {
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, true
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

// bloemCreateUser performs the Bloem account creation paths (lifecycle
// receipts, tenant organizations, direct entitlement templates). It returns
// ok=false once it has written the response itself.
func (h *AdminHandler) bloemCreateUser(w http.ResponseWriter, r *http.Request, body []byte, req createUserRequest, accountInput auth.CreateAccountInput, directEntitlementRequested bool) (*models.User, int64, bool) {
	var err error
	if h.lifecycle != nil && h.lifecycleDigest != nil {
		h.handleLifecycleCreateUser(w, r, body, req, accountInput, directEntitlementRequested)
		return nil, 0, false
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return nil, 0, false
	}

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
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create user")
			return nil, 0, false
		}
	} else {
		defaultProfile := accountInput.DefaultProfile
		accountInput.DefaultProfile.Enabled = false
		user, err = h.accountProvisioner.CreateAccount(r.Context(), accountInput)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create user")
			return nil, 0, false
		}
		applied, applyErr := h.directEntitlements.ApplyDefaultAccountTemplate(
			r.Context(), user.ID, req.EntitlementTemplateKey, req.EntitlementTemplateRevision, false,
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

// bloemLifecycleUpdateUser dispatches PUT /admin/users/{id} to the durable
// lifecycle receipt path when it is wired, and refuses an Idempotency-Key the
// server cannot honor. It reports whether it wrote the response.
func (h *AdminHandler) bloemLifecycleUpdateUser(w http.ResponseWriter, r *http.Request, id int, selector string, body []byte, req updateUserRequest, updateInput models.UpdateUserInput, directEntitlementRequested bool) bool {
	if h.lifecycle != nil && h.lifecycleDigest != nil {
		h.handleLifecycleUpdateUser(w, r, id, selector, body, req, updateInput, directEntitlementRequested)
		return true
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return true
	}
	return false
}

// bloemPrepareUpdateUser runs the scoped-API-key, grouped-admin and
// tenant-scoped access-group checks for the non-lifecycle update path.
func (h *AdminHandler) bloemPrepareUpdateUser(w http.ResponseWriter, r *http.Request, id int, req *updateUserRequest) (*models.User, bool) {
	currentUser, blocked := h.rejectScopedAPIKeyUpdate(w, r, id, req)
	if blocked {
		return currentUser, true
	}
	if req.AccessGroupID.Set {
		currentUser, blocked = h.rejectGroupedAdmin(w, r, id, req, currentUser)
		if blocked {
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

// bloemApplyUpdateUserEntitlement applies a requested direct entitlement
// template after the account update. ok=false means it wrote the response.
func (h *AdminHandler) bloemApplyUpdateUserEntitlement(w http.ResponseWriter, r *http.Request, id int, req updateUserRequest, directEntitlementRequested bool) (int64, bool) {
	var appliedEntitlementRevision int64
	if directEntitlementRequested {
		applied, applyErr := h.directEntitlements.ApplyDefaultAccountTemplate(r.Context(), id, req.EntitlementTemplateKey, req.EntitlementTemplateRevision, false)
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

// bloemLifecycleDeleteUser dispatches DELETE /admin/users/{id} to the
// lifecycle receipt path. It reports whether it wrote the response.
func (h *AdminHandler) bloemLifecycleDeleteUser(w http.ResponseWriter, r *http.Request, id int, selector string) bool {
	if h.lifecycle != nil && h.lifecycleDigest != nil {
		h.handleLifecycleDeleteUser(w, r, id, selector)
		return true
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return true
	}
	return false
}

// bloemLifecycleImpersonateUser dispatches POST /admin/users/{id}/impersonate
// to the lifecycle receipt path. It reports whether it wrote the response.
func (h *AdminHandler) bloemLifecycleImpersonateUser(w http.ResponseWriter, r *http.Request, claims *auth.Claims, targetID int) bool {
	if h.lifecycle != nil && h.lifecycleDigest != nil {
		h.handleLifecycleImpersonateUser(w, r, claims, targetID)
		return true
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return true
	}
	return false
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
	if directEntitlementRequested && h.directEntitlements == nil && (h.lifecycle == nil || h.lifecycleDigest == nil) {
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
