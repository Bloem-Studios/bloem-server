package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/preview"
)

// exchangesPerClientPerHour is spec §6.4's exchange rate limit.
const exchangesPerClientPerHour = 20

type PreviewExchanger interface {
	Exchange(ctx context.Context, in preview.ExchangeInput) (preview.ExchangeResult, error)
}

type PreviewSettingsReader interface {
	Settings(ctx context.Context) (preview.Settings, error)
}

// BloemPreviewHandler serves the public preview endpoints.
type BloemPreviewHandler struct {
	events     *preview.Events
	eventLimit *preview.EventLimiter
	exchange   PreviewExchanger
	settings   PreviewSettingsReader
	buckets    *preview.ClientBuckets
	limiter    *preview.HourlyLimiter
}

func NewBloemPreviewHandler(exchange PreviewExchanger, settings PreviewSettingsReader, buckets *preview.ClientBuckets) *BloemPreviewHandler {
	return &BloemPreviewHandler{exchange: exchange, settings: settings, buckets: buckets, limiter: preview.NewHourlyLimiter(exchangesPerClientPerHour)}
}

// HandleCreateSession exchanges a launch ticket for a guest session.
func (h *BloemPreviewHandler) HandleCreateSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Ticket       string `json:"ticket"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil || req.Ticket == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "A preview ticket is required.")
		return
	}
	ip := apimw.PreviewClientIP(r.Context())
	if ok, wait := h.limiter.Allow(h.buckets.Key(ip)); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many preview starts; try again later.")
		return
	}
	result, err := h.exchange.Exchange(r.Context(), preview.ExchangeInput{
		Ticket: req.Ticket, RefreshToken: req.RefreshToken, Host: auth.HostOnly(r.Host), ClientIP: ip,
	})
	switch {
	case errors.Is(err, preview.ErrUnavailable):
		writeError(w, http.StatusNotFound, "preview_unavailable", "The preview is not available here.")
		return
	case errors.Is(err, preview.ErrTicketInvalid):
		writeError(w, http.StatusForbidden, "preview_ticket_invalid", "This preview link has expired or was already used.")
		return
	case errors.Is(err, preview.ErrBusy):
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusServiceUnavailable, "preview_busy", "The preview is full right now; try again in a minute.")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal_error", "The preview could not start.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"access_token":  result.Tokens.AccessToken,
		"refresh_token": result.Tokens.RefreshToken,
		"token_type":    "Bearer",
		"expires_in":    result.Tokens.ExpiresIn,
		"expires_at":    result.ExpiresAt.UTC().Format(time.RFC3339),
		"item":          result.Item,
		"ref":           result.Ref,
	})
}

// HandleStatus reports whether the preview is on. On the preview host it also
// gives the landing page what it needs; capacity is never public.
func (h *BloemPreviewHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	s, err := h.settings.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Preview status is unavailable.")
		return
	}
	body := map[string]any{"enabled": s.Enabled}
	if s.Host != "" && auth.HostOnly(r.Host) == s.Host {
		body["brand"], body["storefront_url"], body["launch_url"] = s.Brand, s.StorefrontURL, s.LaunchURL
		body["cta_url"], body["cta_label"] = s.CTAURL, s.CTALabel
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, body)
}
