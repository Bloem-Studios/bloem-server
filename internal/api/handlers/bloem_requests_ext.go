package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
)

func (h *RequestsHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, digester lifecycleidempotency.RequestDigester) {
	h.lifecycle = coordinator
	h.lifecycleDigest = digester
}

// discoverSectionsResponse is the GET /requests/discover envelope. It was an
// inline struct literal, which had no nameable type for the client DTO
// registry (contracts/client/v1/registry.json).
type discoverSectionsResponse struct {
	Sections []mediarequests.DiscoverySection `json:"sections"`
}

// discoverStudiosResponse is the GET /requests/discover/studios envelope. It
// was an inline struct literal, which had no nameable type for the client DTO
// registry (contracts/client/v1/registry.json).
type discoverStudiosResponse struct {
	Studios []mediarequests.DiscoverBrandCard `json:"studios"`
}

// discoverNetworksResponse is the GET /requests/discover/networks envelope.
// It was an inline struct literal, which had no nameable type for the client
// DTO registry (contracts/client/v1/registry.json).
type discoverNetworksResponse struct {
	Networks []mediarequests.DiscoverBrandCard `json:"networks"`
}

// discoverGenresResponse is the GET /requests/discover/genres envelope. It
// was an inline struct literal, which had no nameable type for the client DTO
// registry (contracts/client/v1/registry.json).
type discoverGenresResponse struct {
	Genres []mediarequests.DiscoverBrandCard `json:"genres"`
}

// requestListResponse is the GET /requests/mine envelope (the admin list
// endpoint writes the same shape). It was an inline struct literal, which had
// no nameable type for the client DTO registry
// (contracts/client/v1/registry.json).
type requestListResponse struct {
	Requests []*mediarequests.Request `json:"requests"`
}

// requestReasonRequest is the body of POST /requests/{id}/cancel and of the
// admin decline endpoint. Both handlers wrote it as an inline struct literal,
// which had no nameable type for the client DTO registry
// (contracts/client/v1/registry.json).
type requestReasonRequest struct {
	Reason string `json:"reason"`
}

type transactionalRequestLimitService interface {
	UpsertUserLimitInTransaction(context.Context, pgx.Tx, mediarequests.Viewer, mediarequests.UserLimit) (*mediarequests.UserLimit, error)
}

func (h *RequestsHandler) handleLifecycleUpdateUserLimit(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	userSelector := strings.TrimSpace(chi.URLParam(r, "user_id"))
	userID, err := strconv.Atoi(userSelector)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid user_id")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	var limit mediarequests.UserLimit
	if err := json.Unmarshal(body, &limit); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	limit.UserID = userID
	claims := apimw.GetClaims(r.Context())
	actorIncarnation, err := uuid.Parse(claims.AccountIncarnationID)
	if err != nil || actorIncarnation == uuid.Nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authenticated account identity is incomplete")
		return
	}
	service, ok := h.service.(transactionalRequestLimitService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	actorID := claims.UserID
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &actorIncarnation, Method: r.Method, RouteID: "account.request_limit.update",
			RequestHash:  h.lifecycleDigest(r.Method, "account.request_limit.update", map[string]string{"user_id": userSelector}, r.URL.Query(), body),
			TargetSource: lifecycleidempotency.TargetPathAccount,
		},
		ResolveTargets: func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
			return lifecycleidempotency.ResolveAccountTargets(ctx, tx, userID)
		},
	}
	result, err := h.lifecycle.Execute(r.Context(), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		updated, err := service.UpsertUserLimitInTransaction(ctx, tx, viewer, limit)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		response, err := json.Marshal(updated)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		return lifecycleidempotency.Result{Status: http.StatusOK, Body: response, Headers: map[string][]string{"Content-Type": {"application/json"}}}, nil
	})
	if err != nil {
		switch {
		case errors.Is(err, lifecycleidempotency.ErrKeyRequired):
			writeError(w, http.StatusPreconditionRequired, "idempotency_key_required", "Idempotency-Key is required for this lifecycle mutation")
		case errors.Is(err, lifecycleidempotency.ErrKeyMalformed):
			writeError(w, http.StatusBadRequest, "idempotency_key_invalid", "Idempotency-Key must be a bounded opaque ASCII value")
		case errors.Is(err, lifecycleidempotency.ErrConflict):
			writeError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key conflicts with its original lifecycle request")
		case errors.Is(err, lifecycleidempotency.ErrTargetNotFound):
			writeError(w, http.StatusNotFound, "not_found", "User not found")
		case errors.Is(err, lifecycleidempotency.ErrPending):
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusServiceUnavailable, "lifecycle_request_pending", "Lifecycle request completion is pending")
		case errors.Is(err, lifecycleidempotency.ErrInvalidBinding):
			writeError(w, http.StatusUnauthorized, "unauthorized", "Lifecycle request identity is no longer valid")
		default:
			writeRequestServiceError(w, err)
		}
		return
	}
	for key, values := range result.Headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}

// requestValidationErrorResponse is the 400 body writeRequestServiceError
// writes when request creation fails field validation. It was an inline
// map[string]any literal, which had no nameable type for the client DTO
// registry (contracts/client/v1/registry.json). The map marshaled its keys
// in sorted order; the struct declares the fields in that same order so the
// bytes are unchanged, including the empty form_error the map always wrote.
type requestValidationErrorResponse struct {
	Error       string            `json:"error"`
	FieldErrors map[string]string `json:"field_errors"`
	FormError   string            `json:"form_error"`
}

// requestQuotaErrorResponse is the 429 body writeRequestServiceError writes
// when the viewer has exhausted their request quota. It was an inline struct
// literal, which had no nameable type for the client DTO registry
// (contracts/client/v1/registry.json).
type requestQuotaErrorResponse struct {
	Error      string `json:"error"`
	Message    string `json:"message"`
	Used       int    `json:"used"`
	Limit      int    `json:"limit"`
	WindowDays int    `json:"window_days"`
}

// bloemRequestsHandlerExt holds the Bloem-only RequestsHandler dependencies.
type bloemRequestsHandlerExt struct {
	lifecycle       lifecycleidempotency.Coordinator
	lifecycleDigest lifecycleidempotency.RequestDigester
}

// bloemLifecycleUpdateUserLimit dispatches the admin request-limit update to
// the lifecycle receipt path. It reports whether it wrote the response.
func (h *RequestsHandler) bloemLifecycleUpdateUserLimit(w http.ResponseWriter, r *http.Request) bool {
	return dispatchBloemLifecycle(w, r, h.lifecycle != nil && h.lifecycleDigest != nil, func() {
		h.handleLifecycleUpdateUserLimit(w, r)
	})
}
