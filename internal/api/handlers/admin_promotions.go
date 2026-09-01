package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/promotions"
)

// promotionRegistry is the seam the admin handlers talk to; production is
// *promotions.Service, tests use a fake.
type promotionRegistry interface {
	List(ctx context.Context) ([]promotions.Promotion, error)
	Create(ctx context.Context, createdBy int, in promotions.Input) (*promotions.Promotion, error)
	Update(ctx context.Context, id string, in promotions.Input) (*promotions.Promotion, error)
	Delete(ctx context.Context, id string) error
}

// AdminPromotionsHandler exposes the S-2 promotion CRUD under
// /admin/promotions (docs/specs/client-engagement.md section B.1).
type AdminPromotionsHandler struct {
	registry promotionRegistry
}

// NewAdminPromotionsHandler creates the handler; nil-safe when no service is wired.
func NewAdminPromotionsHandler(svc *promotions.Service) *AdminPromotionsHandler {
	h := &AdminPromotionsHandler{}
	if svc != nil {
		h.registry = svc
	}
	return h
}

func writePromotionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, promotions.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Promotion not found")
	case errors.Is(err, promotions.ErrInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Promotion operation failed")
	}
}

func (h *AdminPromotionsHandler) unavailable(w http.ResponseWriter) bool {
	if h == nil || h.registry == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Promotions are not available")
		return true
	}
	return false
}

func decodePromotionInput(w http.ResponseWriter, r *http.Request) (promotions.Input, bool) {
	var in promotions.Input
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return in, false
	}
	return in, true
}

// HandleList handles GET /admin/promotions.
func (h *AdminPromotionsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	list, err := h.registry.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list promotions")
		return
	}
	if list == nil {
		list = []promotions.Promotion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"promotions": list, "surfaces": promotions.Surfaces})
}

// HandleCreate handles POST /admin/promotions.
func (h *AdminPromotionsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	in, ok := decodePromotionInput(w, r)
	if !ok {
		return
	}
	created, err := h.registry.Create(r.Context(), apimw.GetUserID(r.Context()), in)
	if err != nil {
		writePromotionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// HandleUpdate handles PUT /admin/promotions/{id} (full replacement).
func (h *AdminPromotionsHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	in, ok := decodePromotionInput(w, r)
	if !ok {
		return
	}
	updated, err := h.registry.Update(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		writePromotionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// HandleDelete handles DELETE /admin/promotions/{id}.
func (h *AdminPromotionsHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	if err := h.registry.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writePromotionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
