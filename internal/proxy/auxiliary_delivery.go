package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/go-chi/chi/v5"
)

// WithAuxiliaryProducer accepts only an explicitly configured API origin. It
// must be called before serving; neither stream claims nor request Host choose
// the producer. The default remains unavailable until startup wires both seams.
func (s *Server) WithAuxiliaryProducer(origin string, open playback.ExecutorAuxiliaryTransferProviderV3) (*Server, error) {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || open == nil {
		return nil, fmt.Errorf("configured auxiliary API origin and provider required")
	}
	transport := newStreamTransport()
	// Fresh connections prevent transparent GET retry after a reused connection
	// fails. A new viewer request opens its own permit; this exchange is not replayed.
	transport.DisableKeepAlives = true
	s.auxiliaryHTTPClient = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s.auxiliaryAPIOrigin = strings.TrimSuffix(origin, "/")
	s.auxiliaryTransfers = open
	return s, nil
}

func (s *Server) handleGrantSubtitle(w http.ResponseWriter, r *http.Request) {
	s.handleGrantAuxiliary(w, r, false)
}
func (s *Server) handleGrantSubtitleFonts(w http.ResponseWriter, r *http.Request) {
	s.handleGrantAuxiliary(w, r, true)
}
func (s *Server) handleGrantAuxiliary(w http.ResponseWriter, r *http.Request, fonts bool) {
	card, ok := s.authorizeGrant(w, r)
	if !ok {
		return
	}
	if card.ProfileID == "" || r.Header.Get("X-Profile-Id") != card.ProfileID {
		writeGrantError(w, http.StatusForbidden, "forbidden", "Session belongs to another profile")
		return
	}
	if card.Executor == nil {
		writeGrantError(w, http.StatusServiceUnavailable, "unavailable", "Bound auxiliary authority required")
		return
	}
	claims := card.ToClaims()
	cfg := s.watcher.Config()
	if cfg == nil || cfg.Auth.JWTSecret == "" {
		writeNativeProxyAuxiliaryUnavailable(w)
		return
	}
	token, err := streamtoken.Sign(claims, cfg.Auth.JWTSecret, playback.MaxTokenTTL)
	if err != nil {
		writeNativeProxyAuxiliaryUnavailable(w)
		return
	}
	// guardExecutorDelivery compares claims to the immutable card, including JWT
	// metadata supplied by this newly signed internal projection.
	verified, err := streamtoken.Verify(token, cfg.Auth.JWTSecret)
	if err != nil {
		writeNativeProxyAuxiliaryUnavailable(w)
		return
	}
	s.relayAuxiliary(w, r, verified, token, fonts)
}

func writeNativeProxyAuxiliaryUnavailable(w http.ResponseWriter) {
	http.Error(w, "Playback auxiliary authority unavailable", http.StatusServiceUnavailable)
}

func (s *Server) relayAuxiliary(w http.ResponseWriter, r *http.Request, claims *streamtoken.Claims, token string, fonts bool) {
	if s.auxiliaryAPIOrigin == "" || s.auxiliaryTransfers == nil || s.auxiliaryHTTPClient == nil || claims == nil || !claims.ExecutorBound || token == "" {
		writeNativeProxyAuxiliaryUnavailable(w)
		return
	}
	track := chi.URLParam(r, "track")
	if _, _, err := playback.ParseSubtitleTrackParam(track); err != nil {
		http.Error(w, "Invalid subtitle track", http.StatusBadRequest)
		return
	}
	w, r, cleanup, ok := s.guardExecutorDelivery(w, r, claims)
	if !ok {
		return
	}
	defer cleanup()
	card := playback.RecipeCardFromClaims(claims)
	permit, closePermit, err := s.auxiliaryTransfers(r.Context(), transcodeTransportIDFromClaims(claims), *card.Executor)
	if err != nil || permit == "" || closePermit == nil {
		if closePermit != nil {
			closePermit()
		}
		writeNativeProxyAuxiliaryUnavailable(w)
		return
	}
	defer closePermit()
	target := s.auxiliaryAPIOrigin + "/internal/playback/auxiliary/" + url.PathEscape(claims.SessionID) + "/subtitles/" + url.PathEscape(track)
	if fonts {
		target += "/fonts"
	}
	query := url.Values{}
	// Preserve duplicate identity values so the authoritative validator can
	// reject them; do not silently normalize a malformed identity pin.
	for _, key := range []string{"file_id", playback.EmbeddedSubtitleStreamIndexParamV3, playback.ExternalSubtitleKeyParamV3, playback.DownloadedSubtitleIDParamV3, "windowed", "position", "duration"} {
		if values, ok := r.URL.Query()[key]; ok {
			query[key] = values
		}
	}
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, target, nil)
	if err != nil {
		writeNativeProxyAuxiliaryUnavailable(w)
		return
	}
	request.Header.Set("X-Silo-Stream-Token", token)
	request.Header.Set(playback.AuxiliaryTransferHeaderV3, permit)
	for _, key := range []string{"Range", "If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		for _, value := range r.Header.Values(key) {
			request.Header.Add(key, value)
		}
	}
	client := *s.auxiliaryHTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		writeNativeProxyAuxiliaryUnavailable(w)
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 && response.StatusCode != http.StatusNotModified {
		http.Error(w, "Auxiliary producer redirect refused", http.StatusBadGateway)
		return
	}
	for _, key := range []string{"Content-Type", "Content-Length", "Content-Encoding", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Cache-Control", "Vary"} {
		for _, value := range response.Header.Values(key) {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	if r.Method != http.MethodHead {
		if _, err := io.Copy(w, response.Body); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
}
