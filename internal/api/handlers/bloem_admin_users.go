package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
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

type adminUserResponse = AdminUserView

type effectivePolicyResp = EffectivePolicyView
