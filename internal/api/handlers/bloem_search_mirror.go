package handlers

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

// BloemSearchMirrorHandler serves the optional search mirror's status: a read-only view of
// the mirror's configuration (never its key), state, lag and failures. It changes nothing.
type BloemSearchMirrorHandler struct{ pool *pgxpool.Pool }

func NewBloemSearchMirrorHandler(pool *pgxpool.Pool) *BloemSearchMirrorHandler {
	return &BloemSearchMirrorHandler{pool: pool}
}

// authorized accepts a Platform administrative context or an enabled administrator's
// unscoped API key, as BloemLibraryFiguresHandler does. A scoped key never reaches here: no
// scope lists this route.
func (h *BloemSearchMirrorHandler) authorized(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if claims, ok := middleware.GetAdminContextClaims(r.Context()); ok {
		if claims.Scope == auth.AdminScopePlatform && claims.AccountID > 0 {
			return true
		}
	} else if c := middleware.GetClaims(r.Context()); c != nil && c.TokenType == auth.TokenTypeAPIKey && c.Role == "admin" && c.UserID > 0 && len(c.APIKeyScopes) == 0 {
		return true
	}
	writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required.")
	return false
}

func (h *BloemSearchMirrorHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}
	status, err := catalog.SearchMirrorStatusFor(r.Context(), h.pool)
	if err != nil {
		// The error text can carry database detail; the response and the log say only that
		// the status could not be read.
		slog.WarnContext(r.Context(), "search mirror status unavailable", "error", err)
		writeError(w, http.StatusServiceUnavailable, "search_mirror_status_unavailable", "The search mirror status is unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, status)
}
