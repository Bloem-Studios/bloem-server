package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// A playback's stream token is Silo's stream reference (the same `st`), and
// Silo ends it with the login session that started the playback
// (RequireStreamReferenceAuth). Bloem's stream-token gate runs first, so it
// must not keep a signed-out, revoked or disabled account's media URL
// playing: once the attempt's login session has ended, the request goes on
// unmarked and Silo's own gate gives Silo's answer.
func TestStreamTokenAuthEndsWithTheLoginSession(t *testing.T) {
	const (
		liveSession      = "11111111-1111-4111-8111-111111111111"
		signedOutSession = "77777777-7777-4777-8777-777777777777"
		unboundSession   = "66666666-6666-4666-8666-666666666666"
		noAttempt        = "88888888-8888-4888-8888-888888888888"
	)
	attempts := map[string]StreamReferenceAttempt{
		liveSession:      {UserID: 7, ProfileID: "p-kid", LoginSessionID: "sess-live", Live: true},
		signedOutSession: {UserID: 7, ProfileID: "p-kid", LoginSessionID: "sess-revoked", Live: true},
		unboundSession:   {UserID: 7, ProfileID: "p-kid", Live: true},
	}
	var attemptErr error
	sessions := &fakeSessionValidator{roles: map[string]string{"sess-live": "user"}}
	am := NewAuthMiddleware(nil, sessions, nil, nil)
	am.SetStreamReferenceVerifier(func(reference string) (*streamtoken.Claims, error) {
		return streamtoken.Verify(reference, testStreamSecret)
	}, func(_ context.Context, id string) (StreamReferenceAttempt, error) {
		return attempts[id], attemptErr
	})
	reference := func(session string) string {
		token, err := streamtoken.Sign(streamtoken.Claims{SessionID: session, PlayMethod: "direct", UserID: 7, ProfileID: "p-kid"}, testStreamSecret, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	// The v2 delivery chain (apiv2 bloemStreamReferenceChain): Silo's
	// reference gate runs only for a request the stream-token gate left
	// unmarked.
	siloReference := func(next http.Handler) http.Handler {
		reference := am.RequireStreamReferenceAuth(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsStreamTokenAuthorized(r.Context()) {
				next.ServeHTTP(w, r)
				return
			}
			reference.ServeHTTP(w, r)
		})
	}
	serve := func(session string) (int, bool) {
		var marked bool
		router := chi.NewRouter()
		router.With(am.StreamTokenAuth(testStreamSecret), siloReference).Get("/api/v2/stream/{session_id}",
			func(w http.ResponseWriter, r *http.Request) {
				marked = IsStreamTokenAuthorized(r.Context())
				w.WriteHeader(http.StatusOK)
			})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/stream/"+session+"?st="+reference(session), nil))
		return rec.Code, marked
	}

	if code, marked := serve(liveSession); code != http.StatusOK || !marked {
		t.Fatalf("live login session: status %d marked %v, want 200 marked", code, marked)
	}
	// Bloem deliveries without a v3 playback attempt (Live TV) keep the
	// stream-token gate as before.
	if code, marked := serve(noAttempt); code != http.StatusOK || !marked {
		t.Fatalf("no playback attempt: status %d marked %v, want 200 marked", code, marked)
	}
	for name, session := range map[string]string{
		"login session signed out":        signedOutSession,
		"attempt started with an API key": unboundSession,
	} {
		if code, marked := serve(session); code != http.StatusUnauthorized || marked {
			t.Errorf("%s: status %d marked %v, want Silo's 401 unmarked", name, code, marked)
		}
	}

	attemptErr = errors.New("dial tcp: connection refused")
	if code, marked := serve(liveSession); code != http.StatusServiceUnavailable || marked {
		t.Fatalf("attempt store down: status %d marked %v, want 503 unmarked", code, marked)
	}
	attemptErr = nil
	sessions.err = errors.New("dial tcp: connection refused")
	if code, marked := serve(liveSession); code != http.StatusServiceUnavailable || marked {
		t.Fatalf("login session store down: status %d marked %v, want 503 unmarked", code, marked)
	}
}

// Silo decides a delivery request that carries a valid account credential
// by that credential (RequireStreamReferenceAuth): the declared profile's PIN
// proof and the handler's ownership check answer, whatever `st` says. Bloem's
// stream-token gate must leave such a request unmarked. An expired or
// malformed bearer, which Bloem's apps can still send beside the URL, and a
// URL with no bearer keep the token; Live TV keeps its own delivery.
func TestStreamTokenAuthLeavesAValidCredentialToSilo(t *testing.T) {
	const (
		session   = "11111111-1111-4111-8111-111111111111"
		liveTV    = "99999999-9999-4999-8999-999999999999"
		owner     = 7
		otherUser = 9
	)
	jwt := auth.NewJWTService("bearer-secret", 15*time.Minute, time.Hour)
	expired := auth.NewJWTService("bearer-secret", -time.Minute, time.Hour)
	access := func(svc *auth.JWTService, user int, sess string) string {
		token, err := svc.GenerateAccessToken(user, models.RoleUser, sess)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	refresh, err := jwt.GenerateRefreshToken(otherUser, models.RoleUser, "sess-other")
	if err != nil {
		t.Fatal(err)
	}
	sessions := &fakeSessionValidator{roles: map[string]string{"sess-owner": models.RoleUser, "sess-other": models.RoleAdmin}}
	keys := &fakeAPIKeyValidator{key: &models.APIKey{ID: 3, UserID: otherUser, Key: "sa_other"}}
	am := NewAuthMiddleware(jwt, sessions, keys, &fakeAPIKeyUserLoader{user: &models.User{ID: otherUser, Role: models.RoleAdmin, Enabled: true}})
	am.SetStreamReferenceVerifier(func(reference string) (*streamtoken.Claims, error) {
		return streamtoken.Verify(reference, testStreamSecret)
	}, func(_ context.Context, id string) (StreamReferenceAttempt, error) {
		if id == session {
			return StreamReferenceAttempt{UserID: owner, ProfileID: "p-owner", LoginSessionID: "sess-owner", Live: true}, nil
		}
		return StreamReferenceAttempt{}, nil
	})
	st := func(id string) string {
		token, err := streamtoken.Sign(streamtoken.Claims{SessionID: id, PlayMethod: "direct", UserID: owner, ProfileID: "p-owner"}, testStreamSecret, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	serve := func(target, bearer string) bool {
		var marked bool
		handler := am.StreamTokenAuth(testStreamSecret)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			marked = IsStreamTokenAuthorized(r.Context())
		}))
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		handler.ServeHTTP(httptest.NewRecorder(), req)
		return marked
	}
	media := "/api/v2/stream/" + session + "?st=" + st(session)

	for name, bearer := range map[string]string{
		"no bearer":                 "",
		"owner's expired bearer":    access(expired, owner, "sess-owner"),
		"another account's expired": access(expired, otherUser, "sess-other"),
		"a refresh token":           refresh,
		"not a token":               "garbage",
		"an unknown API key":        "sa_unknown",
	} {
		if !serve(media, bearer) {
			t.Errorf("%s: the stream token no longer authorizes the URL", name)
		}
	}
	for name, bearer := range map[string]string{
		"owner's valid bearer":      access(jwt, owner, "sess-owner"),
		"another account's bearer":  access(jwt, otherUser, "sess-other"),
		"another account's API key": "sa_other",
	} {
		if serve(media, bearer) {
			t.Errorf("%s: the stream token decided; Silo's credential gate must", name)
		}
	}
	if serve(media+"&token="+access(jwt, otherUser, "sess-other"), "") {
		t.Error("a valid token query credential: the stream token decided; Silo's credential gate must")
	}

	live := "/api/bloem/v2/livetv/live-hls/" + liveTV + "/index.m3u8?st=" + st(liveTV)
	if !serve(live, access(jwt, otherUser, "sess-other")) {
		t.Error("Live TV delivery with a valid bearer changed behavior")
	}
}
