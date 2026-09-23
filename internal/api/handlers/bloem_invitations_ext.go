package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/jackc/pgx/v5"
)

const invitationAcceptRouteID = "invitation.accept"

type invitationServerIdentity interface {
	Resolve(context.Context) (string, error)
}

// SetLifecycleIdempotency installs durable replay safety for the public
// invitation redemption mutation.
func (h *InvitationHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, digester lifecycleidempotency.RequestDigester, identity invitationServerIdentity, secret []byte) {
	h.lifecycle = coordinator
	h.lifecycleDigest = digester
	h.serverIdentity = identity
	h.lifecycleSecret = append([]byte(nil), secret...)
}

func (h *InvitationHandler) handleLifecycleAcceptInvitation(w http.ResponseWriter, r *http.Request, body []byte, password string) {
	tokenHash := invitations.HashToken(chi.URLParam(r, "token"))
	serverID, err := h.serverIdentity.Resolve(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind:          lifecycleidempotency.ActorPreauthIntent,
			ActorSubjectDigest: invitationAcceptSubjectDigest(h.lifecycleSecret, serverID, tokenHash),
			Method:             r.Method,
			RouteID:            invitationAcceptRouteID,
			RequestHash:        h.lifecycleDigest(r.Method, invitationAcceptRouteID, map[string]string{"token_digest": tokenHash}, r.URL.Query(), body),
			TargetSource:       lifecycleidempotency.TargetBodyAccount,
		},
	}
	result, err := h.lifecycle.ExecuteCreate(r.Context(), request, func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, lifecycleidempotency.Result, error) {
		pair, created, err := h.service.AcceptInTransaction(ctx, tx, tokenHash, password, r.UserAgent(), clientip.FromContext(r.Context()))
		if err != nil {
			return nil, lifecycleidempotency.Result{}, err
		}
		response, err := json.Marshal(buildLoginResponse(pair, created.User, effectiveDownloadAllowed(ctx, created.User, h.accessGroups), nil))
		if err != nil {
			return nil, lifecycleidempotency.Result{}, err
		}
		target := lifecycleidempotency.TargetBinding{
			OrganizationID: created.OrganizationID, MembershipID: created.MembershipID,
			AccountID: created.User.ID, AccountIncarnationID: created.User.AccountIncarnationID,
			ProfileID: created.ProfileID,
		}
		return []lifecycleidempotency.TargetBinding{target}, lifecycleidempotency.Result{
			Status: http.StatusCreated, Body: response, Headers: map[string][]string{"Content-Type": {"application/json"}},
		}, nil
	})
	if err != nil {
		if writeBloemLifecycleError(w, err) {
			return
		}
		switch {
		case errors.Is(err, invitations.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "This invitation is invalid or has expired")
		case errors.Is(err, invitations.ErrNotClaimable):
			writeError(w, http.StatusConflict, "already_used", "This invitation has already been used")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
		}
		return
	}
	writeBloemLifecycleResult(w, result)
}

func invitationAcceptSubjectDigest(secret []byte, serverID, tokenHash string) lifecycleidempotency.Digest {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("bloem.lifecycle-preauth.invitation.v1\x00"))
	writeInvitationDigestPart(mac, serverID)
	writeInvitationDigestPart(mac, tokenHash)
	var digest lifecycleidempotency.Digest
	copy(digest[:], mac.Sum(nil))
	return digest
}

func writeInvitationDigestPart(mac interface{ Write([]byte) (int, error) }, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = mac.Write(length[:])
	_, _ = mac.Write([]byte(value))
}
