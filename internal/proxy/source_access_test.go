package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/nodeconfig"
	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// allowAllSourceAccess is the permissive default the rest of this package's
// tests wire in: they predate this recheck and are not testing it, so they
// need a SourceAccess that never refuses rather than the fail-closed nil
// default (see checkSourceAccess).
type allowAllSourceAccess struct{}

func (allowAllSourceAccess) AllowMediaSource(context.Context, int, int) error { return nil }

// fakeSourceAccess returns a configurable error for every call, and records
// the last account/file it was asked about so a test can assert the recheck
// actually ran with the request's own identity rather than a stub value.
type fakeSourceAccess struct {
	err           error
	lastAccountID int
	lastFileID    int
	calls         int
}

func (f *fakeSourceAccess) AllowMediaSource(_ context.Context, accountID int, mediaFileID int) error {
	f.calls++
	f.lastAccountID = accountID
	f.lastFileID = mediaFileID
	return f.err
}

func newSourceAccessProxyServer(t *testing.T, access SourceAccess) (*Server, string) {
	t.Helper()
	w := nodeconfig.NewWatcher(nil, nil, nil, nodeconfig.BootstrapOverrides{})
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = grantTestSecret
	w.SetConfigForTest(cfg)
	srv := NewServer(w, nodesessions.NewTracker(nil, "http://proxy-1", "proxy-1", "proxy"))
	if access != nil {
		srv.SetSourceAccess(access)
	}
	path := filepath.Join(t.TempDir(), "movie.mp4")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	return srv, path
}

func signDirectPlayToken(t *testing.T, secret, mediaPath string, userID, mediaFileID int) string {
	t.Helper()
	token, err := streamtoken.Sign(streamtoken.Claims{
		SessionID:   "source-access-session",
		MediaPath:   mediaPath,
		PlayMethod:  "direct",
		UserID:      userID,
		ProfileID:   "profile-1",
		MediaFileID: mediaFileID,
	}, secret, time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return token
}

// The delivery guard must recheck library membership, never quality: a
// signed source is the same identity whether the proxy ends up serving it at
// full resolution or not, so AllowMediaSource sees the same file ID the
// direct-play token names.
func TestProxySourceAccessGatesDirectPlayToken(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "allowed", err: nil, wantStatus: http.StatusOK},
		{name: "hidden", err: ErrSourceHidden, wantStatus: http.StatusNotFound},
		{name: "authority unavailable", err: ErrAuthorityUnavailable, wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeSourceAccess{err: test.err}
			srv, path := newSourceAccessProxyServer(t, fake)
			token := signDirectPlayToken(t, grantTestSecret, path, 7, 42)

			req := httptest.NewRequest(http.MethodGet, "/stream/direct/"+token, nil)
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)

			if rr.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rr.Code, test.wantStatus, rr.Body.String())
			}
			if fake.calls != 1 {
				t.Fatalf("AllowMediaSource calls = %d, want 1", fake.calls)
			}
			if fake.lastAccountID != 7 || fake.lastFileID != 42 {
				t.Fatalf("AllowMediaSource(accountID=%d, mediaFileID=%d), want (7, 42)", fake.lastAccountID, fake.lastFileID)
			}
			if test.wantStatus == http.StatusOK && rr.Body.String() != "0123456789" {
				t.Fatalf("allowed request body = %q, want the file's bytes", rr.Body.String())
			}
		})
	}
}

// A proxy this dependency was never wired for must not fail open: nil is not
// "allow" for an account-scoped media route.
func TestProxySourceAccessNilAnswersUnavailable(t *testing.T) {
	srv, path := newSourceAccessProxyServer(t, nil)
	token := signDirectPlayToken(t, grantTestSecret, path, 7, 42)

	req := httptest.NewRequest(http.MethodGet, "/stream/direct/"+token, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rr.Code, rr.Body.String())
	}
}

// The grant route (/stream/v3) reaches the exact same guard through
// serveDirectPlayClaims. A source hidden from the account must 404 there too,
// not just on the legacy token route.
func TestProxySourceAccessGatesGrantRoute(t *testing.T) {
	fake := &fakeSourceAccess{err: ErrSourceHidden}
	srv, path := newSourceAccessProxyServer(t, fake)
	srv.SetMediaGrantAuthority(
		stubGrantStore{cards: map[string]playback.RecipeCard{
			"session-1": {SessionID: "session-1", UserID: 7, ProfileID: "profile-1", MediaFileID: 42, PlayMethod: playback.PlayDirect, InputPath: path},
		}},
		stubLoginSessions{valid: map[string]bool{"login-1": true}},
	)
	bearer := grantAccessToken(t, 7, "login-1")

	req := httptest.NewRequest(http.MethodGet, "/stream/v3/session-1", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rr.Code, rr.Body.String())
	}
	if fake.calls != 1 || fake.lastAccountID != 7 || fake.lastFileID != 42 {
		t.Fatalf("AllowMediaSource(accountID=%d, mediaFileID=%d) called %d times, want (7, 42) once", fake.lastAccountID, fake.lastFileID, fake.calls)
	}
}

// The download route names a source the same way playback does, and must be
// gated the same way — a revoked library must not let a signed download URL
// keep working after the fact.
func TestProxySourceAccessGatesDownloadRoute(t *testing.T) {
	fake := &fakeSourceAccess{err: ErrSourceHidden}
	srv, path := newSourceAccessProxyServer(t, fake)
	token, err := streamtoken.Sign(streamtoken.Claims{
		SessionID:   "download-session",
		MediaPath:   path,
		PlayMethod:  streamtoken.PlayMethodDownload,
		UserID:      9,
		MediaFileID: 55,
	}, grantTestSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/downloads/file/"+token, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rr.Code, rr.Body.String())
	}
	if fake.lastAccountID != 9 || fake.lastFileID != 55 {
		t.Fatalf("AllowMediaSource(accountID=%d, mediaFileID=%d), want (9, 55)", fake.lastAccountID, fake.lastFileID)
	}
}

// The raw transcode-worker/admin routes are protected by the shared node
// secret, never by an account credential. This task must not accidentally
// widen them: neither a viewer's own access token nor a signed media stream
// token may authenticate against them.
func TestWorkerRouteRejectsViewerAndMediaTokens(t *testing.T) {
	w := nodeconfig.NewWatcher(nil, nil, nil, nodeconfig.BootstrapOverrides{})
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = grantTestSecret
	w.SetConfigForTest(cfg)
	srv := NewServer(w, nodesessions.NewTracker(nil, "http://proxy-1", "proxy-1", "proxy"))

	viewerAccessToken := grantAccessToken(t, 7, "login-1")
	mediaStreamToken := signDirectPlayToken(t, grantTestSecret, "/tmp/does-not-matter.mp4", 7, 42)

	for _, test := range []struct {
		name   string
		bearer string
	}{
		{name: "viewer access token", bearer: viewerAccessToken},
		{name: "signed media stream token", bearer: mediaStreamToken},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/status", nil)
			req.Header.Set("Authorization", "Bearer "+test.bearer)
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body %s)", rr.Code, rr.Body.String())
			}
		})
	}

	// Control: the actual node secret does authenticate the same route.
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Bearer "+grantTestSecret)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("node-secret status = %d, want 200 (body %s)", rr.Code, rr.Body.String())
	}
}
