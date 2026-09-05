package jellycompat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/config"
)

func TestLiveTVAccessResolvesCurrentScope(t *testing.T) {
	resolver := &stubScopeResolver{scope: access.Scope{LiveTVAllowed: true}}
	allowed := NewScopeLiveTVAccess(resolver)
	session := &Session{StreamAppUserID: 7, ProfileID: "child"}
	if !allowed(context.Background(), session) {
		t.Fatal("explicit grant denied")
	}
	if resolver.input.UserID != 7 || resolver.input.ProfileID != "child" || !resolver.input.SkipPINVerification {
		t.Fatalf("wrong identity: %+v", resolver.input)
	}
	resolver.scope.LiveTVAllowed = false
	if allowed(context.Background(), session) {
		t.Fatal("revoked grant retained")
	}
	resolver.scope.LiveTVAllowed = true
	resolver.err = errors.New("unavailable")
	if allowed(context.Background(), session) {
		t.Fatal("resolver error allowed access")
	}
	if allowed(context.Background(), nil) {
		t.Fatal("missing identity allowed")
	}
}

func TestLiveTVHandlersFailClosed(t *testing.T) {
	h := NewLiveTVHandler(nil, nil, nil)
	handlers := []http.HandlerFunc{h.HandleInfo, h.HandleGuideInfo, h.HandleChannels, h.HandleChannel, h.HandlePrograms, h.HandleProgram, h.HandleRecommendedPrograms, h.HandleTimers, h.HandleTimer, h.HandleSeriesTimers, h.HandleSeriesTimer, h.HandleRecordings, h.HandleOpenLiveStream, h.HandleLiveStreamFile}
	for i, handler := range handlers {
		rr := httptest.NewRecorder()
		handler(rr, httptest.NewRequest(http.MethodGet, "/", nil))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("handler %d status %d", i, rr.Code)
		}
	}
	if _, err := h.PlaybackMediaSource(context.Background(), &Session{StreamAppUserID: 7}, "channel", true, ""); !errors.Is(err, errLiveTVForbidden) {
		t.Fatalf("playback error: %v", err)
	}
}

func TestLiveTVPolicyReflectsRevocation(t *testing.T) {
	resolver := &stubScopeResolver{scope: access.Scope{LiveTVAllowed: true}}
	h := NewAuthHandler(func() *config.Config { return &config.Config{} }, nil, nil)
	h.SetLiveTVEnabled(true)
	h.liveTVAccess = NewScopeLiveTVAccess(resolver)
	session := &Session{StreamAppUserID: 7, ProfileID: "child"}
	policy := h.userDTO(context.Background(), session).Policy
	if !policy.EnableLiveTVAccess || !policy.EnableAllChannels {
		t.Fatal("granted viewer has no Live TV policy")
	}
	resolver.scope.LiveTVAllowed = false
	policy = h.userDTO(context.Background(), session).Policy
	if policy.EnableLiveTVAccess || policy.EnableAllChannels {
		t.Fatal("revoked viewer still advertises Live TV")
	}
}

func TestLiveTVCloseRemainsAvailableAfterRevocation(t *testing.T) {
	h := NewLiveTVHandler(nil, nil, nil)
	h.streams["owned"] = &openLiveStream{ID: "owned", OpenerToken: "owner"}
	req := httptest.NewRequest(http.MethodPost, "/LiveStreams/Close?LiveStreamId=owned", nil)
	req = req.WithContext(context.WithValue(req.Context(), compatSessionKey, &Session{Token: "owner", StreamAppUserID: 7}))
	rr := httptest.NewRecorder()
	h.HandleCloseLiveStream(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("cleanup denied: %d %s", rr.Code, rr.Body.String())
	}
	if _, exists := h.streams["owned"]; exists {
		t.Fatal("owned stream not closed")
	}
	h.streams["owned"] = &openLiveStream{ID: "owned", OpenerToken: "other-session"}
	rr = httptest.NewRecorder()
	h.HandleCloseLiveStream(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatal("closed another session’s stream")
	}
}
