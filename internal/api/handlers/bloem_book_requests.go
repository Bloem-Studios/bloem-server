package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type BloemBookRequestsService interface {
	Capability(context.Context, bookrequests.Viewer) (bookrequests.Capability, error)
	List(context.Context, bookrequests.Viewer, int, int) ([]bookrequests.Request, error)
	Get(context.Context, bookrequests.Viewer, string) (bookrequests.Request, error)
	Create(context.Context, bookrequests.Viewer, bookrequests.CreateInput, string) (bookrequests.Request, bool, error)
	Act(context.Context, bookrequests.Viewer, string, string, bookrequests.ActionInput) (bookrequests.Request, error)
}

type BloemBookRequestsHandler struct {
	service  BloemBookRequestsService
	profiles BloemSeasonalViewerProfiles
}

func NewBloemBookRequestsHandler(s BloemBookRequestsService, p BloemSeasonalViewerProfiles) *BloemBookRequestsHandler {
	return &BloemBookRequestsHandler{service: s, profiles: p}
}
func (h *BloemBookRequestsHandler) viewer(w http.ResponseWriter, r *http.Request) (bookrequests.Viewer, bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	c := apimw.GetClaims(r.Context())
	if c == nil || c.UserID <= 0 {
		writeError(w, 401, "unauthorized", "Authentication required")
		return bookrequests.Viewer{}, false
	}
	t, ok := tenancy.FromContext(r.Context())
	scope, scoped := access.GetScope(r.Context())
	profile := apimw.GetProfileID(r.Context())
	usable := t.OrganizationStatus == tenancy.OrganizationActive || t.OrganizationStatus == tenancy.OrganizationInitializing && t.Legacy && t.OrganizationDefault
	if !ok || t.AccountID != c.UserID || t.OrganizationID == uuid.Nil || t.MembershipID == uuid.Nil || !usable || t.MembershipStatus != tenancy.MembershipActive || t.PolicyRevision <= 0 || t.SecurityRevision <= 0 || !scoped || scope.UserID != c.UserID || scope.ProfileID != profile || profile == "" || !scope.ProfileVerified {
		writeError(w, 403, "viewer_required", "Select and verify a profile in an active organization")
		return bookrequests.Viewer{}, false
	}
	if h == nil || h.service == nil || h.profiles == nil {
		writeError(w, 503, "unavailable", "Audiobook requests are unavailable")
		return bookrequests.Viewer{}, false
	}
	org, err := h.profiles.ProfileOrganization(r.Context(), c.UserID, profile)
	if err != nil || org != t.OrganizationID {
		writeError(w, 403, "viewer_required", "The profile is unavailable in this organization")
		return bookrequests.Viewer{}, false
	}
	return bookrequests.Viewer{UserID: c.UserID, ProfileID: profile, OrganizationID: t.OrganizationID}, true
}
func (h *BloemBookRequestsHandler) HandleCapability(w http.ResponseWriter, r *http.Request) {
	v, ok := h.viewer(w, r)
	if !ok {
		return
	}
	out, err := h.service.Capability(r.Context(), v)
	if err != nil {
		writeBookRequestError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (h *BloemBookRequestsHandler) HandleMine(w http.ResponseWriter, r *http.Request) {
	v, ok := h.viewer(w, r)
	if !ok {
		return
	}
	h.list(w, r, v)
}
func (h *BloemBookRequestsHandler) HandleAdminList(w http.ResponseWriter, r *http.Request) {
	t, ok := requireBloemOrganizationContext(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.list(w, r, bookrequests.Viewer{UserID: t.AccountID, OrganizationID: t.OrganizationID, Admin: true})
}
func (h *BloemBookRequestsHandler) list(w http.ResponseWriter, r *http.Request, v bookrequests.Viewer) {
	limit, offset := 100, 0
	for k, dst := range map[string]*int{"limit": &limit, "offset": &offset} {
		if raw := r.URL.Query().Get(k); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 0 {
				writeBookRequestError(w, bookrequests.ErrInvalid)
				return
			}
			*dst = n
		}
	}
	out, err := h.service.List(r.Context(), v, limit, offset)
	if err != nil {
		writeBookRequestError(w, err)
		return
	}
	if out == nil {
		out = []bookrequests.Request{}
	}
	writeJSON(w, 200, bookrequests.ListResponse{Requests: out})
}
func (h *BloemBookRequestsHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	v, ok := h.viewer(w, r)
	if !ok {
		return
	}
	out, err := h.service.Get(r.Context(), v, chi.URLParam(r, "id"))
	if err != nil {
		writeBookRequestError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (h *BloemBookRequestsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	v, ok := h.viewer(w, r)
	if !ok {
		return
	}
	var in bookrequests.CreateInput
	if !readBookRequestBody(w, r, &in) {
		return
	}
	out, created, err := h.service.Create(r.Context(), v, in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeBookRequestError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, out)
}
func (h *BloemBookRequestsHandler) HandleCancel(w http.ResponseWriter, r *http.Request) {
	v, ok := h.viewer(w, r)
	if !ok {
		return
	}
	h.act(w, r, v, "cancel")
}
func (h *BloemBookRequestsHandler) HandleAdminAction(w http.ResponseWriter, r *http.Request) {
	t, ok := requireBloemOrganizationContext(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.act(w, r, bookrequests.Viewer{UserID: t.AccountID, OrganizationID: t.OrganizationID, Admin: true}, chi.URLParam(r, "action"))
}
func (h *BloemBookRequestsHandler) act(w http.ResponseWriter, r *http.Request, v bookrequests.Viewer, action string) {
	if _, e := uuid.Parse(chi.URLParam(r, "id")); e != nil {
		writeBookRequestError(w, bookrequests.ErrNotFound)
		return
	}
	var in bookrequests.ActionInput
	if !readBookRequestBody(w, r, &in) {
		return
	}
	out, err := h.service.Act(r.Context(), v, chi.URLParam(r, "id"), action, in)
	if err != nil {
		writeBookRequestError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func readBookRequestBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		writeError(w, 400, "invalid_request", "Provide a valid request body")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, 400, "invalid_request", "Provide exactly one request body")
		return false
	}
	return true
}
func writeBookRequestError(w http.ResponseWriter, err error) {
	status, code, message := 500, "internal_error", "Audiobook request could not be completed"
	switch {
	case errors.Is(err, bookrequests.ErrForbidden):
		status, code, message = 403, "request_forbidden", "Audiobook requests are not permitted"
	case errors.Is(err, bookrequests.ErrNotFound):
		status, code, message = 404, "not_found", "Audiobook request not found"
	case errors.Is(err, bookrequests.ErrInvalid):
		status, code, message = 400, "invalid_request", err.Error()
	case errors.Is(err, bookrequests.ErrDuplicate):
		status, code, message = 409, "already_requested", "This audiobook already has an active request in this organization"
	case errors.Is(err, bookrequests.ErrConflict):
		status, code, message = 409, "request_state_changed", "The request changed; reload and try again"
	case errors.Is(err, bookrequests.ErrQuota):
		status, code, message = 429, "request_quota_exceeded", "Your audiobook request quota is full"
	case errors.Is(err, bookrequests.ErrDisabled):
		status, code, message = 403, "requests_disabled", "Requests are disabled"
	}
	writeError(w, status, code, message)
}
