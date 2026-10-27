package handlers

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/preview"
)

type BloemPreviewAdminHandler struct{ admin *preview.Admin }

func NewBloemPreviewAdminHandler(admin *preview.Admin) *BloemPreviewAdminHandler {
	return &BloemPreviewAdminHandler{admin: admin}
}

func (h *BloemPreviewAdminHandler) actor(w http.ResponseWriter, r *http.Request) (int, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if claims, ok := middleware.GetAdminContextClaims(r.Context()); ok {
		if claims.Scope == auth.AdminScopePlatform && claims.AccountID > 0 {
			return claims.AccountID, true
		}
	} else if c := middleware.GetClaims(r.Context()); c != nil && c.TokenType == auth.TokenTypeAPIKey && c.Role == "admin" && c.UserID > 0 && (len(c.APIKeyScopes) == 0 || slices.Contains(c.APIKeyScopes, auth.ScopeAdminPreview)) {
		return c.UserID, true
	}
	writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required.")
	return 0, false
}

func previewAdminInput(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid preview configuration.")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "One JSON document is required.")
		return false
	}
	return true
}

func previewAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, preview.ErrForbidden):
		writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required.")
	case errors.Is(err, preview.ErrInvalidSettings):
		writeError(w, http.StatusBadRequest, "invalid_preview_settings", "Invalid preview configuration.")
	case errors.Is(err, entitlements.ErrInvalidPolicy):
		writeError(w, http.StatusBadRequest, "invalid_preview_libraries", "Select existing enabled libraries.")
	case errors.Is(err, preview.ErrTicketInvalid):
		writeError(w, http.StatusForbidden, "preview_ticket_invalid", "This ticket is invalid or expired.")
	case errors.Is(err, preview.ErrUnavailable):
		writeError(w, http.StatusConflict, "preview_not_configured", "Ensure the preview account first.")
	default:
		writeError(w, http.StatusServiceUnavailable, "preview_unavailable", "Preview administration is unavailable.")
	}
}

func (h *BloemPreviewAdminHandler) HandleEnsureAccount(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var in struct {
		Issuer string `json:"issuer"`
	}
	if !previewAdminInput(w, r, &in) {
		return
	}
	// A missing issuer is a bad request; the conflict below is for a setup
	// that disagrees with the one already stored.
	if in.Issuer == "" {
		previewAdminError(w, preview.ErrInvalidSettings)
		return
	}
	id, err := h.admin.EnsureAccount(r.Context(), actor, in.Issuer)
	if errors.Is(err, preview.ErrInvalidSettings) {
		writeError(w, http.StatusConflict, "preview_setup_conflict", "Preview setup conflicts with an existing account or issuer.")
		return
	}
	if err != nil {
		previewAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"account_id": id})
}

func (h *BloemPreviewAdminHandler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	s, err := h.admin.Settings(r.Context(), actor)
	if err != nil {
		previewAdminError(w, err)
		return
	}
	keys := []string{}
	for _, key := range s.TicketPublicKeys {
		keys = append(keys, base64.StdEncoding.EncodeToString(key))
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": s.Enabled, "host": s.Host, "issuer": s.Issuer, "account_id": s.AccountID, "ticket_public_keys": keys, "session_ttl_seconds": int(s.SessionTTL / time.Second), "idle_ttl_seconds": int(s.IdleTTL / time.Second), "max_concurrent": s.MaxConcurrent, "brand": s.Brand, "cta_url": s.CTAURL, "cta_label": s.CTALabel, "item_cta_label": s.ItemCTALabel, "cta_message": s.CTAMessage, "storefront_url": s.StorefrontURL, "launch_url": s.LaunchURL, "maturity_ceiling": s.MaturityCeiling})
}

func (h *BloemPreviewAdminHandler) HandlePutSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var in preview.SettingsPatch
	if !previewAdminInput(w, r, &in) {
		return
	}
	if err := h.admin.UpdateSettings(r.Context(), actor, in, r.Host); err != nil {
		previewAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *BloemPreviewAdminHandler) HandlePutLibraries(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var in struct {
		Issuer     string `json:"issuer"`
		LibraryIDs []int  `json:"library_ids"`
	}
	if !previewAdminInput(w, r, &in) {
		return
	}
	if len(in.LibraryIDs) > 1000 {
		writeError(w, http.StatusBadRequest, "invalid_preview_libraries", "Too many libraries.")
		return
	}
	revision, err := h.admin.SetLibraries(r.Context(), actor, in.Issuer, in.LibraryIDs)
	if err != nil {
		previewAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"template_revision": revision})
}

func (h *BloemPreviewAdminHandler) HandlePutKeys(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var in struct {
		Issuer     string   `json:"issuer"`
		PublicKeys []string `json:"public_keys"`
	}
	if !previewAdminInput(w, r, &in) {
		return
	}
	if len(in.PublicKeys) < 1 || len(in.PublicKeys) > 2 {
		previewAdminError(w, preview.ErrInvalidSettings)
		return
	}
	keys := []ed25519.PublicKey{}
	for _, encoded := range in.PublicKeys {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			previewAdminError(w, preview.ErrInvalidSettings)
			return
		}
		keys = append(keys, ed25519.PublicKey(raw))
	}
	if err := h.admin.RotateKeys(r.Context(), actor, in.Issuer, keys); err != nil {
		previewAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *BloemPreviewAdminHandler) HandleRevokeSessions(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var in struct {
		Issuer string `json:"issuer"`
	}
	if !previewAdminInput(w, r, &in) {
		return
	}
	if err := h.admin.Revoke(r.Context(), actor, in.Issuer); err != nil {
		previewAdminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *BloemPreviewAdminHandler) HandleVerifyTicket(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var in struct {
		Ticket string `json:"ticket"`
	}
	if !previewAdminInput(w, r, &in) {
		return
	}
	ticket, err := h.admin.Verify(r.Context(), actor, in.Ticket)
	if err != nil {
		previewAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "account_ready": true, "item": ticket.Item, "expires_at": ticket.ExpiresAt.UTC().Format(time.RFC3339)})
}

func (h *BloemPreviewAdminHandler) HandleCapacity(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	capacity, err := h.admin.Capacity(r.Context(), actor)
	if err != nil {
		previewAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, capacity)
}

func (h *BloemPreviewAdminHandler) HandleCatalog(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	items, err := h.admin.Catalog(r.Context(), actor, q.Get("query"), q.Get("provider"), q.Get("provider_id"))
	if err != nil {
		previewAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
