package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func captureBloemLifecycleBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	return captureBloemLifecycleBodyLimit(w, r, adminPlatformBodyLimit)
}

func captureBloemLifecycleBodyLimit(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid administrative request")
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, true
}

func bloemLifecycleRequest(r *http.Request, claims auth.AdminContextClaims, digest lifecycleidempotency.RequestDigester, routeID string, source lifecycleidempotency.TargetSource, selectors map[string]string, body []byte) (lifecycleidempotency.Request, bool) {
	if claims.AccountID <= 0 || claims.AccountIncarnationID == uuid.Nil || digest == nil {
		return lifecycleidempotency.Request{}, false
	}
	actorID, incarnation := claims.AccountID, claims.AccountIncarnationID
	return lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &incarnation, Method: r.Method, RouteID: routeID,
			RequestHash:  digest(r.Method, routeID, selectors, r.URL.Query(), body),
			TargetSource: source,
		},
	}, true
}

func resolveBloemOrganizationOwnerTarget(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID) ([]lifecycleidempotency.TargetBinding, error) {
	var target lifecycleidempotency.TargetBinding
	err := tx.QueryRow(ctx, `SELECT o.id,m.id,u.id,u.account_incarnation_id FROM organizations o JOIN organization_memberships m ON m.organization_id=o.id AND m.account_id=o.owner_account_id JOIN users u ON u.id=m.account_id WHERE o.id=$1 FOR UPDATE OF o,m,u`, organizationID).Scan(&target.OrganizationID, &target.MembershipID, &target.AccountID, &target.AccountIncarnationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, lifecycleidempotency.ErrTargetNotFound
	}
	if err != nil {
		return nil, err
	}
	return []lifecycleidempotency.TargetBinding{target}, nil
}

func resolveBloemExactMembershipTarget(ctx context.Context, tx pgx.Tx, organizationID, membershipID uuid.UUID) ([]lifecycleidempotency.TargetBinding, error) {
	var target lifecycleidempotency.TargetBinding
	err := tx.QueryRow(ctx, `SELECT m.organization_id,m.id,u.id,u.account_incarnation_id FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE m.organization_id=$1 AND m.id=$2 FOR UPDATE OF m,u`, organizationID, membershipID).Scan(&target.OrganizationID, &target.MembershipID, &target.AccountID, &target.AccountIncarnationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, lifecycleidempotency.ErrTargetNotFound
	}
	if err != nil {
		return nil, err
	}
	return []lifecycleidempotency.TargetBinding{target}, nil
}

func resolveBloemAccountMembershipTarget(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int, profileID string) ([]lifecycleidempotency.TargetBinding, error) {
	var target lifecycleidempotency.TargetBinding
	err := tx.QueryRow(ctx, `SELECT m.organization_id,m.id,u.id,u.account_incarnation_id FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE m.organization_id=$1 AND m.account_id=$2 FOR UPDATE OF m,u`, organizationID, accountID).Scan(&target.OrganizationID, &target.MembershipID, &target.AccountID, &target.AccountIncarnationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, lifecycleidempotency.ErrTargetNotFound
	}
	if err != nil {
		return nil, err
	}
	if profileID != "" {
		if err := tx.QueryRow(ctx, `SELECT id FROM user_profiles WHERE organization_id=$1 AND user_id=$2 AND id=$3 FOR UPDATE`, organizationID, accountID, profileID).Scan(&target.ProfileID); errors.Is(err, pgx.ErrNoRows) {
			return nil, lifecycleidempotency.ErrTargetNotFound
		} else if err != nil {
			return nil, err
		}
	}
	return []lifecycleidempotency.TargetBinding{target}, nil
}

func writeBloemLifecycleResult(w http.ResponseWriter, result lifecycleidempotency.Result) {
	for key, values := range result.Headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	if result.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}

func writeBloemLifecycleError(w http.ResponseWriter, err error) bool {
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
	default:
		return false
	}
	return true
}
