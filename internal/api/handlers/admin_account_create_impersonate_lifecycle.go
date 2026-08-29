package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/cache"
	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type lifecycleDefaultProfileCreator interface {
	CreateProfileInTransaction(context.Context, pgx.Tx, userstore.Profile) error
}

func (h *AdminHandler) handleLifecycleCreateUser(w http.ResponseWriter, r *http.Request, body []byte, req createUserRequest, input auth.CreateAccountInput, directEntitlement bool) {
	actorID, actorIncarnation, ok := lifecycleActor(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authenticated account identity is incomplete")
		return
	}
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &actorIncarnation, Method: r.Method, RouteID: "account.create",
			RequestHash:  h.lifecycleDigest(r.Method, "account.create", nil, r.URL.Query(), body),
			TargetSource: lifecycleidempotency.TargetBodyAccount,
		},
	}
	result, err := h.lifecycle.ExecuteCreate(r.Context(), request, func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, lifecycleidempotency.Result, error) {
		created, err := h.createLifecycleAccountInTransaction(ctx, tx, req.OrganizationID, input)
		if err != nil {
			return nil, lifecycleidempotency.Result{}, err
		}
		var appliedRevision int64
		if directEntitlement {
			writer, ok := h.directEntitlements.(transactionalDirectEntitlementProvisioner)
			if !ok {
				return nil, lifecycleidempotency.Result{}, errors.New("entitlement store does not support caller-owned transactions")
			}
			applied, err := writer.ApplyDefaultAccountTemplateInTransaction(ctx, tx, created.User.ID, req.EntitlementTemplateKey, req.EntitlementTemplateRevision, false)
			if err != nil {
				return nil, lifecycleidempotency.Result{}, err
			}
			created.User.AccessGroupID = &applied.GroupID
			appliedRevision = applied.TemplateRevision
		}
		policy, err := h.lifecycleGroupPolicy(ctx, tx, created.User)
		if err != nil {
			return nil, lifecycleidempotency.Result{}, err
		}
		response := toAdminUserResponse(created.User, policy)
		response.AppliedEntitlementRevision = appliedRevision
		encoded, err := lifecycleJSONResult(http.StatusCreated, response)
		return createdAccountLifecycleTargets(created), encoded, err
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "lifecycle account create failed", "component", "api", "error", err)
		switch {
		case auth.IsDuplicate(err):
			writeError(w, http.StatusConflict, "duplicate", "A user with that username or email already exists")
		case errors.Is(err, entitlements.ErrTemplateNotFound), errors.Is(err, entitlements.ErrTemplateUnavailable):
			writeError(w, http.StatusUnprocessableEntity, "entitlement_template_unavailable", "Entitlement template revision is unavailable")
		case errors.Is(err, tenancy.ErrTenantOrganizationNotFound):
			writeError(w, http.StatusUnprocessableEntity, "validation", "No such tenant")
		case errors.Is(err, tenancy.ErrTenantSlotsExhausted):
			writeError(w, http.StatusConflict, "tenant_slots_exhausted", "This tenant has no free account slots (or is frozen)")
		default:
			h.writeAccountResourceLifecycleError(w, err, "Failed to create user")
		}
		return
	}
	if !result.Replayed {
		h.invalidateStats(r.Context(), cache.ChannelAdmin, cache.EventAdminStatsInvalidated, "create")
	}
	writeLifecycleResult(w, result)
}

func (h *AdminHandler) createLifecycleAccountInTransaction(ctx context.Context, tx pgx.Tx, organizationID *uuid.UUID, input auth.CreateAccountInput) (auth.CreatedAccount, error) {
	if organizationID == nil {
		return h.accountProvisioner.CreateAccountInTransaction(ctx, tx, input)
	}
	if h.tenantStore == nil {
		return auth.CreatedAccount{}, tenancy.ErrTenantOrganizationNotFound
	}
	user, conflict, err := h.accountProvisioner.CreateUserInTransaction(ctx, tx, input.User)
	if err != nil {
		return auth.CreatedAccount{}, err
	}
	if conflict {
		return auth.CreatedAccount{}, auth.ErrDuplicate
	}
	membership, err := h.tenantStore.ProvisionTenantMembershipInTransaction(ctx, tx, *organizationID, user.ID, auth.MembershipLegacyRole(input.User.Role))
	if err != nil {
		return auth.CreatedAccount{}, err
	}
	created := auth.CreatedAccount{User: user, OrganizationID: *organizationID, MembershipID: membership.ID}
	if !input.DefaultProfile.Enabled {
		return created, nil
	}
	if h.storeProv == nil {
		return auth.CreatedAccount{}, errors.New("user store provider unavailable")
	}
	store, err := h.storeProv.ForUser(ctx, user.ID)
	if err != nil {
		return auth.CreatedAccount{}, err
	}
	profiles, ok := store.(lifecycleDefaultProfileCreator)
	if !ok {
		return auth.CreatedAccount{}, errors.New("user store does not support caller-owned profile creation")
	}
	name := strings.TrimSpace(input.DefaultProfile.Name)
	if name == "" {
		name = strings.TrimSpace(input.User.Username)
	}
	created.ProfileID = uuid.NewString()
	if err := profiles.CreateProfileInTransaction(ctx, tx, userstore.Profile{ID: created.ProfileID, Name: name, ShowForcedSubtitles: true}); err != nil {
		return auth.CreatedAccount{}, err
	}
	return created, nil
}

func (h *AdminHandler) handleLifecycleImpersonateUser(w http.ResponseWriter, r *http.Request, claims *auth.Claims, targetID int) {
	actorID, actorIncarnation, ok := lifecycleActor(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authenticated account identity is incomplete")
		return
	}
	service, ok := h.ImpersonationService.(transactionalImpersonationService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Impersonation request safety is temporarily unavailable")
		return
	}
	selector := strconv.Itoa(targetID)
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &actorIncarnation, Method: r.Method, RouteID: "account.impersonate",
			RequestHash:  h.lifecycleDigest(r.Method, "account.impersonate", map[string]string{"id": selector}, r.URL.Query(), nil),
			TargetSource: lifecycleidempotency.TargetPathAccount,
		},
		ResolveTargets: func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
			return lifecycleidempotency.ResolveAccountTargets(ctx, tx, targetID)
		},
	}
	result, err := h.lifecycle.Execute(auth.WithClaims(r.Context(), claims), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		pair, impersonator, target, err := service.StartImpersonationInTransaction(auth.WithClaims(ctx, claims), tx, claims.UserID, targetID, r.UserAgent(), clientip.FromContext(r.Context()))
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		policy, err := h.lifecycleGroupPolicy(ctx, tx, target)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		return lifecycleJSONResult(http.StatusOK, buildLoginResponse(pair, target, access.ApplyGroupPolicy(target, policy).DownloadAllowed, impersonator))
	})
	if err != nil {
		switch {
		case auth.IsNotFound(err), errors.Is(err, lifecycleidempotency.ErrTargetNotFound):
			writeError(w, http.StatusNotFound, "not_found", "User not found")
		case errors.Is(err, auth.ErrAlreadyImpersonating):
			writeError(w, http.StatusConflict, "already_impersonating", "An impersonation session is already active")
		case errors.Is(err, auth.ErrImpersonationNotAllowed):
			writeError(w, http.StatusForbidden, "impersonation_not_allowed", "Impersonation is not allowed")
		default:
			h.writeAccountResourceLifecycleError(w, err, "Failed to start impersonation")
		}
		return
	}
	writeLifecycleResult(w, result)
}

func (h *AdminHandler) lifecycleGroupPolicy(ctx context.Context, tx pgx.Tx, user *models.User) (*access.GroupPolicy, error) {
	if !access.GroupApplies(user) || h.AccessGroups == nil {
		return nil, nil
	}
	groups, ok := h.AccessGroups.(transactionalProfileAccessGroups)
	if !ok {
		return nil, errors.New("access groups do not support caller-owned transactions")
	}
	group, err := groups.GetForAccountInTransaction(ctx, tx, user.ID, *user.AccessGroupID)
	if err != nil {
		return nil, err
	}
	policy := group.Policy()
	return &policy, nil
}
