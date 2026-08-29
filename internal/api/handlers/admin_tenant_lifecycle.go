package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type tenantAccountTransactionalDeleter interface {
	DeleteInTransaction(context.Context, pgx.Tx, int) error
}

func (h *AdminTenantsHandler) handleLifecycleCreate(w http.ResponseWriter, r *http.Request, input tenancy.CreateTenantOrganizationInput, body []byte) {
	request, ok := h.tenantLifecycleRequest(w, r, "tenant.create", nil, body, lifecycleidempotency.TargetBodyAccount)
	if !ok {
		return
	}
	result, err := h.lifecycle.ExecuteCreate(r.Context(), request,
		func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, lifecycleidempotency.Result, error) {
			tenant, err := h.store.CreateTenantOrganizationInTransaction(ctx, tx, input)
			if err != nil {
				return nil, lifecycleidempotency.Result{}, err
			}
			created, err := memberLifecycleJSONResult(http.StatusCreated, toTenantResponse(tenant))
			if err != nil {
				return nil, lifecycleidempotency.Result{}, err
			}
			// In the optional phase an unkeyed request writes no receipt and can
			// preserve the legacy empty-tenant create behavior.
			if strings.TrimSpace(request.IdempotencyKey) == "" {
				return nil, created, nil
			}
			targets, err := lifecycleidempotency.ResolveTenantOrganizationTargets(ctx, tx, tenant.ID)
			return targets, created, err
		})
	if err != nil {
		h.writeTenantLifecycleError(w, err)
		return
	}
	if !result.Replayed {
		h.store.CompleteTenantOrganizationMutationAfterCommit()
	}
	writeLifecycleResult(w, result)
}

func (h *AdminTenantsHandler) handleLifecycleFrozen(w http.ResponseWriter, r *http.Request, id uuid.UUID, frozen bool) {
	routeID := "tenant.thaw"
	if frozen {
		routeID = "tenant.freeze"
	}
	request, ok := h.tenantLifecycleRequest(w, r, routeID, map[string]string{"tenant_id": id.String()}, nil, lifecycleidempotency.TargetExactMembership)
	if !ok {
		return
	}
	request.ResolveTargets = func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
		return lifecycleidempotency.ResolveTenantOrganizationTargets(ctx, tx, id)
	}
	result, err := h.lifecycle.Execute(r.Context(), request,
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			_, err := h.store.SetTenantOrganizationFrozenInTransaction(ctx, tx, id, frozen)
			return lifecycleidempotency.Result{Status: http.StatusNoContent}, err
		})
	if err != nil {
		h.writeTenantLifecycleError(w, err)
		return
	}
	if !result.Replayed {
		h.store.CompleteTenantOrganizationMutationAfterCommit()
	}
	writeLifecycleResult(w, result)
}

func (h *AdminTenantsHandler) handleLifecycleLimits(w http.ResponseWriter, r *http.Request, id uuid.UUID, slots, transcodes int, body []byte) {
	request, ok := h.tenantLifecycleRequest(w, r, "tenant.limits.update", map[string]string{"tenant_id": id.String()}, body, lifecycleidempotency.TargetExactMembership)
	if !ok {
		return
	}
	request.ResolveTargets = func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
		return lifecycleidempotency.ResolveTenantOrganizationTargets(ctx, tx, id)
	}
	result, err := h.lifecycle.Execute(r.Context(), request,
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			tenant, err := h.store.UpdateTenantOrganizationLimitsInTransaction(ctx, tx, id, slots, transcodes)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			return memberLifecycleJSONResult(http.StatusOK, toTenantResponse(tenant))
		})
	if err != nil {
		h.writeTenantLifecycleError(w, err)
		return
	}
	if !result.Replayed {
		h.store.CompleteTenantOrganizationMutationAfterCommit()
	}
	writeLifecycleResult(w, result)
}

func (h *AdminTenantsHandler) handleLifecycleDelete(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	deleter, ok := h.userRepo.(tenantAccountTransactionalDeleter)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	request, requestOK := h.tenantLifecycleRequest(w, r, "tenant.delete", map[string]string{"tenant_id": id.String()}, nil, lifecycleidempotency.TargetExactMembership)
	if !requestOK {
		return
	}
	request.ResolveTargets = func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
		return lifecycleidempotency.ResolveTenantOrganizationTargets(ctx, tx, id)
	}
	result, err := h.lifecycle.Execute(r.Context(), request,
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			memberIDs, err := h.store.TenantMemberAccountIDsInTransaction(ctx, tx, id)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			if err := h.store.DeleteTenantOrganizationInTransaction(ctx, tx, id); err != nil {
				return lifecycleidempotency.Result{}, err
			}
			for _, accountID := range memberIDs {
				if err := deleter.DeleteInTransaction(ctx, tx, accountID); err != nil {
					return lifecycleidempotency.Result{}, err
				}
			}
			return lifecycleidempotency.Result{Status: http.StatusNoContent}, nil
		})
	if err != nil {
		h.writeTenantLifecycleError(w, err)
		return
	}
	if !result.Replayed {
		h.store.CompleteTenantOrganizationMutationAfterCommit()
	}
	writeLifecycleResult(w, result)
}

func (h *AdminTenantsHandler) tenantLifecycleRequest(w http.ResponseWriter, r *http.Request, routeID string, selectors map[string]string, body []byte, source lifecycleidempotency.TargetSource) (lifecycleidempotency.Request, bool) {
	claims := apimw.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return lifecycleidempotency.Request{}, false
	}
	incarnation, err := uuid.Parse(claims.AccountIncarnationID)
	if err != nil || incarnation == uuid.Nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authenticated account identity is incomplete")
		return lifecycleidempotency.Request{}, false
	}
	actorID := claims.UserID
	return lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &incarnation, Method: r.Method, RouteID: routeID,
			RequestHash: h.digest(r.Method, routeID, selectors, r.URL.Query(), body), TargetSource: source,
		},
	}, true
}

func (h *AdminTenantsHandler) writeTenantLifecycleError(w http.ResponseWriter, err error) {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, lifecycleidempotency.ErrKeyRequired):
		writeError(w, http.StatusPreconditionRequired, "idempotency_key_required", "Idempotency-Key is required for this lifecycle mutation")
	case errors.Is(err, lifecycleidempotency.ErrKeyMalformed):
		writeError(w, http.StatusBadRequest, "idempotency_key_invalid", "Idempotency-Key must be a bounded opaque ASCII value")
	case errors.Is(err, lifecycleidempotency.ErrConflict):
		writeError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key conflicts with its original lifecycle request")
	case errors.Is(err, lifecycleidempotency.ErrPending):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "lifecycle_request_pending", "Lifecycle request completion is pending")
	case errors.Is(err, lifecycleidempotency.ErrInvalidBinding):
		writeError(w, http.StatusUnauthorized, "unauthorized", "Lifecycle request identity is no longer valid")
	case errors.Is(err, lifecycleidempotency.ErrTargetUnavailable):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "lifecycle_target_unavailable", "Tenant lifecycle safety is temporarily unavailable")
	case errors.Is(err, lifecycleidempotency.ErrTargetNotFound), errors.Is(err, tenancy.ErrTenantOrganizationNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such tenant")
	case errors.Is(err, tenancy.ErrTenantOrganizationInvalid):
		writeError(w, http.StatusUnprocessableEntity, "validation", "A tenant needs a name, an external service reference, and at least one slot")
	case errors.As(err, &pgErr) && pgErr.Code == "P0001" && pgErr.Message == "membership_policy_fenced":
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "membership_policy_fenced", "Tenant mutation is temporarily unavailable during membership policy migration")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to mutate tenant")
	}
}
