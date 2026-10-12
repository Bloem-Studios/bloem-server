package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type BloemAudiobookAcquisitionService interface {
	AudiobookAcquisition(context.Context, bookrequests.Viewer, string) (bookrequests.AudiobookAcquisition, error)
	ListAudiobookAcquisitions(context.Context, bookrequests.Viewer, string, string, int) (bookrequests.AudiobookAcquisitionList, error)
	ApproveAudiobookCandidate(context.Context, bookrequests.Viewer, string, bookrequests.AudiobookCandidateApproval) (bookrequests.Request, error)
	AcceptAudiobookDownload(context.Context, bookrequests.Viewer, string, bookrequests.ActionInput) (bookrequests.Request, error)
}

func (h *BloemBookRequestsHandler) SetAudiobookAcquisition(s BloemAudiobookAcquisitionService) {
	if h != nil {
		h.acquisition = s
	}
}
func (h *BloemBookRequestsHandler) acquisitionViewer(w http.ResponseWriter, r *http.Request) (bookrequests.Viewer, bool) {
	t, ok := requireBloemOrganizationContext(w, r)
	if !ok {
		return bookrequests.Viewer{}, false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if h == nil || h.acquisition == nil {
		h.unavailable(w)
		return bookrequests.Viewer{}, false
	}
	return bookrequests.Viewer{UserID: t.AccountID, OrganizationID: t.OrganizationID, Admin: true}, true
}
func (h *BloemBookRequestsHandler) HandleAudiobookAcquisitionList(w http.ResponseWriter, r *http.Request) {
	v, ok := h.acquisitionViewer(w, r)
	if !ok {
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			h.writeRequestError(w, bookrequests.ErrInvalid)
			return
		}
		limit = n
	}
	out, err := h.acquisition.ListAudiobookAcquisitions(r.Context(), v, r.URL.Query().Get("state"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (h *BloemBookRequestsHandler) HandleAudiobookAcquisition(w http.ResponseWriter, r *http.Request) {
	v, ok := h.acquisitionViewer(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		h.writeRequestError(w, bookrequests.ErrNotFound)
		return
	}
	out, err := h.acquisition.AudiobookAcquisition(r.Context(), v, id)
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (h *BloemBookRequestsHandler) HandleAudiobookCandidateApproval(w http.ResponseWriter, r *http.Request) {
	v, ok := h.acquisitionViewer(w, r)
	if !ok {
		return
	}
	var in bookrequests.AudiobookCandidateApproval
	if !readBookRequestBody(w, r, &in) {
		return
	}
	out, err := h.acquisition.ApproveAudiobookCandidate(r.Context(), v, chi.URLParam(r, "id"), in)
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (h *BloemBookRequestsHandler) HandleAudiobookDownloadAcceptance(w http.ResponseWriter, r *http.Request) {
	v, ok := h.acquisitionViewer(w, r)
	if !ok {
		return
	}
	var in bookrequests.ActionInput
	if !readBookRequestBody(w, r, &in) {
		return
	}
	out, err := h.acquisition.AcceptAudiobookDownload(r.Context(), v, chi.URLParam(r, "id"), in)
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
