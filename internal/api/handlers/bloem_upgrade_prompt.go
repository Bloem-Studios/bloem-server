package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

// BloemUpgradePromptHandler reads and replaces the alert a viewer gets when a
// stream is refused at their plan's simultaneous-stream limit. The storefront
// sets it, so it takes the same credentials as the library figures: a
// Platform context or an administrator's unscoped API key.
type BloemUpgradePromptHandler struct{ pool *pgxpool.Pool }

func NewBloemUpgradePromptHandler(pool *pgxpool.Pool) *BloemUpgradePromptHandler {
	return &BloemUpgradePromptHandler{pool: pool}
}

func (h *BloemUpgradePromptHandler) actor(w http.ResponseWriter, r *http.Request) (int, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if claims, ok := middleware.GetAdminContextClaims(r.Context()); ok {
		if claims.Scope == auth.AdminScopePlatform && claims.AccountID > 0 {
			return claims.AccountID, true
		}
	} else if c := middleware.GetClaims(r.Context()); c != nil && c.TokenType == auth.TokenTypeAPIKey && c.Role == "admin" && c.UserID > 0 && len(c.APIKeyScopes) == 0 {
		return c.UserID, true
	}
	writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required.")
	return 0, false
}

// eligible rechecks that the actor is still an enabled administrator.
func (h *BloemUpgradePromptHandler) eligible(w http.ResponseWriter, ctx context.Context, actor int) bool {
	if h.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "upgrade_prompt_unavailable", "The upgrade prompt is unavailable.")
		return false
	}
	var ok bool
	err := h.pool.QueryRow(ctx, `SELECT COALESCE(enabled, false) AND role = 'admin' FROM users WHERE id = $1`, actor).Scan(&ok)
	switch {
	case errors.Is(err, pgx.ErrNoRows) || err == nil && !ok:
		writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required.")
		return false
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "upgrade_prompt_unavailable", "The upgrade prompt is unavailable.")
		return false
	}
	return true
}

func (h *BloemUpgradePromptHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok || !h.eligible(w, r.Context(), actor) {
		return
	}
	prompt, err := notifications.LoadUpgradePrompt(r.Context(), h.pool)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "upgrade_prompt_unavailable", "The upgrade prompt is unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, prompt)
}

func (h *BloemUpgradePromptHandler) HandlePut(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var in notifications.UpgradePrompt
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "The request body is not a valid upgrade prompt.")
		return
	}
	if !h.eligible(w, r.Context(), actor) {
		return
	}
	saved, err := notifications.SaveUpgradePrompt(r.Context(), h.pool, in)
	switch {
	case errors.Is(err, notifications.ErrUpgradePromptInvalid):
		reason := strings.TrimPrefix(err.Error(), notifications.ErrUpgradePromptInvalid.Error()+": ")
		writeError(w, http.StatusUnprocessableEntity, "invalid_upgrade_prompt", reason)
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "upgrade_prompt_unavailable", "The upgrade prompt is unavailable.")
	default:
		writeJSON(w, http.StatusOK, saved)
	}
}
