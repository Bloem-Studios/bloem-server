package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
)

type RequestService interface {
	Search(ctx context.Context, viewer mediarequests.Viewer, query string, mediaType mediarequests.MediaType, page int) (*mediarequests.MediaPage, error)
	Discover(ctx context.Context, viewer mediarequests.Viewer, section string, page int) (*mediarequests.DiscoverySection, error)
	DiscoverAll(ctx context.Context, viewer mediarequests.Viewer) ([]mediarequests.DiscoverySection, error)
	GetDetail(ctx context.Context, viewer mediarequests.Viewer, mediaType mediarequests.MediaType, tmdbID int) (*mediarequests.MediaDetail, error)
	CreateRequest(ctx context.Context, viewer mediarequests.Viewer, input mediarequests.CreateRequestInput) (*mediarequests.Request, error)
	ListMine(ctx context.Context, viewer mediarequests.Viewer, filter mediarequests.ListFilter) ([]*mediarequests.Request, error)
	ListAdmin(ctx context.Context, viewer mediarequests.Viewer, filter mediarequests.ListFilter) ([]*mediarequests.Request, error)
	GetRequest(ctx context.Context, viewer mediarequests.Viewer, id string) (*mediarequests.Request, error)
	Approve(ctx context.Context, viewer mediarequests.Viewer, id string) (*mediarequests.Request, error)
	Decline(ctx context.Context, viewer mediarequests.Viewer, id, reason string) (*mediarequests.Request, error)
	Cancel(ctx context.Context, viewer mediarequests.Viewer, id, reason string) (*mediarequests.Request, error)
	Retry(ctx context.Context, viewer mediarequests.Viewer, id string) (*mediarequests.Request, error)
	GetFeatureStatus(ctx context.Context, viewer mediarequests.Viewer) (mediarequests.FeatureStatus, error)
	GetSettings(ctx context.Context, viewer mediarequests.Viewer) (mediarequests.Settings, error)
	UpdateSettings(ctx context.Context, viewer mediarequests.Viewer, settings mediarequests.Settings) (mediarequests.Settings, error)
	GetUserLimit(ctx context.Context, viewer mediarequests.Viewer, userID int) (*mediarequests.UserLimit, error)
	UpsertUserLimit(ctx context.Context, viewer mediarequests.Viewer, limit mediarequests.UserLimit) (*mediarequests.UserLimit, error)
	ListIntegrations(ctx context.Context, viewer mediarequests.Viewer) ([]mediarequests.Integration, error)
	CreateIntegration(ctx context.Context, viewer mediarequests.Viewer, integration mediarequests.Integration) (*mediarequests.Integration, error)
	UpdateIntegration(ctx context.Context, viewer mediarequests.Viewer, integration mediarequests.Integration) (*mediarequests.Integration, error)
	DeleteIntegration(ctx context.Context, viewer mediarequests.Viewer, id string) error
	LoadIntegrationOptions(ctx context.Context, viewer mediarequests.Viewer, integration mediarequests.Integration) (map[string][]mediarequests.RouterOption, error)

	ListStudios(ctx context.Context, viewer mediarequests.Viewer) ([]mediarequests.DiscoverBrandCard, error)
	ListNetworks(ctx context.Context, viewer mediarequests.Viewer) ([]mediarequests.DiscoverBrandCard, error)
	ListGenres(ctx context.Context, viewer mediarequests.Viewer) ([]mediarequests.DiscoverBrandCard, error)
	BrowseStudio(ctx context.Context, viewer mediarequests.Viewer, slug, sort string, page int) (*mediarequests.DiscoverBrowseResponse, error)
	BrowseNetwork(ctx context.Context, viewer mediarequests.Viewer, slug, sort string, page int) (*mediarequests.DiscoverBrowseResponse, error)
	BrowseGenre(ctx context.Context, viewer mediarequests.Viewer, slug string, mediaType mediarequests.MediaType, sort string, page int) (*mediarequests.DiscoverBrowseResponse, error)
}

type RequestsHandler struct {
	service         RequestService
	lifecycle       lifecycleidempotency.Coordinator
	lifecycleDigest lifecycleidempotency.RequestDigester
}

func (h *RequestsHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, digester lifecycleidempotency.RequestDigester) {
	h.lifecycle = coordinator
	h.lifecycleDigest = digester
}

func NewRequestsHandler(service RequestService) *RequestsHandler {
	return &RequestsHandler{service: service}
}

func (h *RequestsHandler) HandleSearch(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	page, ok := parsePositiveIntQuery(w, r, "page", 1)
	if !ok {
		return
	}
	result, err := h.service.Search(
		r.Context(),
		viewer,
		r.URL.Query().Get("q"),
		mediarequests.MediaType(r.URL.Query().Get("media_type")),
		page,
	)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// discoverSectionsResponse is the GET /requests/discover envelope. It was an
// inline struct literal, which had no nameable type for the client DTO
// registry (contracts/client/v1/registry.json).
type discoverSectionsResponse struct {
	Sections []mediarequests.DiscoverySection `json:"sections"`
}

func (h *RequestsHandler) HandleDiscover(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	sections, err := h.service.DiscoverAll(r.Context(), viewer)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, discoverSectionsResponse{Sections: sections})
}

func (h *RequestsHandler) HandleDiscoverSection(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	page, ok := parsePositiveIntQuery(w, r, "page", 1)
	if !ok {
		return
	}
	section, err := h.service.Discover(r.Context(), viewer, chi.URLParam(r, "section"), page)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, section)
}

func (h *RequestsHandler) HandleListStudios(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	studios, err := h.service.ListStudios(r.Context(), viewer)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Studios []mediarequests.DiscoverBrandCard `json:"studios"`
	}{Studios: studios})
}

func (h *RequestsHandler) HandleListNetworks(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	networks, err := h.service.ListNetworks(r.Context(), viewer)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Networks []mediarequests.DiscoverBrandCard `json:"networks"`
	}{Networks: networks})
}

func (h *RequestsHandler) HandleListGenres(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	genres, err := h.service.ListGenres(r.Context(), viewer)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Genres []mediarequests.DiscoverBrandCard `json:"genres"`
	}{Genres: genres})
}

func (h *RequestsHandler) HandleBrowseStudio(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	page, ok := parsePositiveIntQuery(w, r, "page", 1)
	if !ok {
		return
	}
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	sort := strings.TrimSpace(r.URL.Query().Get("sort"))
	resp, err := h.service.BrowseStudio(r.Context(), viewer, slug, sort, page)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *RequestsHandler) HandleBrowseNetwork(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	page, ok := parsePositiveIntQuery(w, r, "page", 1)
	if !ok {
		return
	}
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	sort := strings.TrimSpace(r.URL.Query().Get("sort"))
	resp, err := h.service.BrowseNetwork(r.Context(), viewer, slug, sort, page)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *RequestsHandler) HandleBrowseGenre(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	page, ok := parsePositiveIntQuery(w, r, "page", 1)
	if !ok {
		return
	}
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	sort := strings.TrimSpace(r.URL.Query().Get("sort"))
	mediaType := mediarequests.MediaType(strings.TrimSpace(r.URL.Query().Get("media_type")))
	resp, err := h.service.BrowseGenre(r.Context(), viewer, slug, mediaType, sort, page)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *RequestsHandler) HandleGetDetail(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	mediaType := mediarequests.MediaType(strings.TrimSpace(chi.URLParam(r, "media_type")))
	tmdbID, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "tmdb_id")))
	if err != nil || tmdbID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid tmdb id")
		return
	}
	detail, err := h.service.GetDetail(r.Context(), viewer, mediaType, tmdbID)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *RequestsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	var input mediarequests.CreateRequestInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	req, err := h.service.CreateRequest(r.Context(), viewer, input)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

func (h *RequestsHandler) HandleListMine(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	requests, err := h.service.ListMine(r.Context(), viewer, parseRequestListFilter(r))
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Requests []*mediarequests.Request `json:"requests"`
	}{Requests: requests})
}

func (h *RequestsHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	req, err := h.service.GetRequest(r.Context(), viewer, chi.URLParam(r, "id"))
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (h *RequestsHandler) HandleAdminList(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	requests, err := h.service.ListAdmin(r.Context(), viewer, parseRequestListFilter(r))
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Requests []*mediarequests.Request `json:"requests"`
	}{Requests: requests})
}

func (h *RequestsHandler) HandleApprove(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	req, err := h.service.Approve(r.Context(), viewer, chi.URLParam(r, "id"))
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (h *RequestsHandler) HandleDecline(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
			return
		}
	}
	req, err := h.service.Decline(r.Context(), viewer, chi.URLParam(r, "id"), body.Reason)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (h *RequestsHandler) HandleCancel(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
			return
		}
	}
	req, err := h.service.Cancel(r.Context(), viewer, chi.URLParam(r, "id"), body.Reason)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (h *RequestsHandler) HandleRetry(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	req, err := h.service.Retry(r.Context(), viewer, chi.URLParam(r, "id"))
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (h *RequestsHandler) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, true)
	if !ok {
		return
	}
	status, err := h.service.GetFeatureStatus(r.Context(), viewer)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *RequestsHandler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	settings, err := h.service.GetSettings(r.Context(), viewer)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *RequestsHandler) HandleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	var settings mediarequests.Settings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	updated, err := h.service.UpdateSettings(r.Context(), viewer, settings)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *RequestsHandler) HandleListIntegrations(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	integrations, err := h.service.ListIntegrations(r.Context(), viewer)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Integrations []requestIntegrationResponse `json:"integrations"`
	}{Integrations: toIntegrationResponses(integrations)})
}

func (h *RequestsHandler) HandleCreateIntegration(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	var integration mediarequests.Integration
	if err := json.NewDecoder(r.Body).Decode(&integration); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	created, err := h.service.CreateIntegration(r.Context(), viewer, integration)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, requestIntegrationResponseFrom(*created))
}

func (h *RequestsHandler) HandleUpdateIntegration(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	var integration mediarequests.Integration
	if err := json.NewDecoder(r.Body).Decode(&integration); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	integration.ID = chi.URLParam(r, "id")
	updated, err := h.service.UpdateIntegration(r.Context(), viewer, integration)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, requestIntegrationResponseFrom(*updated))
}

func (h *RequestsHandler) HandleDeleteIntegration(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	if err := h.service.DeleteIntegration(r.Context(), viewer, chi.URLParam(r, "id")); err != nil {
		writeRequestServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *RequestsHandler) HandleLoadIntegrationOptions(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	var integration mediarequests.Integration
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&integration); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
			return
		}
	}
	if id := strings.TrimSpace(chi.URLParam(r, "id")); id != "" {
		integration.ID = id
	}
	options, err := h.service.LoadIntegrationOptions(r.Context(), viewer, integration)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, options)
}

func (h *RequestsHandler) HandleGetUserLimit(w http.ResponseWriter, r *http.Request) {
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	userID, ok := parsePositivePathInt(w, r, "user_id")
	if !ok {
		return
	}
	limit, err := h.service.GetUserLimit(r.Context(), viewer, userID)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, limit)
}

func (h *RequestsHandler) HandleUpdateUserLimit(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.lifecycleDigest != nil {
		h.handleLifecycleUpdateUserLimit(w, r)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	viewer, ok := requestViewer(w, r, false)
	if !ok {
		return
	}
	userID, ok := parsePositivePathInt(w, r, "user_id")
	if !ok {
		return
	}
	var limit mediarequests.UserLimit
	if err := json.NewDecoder(r.Body).Decode(&limit); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	limit.UserID = userID
	updated, err := h.service.UpsertUserLimit(r.Context(), viewer, limit)
	if err != nil {
		writeRequestServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
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

func requestViewer(w http.ResponseWriter, r *http.Request, requireProfile bool) (mediarequests.Viewer, bool) {
	claims := apimw.GetClaims(r.Context())
	if claims == nil || claims.UserID == 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return mediarequests.Viewer{}, false
	}
	profileID := strings.TrimSpace(apimw.GetProfileID(r.Context()))
	if requireProfile && profileID == "" {
		writeError(w, http.StatusBadRequest, "profile_required", "Profile is required")
		return mediarequests.Viewer{}, false
	}
	return mediarequests.Viewer{
		UserID:    claims.UserID,
		ProfileID: profileID,
		IsAdmin:   claims.Role == "admin",
	}, true
}

func parseRequestListFilter(r *http.Request) mediarequests.ListFilter {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	return mediarequests.ListFilter{
		Status:  mediarequests.Status(strings.TrimSpace(q.Get("status"))),
		Outcome: mediarequests.Outcome(strings.TrimSpace(q.Get("outcome"))),
		Limit:   limit,
		Offset:  offset,
	}
}

func parsePositiveIntQuery(w http.ResponseWriter, r *http.Request, key string, fallback int) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid "+key)
		return 0, false
	}
	return value, true
}

func parsePositivePathInt(w http.ResponseWriter, r *http.Request, key string) (int, bool) {
	value, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, key)))
	if err != nil || value <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid "+key)
		return 0, false
	}
	return value, true
}

type requestIntegrationResponse struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	CapabilityID        string         `json:"capability_id"`
	InstallationID      *int           `json:"installation_id,omitempty"`
	SupportedMediaTypes []string       `json:"supported_media_types"`
	PluginConfig        map[string]any `json:"plugin_config"`
	Enabled             bool           `json:"enabled"`
	BaseURL             string         `json:"base_url"`
	HasAPIKey           bool           `json:"has_api_key"`
	LastCheckAt         *time.Time     `json:"last_check_at,omitempty"`
	LastCheckStatus     string         `json:"last_check_status,omitempty"`
	LastCheckError      string         `json:"last_check_error,omitempty"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

func requestIntegrationResponseFrom(integration mediarequests.Integration) requestIntegrationResponse {
	return requestIntegrationResponse{
		ID:                  integration.ID,
		Name:                integration.Name,
		CapabilityID:        integration.CapabilityID,
		InstallationID:      integration.InstallationID,
		SupportedMediaTypes: integration.SupportedMediaTypes,
		PluginConfig:        integration.PluginConfig,
		Enabled:             integration.Enabled,
		BaseURL:             integration.BaseURL,
		HasAPIKey:           strings.TrimSpace(integration.APIKeyRef) != "",
		LastCheckAt:         integration.LastCheckAt,
		LastCheckStatus:     integration.LastCheckStatus,
		LastCheckError:      integration.LastCheckError,
		UpdatedAt:           integration.UpdatedAt,
	}
}

func toIntegrationResponses(integrations []mediarequests.Integration) []requestIntegrationResponse {
	out := make([]requestIntegrationResponse, 0, len(integrations))
	for _, integration := range integrations {
		out = append(out, requestIntegrationResponseFrom(integration))
	}
	return out
}

func writeRequestServiceError(w http.ResponseWriter, err error) {
	// Plugin/instance validation failures carry inline field/form errors; surface
	// them as a structured 400 so any handler routing through here renders them
	// inline. Checked first because *ValidationError does not wrap a sentinel.
	var verr *mediarequests.ValidationError
	if errors.As(err, &verr) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":        "validation_failed",
			"field_errors": verr.FieldErrors,
			"form_error":   verr.FormError,
		})
		return
	}
	var quota mediarequests.QuotaError
	switch {
	case errors.As(err, &quota):
		writeJSON(w, http.StatusTooManyRequests, struct {
			Error      string `json:"error"`
			Message    string `json:"message"`
			Used       int    `json:"used"`
			Limit      int    `json:"limit"`
			WindowDays int    `json:"window_days"`
		}{
			Error:      "quota_exceeded",
			Message:    "Request quota exceeded",
			Used:       quota.Used,
			Limit:      quota.Limit,
			WindowDays: quota.WindowDays,
		})
	case errors.Is(err, mediarequests.ErrInvalidInput), errors.Is(err, mediarequests.ErrInvalidMediaType):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, mediarequests.ErrRequestsDisabled):
		writeError(w, http.StatusForbidden, "requests_disabled", "Requests are disabled")
	case errors.Is(err, mediarequests.ErrUserBlocked):
		writeError(w, http.StatusForbidden, "requesting_blocked", "User is blocked from requesting")
	case errors.Is(err, mediarequests.ErrAlreadyAvailable):
		writeError(w, http.StatusConflict, "already_available", "Media is already available")
	case errors.Is(err, mediarequests.ErrAlreadyRequested):
		writeError(w, http.StatusConflict, "already_requested", "Media is already requested")
	case errors.Is(err, mediarequests.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "Request access denied")
	case errors.Is(err, mediarequests.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Request not found")
	case errors.Is(err, mediarequests.ErrInvalidState):
		writeError(w, http.StatusConflict, "invalid_state", "Request is not in a valid state for this action")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Request operation failed")
	}
}
