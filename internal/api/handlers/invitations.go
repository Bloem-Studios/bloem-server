package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/jackc/pgx/v5"
)

const invitationAcceptRouteID = "invitation.accept"

type invitationServerIdentity interface {
	Resolve(context.Context) (string, error)
}

// InvitationHandler handles the public (unauthenticated) claim endpoints.
type InvitationHandler struct {
	service         *invitations.Service
	accessGroups    access.GroupPolicyProvider
	lifecycle       lifecycleidempotency.Coordinator
	lifecycleDigest lifecycleidempotency.RequestDigester
	serverIdentity  invitationServerIdentity
	lifecycleSecret []byte
}

// SetLifecycleIdempotency installs durable replay safety for the public
// invitation redemption mutation.
func (h *InvitationHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, digester lifecycleidempotency.RequestDigester, identity invitationServerIdentity, secret []byte) {
	h.lifecycle = coordinator
	h.lifecycleDigest = digester
	h.serverIdentity = identity
	h.lifecycleSecret = append([]byte(nil), secret...)
}

// NewInvitationHandler creates a new InvitationHandler.
func NewInvitationHandler(service *invitations.Service) *InvitationHandler {
	return &InvitationHandler{service: service}
}

// SetAccessGroupProvider wires the access-group policy source used to resolve
// the effective policy reported on the accept-invitation login response.
func (h *InvitationHandler) SetAccessGroupProvider(provider access.GroupPolicyProvider) {
	h.accessGroups = provider
}

type invitationLookupResponse struct {
	Email       string    `json:"email"`
	InviterName string    `json:"inviter_name,omitempty"`
	ServerName  string    `json:"server_name"`
	ExpiresAt   time.Time `json:"expires_at"`
	ShowTour    bool      `json:"show_tour"`
}

type acceptInvitationRequest struct {
	Password string `json:"password"`
}

// HandleLookupInvitation handles GET /invitations/{token}. Unknown, expired,
// revoked, and accepted tokens return an identical 404 so a probe learns
// nothing about which.
func (h *InvitationHandler) HandleLookupInvitation(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Lookup(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "This invitation is invalid or has expired")
		return
	}
	writeJSON(w, http.StatusOK, invitationLookupResponse{
		Email:       result.Email,
		InviterName: result.InviterName,
		ServerName:  result.ServerName,
		ExpiresAt:   result.ExpiresAt,
		ShowTour:    result.ShowTour,
	})
}

// HandleAcceptInvitation handles POST /invitations/{token}/accept. On
// success it returns the same login response shape as signup, so clients
// reuse their existing session plumbing.
func (h *InvitationHandler) HandleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, adminPlatformBodyLimit))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var req acceptInvitationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "weak_password", "Password must be at least 8 characters")
		return
	}
	if h.lifecycle != nil && h.lifecycleDigest != nil && h.serverIdentity != nil && len(h.lifecycleSecret) > 0 {
		h.handleLifecycleAcceptInvitation(w, r, body, req.Password)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	pair, user, err := h.service.Accept(
		r.Context(),
		chi.URLParam(r, "token"),
		req.Password,
		r.UserAgent(),
		clientip.FromContext(r.Context()),
	)
	if err != nil {
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

	writeJSON(w, http.StatusCreated, buildLoginResponse(pair, user, effectiveDownloadAllowed(r.Context(), user, h.accessGroups), nil))
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
