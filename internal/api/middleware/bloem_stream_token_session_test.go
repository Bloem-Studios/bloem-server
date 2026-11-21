package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

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
