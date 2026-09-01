package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/go-chi/chi/v5"
)

// announcementService is the seam AdminAnnouncementsHandler talks to;
// production is *notifications.AnnouncementService, tests use a fake.
type announcementService interface {
	Create(ctx context.Context, createdBy int, in notifications.AnnouncementInput) (*notifications.Announcement, error)
	List(ctx context.Context) ([]notifications.Announcement, error)
	Withdraw(ctx context.Context, id string) error
}

// AdminAnnouncementsHandler exposes admin compose/list/withdraw for S-1
// alert notifications, mounted inside the admin-only group beside the
// server-channel CRUD.
type AdminAnnouncementsHandler struct {
	service announcementService
}

// NewAdminAnnouncementsHandler creates the handler; nil-safe when the
// notification system has no announcement service.
func NewAdminAnnouncementsHandler(system *notifications.System) *AdminAnnouncementsHandler {
	h := &AdminAnnouncementsHandler{}
	if system != nil && system.Announcements != nil {
		h.service = system.Announcements
	}
	return h
}

func writeAnnouncementError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notifications.ErrAnnouncementNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Announcement not found")
	case errors.Is(err, notifications.ErrAnnouncementNoRecipients):
		writeError(w, http.StatusUnprocessableEntity, "no_recipients", "The targeting matches no recipients")
	case errors.Is(err, notifications.ErrAnnouncementInvalid), errors.Is(err, notifications.ErrAlertBodyInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Announcement operation failed")
	}
}

// HandleList handles GET /admin/notifications/announcements.
func (h *AdminAnnouncementsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.service == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Announcements are not available")
		return
	}
	items, err := h.service.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list announcements")
		return
	}
	if items == nil {
		items = []notifications.Announcement{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"announcements": items})
}

// HandleCreate handles POST /admin/notifications/announcements: validates,
// fans out to the resolved audience, and returns the announcement with its
// recipient count.
func (h *AdminAnnouncementsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.service == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Announcements are not available")
		return
	}
	var in notifications.AnnouncementInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	created, err := h.service.Create(r.Context(), apimw.GetUserID(r.Context()), in)
	if err != nil {
		writeAnnouncementError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// HandleDelete handles DELETE /admin/notifications/announcements/{id}
// (withdraw). Idempotent for an already-withdrawn announcement.
func (h *AdminAnnouncementsHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.service == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Announcements are not available")
		return
	}
	if err := h.service.Withdraw(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAnnouncementError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
