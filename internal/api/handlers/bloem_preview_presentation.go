package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

func (h *BloemPreviewAdminHandler) HandlePersonalPresentation(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var in struct {
		Issuer string     `json:"issuer"`
		EndsAt *time.Time `json:"ends_at"`
	}
	if !previewAdminInput(w, r, &in) {
		return
	}
	account, err := strconv.Atoi(chi.URLParam(r, "account_id"))
	if err != nil || account <= 0 {
		writeError(w, 400, "invalid_request", "Invalid account.")
		return
	}
	if err = h.admin.SetPersonalPresentation(r.Context(), actor, in.Issuer, account, in.EndsAt); err != nil {
		previewAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
