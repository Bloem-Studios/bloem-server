package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type tenantMemberTransactionalService interface {
	UpdateInTransaction(context.Context, pgx.Tx, uuid.UUID, int, tenancy.UpdateMemberInput) (models.User, error)
	SetSuspendedInTransaction(context.Context, pgx.Tx, uuid.UUID, int, bool) (models.User, error)
	ResetPasswordInTransaction(context.Context, pgx.Tx, uuid.UUID, int, string) (models.User, error)
	InvalidateCompatSessionsAfterCommit(context.Context, int, string) error
}

func (h *AdminTenantMembersHandler) lifecycleService(w http.ResponseWriter) (tenantMemberTransactionalService, bool) {
	service, ok := h.members.(tenantMemberTransactionalService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
	}
	return service, ok
}

func (h *AdminTenantMembersHandler) handleLifecycleUpdate(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID, userID int, username, email *string, body []byte) {
	service, ok := h.lifecycleService(w)
	if !ok {
		return
	}
	h.executeMemberLifecycle(w, r, tenantID, userID, "tenant.member.update", body,
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			member, err := service.UpdateInTransaction(ctx, tx, tenantID, userID, tenancy.UpdateMemberInput{Username: username, Email: email})
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			return memberLifecycleJSONResult(http.StatusOK, toTenantMemberResponse(member))
		}, func(ctx context.Context) error {
			return service.InvalidateCompatSessionsAfterCommit(ctx, userID, "identity update")
		})
}

func (h *AdminTenantMembersHandler) handleLifecycleState(w http.ResponseWriter, r *http.Request, suspended bool) {
	tenantID, userID, ok := h.memberScope(w, r)
	if !ok {
		return
	}
	service, ok := h.lifecycleService(w)
	if !ok {
		return
	}
	routeID := "tenant.member.resume"
	if suspended {
		routeID = "tenant.member.suspend"
	}
	h.executeMemberLifecycle(w, r, tenantID, userID, routeID, nil,
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			member, err := service.SetSuspendedInTransaction(ctx, tx, tenantID, userID, suspended)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			return memberLifecycleJSONResult(http.StatusOK, toTenantMemberResponse(member))
		}, func(ctx context.Context) error {
			if !suspended {
				return nil
			}
			return service.InvalidateCompatSessionsAfterCommit(ctx, userID, "suspension")
		})
}

func (h *AdminTenantMembersHandler) handleLifecycleResetPassword(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID, userID int, password string, body []byte) {
	service, ok := h.lifecycleService(w)
	if !ok {
		return
	}
	h.executeMemberLifecycle(w, r, tenantID, userID, "tenant.member.reset_password", body,
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			member, err := service.ResetPasswordInTransaction(ctx, tx, tenantID, userID, password)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			return memberLifecycleJSONResult(http.StatusOK, toTenantMemberResponse(member))
		}, func(ctx context.Context) error {
			return service.InvalidateCompatSessionsAfterCommit(ctx, userID, "password reset")
		})
}

func (h *AdminTenantMembersHandler) executeMemberLifecycle(
	w http.ResponseWriter,
	r *http.Request,
	tenantID uuid.UUID,
	userID int,
	routeID string,
	body []byte,
	mutate lifecycleidempotency.Mutator,
	afterCommit func(context.Context) error,
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
	tenantSelector, userSelector := tenantID.String(), strconv.Itoa(userID)
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &actorIncarnation, Method: r.Method, RouteID: routeID,
			RequestHash: h.digest(r.Method, routeID, map[string]string{
				"tenant_id": tenantSelector, "user_id": userSelector,
			}, r.URL.Query(), body),
			TargetSource: lifecycleidempotency.TargetPathTenantMember,
		},
		ResolveTargets: func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
			target, err := lifecycleidempotency.ResolveTenantMemberTarget(ctx, tx, tenantID, userID)
			if err != nil {
				return nil, err
			}
			return []lifecycleidempotency.TargetBinding{target}, nil
		},
	}
	result, err := h.lifecycle.Execute(r.Context(), request, mutate)
	if err != nil {
		h.writeMemberLifecycleError(w, err)
		return
	}
	if !result.Replayed && afterCommit != nil {
		if err := afterCommit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Member mutation committed but compatibility-session invalidation failed")
			return
		}
	}
	writeLifecycleResult(w, result)
}

func memberLifecycleJSONResult(status int, value any) (lifecycleidempotency.Result, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return lifecycleidempotency.Result{}, err
	}
	body = append(body, '\n')
	return lifecycleidempotency.Result{
		Status: status, Body: body, Headers: map[string][]string{"Content-Type": {"application/json"}},
	}, nil
}

func writeLifecycleResult(w http.ResponseWriter, result lifecycleidempotency.Result) {
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

func (h *AdminTenantMembersHandler) writeMemberLifecycleError(w http.ResponseWriter, err error) {
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
	case errors.Is(err, lifecycleidempotency.ErrTargetNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such tenant member")
	default:
		h.writeError(w, err, "Failed to mutate tenant member")
	}
}
