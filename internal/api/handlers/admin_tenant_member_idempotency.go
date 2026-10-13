package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiresponse"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/sessioninvalidation"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type tenantMemberTransactionalService interface {
	UpdateInTransaction(context.Context, pgx.Tx, uuid.UUID, int, tenancy.UpdateMemberInput) (models.User, error)
	SetSuspendedInTransaction(context.Context, pgx.Tx, uuid.UUID, int, bool) (models.User, error)
	ResetPasswordInTransaction(context.Context, pgx.Tx, uuid.UUID, int, string) (models.User, error)
	DeleteInTransaction(context.Context, pgx.Tx, uuid.UUID, int) error
	InvalidateCompatSessionsAfterCommit(context.Context, int, string) error
	CompleteDeleteAfterCommit(context.Context, uuid.UUID, int) error
}

type tenantMemberSessionTransactionalRepository interface {
	RevokeByUserAndSessionInTransaction(context.Context, pgx.Tx, int, string) error
	RevokeAllByUserAndProfilesInTransaction(context.Context, pgx.Tx, int, []string) error
}

var errTenantMemberAuthSessionNotFound = errors.New("tenant member authentication session not found")

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
	h.executeMemberLifecycle(w, r, tenantID, userID, "tenant.member.update", body, nil,
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
	h.executeMemberLifecycle(w, r, tenantID, userID, routeID, nil, nil,
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
	h.executeMemberLifecycle(w, r, tenantID, userID, "tenant.member.reset_password", body, nil,
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

func (h *AdminTenantMembersHandler) handleLifecycleDelete(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID, userID int) {
	service, ok := h.lifecycleService(w)
	if !ok {
		return
	}
	h.executeMemberLifecycle(w, r, tenantID, userID, "tenant.member.delete", nil, nil,
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			if err := service.DeleteInTransaction(ctx, tx, tenantID, userID); err != nil {
				return lifecycleidempotency.Result{}, err
			}
			return lifecycleidempotency.Result{Status: http.StatusNoContent}, nil
		}, func(ctx context.Context) error {
			return service.CompleteDeleteAfterCommit(ctx, tenantID, userID)
		})
}

func (h *AdminTenantMembersHandler) handleLifecycleRevokeAuthSession(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := h.memberScope(w, r)
	if !ok {
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Session ID is required")
		return
	}
	repository, ok := h.lifecycleSessionRepository(w)
	if !ok {
		return
	}
	var invalidatedProfileIDs []string
	h.executeMemberLifecycle(w, r, tenantID, userID, "tenant.member.session.delete", nil, map[string]string{"session_id": sessionID},
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			var sessionUserID int
			var profileID *string
			err := tx.QueryRow(ctx, `
SELECT user_id,profile_id FROM auth_sessions WHERE id=$1 FOR UPDATE`, sessionID).Scan(&sessionUserID, &profileID)
			if errors.Is(err, pgx.ErrNoRows) {
				return lifecycleidempotency.Result{Status: http.StatusNoContent}, nil
			}
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			if sessionUserID != userID || profileID == nil {
				return lifecycleidempotency.Result{}, errTenantMemberAuthSessionNotFound
			}
			var inOrganization bool
			if err := tx.QueryRow(ctx, `
SELECT EXISTS(SELECT 1 FROM user_profiles WHERE id=$1 AND user_id=$2 AND organization_id=$3)`,
				*profileID, userID, tenantID).Scan(&inOrganization); err != nil {
				return lifecycleidempotency.Result{}, err
			}
			if !inOrganization {
				return lifecycleidempotency.Result{}, errTenantMemberAuthSessionNotFound
			}
			if err := repository.RevokeByUserAndSessionInTransaction(ctx, tx, userID, sessionID); err != nil {
				return lifecycleidempotency.Result{}, err
			}
			invalidatedProfileIDs = []string{*profileID}
			return lifecycleidempotency.Result{Status: http.StatusNoContent}, nil
		}, func(ctx context.Context) error {
			return h.invalidateTenantMemberProfilesAfterCommit(ctx, userID, invalidatedProfileIDs)
		})
}

func (h *AdminTenantMembersHandler) handleLifecycleRevokeAllAuthSessions(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := h.memberScope(w, r)
	if !ok {
		return
	}
	repository, ok := h.lifecycleSessionRepository(w)
	if !ok {
		return
	}
	var invalidatedProfileIDs []string
	h.executeMemberLifecycle(w, r, tenantID, userID, "tenant.member.sessions.delete", nil, nil,
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			rows, err := tx.Query(ctx, `
SELECT id FROM user_profiles WHERE user_id=$1 AND organization_id=$2 ORDER BY id`, userID, tenantID)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var profileID string
				if err := rows.Scan(&profileID); err != nil {
					return lifecycleidempotency.Result{}, err
				}
				invalidatedProfileIDs = append(invalidatedProfileIDs, profileID)
			}
			if err := rows.Err(); err != nil {
				return lifecycleidempotency.Result{}, err
			}
			if err := repository.RevokeAllByUserAndProfilesInTransaction(ctx, tx, userID, invalidatedProfileIDs); err != nil {
				return lifecycleidempotency.Result{}, err
			}
			return lifecycleidempotency.Result{Status: http.StatusNoContent}, nil
		}, func(ctx context.Context) error {
			return h.invalidateTenantMemberProfilesAfterCommit(ctx, userID, invalidatedProfileIDs)
		})
}

func (h *AdminTenantMembersHandler) lifecycleSessionRepository(w http.ResponseWriter) (tenantMemberSessionTransactionalRepository, bool) {
	if h.adminUsers == nil || h.adminUsers.sessionRepo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Authentication sessions are not configured")
		return nil, false
	}
	repository, ok := h.adminUsers.sessionRepo.(tenantMemberSessionTransactionalRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
	}
	return repository, ok
}

func (h *AdminTenantMembersHandler) invalidateTenantMemberProfilesAfterCommit(ctx context.Context, userID int, profileIDs []string) error {
	if len(profileIDs) == 0 || h.adminUsers == nil || h.adminUsers.OnUserProfileSessionsRevoked == nil {
		return nil
	}
	return sessioninvalidation.Run(ctx, func(callbackCtx context.Context) error {
		return h.adminUsers.OnUserProfileSessionsRevoked(callbackCtx, userID, profileIDs)
	})
}

func (h *AdminTenantMembersHandler) executeMemberLifecycle(
	w http.ResponseWriter,
	r *http.Request,
	tenantID uuid.UUID,
	userID int,
	routeID string,
	body []byte,
	additionalSelectors map[string]string,
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
	selectors := map[string]string{"tenant_id": tenantSelector, "user_id": userSelector}
	for name, value := range additionalSelectors {
		selectors[name] = value
	}
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &actorIncarnation, Method: r.Method, RouteID: routeID,
			RequestHash:  h.digest(r.Method, routeID, selectors, r.URL.Query(), body),
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
	case errors.Is(err, tenancy.ErrMembershipPolicyWriteUnavailable):
		apiresponse.WriteRetryableUnavailable(w, "membership_policy_rollout_pending", "Membership policy rollout is not ready for this mutation", 1)
	case errors.Is(err, errTenantMemberAuthSessionNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Authentication session not found")
	default:
		h.writeError(w, err, "Failed to mutate tenant member")
	}
}
