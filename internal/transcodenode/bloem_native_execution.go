package transcodenode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// NativeExecutionRequest describes a candidate execution, not authority. Resolve
// must verify the host grant against current worker, playback session, transport,
// catalog file and immutable revision before returning a source factory. Start
// comes from unsigned JSON; Recipe comes from a verified token/control-plane
// store. HTTPRequest carries the host grant and must not be logged or persisted.
type NativeExecutionRequest struct {
	WorkerID    int
	TransportID string
	InputPath   string
	HTTPRequest *http.Request
	Start       *TranscodeStartRequest
	Recipe      *playback.RecipeCard
	Claims      *streamtoken.Claims
}

type NativeExecutionGrant struct {
	WorkerID          int
	PlaybackSessionID string
	TransportID       string
	InputPath         string
	Revision          string
	InputFormat       string
	InputSource       *playback.TranscodeInputSource
	ExpiresAt         time.Time
	// OpenDelivery retains exact source and grant authority for generated
	// artifacts without reopening a tool input or probing on every write.
	OpenDelivery func(context.Context) (NativeDeliveryAuthority, error)
}

// NativeDeliveryAuthority owns a native source admission until Close joins it.
// Cancel interrupts authority work without releasing that admission early.
type NativeDeliveryAuthority interface {
	Reauthorize(context.Context) error
	Cancel()
	Close() error
}

type NativeExecutionConfig struct {
	NodeID  int
	Resolve func(context.Context, NativeExecutionRequest) (*NativeExecutionGrant, error)
}

// NativeInputPathAuthorizer leaves ordinary catalog checks with their owner.
// A native location is admitted only within a host-verified execution binding.
type NativeInputPathAuthorizer struct{ local InputPathAuthorizer }

func NewNativeInputPathAuthorizer(local InputPathAuthorizer) *NativeInputPathAuthorizer {
	return &NativeInputPathAuthorizer{local: local}
}

func (a *NativeInputPathAuthorizer) Allowed(ctx context.Context, path string) (bool, error) {
	if storagesource.IsNativeLocation(path) {
		return playback.TranscodeInputSourceMatches(ctx, path), nil
	}
	if a == nil || a.local == nil {
		return false, nil
	}
	return a.local.Allowed(ctx, path)
}

// WrapNativeExecution adds runtime sources to existing node execution routes.
// Existing bearer authentication runs before resolution; the grant adds exact
// source/session/worker authority. Every reopened input must continuously check
// that authority through its own lifetime. Neither wrapper nor path authorizer
// turns a provider URL or a client-supplied body into a grant.
func (s *Server) WrapNativeExecution(next http.Handler, cfg NativeExecutionConfig) http.Handler {
	authorized := s.requireBearer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		candidate, err := s.nativeExecutionCandidate(w, r, cfg.NodeID)
		if err != nil {
			http.Error(w, "native execution identity unavailable", http.StatusForbidden)
			return
		}
		if candidate == nil || !storagesource.IsNativeLocation(candidate.InputPath) {
			next.ServeHTTP(w, r)
			return
		}
		if cfg.NodeID <= 0 || cfg.Resolve == nil {
			http.Error(w, "native execution authority unavailable", http.StatusServiceUnavailable)
			return
		}
		grant, err := cfg.Resolve(r.Context(), *candidate)
		if err != nil || !validNativeExecutionGrant(candidate, grant) {
			http.Error(w, "native execution grant rejected", http.StatusForbidden)
			return
		}
		ctx := playback.WithTranscodeInputSource(r.Context(), playback.TranscodeInputBinding{
			InputPath: grant.InputPath, InputFormat: grant.InputFormat,
			SessionID: grant.TransportID, InputSource: grant.InputSource,
		})
		if strings.HasPrefix(r.URL.Path, "/remux/") {
			s.handleNativeSourceRemux(w, r.WithContext(ctx), candidate, grant)
			return
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			serveNativeExecutionDelivery(next, w, r.WithContext(ctx), grant)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/transcode/start" ||
			(r.Method == http.MethodGet || r.Method == http.MethodHead) && (strings.HasPrefix(r.URL.Path, "/transcode/") || strings.HasPrefix(r.URL.Path, "/remux/")) {
			authorized.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func validNativeExecutionGrant(request *NativeExecutionRequest, grant *NativeExecutionGrant) bool {
	if grant == nil || grant.WorkerID != request.WorkerID || grant.TransportID != request.TransportID || grant.InputPath != request.InputPath ||
		grant.PlaybackSessionID == "" || grant.Revision == "" || grant.InputFormat == "" || grant.InputSource == nil || grant.InputSource.Open == nil || !time.Now().Before(grant.ExpiresAt) {
		return false
	}
	return request.Recipe == nil || request.Recipe.SessionID == grant.PlaybackSessionID
}

func (s *Server) nativeExecutionCandidate(w http.ResponseWriter, r *http.Request, nodeID int) (*NativeExecutionRequest, error) {
	request := &NativeExecutionRequest{WorkerID: nodeID, HTTPRequest: r}
	if r.Method == http.MethodPost {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		var start TranscodeStartRequest
		if err := json.Unmarshal(data, &start); err != nil {
			return nil, err
		}
		request.InputPath, request.TransportID, request.Start = start.InputPath, start.SessionID, &start
		return request, nil
	}
	if strings.HasPrefix(r.URL.Path, "/remux/") {
		cfg := s.watcher.Config()
		if cfg == nil {
			return nil, nil
		}
		claims, err := streamtoken.Verify(r.Header.Get("X-Silo-Stream-Token"), cfg.Auth.JWTSecret)
		if err != nil {
			return nil, nil //nolint:nilerr // The existing remux handler owns invalid-token responses.
		}
		if !storagesource.IsNativeLocation(claims.MediaPath) {
			return nil, nil
		}
		transport := claims.TranscodeTransportID
		if transport == "" {
			transport = claims.SessionID
		}
		if transport == "" || transport != strings.TrimPrefix(r.URL.Path, "/remux/") {
			return nil, errors.New("remux transport mismatch")
		}
		card := playback.RecipeCardFromClaims(claims)
		request.TransportID, request.InputPath, request.Claims, request.Recipe = transport, claims.MediaPath, claims, &card
		return request, nil
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/transcode/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		return nil, nil
	}
	request.TransportID = parts[0]
	// Ordinary live sessions retain the upstream fast path and policy. Native
	// sessions still require a fresh grant even when no reconstruction is needed.
	s.mu.RLock()
	existing := s.sessions[request.TransportID]
	s.mu.RUnlock()
	if existing != nil {
		request.InputPath = existing.Opts().InputPath
		if !storagesource.IsNativeLocation(request.InputPath) {
			return nil, nil
		}
	}
	if token := r.Header.Get("X-Silo-Stream-Token"); token != "" {
		cfg := s.watcher.Config()
		if cfg == nil {
			return nil, errors.New("node configuration unavailable")
		}
		claims, err := streamtoken.Verify(token, cfg.Auth.JWTSecret)
		if err != nil {
			return nil, err
		}
		card := playback.RecipeCardFromClaims(claims)
		if !recipeServesTransport(card, request.TransportID) {
			return nil, errors.New("recipe transport mismatch")
		}
		if recipeIsComplete(card) {
			request.Recipe = &card
		}
	}
	if request.Recipe == nil && s.recipeStore != nil {
		if card, found := s.recipeStore.Get(r.Context(), request.TransportID); found && card != nil && recipeServesTransport(*card, request.TransportID) && recipeIsComplete(*card) {
			request.Recipe = card
		}
	}
	if request.Recipe != nil {
		if request.InputPath != "" && request.InputPath != request.Recipe.InputPath {
			return nil, errors.New("source recipe mismatch")
		}
		request.InputPath = request.Recipe.InputPath
	}
	if request.InputPath == "" {
		return nil, nil
	}
	return request, nil
}
